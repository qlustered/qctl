package testutil

import (
	"strings"
	"testing"
)

// PeriodLockedHintLead is the opening of the Hint line every PERIOD_LOCKED
// refusal renders.
const PeriodLockedHintLead = "\nHint: An account with deliverable:admin can run `qctl table-series unlock "

// AssertOnePeriodUnlockHint fails t unless err's text carries exactly one
// "Hint:" line, the PERIOD_LOCKED one, and none of the unlock advice
// command-level explainers used to add beside it.
func AssertOnePeriodUnlockHint(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want a PERIOD_LOCKED refusal, got nil")
	}
	msg := err.Error()
	if n := strings.Count(msg, "Hint:"); n != 1 {
		t.Errorf("error has %d Hint lines, want 1:\n%s", n, msg)
	}
	if n := strings.Count(msg, PeriodLockedHintLead); n != 1 {
		t.Errorf("error carries %q %d times, want 1:\n%s", PeriodLockedHintLead, n, msg)
	}
	for _, dup := range []string{"unlock the period first", "unlock it first", "unlock that period"} {
		if strings.Contains(msg, dup) {
			t.Errorf("error repeats unlock advice %q:\n%s", dup, msg)
		}
	}
}
