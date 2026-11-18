package dataview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
)

func TestSelfExclusionMarksOnlyFlaggedRows(t *testing.T) {
	for _, requested := range [][]string{nil, {"keep_in_quarantine_"}} {
		t.Run(fmt.Sprint(requested), func(t *testing.T) {
			s := &api.DataWithCellHistorySchema{
				Fields:             []string{"id", "keep_in_quarantine_"},
				Results:            [][]any{{7, "undecided"}, {3, "hold"}, {5, "release"}},
				SentOrChangedByMe:  &[]bool{true, false, true},
				WaitingForApproval: &[]bool{true, false, false},
			}
			page := mustNormalize(t, s, NormalizeOptions{RequestedFields: requested})
			for i, want := range []bool{true, false, true} {
				if got := contains(page.Rows[i].State, "cannot-approve=self"); got != want {
					t.Errorf("row %d state = %v, want self-exclusion %v", i, page.Rows[i].State, want)
				}
			}
			out := render(t, page, nil)
			for _, want := range []string{"waiting-for-approval, cannot-approve=self", "another approver must approve it", "approval permission and any reviewer designation"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func TestSelfExclusionSidecarWithoutStateColumns(t *testing.T) {
	for _, flags := range []*[]bool{nil, {false}, {true}} {
		s := &api.DataWithCellHistorySchema{Fields: []string{"id"}, Results: [][]any{{7}}, SentOrChangedByMe: flags}
		page := mustNormalize(t, s, NormalizeOptions{})
		out := render(t, page, nil)
		if got := strings.Contains(out, "| Row State |"); got != (flags != nil) {
			t.Errorf("flags %v: Row State presence = %v:\n%s", flags, got, out)
		}
		want := flags != nil && (*flags)[0]
		if got := strings.Contains(out, "cannot-approve=self"); got != want {
			t.Errorf("flags %v: self-exclusion marker presence = %v:\n%s", flags, got, out)
		}
	}
}

func TestSelfExclusionRejectsMisalignedFlags(t *testing.T) {
	for _, flags := range [][]bool{{}, {true, false}} {
		s := &api.DataWithCellHistorySchema{Fields: []string{"id"}, Results: [][]any{{7}}, SentOrChangedByMe: &flags}
		if _, err := Normalize(s, NormalizeOptions{}); err == nil || !strings.Contains(err.Error(), "sent_or_changed_by_me") {
			t.Errorf("flags %v: err = %v, want alignment error", flags, err)
		}
	}
}
