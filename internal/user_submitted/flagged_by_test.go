package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

var (
	flagDana = api.UserInfoTinySchema{ID: uuid.MustParse("00000000-0000-0000-0000-00000000000d"), FirstName: "Dana", LastName: "Ray", Email: "dana@x.io"}
	flagSam  = api.UserInfoTinySchema{ID: uuid.MustParse("00000000-0000-0000-0000-00000000000e"), FirstName: "Sam", LastName: "Ortiz", Email: "sam@x.io"}

	flagThreadA = uuid.MustParse("0190aaaa-0000-0000-0000-00000000000a")
	flagThreadB = uuid.MustParse("0190bbbb-0000-0000-0000-00000000000b")

	flagCreated = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	// 23:30 at UTC-2 is the next UTC day: the marker date must be UTC.
	flagRetracted = time.Date(2026, 10, 13, 23, 30, 0, 0, time.FixedZone("x", -2*60*60))
	flagDetached  = time.Date(2026, 10, 15, 8, 0, 0, 0, time.UTC)
)

func flag(by api.UserInfoTinySchema, row int, thread *uuid.UUID, fields ...string) api.UserSubmittedIssueReadSchema {
	return api.UserSubmittedIssueReadSchema{
		ID:          uuid.New(),
		RowID:       row,
		FieldNames:  fields,
		SubmittedBy: by,
		ThreadID:    thread,
		CreatedAt:   flagCreated,
	}
}

func retracted(i api.UserSubmittedIssueReadSchema) api.UserSubmittedIssueReadSchema {
	t := flagRetracted
	i.RetractedAt = &t
	return i
}

func detached(i api.UserSubmittedIssueReadSchema) api.UserSubmittedIssueReadSchema {
	t := flagDetached
	i.DetachedAt = &t
	return i
}

func TestRenderFlaggedByPlain(t *testing.T) {
	tests := []struct {
		name   string
		issues []api.UserSubmittedIssueReadSchema
		want   string
	}{
		{
			name: "empty",
			want: "",
		},
		{
			name: "groups by flagger and thread, rows ascending, fields unioned in first-seen order",
			issues: []api.UserSubmittedIssueReadSchema{
				flag(flagDana, 1043, &flagThreadA, "price"),
				flag(flagSam, 1050, &flagThreadB, "price", "qty"),
				flag(flagDana, 1042, &flagThreadA, "qty", "price"),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray <dana@x.io>: rows 1042, 1043 (price, qty) — comment-thread/" + flagThreadA.String() + "\n" +
				"  Sam Ortiz <sam@x.io>: row 1050 (price, qty) — comment-thread/" + flagThreadB.String() + "\n" +
				"Next: qctl describe comment-thread " + flagThreadA.String() + " --table-id 42\n",
		},
		{
			name: "retracted and detached are their own groups after the live ones, retracted wins, Next prefers a live thread",
			issues: []api.UserSubmittedIssueReadSchema{
				retracted(flag(flagSam, 1050, &flagThreadB, "price")),
				detached(flag(flagDana, 1060, &flagThreadA, "price")),
				detached(retracted(flag(flagSam, 1051, &flagThreadB, "price"))),
				flag(flagDana, 1042, &flagThreadA, "price"),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray <dana@x.io>: row 1042 (price) — comment-thread/" + flagThreadA.String() + "\n" +
				"  Sam Ortiz <sam@x.io>: rows 1050, 1051 (price) — comment-thread/" + flagThreadB.String() + " [retracted 2026-10-14]\n" +
				"  Dana Ray <dana@x.io>: row 1060 (price) — comment-thread/" + flagThreadA.String() + " [detached 2026-10-15]\n" +
				"Next: qctl describe comment-thread " + flagThreadA.String() + " --table-id 42\n",
		},
		{
			name: "Next falls back to the first printed thread when no live group has one",
			issues: []api.UserSubmittedIssueReadSchema{
				flag(flagDana, 1042, nil, "price"),
				retracted(flag(flagSam, 1050, &flagThreadB, "qty")),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray <dana@x.io>: row 1042 (price)\n" +
				"  Sam Ortiz <sam@x.io>: row 1050 (qty) — comment-thread/" + flagThreadB.String() + " [retracted 2026-10-14]\n" +
				"Next: qctl describe comment-thread " + flagThreadB.String() + " --table-id 42\n",
		},
		{
			name: "a flagger's role sits between the name and the email",
			issues: []api.UserSubmittedIssueReadSchema{
				flag(api.UserInfoTinySchema{ID: flagDana.ID, FirstName: "Dana", LastName: "Ray", Email: "dana@x.io", Role: rolePtr(api.RoleEnumExternal)}, 1042, nil, "price"),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray (external) <dana@x.io>: row 1042 (price)\n",
		},
		{
			name: "no thread anywhere: no tail and no Next line",
			issues: []api.UserSubmittedIssueReadSchema{
				flag(flagDana, 1042, nil, "price"),
				flag(flagDana, 1041, nil, "price"),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray <dana@x.io>: rows 1041, 1042 (price)\n",
		},
		{
			name: "server strings are sanitized onto one line",
			issues: []api.UserSubmittedIssueReadSchema{
				flag(flagDana, 1042, nil, "pri\x1b[2Jce", "a\nb"),
			},
			want: "\nFLAGGED BY\n" +
				"  Dana Ray <dana@x.io>: row 1042 (pri\\x1b[2Jce, a\\nb)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RenderFlaggedByPlain(tt.issues, 42); got != tt.want {
				t.Errorf("RenderFlaggedByPlain =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

// Twelve groups print ten lines and fold two. The live groups come last in the
// input and print first, so the retracted ones are what fold, and Next names
// the live thread.
func TestRenderFlaggedByPlainCapsTheGroups(t *testing.T) {
	var issues []api.UserSubmittedIssueReadSchema
	for i := 0; i < 10; i++ {
		issues = append(issues, retracted(flag(flagDana, 2000+i, &flagThreadA, "price")))
		// Each retraction lands on its own day, so each is its own group.
		at := flagRetracted.Add(time.Duration(i) * 24 * time.Hour)
		issues[i].RetractedAt = &at
	}
	issues = append(issues, flag(flagSam, 3000, &flagThreadB, "qty"), flag(flagSam, 3001, nil, "qty"))

	want := "\nFLAGGED BY\n" +
		"  Sam Ortiz <sam@x.io>: row 3000 (qty) — comment-thread/" + flagThreadB.String() + "\n" +
		"  Sam Ortiz <sam@x.io>: row 3001 (qty)\n"
	for i := 0; i < 8; i++ {
		want += fmt.Sprintf("  Dana Ray <dana@x.io>: row %d (price) — comment-thread/%s [retracted 2026-10-%d]\n",
			2000+i, flagThreadA, 14+i)
	}
	want += "  +2 more (see -o json)\n"
	want += "Next: qctl describe comment-thread " + flagThreadB.String() + " --table-id 42\n"

	if got := RenderFlaggedByPlain(issues, 42); got != want {
		t.Errorf("RenderFlaggedByPlain =\n%s\nwant\n%s", got, want)
	}
}
