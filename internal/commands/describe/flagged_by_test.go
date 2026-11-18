package describe

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/warnings"
	"gopkg.in/yaml.v3"
)

var (
	flaggedByThread = uuid.MustParse("0190aaaa-1111-2222-3333-444444444444")
	flaggedByUser   = api.UserInfoTinySchema{
		ID:        uuid.MustParse("00000000-0000-0000-0000-00000000000d"),
		FirstName: "Dana",
		LastName:  "Ray",
		Email:     "dana@x.io",
	}
)

// flaggedByIssues is one live flag on two rows and one retracted flag with no
// thread.
func flaggedByIssues() *[]api.UserSubmittedIssueReadSchema {
	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.FixedZone("x", 2*60*60))
	retractedAt := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	return &[]api.UserSubmittedIssueReadSchema{
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000001"),
			RowID:       1043,
			FieldNames:  []string{"price"},
			SubmittedBy: flaggedByUser,
			ThreadID:    &flaggedByThread,
			CreatedAt:   created,
		},
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000002"),
			RowID:       1042,
			FieldNames:  []string{"price"},
			SubmittedBy: flaggedByUser,
			ThreadID:    &flaggedByThread,
			CreatedAt:   created,
		},
		{
			ID:          uuid.MustParse("00000000-0000-0000-0000-000000000003"),
			RowID:       1050,
			FieldNames:  []string{"qty"},
			SubmittedBy: flaggedByUser,
			CreatedAt:   created,
			RetractedAt: &retractedAt,
		},
	}
}

// flaggedByFooter is the plain FLAGGED BY section for flaggedByIssues on a
// dataset with the given id.
func flaggedByFooter(datasetID string) string {
	return "\nFLAGGED BY\n" +
		"  Dana Ray <dana@x.io>: rows 1042, 1043 (price) — comment-thread/" + flaggedByThread.String() + "\n" +
		"  Dana Ray <dana@x.io>: row 1050 (qty) [retracted 2026-10-14]\n" +
		"Next: qctl describe comment-thread " + flaggedByThread.String() + " --table-id " + datasetID + "\n"
}

func commented(block string) string {
	lines := strings.Split(block, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = "# " + l
		}
	}
	return strings.Join(lines, "\n")
}

func flaggedAlert() alerts.AlertFull {
	a := sampleAlertFull()
	a.UserSubmittedIssues = flaggedByIssues()
	return a
}

