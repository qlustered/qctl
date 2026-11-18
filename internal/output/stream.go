package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/terminal"
)

// Stream writes successive snapshots of one value, such as each poll of a
// watched list, in its printer's format:
//
//   - json: each snapshot as one compact line.
//   - yaml: each snapshot as a document after a "---" line.
//   - name: the name of each item added, changed or removed since the last
//     snapshot, and of every item in the first.
//   - table: on a terminal, the table redrawn in place; elsewhere, the table
//     appended. An empty snapshot prints the header alone when columns are set.
//
// name and table print a List snapshot's items. A Stream that skips
// unchanged snapshots compares their data, except a table on a terminal,
// which compares its rendered text.
type Stream struct {
	mu            sync.Mutex
	p             *Printer
	tty           bool
	skipUnchanged bool
	wrote         bool
	prevKey       []byte
	prevItems     map[string]string
	prevText      string
	prevLines     int
}

// NewStream returns a Stream writing to the printer's writer. tty selects the
// in-place table redraw; skipUnchanged drops a snapshot equal to the last one
// written.
func (p *Printer) NewStream(tty, skipUnchanged bool) *Stream {
	return &Stream{p: p, tty: tty, skipUnchanged: skipUnchanged}
}

// Write writes snapshot, or nothing when the Stream skips unchanged snapshots
// and snapshot is unchanged.
func (s *Stream) Write(snapshot any) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch s.p.opts.Format {
	case FormatJSON, FormatYAML:
		plain := s.p.plainData(snapshot)
		// Marshalled before YAMLNumbers, which rewrites plain's numbers in
		// place into values only a YAML encoder takes.
		key, err := json.Marshal(plain)
		if err != nil {
			return err
		}
		if s.unchanged(key) {
			return nil
		}
		if s.p.opts.Format == FormatJSON {
			_, err = s.p.opts.Writer.Write(append(key, '\n'))
			return err
		}
		var doc bytes.Buffer
		enc := jsonnum.NewYAMLEncoder(&doc)
		enc.SetIndent(2)
		if err := enc.Encode(jsonnum.YAMLNumbers(plain)); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		_, err = io.WriteString(s.p.opts.Writer, "---\n"+doc.String())
		return err
	case FormatName:
		return s.writeNames(streamItems(snapshot))
	default:
		return s.writeTable(listItems(snapshot))
	}
}

// WriteTable writes a pre-rendered table snapshot, redrawing it on a terminal.
func (s *Stream) WriteTable(text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeTableText(text)
}

// NoticeWriter returns a writer that serializes notices with snapshots and
// forgets the previous table's cursor position after each notice.
func (s *Stream) NoticeWriter(w io.Writer) io.Writer {
	return streamNoticeWriter{stream: s, writer: w}
}

type streamNoticeWriter struct {
	stream *Stream
	writer io.Writer
}

func (w streamNoticeWriter) Write(p []byte) (int, error) {
	w.stream.mu.Lock()
	defer w.stream.mu.Unlock()
	w.stream.prevLines = 0
	return w.writer.Write(p)
}

// unchanged reports whether key repeats the last snapshot written while the
// Stream skips unchanged snapshots, and otherwise records key as the last.
func (s *Stream) unchanged(key []byte) bool {
	if s.skipUnchanged && s.wrote && bytes.Equal(key, s.prevKey) {
		return true
	}
	s.prevKey = key
	s.wrote = true
	return false
}

// writeNames prints the name of each item that is new or changed, then of
// each item that is gone, in name order.
func (s *Stream) writeNames(items []any) error {
	current := make(map[string]string, len(items))
	var names []string
	for _, item := range items {
		name := NameRef(s.p.opts.NameKind, s.p.opts.NameField, item)
		if name == "" {
			continue
		}
		data, err := json.Marshal(s.p.plainData(item))
		if err != nil {
			return err
		}
		if _, seen := current[name]; !seen {
			names = append(names, name)
		}
		current[name] = string(data)
	}
	var gone []string
	for name := range s.prevItems {
		if _, ok := current[name]; !ok {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	for _, name := range names {
		if prev, ok := s.prevItems[name]; s.wrote && s.skipUnchanged && ok && prev == current[name] {
			continue
		}
		fmt.Fprintln(s.p.opts.Writer, terminal.Sanitize(name))
	}
	for _, name := range gone {
		fmt.Fprintln(s.p.opts.Writer, terminal.Sanitize(name))
	}
	s.prevItems = current
	s.wrote = true
	return nil
}

// writeTable prints items as a table: off a terminal appended when their
// data changed, on one redrawn over the last table when its text changed.
func (s *Stream) writeTable(items any) error {
	if !s.tty {
		key, err := json.Marshal(s.p.plainData(items))
		if err != nil {
			return err
		}
		if s.unchanged(key) {
			return nil
		}
		return s.renderTable(s.p.opts.Writer, items)
	}
	var buf bytes.Buffer
	if err := s.renderTable(&buf, items); err != nil {
		return err
	}
	return s.writeTableText(buf.String())
}

func (s *Stream) writeTableText(text string) error {
	if s.skipUnchanged && s.wrote && text == s.prevText {
		return nil
	}
	if s.tty && s.prevLines > 0 {
		fmt.Fprintf(s.p.opts.Writer, "\033[%dA\033[J", s.prevLines)
	}
	if _, err := io.WriteString(s.p.opts.Writer, text); err != nil {
		return err
	}
	s.prevText = text
	s.prevLines = strings.Count(text, "\n")
	s.wrote = true
	return nil
}

// renderTable writes items to w as the printer's table, or its header alone
// when there are no rows and columns are set.
func (s *Stream) renderTable(w io.Writer, items any) error {
	p := *s.p
	p.opts.Writer = w
	if err := p.printTable(items); err != nil {
		return err
	}
	if len(p.dataToRows(items)) == 0 && len(p.opts.Columns) > 0 && !p.opts.NoHeaders {
		return WriteAligned(w, tableHeader(p.opts.Columns)+"\n")
	}
	return nil
}

// listItems returns a List snapshot's items, or any other snapshot as it is.
func listItems(snapshot any) any {
	switch l := snapshot.(type) {
	case List:
		return l.Items
	case *List:
		return l.Items
	}
	return snapshot
}

// streamItems returns the items a snapshot holds: a List's items, a slice's
// elements, or the snapshot itself.
func streamItems(snapshot any) []any {
	snapshot = listItems(snapshot)
	v := reflect.ValueOf(snapshot)
	if !v.IsValid() {
		return nil
	}
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return []any{snapshot}
	}
	items := make([]any, v.Len())
	for i := range items {
		items[i] = v.Index(i).Interface()
	}
	return items
}
