package manifest

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrDeclined is returned by a per-document function whose confirmation prompt
// was answered "n".
//
// In a MULTI-document run declining is not a failure — the document was
// skipped on purpose, the rest still run, and Run must not turn it into a
// non-zero exit code.
//
// In a ONE-document run it is an ordinary failure and keeps its exit code (1,
// since this is a bare errors.New). A one-document -f is the manifest spelling
// of `resolve <id> --action ...`: you asked for one thing, you said no, and
// nothing happened, which is exactly the case cmdutil.Confirm documents as
// exit 1. Treating it as a skip would have the command exit 0 having done
// nothing — and, before this was fixed, print nothing either.
//
// The producer is confirmResolution on the alert and warning sides, which maps
// cmdutil.ErrDeclined onto this sentinel; internal/pkg/manifest cannot import
// cmdutil (cmdutil already depends on this package), which is why there are
// two sentinels rather than one.
var ErrDeclined = errors.New("declined at the prompt")

// docFailure is one document's failure, labelled the way its outcome line is.
type docFailure struct {
	index int
	err   error
}

// StreamError reports how a multi-document run ended.
//
// It wraps the FIRST failure so qerrors.GetExitCode still sees the HTTP status
// that caused it. A bare fmt.Errorf here reads perfectly and silently degrades
// every 404 and 403 to exit 1 — the defect internal/commands/apply/stream.go
// documents having had.
type StreamError struct {
	first       error
	total       int
	failures    []docFailure
	unattempted int
}

func (e *StreamError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d documents failed", len(e.failures), e.total)
	for _, f := range e.failures {
		fmt.Fprintf(&b, "\n  document %d: %s", f.index, f.err)
	}
	// Without this line "1 of 5 documents failed" reads as though the other
	// four succeeded, when --fail-fast never attempted them. Same reason
	// internal/commands/apply/stream.go counts its skipped documents into its
	// summary rather than letting the remainder go unmentioned.
	if e.unattempted > 0 {
		fmt.Fprintf(&b, "\n  %d document%s not attempted (--fail-fast)", e.unattempted, plural(e.unattempted))
	}
	return b.String()
}

func (e *StreamError) Unwrap() error { return e.first }

// plural is the repo's existing idiom (internal/user_submitted/report.go),
// copied rather than exported: a package-private clone is cheaper than a new
// cross-package dependency, and that one is unexported too.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// reportSkipped names every document --fail-fast left untried and returns how
// many there were. It is the sibling of the function of the same name in
// internal/commands/apply/stream.go, and exists for the same reason: a
// silent remainder is indistinguishable from a remainder that succeeded.
//
// rest is the documents after the one that failed, and index is that failing
// document's 1-based position, so the lines printed here continue the run's own
// numbering.
func reportSkipped(out io.Writer, index int, rest [][]byte) int {
	for i := range rest {
		fmt.Fprintf(out, "document %d: skipped (--fail-fast)\n", index+1+i)
	}
	return len(rest)
}

// Run applies run to each document in order and never concurrently, printing
// each document's outcome to out.
//
// A ONE-document run prints the callback's outcome without a document prefix
// and returns its failure directly. The unprefixed line preserves ordinary CLI
// output while ensuring a successful one-document file is not silent. A
// declined one-document run is one of those failures, not a skip; see
// ErrDeclined.
//
// With --fail-fast the run stops at the first failure, and every document it
// therefore never attempted is named on out and counted in the returned
// StreamError. Silence there is indistinguishable from success: "1 of 5
// documents failed" reads as four that worked.
//
// internal/commands/apply/stream.go is the older sibling of this function and
// is deliberately NOT migrated to it in this branch: it is welded to apply's
// kind dispatch and its -o json result encoding, neither of which a verb that
// accepts exactly one kind has. Migrate it in a branch of its own.
func Run(out io.Writer, docs [][]byte, failFast bool, run func(index int, doc []byte) (string, error)) error {
	single := len(docs) == 1
	stream := &StreamError{total: len(docs)}
	for i, doc := range docs {
		index := i + 1
		line, err := run(index, doc)
		switch {
		// A decline is a skip only in a multi-document run; see ErrDeclined.
		// A single-document decline falls through to the failure arm below, so
		// it keeps exit 1 and its message instead of exiting 0 in silence.
		case errors.Is(err, ErrDeclined) && !single:
			fmt.Fprintf(out, "document %d: skipped\n", index)
		case err != nil:
			if !single {
				fmt.Fprintf(out, "document %d: FAILED: %s\n", index, err)
			}
			if stream.first == nil {
				stream.first = err
			}
			stream.failures = append(stream.failures, docFailure{index: index, err: err})
			if failFast {
				if len(docs) == 1 {
					return err
				}
				stream.unattempted = reportSkipped(out, index, docs[i+1:])
				return stream
			}
		default:
			if single {
				fmt.Fprintln(out, line)
			} else {
				fmt.Fprintf(out, "document %d: %s\n", index, line)
			}
		}
	}
	if len(stream.failures) > 0 {
		if len(docs) == 1 {
			return stream.first
		}
		return stream
	}
	return nil
}
