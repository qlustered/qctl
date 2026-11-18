package apply

import (
	"cmp"
	stderrors "errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// errDocumentDeclined is returned by a handler whose confirmation prompt was
// answered "n". Declining is not a failure — the document was skipped on
// purpose — so it must not become a non-zero exit code.
var errDocumentDeclined = stderrors.New("declined at the prompt")

// resolveKind maps one document to the handler that applies it.
type resolveKind func(doc manifestDoc) (applyFunc, error)

// docFailure is one document's failure, labelled the way the per-document
// failure line labels it.
type docFailure struct {
	label string
	err   error
}

type applyResults struct {
	documents []any
	names     []string
}

func (r *applyResults) add(result *applyResult) {
	if result == nil {
		return
	}
	if !isNilResult(result.document) {
		r.documents = append(r.documents, result.document)
	}
	if result.name != "" {
		r.names = append(r.names, result.name)
	}
}

// multiDocError reports how a multi-document apply ended.
//
// It wraps the first failure so GetExitCode still sees the HTTP status that
// caused it: a bare fmt.Errorf here reads perfectly and silently degrades every
// 404 and 403 to exit 1, which is exactly what the pre-existing
// "%d of %d documents failed" in rules.go did.
//
// It also enumerates every failure's cause. A count alone loses the causes
// entirely under -o json, where the per-document failure lines are the only
// other place they appear.
type multiDocError struct {
	first    error
	total    int
	failed   int
	failures []docFailure
}

func (e *multiDocError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d documents failed", e.failed, e.total)
	if len(e.failures) == 0 {
		return b.String()
	}
	b.WriteString(":")
	for _, f := range e.failures {
		fmt.Fprintf(&b, "\n  %s: %s", f.label, f.err)
	}
	return b.String()
}

func (e *multiDocError) Unwrap() error { return e.first }

// applyStream applies every document in filePath, in file order, dispatching
// each through resolve.
func applyStream(cmd *cobra.Command, filePath string, resolve resolveKind) error {
	docs, err := readManifestDocuments(filePath)
	if err != nil {
		return err
	}
	return applyDocuments(cmd, docs, resolve)
}

// applyDocuments applies docs in order, dispatching each through resolve.
//
// Documents are applied one at a time and never concurrently. That is
// deliberate: the backend caps concurrent dry runs per user at 3
// (atlas cettings/cettings/controllers/dry_run_helpers.py), so a parallel batch
// would throttle itself.
//
// Under -o json or yaml, stdout carries the documents the handlers returned,
// printed once the run ends: a one-document file prints its document, and a
// file holding more prints one output.List of every document returned, in
// file order, whether or not another document failed.
//
// A document whose error carries a *qerrors.Interrupted ends the apply at
// once with that error: no later document is tried, and nothing but the
// documents already returned is printed.
func applyDocuments(cmd *cobra.Command, docs []manifestDoc, resolve resolveKind) error {
	failFast, _ := cmd.Flags().GetBool("fail-fast")
	results := cmdutil.NewResultWriter(cmd)

	// The runner's own lines are prose, and the two reasons to keep them off
	// stdout are not the same reason. A one-document file gets none of them at
	// all: its handler's output and its error already say everything, and that
	// is the behavior every existing manifest depends on. Under -o json or yaml
	// stdout belongs to the results, so the prose moves to stderr rather than
	// disappearing — the only place a structured-output consumer can learn
	// that a document failed. The single-document check comes first so that
	// ordering cannot regress.
	out := cmd.OutOrStdout()
	switch {
	case len(docs) == 1:
		out = io.Discard
	case results.Machine():
		out = cmd.ErrOrStderr()
	}

	var succeeded, failed, skipped int
	var firstErr error
	var failures []docFailure
	var applied applyResults

	for i, doc := range docs {
		result, err := applyOneDocument(cmd, doc, resolve)
		applied.add(result)
		var interrupted *qerrors.Interrupted
		if stderrors.As(err, &interrupted) {
			return writeAppliedDocuments(results, len(docs), applied, err)
		}
		switch {
		case err == nil:
			succeeded++
		case stderrors.Is(err, errDocumentDeclined):
			skipped++
		default:
			failed++
			if firstErr == nil {
				firstErr = err
			}
			// One label, both consumers: the enumeration in the aggregate error
			// and the per-document line can never drift apart.
			label := docLabel(doc)
			failures = append(failures, docFailure{label: label, err: err})
			fmt.Fprintf(out, "%s failed: %s\n", label, terminal.Sanitize(err.Error()))
		}
		if failFast && failed > 0 {
			skipped += reportSkipped(out, docs[i+1:])
			break
		}
	}

	if len(docs) > 1 {
		fmt.Fprintln(out, summaryLine(len(docs), succeeded, failed, skipped))
	}
	var runErr error
	switch {
	case failed == 0:
	case len(docs) == 1:
		runErr = firstErr
	default:
		runErr = &multiDocError{first: firstErr, total: len(docs), failed: failed, failures: failures}
	}
	return writeAppliedDocuments(results, len(docs), applied, runErr)
}

// writeAppliedDocuments prints the run's documents or resource references.
func writeAppliedDocuments(results cmdutil.ResultWriter, total int, applied applyResults, runErr error) error {
	writeErr := results.WriteRun(total, applied.documents, applied.names...)
	if runErr != nil {
		return runErr
	}
	return writeErr
}

// isNilResult reports whether a handler returned no document: a nil
// interface, or a nil pointer, map or slice inside one.
func isNilResult(result any) bool {
	if result == nil {
		return true
	}
	switch v := reflect.ValueOf(result); v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func applyOneDocument(cmd *cobra.Command, doc manifestDoc, resolve resolveKind) (*applyResult, error) {
	if doc.Err != nil {
		return nil, doc.Err
	}
	apply, err := resolve(doc)
	if err != nil {
		return nil, err
	}
	return apply(cmd, doc)
}

// reportSkipped names every document --fail-fast left untried and returns how
// many there were. Naming them is the point: a silent remainder is the same
// failure mode as the silent truncation this whole change removes.
func reportSkipped(out io.Writer, rest []manifestDoc) int {
	for _, doc := range rest {
		fmt.Fprintf(out, "%s skipped (--fail-fast)\n", docLabel(doc))
	}
	return len(rest)
}

// summaryLine is the trailing count a multi-document apply ends with.
func summaryLine(total, succeeded, failed, skipped int) string {
	parts := []string{fmt.Sprintf("%d succeeded", succeeded)}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	if skipped > 0 {
		parts = append(parts, fmt.Sprintf("%d skipped", skipped))
	}
	return fmt.Sprintf("%d documents: %s", total, strings.Join(parts, ", "))
}

// onlyKind is the resolver a typed `apply <resource>` subcommand uses: every
// document goes to the one handler, which rejects a document of the wrong kind
// with its own, more specific message ("expected kind 'Table', got ...")
// rather than the dispatcher's list of every kind apply -f accepts.
func onlyKind(fn applyFunc) resolveKind {
	return func(manifestDoc) (applyFunc, error) { return fn, nil }
}

// docLabel names a document on one line using its metadata or recovered identity.
func docLabel(doc manifestDoc) string {
	id := doc.Identity
	kind := cmp.Or(doc.Kind, id.Kind)
	label := resourceNameForKind(kind)
	if label == "" {
		label = labelValue(kind)
	}
	if label == "" {
		label = "document"
	}
	if name := cmp.Or(doc.Name, id.Name); name != "" {
		return label + "/" + labelValue(name)
	}
	switch {
	case id.InstanceName != "":
		label += fmt.Sprintf(" %q", id.InstanceName)
	case id.Rule != "":
		label += " for rule " + labelValue(id.Rule)
		if id.DatasetID != "" {
			label += " on table " + labelValue(id.DatasetID)
		}
	}
	return fmt.Sprintf("%s (document %d of %d)", label, doc.Index, doc.Total)
}

// labelValue renders a file-supplied value for docLabel: sanitized, with LF
// escaped as well so the label stays one line.
func labelValue(s string) string {
	return strings.ReplaceAll(terminal.Sanitize(s), "\n", `\n`)
}

var (
	kindResourceOnce sync.Once
	kindResourceMap  map[string]string
)

// resourceNameForKind resolves a manifest kind to its resource name using the
// resource pages — the same join catalog.Build makes — so a renamed resource
// cannot leave apply printing the old word. internal/resources imports nothing
// else under internal/, so this cannot cycle. Returns "" when no page declares
// the kind.
func resourceNameForKind(kind string) string {
	if kind == "" {
		return ""
	}
	kind = pkgmanifest.CanonicalKind(kind)
	kindResourceOnce.Do(func() {
		kindResourceMap = map[string]string{}
		pages, err := resources.Pages()
		if err != nil {
			return
		}
		for _, p := range pages {
			if p.Kind != "" {
				kindResourceMap[p.Kind] = p.Name
			}
		}
	})
	return kindResourceMap[kind]
}