func TestDescribeAlertCarriesUserSubmittedIssuesAtTheDefaultTier(t *testing.T) {
	out := runDescribeAlert(t, flaggedAlert(), "describe", "alert", "1", "-o", "json")

	var doc struct {
		Spec struct {
			UserSubmittedIssues []map[string]any `json:"user_submitted_issues"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("output did not parse as JSON: %v\n%s", err, out)
	}
	got, err := json.Marshal(doc.Spec.UserSubmittedIssues)
	if err != nil {
		t.Fatal(err)
	}
	want := `[` +
		`{"created_at":"2026-10-01T07:00:00Z","field_names":["price"],"id":"00000000-0000-0000-0000-000000000001","row_id":1043,` +
		`"submitted_by":{"email":"dana@x.io","first_name":"Dana","id":"00000000-0000-0000-0000-00000000000d","last_name":"Ray"},"thread_id":"` + flaggedByThread.String() + `"},` +
		`{"created_at":"2026-10-01T07:00:00Z","field_names":["price"],"id":"00000000-0000-0000-0000-000000000002","row_id":1042,` +
		`"submitted_by":{"email":"dana@x.io","first_name":"Dana","id":"00000000-0000-0000-0000-00000000000d","last_name":"Ray"},"thread_id":"` + flaggedByThread.String() + `"},` +
		`{"created_at":"2026-10-01T07:00:00Z","field_names":["qty"],"id":"00000000-0000-0000-0000-000000000003","retracted_at":"2026-10-14T12:00:00Z","row_id":1050,` +
		`"submitted_by":{"email":"dana@x.io","first_name":"Dana","id":"00000000-0000-0000-0000-00000000000d","last_name":"Ray"}}` +
		`]`
	if string(got) != want {
		t.Errorf("spec.user_submitted_issues =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(out, "FLAGGED BY") {
		t.Errorf("-o json must carry no footer, got:\n%s", out)
	}
}

func TestDescribeAlertOmitsUserSubmittedIssuesWhenThereAreNone(t *testing.T) {
	out := runDescribeAlert(t, sampleAlertFull(), "describe", "alert", "1")
	for _, unwanted := range []string{"user_submitted_issues", "FLAGGED BY"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("output should not contain %q, got:\n%s", unwanted, out)
		}
	}
}

func TestDescribeAlertFlaggedByFooter(t *testing.T) {
	yamlOut := runDescribeAlert(t, flaggedAlert(), "describe", "alert", "1")
	if !strings.HasSuffix(yamlOut, commented(flaggedByFooter("10"))) {
		t.Errorf("default view should end in the commented FLAGGED BY footer, got:\n%s", yamlOut)
	}

	tableOut := runDescribeAlert(t, flaggedAlert(), "describe", "alert", "1", "-o", "table")
	if !strings.HasSuffix(tableOut, flaggedByFooter("10")) {
		t.Errorf("-o table should end in the plain FLAGGED BY footer, got:\n%s", tableOut)
	}
}

func TestDescribeAlertTableSummarizesLargeFlagLists(t *testing.T) {
	a := flaggedAlert()
	issues := make([]api.UserSubmittedIssueReadSchema, 1000)
	for i := range issues {
		issues[i] = (*a.UserSubmittedIssues)[0]
		id := uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1))
		issues[i].ID = id
		issues[i].ThreadID = &id
		issues[i].RowID = i + 1
	}
	a.UserSubmittedIssues = &issues
	for _, verbosity := range []string{"", "-v"} {
		t.Run(verbosity, func(t *testing.T) {
			args := []string{"describe", "alert", "1", "-o", "table", "--max-column-width", "0"}
			if verbosity != "" {
				args = append(args, verbosity)
			}
			out := runDescribeAlert(t, a, args...)
			if strings.Contains(out, "user_submitted_issues") || strings.Contains(out, issues[999].ID.String()) {
				t.Errorf("table contains the full flag list (first 1000 bytes):\n%.1000s", out)
			}
			for _, want := range []string{
				"SPEC", `"issue_type":"MISSING_COLUMN"`, "FLAGGED BY",
				"  +990 more (see -o json)\n",
				"Next: qctl describe comment-thread " + issues[0].ThreadID.String() + " --table-id 10\n",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("table is missing %q (first 1000 bytes):\n%.1000s", want, out)
				}
			}
		})
	}
}

func TestDescribeAlertPreservesFlagsInStructuredAndRawViews(t *testing.T) {
	for _, verbosity := range []string{"", "-v", "-vv"} {
		for _, format := range []string{"json", "yaml", "table"} {
			if format == "table" && verbosity != "-vv" {
				continue
			}
			t.Run(format+verbosity, func(t *testing.T) {
				args := []string{"describe", "alert", "1", "-o", format, "--max-column-width", "0"}
				if verbosity != "" {
					args = append(args, verbosity)
				}
				out := runDescribeAlert(t, flaggedAlert(), args...)
				if format == "table" {
					if !strings.Contains(out, `"user_submitted_issues":[`) {
						t.Errorf("raw table lost the flag list:\n%s", out)
					}
					return
				}
				var doc struct {
					Spec        map[string]any `yaml:"spec"`
					RawResponse map[string]any `yaml:"raw_response"`
				}
				// YAML also parses JSON and ignores the YAML footer comments.
				if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
					t.Fatalf("cannot parse output: %v", err)
				}
				fields := doc.Spec
				if verbosity == "-vv" {
					fields = doc.RawResponse
				}
				issues, ok := fields["user_submitted_issues"].([]any)
				if !ok || len(issues) != len(*flaggedByIssues()) {
					t.Errorf("user_submitted_issues lost flags:\n%s", out)
				}
			})
		}
	}
}

func TestDescribeWarningFlaggedByFooter(t *testing.T) {
	w := describeWarningFixture(true)
	w.UserSubmittedIssues = flaggedByIssues()

	yamlOut := runDescribeWarning(t, w, "describe", "warning", "55")
	if !strings.HasSuffix(yamlOut, commented(flaggedByFooter("42"))) {
		t.Errorf("default view should end in the commented FLAGGED BY footer, got:\n%s", yamlOut)
	}

	tableOut := runDescribeWarning(t, w, "describe", "warning", "55", "-o", "table")
	if !strings.HasSuffix(tableOut, flaggedByFooter("42")) {
		t.Errorf("-o table should end in the plain FLAGGED BY footer, got:\n%s", tableOut)
	}

	jsonOut := runDescribeWarning(t, w, "describe", "warning", "55", "-o", "json")
	if strings.Contains(jsonOut, "FLAGGED BY") {
		t.Errorf("-o json must carry no footer, got:\n%s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"user_submitted_issues"`) {
		t.Errorf("-o json should still carry the raw list, got:\n%s", jsonOut)
	}
}

// Both wrappers render nothing for a nil resource.
func TestFlaggedByWrappersTolerateNil(t *testing.T) {
	if got := alerts.RenderFlaggedBy(nil); got != "" {
		t.Errorf("alerts.RenderFlaggedBy(nil) = %q", got)
	}
	if got := warnings.RenderFlaggedBy(nil); got != "" {
		t.Errorf("warnings.RenderFlaggedBy(nil) = %q", got)
	}
}
