package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"strings"
	"testing"
	"unicode/utf8"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Build is the happy path: targets are the cross product of --row and
// --field, exactly as comment-thread create expands them.
func TestSubmitBuildExpandsTheCrossProduct(t *testing.T) {
	body, err := SubmitRequest{
		Rows:    []int{1042, 1043},
		Fields:  []string{"amount", "currency"},
		Comment: "wrong FX rate",
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(body.Rows) != 2 {
		t.Fatalf("Rows = %d, want 2", len(body.Rows))
	}
	if body.Rows[0].RowID != 1042 || body.Rows[1].RowID != 1043 {
		t.Errorf("row ids = %d, %d", body.Rows[0].RowID, body.Rows[1].RowID)
	}
	for i, row := range body.Rows {
		if strings.Join(row.FieldNames, ",") != "amount,currency" {
			t.Errorf("row %d fields = %v", i, row.FieldNames)
		}
	}
	if body.Comment != "wrong FX rate" {
		t.Errorf("Comment = %q", body.Comment)
	}
	// Omitted --suggest must be a nil pointer, not an empty list: the backend
	// folds suggested_values into the aggregate signature, and [] and null are
	// the same complaint only because canonical_suggested_values maps both to
	// None. Sending nil is the shape that cannot drift.
	if body.SuggestedValues != nil {
		t.Errorf("SuggestedValues = %v, want nil", body.SuggestedValues)
	}
}

func TestSubmitBuildCarriesSuggestions(t *testing.T) {
	body, err := SubmitRequest{
		Rows:    []int{1042},
		Fields:  []string{"amount"},
		Comment: "wrong FX rate",
		Suggest: []string{"1420.00", "1425.00"},
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if body.SuggestedValues == nil || len(*body.SuggestedValues) != 2 {
		t.Fatalf("SuggestedValues = %v", body.SuggestedValues)
	}
	if (*body.SuggestedValues)[0].Value != "1420.00" {
		t.Errorf("first suggestion = %q", (*body.SuggestedValues)[0].Value)
	}
	// The backend defaults strong to true and materialises that default on
	// write, so the stored flag is true either way. qctl states it explicitly
	// rather than inheriting it, so a change to the backend's default cannot
	// silently move what a submitted suggestion means.
	if strong := (*body.SuggestedValues)[0].Strong; strong == nil || !*strong {
		t.Errorf("Strong = %v, want true", strong)
	}
}

// Every rejection below is exit 2. Asserting the code, not the text, is the
// repo's contract: a substring assertion passes against a broken exit code.
func TestSubmitBuildRejections(t *testing.T) {
	longComment := strings.Repeat("x", MaxCommentChars+1)
	tooManyRows := make([]int, MaxRows+1)
	for i := range tooManyRows {
		tooManyRows[i] = i + 1
	}

	cases := []struct {
		name string
		req  SubmitRequest
	}{
		{"no rows", SubmitRequest{Fields: []string{"amount"}, Comment: "x"}},
		{"too many rows", SubmitRequest{Rows: tooManyRows, Fields: []string{"amount"}, Comment: "x"}},
		{"duplicate row", SubmitRequest{Rows: []int{7, 7}, Fields: []string{"amount"}, Comment: "x"}},
		{"non-positive row", SubmitRequest{Rows: []int{0}, Fields: []string{"amount"}, Comment: "x"}},
		{"no fields", SubmitRequest{Rows: []int{7}, Comment: "x"}},
		{"blank field", SubmitRequest{Rows: []int{7}, Fields: []string{" "}, Comment: "x"}},
		{"empty comment", SubmitRequest{Rows: []int{7}, Fields: []string{"amount"}, Comment: "  "}},
		{"comment too long", SubmitRequest{Rows: []int{7}, Fields: []string{"amount"}, Comment: longComment}},
		{"blank suggestion", SubmitRequest{Rows: []int{7}, Fields: []string{"amount"}, Comment: "x", Suggest: []string{""}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.req.Build()
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// A 2000-rune comment of multi-byte characters is under the cap: Pydantic's
// max_length counts characters, not bytes.
func TestSubmitCommentCapCountsRunes(t *testing.T) {
	req := SubmitRequest{
		Rows:    []int{7},
		Fields:  []string{"amount"},
		Comment: strings.Repeat("é", MaxCommentChars),
	}
	if _, err := req.Build(); err != nil {
		t.Fatalf("a %d-rune comment was rejected: %v", MaxCommentChars, err)
	}
}

// The comment is folded into the aggregate signature raw — there is no
// canonical_comment — so stray whitespace must not reach the wire, or one
// objection mints two aggregates.
func TestSubmitCommentIsTrimmedOnTheWire(t *testing.T) {
	body, err := SubmitRequest{
		Rows:    []int{7},
		Fields:  []string{"amount"},
		Comment: "  wrong FX rate\n",
	}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if body.Comment != "wrong FX rate" {
		t.Errorf("Comment = %q, want %q", body.Comment, "wrong FX rate")
	}

	// The cap is measured on the trimmed value, so a comment that is exactly at
	// the limit once the whitespace is gone is ACCEPTED — the backend would have
	// accepted it, and qctl must not enforce a stricter cap than the server's.
	atCap := strings.Repeat("x", MaxCommentChars)
	body, err = SubmitRequest{
		Rows:    []int{7},
		Fields:  []string{"amount"},
		Comment: "  " + atCap + "\n",
	}.Build()
	if err != nil {
		t.Fatalf("a %d-rune comment padded with whitespace was rejected: %v", MaxCommentChars, err)
	}
	if body.Comment != atCap {
		t.Errorf("Comment = %d runes, want the trimmed %d", utf8.RuneCountInString(body.Comment), MaxCommentChars)
	}
}

func TestRetractBuild(t *testing.T) {
	body, err := RetractRequest{IssueItemID: 8817, Rows: []int{1042, 1043}}.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if body.IssueItemID != 8817 {
		t.Errorf("IssueItemID = %d", body.IssueItemID)
	}
	if len(body.RowIds) != 2 || body.RowIds[0] != 1042 {
		t.Errorf("RowIds = %v", body.RowIds)
	}
}

func TestRetractBuildRejections(t *testing.T) {
	tooManyRows := make([]int, MaxRows+1)
	for i := range tooManyRows {
		tooManyRows[i] = i + 1
	}
	cases := []struct {
		name string
		req  RetractRequest
	}{
		{"no rows", RetractRequest{IssueItemID: 8817}},
		{"too many rows", RetractRequest{IssueItemID: 8817, Rows: tooManyRows}},
		{"duplicate row", RetractRequest{IssueItemID: 8817, Rows: []int{7, 7}}},
		{"non-positive row", RetractRequest{IssueItemID: 8817, Rows: []int{-1}}},
		{"non-positive issue item", RetractRequest{IssueItemID: 0, Rows: []int{7}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.req.Build()
			if err == nil {
				t.Fatal("expected a usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}
