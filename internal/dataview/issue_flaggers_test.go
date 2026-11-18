package dataview

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/qlustered/qctl/internal/api"
)

// msgsOnly hydrates ids with messages and no flags.
func msgsOnly(m map[int]string) map[int]IssueMsg {
	out := make(map[int]IssueMsg, len(m))
	for id, msg := range m {
		out[id] = IssueMsg{Msg: msg}
	}
	return out
}

var flagEpoch = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// flagBy is a live flag by user n on rowID, created offset minutes after
// flagEpoch.
func flagBy(n int, first, last string, rowID, offset int) api.UserSubmittedIssueReadSchema {
	return api.UserSubmittedIssueReadSchema{
		ID:    uuid.New(),
		RowID: rowID,
		SubmittedBy: api.UserInfoTinySchema{
			ID:        uuid.MustParse(fmt.Sprintf("00000000-0000-4000-8000-%012d", n)),
			FirstName: first,
			LastName:  last,
			Email:     fmt.Sprintf("user%d@x.com", n),
		},
		CreatedAt: flagEpoch.Add(time.Duration(offset) * time.Minute),
	}
}

// renderFlagged renders issueSchema's page (rows 8943 and 8944; A8817, A8818,
// A8819 and W55 on row 8943) with alert 8817 carrying flags.
func renderFlagged(t *testing.T, flags ...api.UserSubmittedIssueReadSchema) string {
	t.Helper()
	page, _ := Normalize(issueSchema(), NormalizeOptions{RequestedPage: 1})
	AttachIssueMessages(page, map[int]IssueMsg{
		8817: {Msg: "Not a valid email", Flags: flags},
		8818: {Msg: "Unexpected value"},
	}, msgsOnly(map[int]string{55: "Anomalous value"}))
	var b strings.Builder
	if err := Render(&b, page, nil); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return b.String()
}

// dictLine returns the dictionary line for label, or "" if there is none.
func dictLine(out, label string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "- "+label+": ") {
			return line
		}
	}
	return ""
}

func TestIssueDictionaryNamesALiveFlaggerOnAPageRow(t *testing.T) {
	out := renderFlagged(t, flagBy(1, "Ann", "Lee", 8943, 0))
	if got, want := dictLine(out, "A8817"), "- A8817: Not a valid email — flagged by Ann Lee"; got != want {
		t.Errorf("A8817 line = %q, want %q\n%s", got, want, out)
	}
	// Only the flagged aggregate gains a suffix.
	if got := dictLine(out, "A8818"); got != "- A8818: Unexpected value" {
		t.Errorf("A8818 line = %q", got)
	}
	if got := dictLine(out, "W55"); got != "- W55: Anomalous value" {
		t.Errorf("W55 line = %q", got)
	}
}

// A flag names the row it was raised on. One aggregate can hold flags on many
// rows, and a flag on a row this page does not show is not named.
func TestIssueDictionaryIgnoresAFlagOnARowOffThePage(t *testing.T) {
	out := renderFlagged(t, flagBy(1, "Ann", "Lee", 9999, 0))
	if got := dictLine(out, "A8817"); got != "- A8817: Not a valid email" {
		t.Errorf("A8817 line = %q, want no flaggers\n%s", got, out)
	}
}

func TestIssueDictionaryIgnoresRetractedAndDetachedFlags(t *testing.T) {
	retracted := flagBy(1, "Ann", "Lee", 8943, 0)
	at := flagEpoch.Add(time.Hour)
	retracted.RetractedAt = &at
	detached := flagBy(2, "Bo", "Diaz", 8943, 1)
	detached.DetachedAt = &at

	out := renderFlagged(t, retracted, detached)
	if got := dictLine(out, "A8817"); got != "- A8817: Not a valid email" {
		t.Errorf("A8817 line = %q, want no flaggers\n%s", got, out)
	}
}

// Flaggers are distinct by user id, in order of each one's earliest flag on
// this page.
func TestIssueDictionaryNamesTwoFlaggersOnceEach(t *testing.T) {
	out := renderFlagged(t,
		flagBy(2, "Bo", "Diaz", 8943, 0),
		flagBy(1, "Ann", "Lee", 8944, 1),
		flagBy(2, "Bo", "Diaz", 8944, 2),
	)
	if got, want := dictLine(out, "A8817"), "- A8817: Not a valid email — flagged by Bo Diaz, Ann Lee"; got != want {
		t.Errorf("A8817 line = %q, want %q", got, want)
	}
}

func TestIssueDictionaryCountsFlaggersPastThree(t *testing.T) {
	var flags []api.UserSubmittedIssueReadSchema
	for n := 1; n <= 5; n++ {
		flags = append(flags, flagBy(n, fmt.Sprintf("User%d", n), "", 8943, n))
	}
	out := renderFlagged(t, flags...)
	if got, want := dictLine(out, "A8817"), "- A8817: Not a valid email — flagged by User1, User2, User3 +2 more"; got != want {
		t.Errorf("A8817 line = %q, want %q", got, want)
	}
}

// A name is user-chosen text. A pipe, a line break or Markdown syntax in it
// must stay literal and on the one dictionary line.
func TestIssueDictionaryEscapesAFlaggerName(t *testing.T) {
	out := renderFlagged(t, flagBy(1, "Ann | `x`\n# *Bo*", "Lee", 8943, 0))
	want := "- A8817: Not a valid email — flagged by Ann \\| \\`x\\`\\n# \\*Bo\\* Lee"
	if got := dictLine(out, "A8817"); got != want {
		t.Errorf("A8817 line = %q, want %q\n%s", got, want, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "# ") && strings.Contains(line, "Bo") {
			t.Errorf("a name started a Markdown block: %q\n%s", line, out)
		}
	}
}

// An id the server did not answer for carries no flags: its line is today's.
func TestIssueDictionaryUnansweredIDKeepsItsLine(t *testing.T) {
	out := renderFlagged(t, flagBy(1, "Ann", "Lee", 8943, 0))
	if got := dictLine(out, "A8819"); got != "- A8819: (no message returned by the server)" {
		t.Errorf("A8819 line = %q", got)
	}
}

// A flagger's role follows the name, and the Markdown escaping covers it too.
func TestIssueDictionaryNamesAFlaggerRole(t *testing.T) {
	external := flagBy(1, "Ann", "Lee", 8943, 0)
	role := api.RoleEnumExternal
	external.SubmittedBy.Role = &role
	odd := flagBy(2, "Bo", "Diaz", 8943, 1)
	oddRole := api.RoleEnum("*x*")
	odd.SubmittedBy.Role = &oddRole

	out := renderFlagged(t, external, odd)
	want := "- A8817: Not a valid email — flagged by Ann Lee (external), Bo Diaz (\\*x\\*)"
	if got := dictLine(out, "A8817"); got != want {
		t.Errorf("A8817 line = %q, want %q\n%s", got, want, out)
	}
}
