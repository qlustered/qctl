package output

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

type streamItem struct {
	ID    int    `json:"id"`
	State string `json:"state"`
	Token string `json:"token"`
}

// json and yaml snapshots go through the printer's secret masking, which a
// bare json.Marshal of the snapshot would skip.
func TestStreamMasksSecrets(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML} {
		var out bytes.Buffer
		s := NewPrinter(Options{Format: format, Writer: &out}).NewStream(false, true)
		if err := s.Write(NewList([]streamItem{{ID: 1, Token: "s3cr3t"}}, nil, nil, nil, nil)); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
		if strings.Contains(out.String(), "s3cr3t") || !strings.Contains(out.String(), "***") {
			t.Errorf("%s stream = %q, want the token masked", format, out.String())
		}
	}
}

// --allow-plaintext-secrets reaches the stream as it reaches Print.
func TestStreamAllowsPlaintextSecrets(t *testing.T) {
	var out bytes.Buffer
	s := NewPrinter(Options{Format: FormatJSON, Writer: &out, AllowPlaintextSecrets: true}).NewStream(false, true)
	if err := s.Write([]streamItem{{ID: 1, Token: "s3cr3t"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "s3cr3t") {
		t.Errorf("stream = %q, want the token in plain text", out.String())
	}
}

// Every yaml document starts with its own "---" line.
func TestStreamYAMLSeparatesDocuments(t *testing.T) {
	var out bytes.Buffer
	s := NewPrinter(Options{Format: FormatYAML, Writer: &out}).NewStream(false, true)
	for _, state := range []string{"queued", "running"} {
		if err := s.Write([]streamItem{{ID: 1, State: state}}); err != nil {
			t.Fatal(err)
		}
	}
	want := "---\n- id: 1\n  state: queued\n  token: '***'\n---\n- id: 1\n  state: running\n  token: '***'\n"
	if out.String() != want {
		t.Errorf("yaml stream = %q, want %q", out.String(), want)
	}
}

// A Stream that keeps unchanged snapshots writes every one, in every format.
func TestStreamKeepsUnchangedSnapshotsWhenAsked(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML, FormatName, FormatTable} {
		var out bytes.Buffer
		s := NewPrinter(Options{Format: format, Writer: &out, NoHeaders: true}).NewStream(false, false)
		for range 2 {
			if err := s.Write([]streamItem{{ID: 7, State: "queued"}}); err != nil {
				t.Fatalf("%s: %v", format, err)
			}
		}
		if n := strings.Count(out.String(), "7"); n != 2 {
			t.Errorf("%s wrote %q, want the snapshot twice", format, out.String())
		}
	}
}

// On a terminal a changed table is redrawn over the last one, and an
// unchanged one is not written again.
func TestStreamRedrawsTableOnTerminal(t *testing.T) {
	var out bytes.Buffer
	s := NewPrinter(Options{Format: FormatTable, Writer: &out, Columns: []string{"id", "state"}}).NewStream(true, true)
	for _, state := range []string{"queued", "queued", "running"} {
		if err := s.Write([]streamItem{{ID: 1, State: state}}); err != nil {
			t.Fatal(err)
		}
	}
	want := "ID  STATE\n1   queued\n" + "\033[2A\033[J" + "ID  STATE\n1   running\n"
	if out.String() != want {
		t.Errorf("tty table = %q, want %q", out.String(), want)
	}
}

func TestStreamNoticesEndTableRedraw(t *testing.T) {
	for _, rendered := range []bool{false, true} {
		t.Run(fmt.Sprintf("rendered=%t", rendered), func(t *testing.T) {
			var out bytes.Buffer
			s := NewPrinter(Options{Format: FormatTable, Writer: &out, Columns: []string{"id", "state"}}).NewStream(true, true)
			write := func(state string) {
				t.Helper()
				var err error
				if rendered {
					err = s.WriteTable("ID  STATE\n1   " + state + "\n")
				} else {
					err = s.Write([]streamItem{{ID: 1, State: state}})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			write("queued")
			notices := s.NoticeWriter(&out)
			for range 2 {
				if _, err := io.WriteString(notices, "retrying\n"); err != nil {
					t.Fatal(err)
				}
			}
			write("queued")
			write("running")
			write("finished")
			want := "ID  STATE\n1   queued\nretrying\nretrying\nID  STATE\n1   running\n" +
				"\033[2A\033[JID  STATE\n1   finished\n"
			if out.String() != want {
				t.Errorf("stream = %q, want %q", out.String(), want)
			}
		})
	}
}

func TestStreamNoticePreservesUnchangedSuppression(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML, FormatName, FormatTable} {
		for _, tty := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/tty=%t", format, tty), func(t *testing.T) {
				var out, errOut bytes.Buffer
				s := NewPrinter(Options{Format: format, Writer: &out, Columns: []string{"id", "state"}, NameKind: "job"}).NewStream(tty, true)
				snapshot := []streamItem{{ID: 1, State: "queued"}}
				if err := s.Write(snapshot); err != nil {
					t.Fatal(err)
				}
				first := out.String()
				if _, err := io.WriteString(s.NoticeWriter(&errOut), "retrying\n"); err != nil {
					t.Fatal(err)
				}
				if err := s.Write(snapshot); err != nil {
					t.Fatal(err)
				}
				if out.String() != first || errOut.String() != "retrying\n" {
					t.Errorf("stdout = %q, stderr = %q, want %q and the notice alone", out.String(), errOut.String(), first)
				}
			})
		}
	}
}

func TestStreamNoticesDoNotInterleaveRedraws(t *testing.T) {
	var out bytes.Buffer
	s := NewPrinter(Options{Writer: &out}).NewStream(true, false)
	notices := s.NoticeWriter(&out)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 100 {
			if err := s.WriteTable("ID\n1\n"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			if _, err := io.WriteString(notices, "retrying\n"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
	text := out.String()
	if strings.Count(text, "ID\n1\n") != 100 || strings.Count(text, "retrying\n") != 100 {
		t.Fatalf("stream lost snapshots or notices: %q", text)
	}
	for text != "" {
		matched := false
		for _, chunk := range []string{"ID\n1\n", "\033[2A\033[JID\n1\n", "retrying\n"} {
			if strings.HasPrefix(text, chunk) {
				text = strings.TrimPrefix(text, chunk)
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("notice interrupted a redraw: %q", text)
		}
	}
}

// json and yaml on a terminal append; only a table is redrawn.
func TestStreamAppendsJSONOnTerminal(t *testing.T) {
	var out bytes.Buffer
	s := NewPrinter(Options{Format: FormatJSON, Writer: &out}).NewStream(true, true)
	for _, state := range []string{"queued", "running"} {
		if err := s.Write([]streamItem{{ID: 1, State: state}}); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(out.String(), "\033[") || strings.Count(out.String(), "\n") != 2 {
		t.Errorf("tty json = %q, want two lines and no cursor movement", out.String())
	}
}

// An unknown --columns name exits 2 on an empty first snapshot, on and off a
// terminal, as Print does for the same empty list.
func TestStreamTableRejectsUnknownColumnOnEmptyList(t *testing.T) {
	for _, tty := range []bool{false, true} {
		var out bytes.Buffer
		s := NewPrinter(Options{Format: FormatTable, Writer: &out, Columns: []string{"id", "bogus"}}).NewStream(tty, true)
		err := s.Write(NewList([]streamItem{}, nil, nil, nil, nil))
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("tty=%t: err = %v (exit %d), want exit %d; wrote %q", tty, err, got, qerrors.ExitBadUsage, out.String())
		}
	}
}
