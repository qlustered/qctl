package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type statusErr struct{ code int }

func (e *statusErr) Error() string { return fmt.Sprintf("status %d", e.code) }

func TestRunAppliesEveryDocumentInOrder(t *testing.T) {
	var out bytes.Buffer
	var seen []int
	err := Run(&out, [][]byte{{'a'}, {'b'}, {'c'}}, false, func(i int, _ []byte) (string, error) {
		seen = append(seen, i)
		return fmt.Sprintf("doc %d ok", i), nil
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if fmt.Sprint(seen) != "[1 2 3]" {
		t.Errorf("order = %v, want [1 2 3]", seen)
	}
	for _, want := range []string{"doc 1 ok", "doc 2 ok", "doc 3 ok"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output missing %q, got %q", want, out.String())
		}
	}
}

// A single-document file prints the callback's outcome without adding a
// "document 1" prefix. Dropping this line would make a successful one-document
// resolve silent, while adding the prefix would churn the ordinary CLI output.
func TestRunPrintsTheOutcomeForASingleDocument(t *testing.T) {
	var out bytes.Buffer
	if err := Run(&out, [][]byte{{'a'}}, false, func(int, []byte) (string, error) {
		return "resolved", nil
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.String() != "resolved\n" {
		t.Errorf("single-document run wrote %q, want the unprefixed outcome", out.String())
	}
}

func TestRunReturnsTheOriginalSingleDocumentFailure(t *testing.T) {
	var out bytes.Buffer
	want := errors.New("boom")
	err := Run(&out, [][]byte{{'a'}}, false, func(int, []byte) (string, error) {
		return "", want
	})
	if err != want {
		t.Errorf("Run error = %v, want the original single-document error", err)
	}
	if out.Len() != 0 {
		t.Errorf("single-document run wrote %q, want nothing", out.String())
	}
}

func TestRunContinuesPastAFailureAndReportsTheCount(t *testing.T) {
	var out bytes.Buffer
	attempted := 0
	err := Run(&out, [][]byte{{'a'}, {'b'}, {'c'}}, false, func(i int, _ []byte) (string, error) {
		attempted++
		if i == 2 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if attempted != 3 {
		t.Errorf("attempted = %d, want 3 (no fail-fast)", attempted)
	}
	if err == nil || !strings.Contains(err.Error(), "1 of 3 documents failed") {
		t.Errorf("err = %v, want a 1-of-3 summary", err)
	}
}

func TestRunFailFastStopsAtTheFirstFailure(t *testing.T) {
	var out bytes.Buffer
	attempted := 0
	err := Run(&out, [][]byte{{'a'}, {'b'}, {'c'}}, true, func(i int, _ []byte) (string, error) {
		attempted++
		if i == 2 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if attempted != 2 {
		t.Errorf("attempted = %d, want 2 (fail-fast)", attempted)
	}
	if err == nil {
		t.Fatal("want an error")
	}
}

// The exit code must survive the summary: a bare fmt.Errorf here reads
// perfectly and silently degrades every 404 and 403 to exit 1.
func TestStreamErrorUnwrapsToTheFirstFailure(t *testing.T) {
	var out bytes.Buffer
	first := &statusErr{code: 404}
	err := Run(&out, [][]byte{{'a'}, {'b'}}, false, func(int, []byte) (string, error) {
		return "", first
	})
	var target *statusErr
	if !errors.As(err, &target) || target.code != 404 {
		t.Errorf("errors.As did not reach the first failure; err = %v", err)
	}
}

// Declining a confirmation prompt skipped the document on purpose; it must not
// become a failure or a non-zero exit code.
func TestRunTreatsErrDeclinedAsASkip(t *testing.T) {
	var out bytes.Buffer
	err := Run(&out, [][]byte{{'a'}, {'b'}}, false, func(i int, _ []byte) (string, error) {
		if i == 1 {
			return "", ErrDeclined
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil: declining is not a failure", err)
	}
	if !strings.Contains(out.String(), "skipped") {
		t.Errorf("output should say the document was skipped, got %q", out.String())
	}
}

// A ONE-document file is the manifest spelling of `resolve <id> --action ...`:
// declining there did the thing you asked for exactly zero times, so it keeps
// the exit code cmdutil.Confirm documents for a decline. Restoring the old
// `case errors.Is(err, ErrDeclined):` arm — which returned nil and printed
// nothing for a single document — must make this fail.
func TestRunFailsASingleDeclinedDocument(t *testing.T) {
	var out bytes.Buffer
	err := Run(&out, [][]byte{{'a'}}, false, func(int, []byte) (string, error) {
		return "", fmt.Errorf("aborted: %w", ErrDeclined)
	})
	if err == nil {
		t.Fatal("a declined single-document run must not report success")
	}
	if !errors.Is(err, ErrDeclined) {
		t.Errorf("err = %v, want the decline itself, not a rewrapped one", err)
	}
	if err.Error() == "" {
		t.Error("the decline must carry a message; exiting non-zero in silence was the bug")
	}
	if strings.Contains(out.String(), "skipped") {
		t.Errorf("a single document has no skip line, got %q", out.String())
	}
}

// The other half of the same rule: declining every document of a MULTI-document
// file is still a clean run, because each decline was a deliberate skip.
func TestRunDecliningEveryDocumentIsStillASuccess(t *testing.T) {
	var out bytes.Buffer
	err := Run(&out, [][]byte{{'a'}, {'b'}}, false, func(int, []byte) (string, error) {
		return "", ErrDeclined
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil", err)
	}
	if got := strings.Count(out.String(), "skipped"); got != 2 {
		t.Errorf("skip lines = %d, want 2; got %q", got, out.String())
	}
}

// --fail-fast stops at a FAILURE, and a skip is not one: a declined document
// must not end the run early. Moving the decline arm below the failure arm —
// so a decline fell into it — would make this fail on the attempted count.
func TestRunFailFastDoesNotStopAtASkip(t *testing.T) {
	var out bytes.Buffer
	attempted := 0
	err := Run(&out, [][]byte{{'a'}, {'b'}, {'c'}}, true, func(i int, _ []byte) (string, error) {
		attempted++
		if i == 1 {
			return "", ErrDeclined
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("Run = %v, want nil: a skip is not a failure", err)
	}
	if attempted != 3 {
		t.Errorf("attempted = %d, want 3: --fail-fast stops at failures, not skips", attempted)
	}
}

// --fail-fast stopping is only half the contract: the documents it never
// attempted have to be named. Silence there is indistinguishable from success,
// and "1 of 5 documents failed" reads as four that worked.
func TestRunFailFastNamesTheDocumentsItDidNotAttempt(t *testing.T) {
	var out bytes.Buffer
	attempted := 0
	err := Run(&out, [][]byte{{'a'}, {'b'}, {'c'}, {'d'}, {'e'}}, true, func(i int, _ []byte) (string, error) {
		attempted++
		if i == 1 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if attempted != 1 {
		t.Fatalf("attempted = %d, want 1 (fail-fast at document 1)", attempted)
	}
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{
		"document 2: skipped (--fail-fast)",
		"document 3: skipped (--fail-fast)",
		"document 4: skipped (--fail-fast)",
		"document 5: skipped (--fail-fast)",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output is missing %q, got:\n%s", want, out.String())
		}
	}
	if !strings.Contains(err.Error(), "4 documents not attempted (--fail-fast)") {
		t.Errorf("the error must not read as 4 successes, got %q", err)
	}
}

// The last document failing leaves nothing un-attempted, so nothing is claimed.
func TestRunFailFastOnTheLastDocumentReportsNoSkips(t *testing.T) {
	var out bytes.Buffer
	err := Run(&out, [][]byte{{'a'}, {'b'}}, true, func(i int, _ []byte) (string, error) {
		if i == 2 {
			return "", errors.New("boom")
		}
		return "ok", nil
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(out.String(), "--fail-fast") {
		t.Errorf("nothing was left un-attempted, got:\n%s", out.String())
	}
	if strings.Contains(err.Error(), "not attempted") {
		t.Errorf("nothing was left un-attempted, got %q", err)
	}
}

// One un-attempted document is "1 document", not "1 documents".
func TestRunFailFastCountsOneUnattemptedDocumentInTheSingular(t *testing.T) {
	var out bytes.Buffer
	err := Run(&out, [][]byte{{'a'}, {'b'}}, true, func(int, []byte) (string, error) {
		return "", errors.New("boom")
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "1 document not attempted (--fail-fast)") {
		t.Errorf("want the singular, got %q", err)
	}
}
