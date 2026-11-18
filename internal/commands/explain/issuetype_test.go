package explain

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestIssueTypeActionsOnlyMatchesIssueType(t *testing.T) {
	if decorationFor("alert", "msg").valueActions != nil {
		t.Error("only issue_type is decorated")
	}
	if decorationFor("table", "issue_type").valueActions != nil {
		t.Error("only alert and warning have action maps")
	}
	if decorationFor("alert", "issue_type").valueActions == nil {
		t.Error("alert.issue_type must decorate")
	}
	if decorationFor("warning", "issue_type").valueActions == nil {
		t.Error("warning.issue_type must decorate")
	}
}

func TestGeneratedIssueTypeActionsIncludeMinorSpecAdditions(t *testing.T) {
	for _, tc := range []struct {
		resource  string
		issueType string
	}{
		{resource: "alert", issueType: "guest_failure"},
		{resource: "alert", issueType: "composite_run_job_wedged"},
	} {
		actions := decorationFor(tc.resource, "issue_type").valueActions[tc.issueType]
		if len(actions) != 1 || actions[0] != "try_again" {
			t.Errorf("%s.%s actions = %v, want [try_again]", tc.resource, tc.issueType, actions)
		}
	}
	if actions := decorationFor("warning", "issue_type").valueActions["guest_failure"]; len(actions) != 0 {
		t.Errorf("warning.guest_failure actions = %v, want none: no warning offers try_again", actions)
	}
	if _, ok := decorationFor("warning", "issue_type").valueActions["composite_run_job_wedged"]; ok {
		t.Error("composite_run_job_wedged is alert-only but appears in warning actions")
	}
}

func TestPrintFieldTextDecoratesAllowedValues(t *testing.T) {
	var buf bytes.Buffer
	info := &schema.FieldInfo{
		Name: "issue_type",
		Type: "string",
		Enum: []string{"needs_review_migration", "access_denied"},
	}
	printFieldText(&buf, info, 0, decorationFor("alert", "issue_type"))
	out := buf.String()

	if !strings.Contains(out, "needs_review_migration → apply_migration") {
		t.Errorf("migration value not decorated with its actions\n%s", out)
	}
	if !strings.Contains(out, "discard_migration") {
		t.Errorf("the action list is truncated\n%s", out)
	}
	if !strings.Contains(out, "access_denied → try_again") {
		t.Errorf("second value not decorated\n%s", out)
	}
}

func TestPrintFieldTextUndecoratedIsUnchanged(t *testing.T) {
	var buf bytes.Buffer
	info := &schema.FieldInfo{Name: "state", Type: "string", Enum: []string{"a", "b"}}
	printFieldText(&buf, info, 0, fieldDecoration{})
	out := buf.String()
	if !strings.Contains(out, "  - a\n") || !strings.Contains(out, "  - b\n") {
		t.Errorf("an undecorated enum must render exactly as before\n%s", out)
	}
	if strings.Contains(out, "→") {
		t.Errorf("no arrow without an action map\n%s", out)
	}
}

func TestPrintFieldTextSkipsValuesWithNoActions(t *testing.T) {
	var buf bytes.Buffer
	info := &schema.FieldInfo{Name: "issue_type", Enum: []string{"not_a_real_issue_type"}}
	printFieldText(&buf, info, 0, decorationFor("alert", "issue_type"))
	if strings.Contains(buf.String(), "→") {
		t.Errorf("a value the map does not cover renders bare\n%s", buf.String())
	}
}

// alertSchemaWithIssueType is a synthetic OpenAPI document with just enough
// of AlertSchema for the command-level gate tests below: an issue_type enum
// carrying two values api.IssueTypeAlertActions covers (access_denied,
// needs_review_migration — the same two the unit tests above exercise).
const alertSchemaWithIssueType = `{
  "openapi": "3.0.3",
  "info": {"title": "qluster", "version": "0.0.1"},
  "paths": {},
  "components": {
    "schemas": {
      "AlertSchema": {
        "type": "object",
        "description": "An alert.",
        "properties": {
          "issue_type": {
            "type": "string",
            "enum": ["access_denied", "needs_review_migration"],
            "description": "What kind of issue raised this alert."
          }
        }
      }
    }
  }
}`

// TestExplainAlertIssueTypeFrozenFormatGate drives the real command path —
// not printFieldText in isolation, which TestPrintFieldTextUndecoratedIsUnchanged
// already covers — to prove the gate in explainSchemaOnly that withholds
// `actions` under -o json / -o yaml (Decision 9) actually reaches the
// command for the one field this task decorates. The pre-existing frozen-
// output goldens (TestExplainMachineOutputUnchanged, TestExplainOpenAPIMachineOutput)
// cover table-rule, data-review-policy, rule-review-policy, table-rule.spec
// and table — none has an issue_type field and none uses a dotted field
// path, so none of them could have caught a regression that let the arrow
// leak into machine output.
func TestExplainAlertIssueTypeFrozenFormatGate(t *testing.T) {
	for _, format := range []string{explainJSONOutputFormat, "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
				// Unique per subtest: avoids hitting another test's in-memory/disk spec cache.
				testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": "explain-issuetype-gate-" + format})
			})
			mock.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondText(w, http.StatusOK, alertSchemaWithIssueType)
			})

			out, err := execExplain(t, mock, "alert.issue_type", "-o", format)
			if err != nil {
				t.Fatalf("explain alert.issue_type -o %s: %v", format, err)
			}
			if strings.Contains(out, "→") {
				t.Errorf("-o %s must stay frozen (Decision 9): decoration arrow leaked into machine output\n%s", format, out)
			}
			if !strings.Contains(out, "access_denied") || !strings.Contains(out, "needs_review_migration") {
				t.Errorf("-o %s must still list the enum values verbatim\n%s", format, out)
			}
		})
	}
}

// TestExplainAlertIssueTypeTextIsDecorated is the control for the gate test
// above: the same field, same schema, default (text) format — decoration
// must appear here so the frozen-format assertions above are proof of a
// gate, not proof the feature never fires at all.
func TestExplainAlertIssueTypeTextIsDecorated(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": "explain-issuetype-gate-text"})
	})
	mock.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondText(w, http.StatusOK, alertSchemaWithIssueType)
	})

	out, err := execExplain(t, mock, "alert.issue_type")
	if err != nil {
		t.Fatalf("explain alert.issue_type: %v", err)
	}
	if !strings.Contains(out, "access_denied → try_again") {
		t.Errorf("default text format must decorate issue_type; got:\n%s", out)
	}
}

func TestPrintFieldTextNamesEachIssueTypesLabelAndFixOwner(t *testing.T) {
	var labelled string
	for issueType, it := range api.AlertActionCatalog.IssueTypes {
		if it.BusinessLabel != "" && it.FixOwner != "" {
			labelled = issueType
			break
		}
	}
	if labelled == "" {
		t.Fatal("no alert issue type in the catalog has both a business label and a fix owner")
	}
	it := api.AlertActionCatalog.IssueTypes[labelled]

	var buf bytes.Buffer
	info := &schema.FieldInfo{Name: "issue_type", Enum: []string{labelled}}
	printFieldText(&buf, info, 0, decorationFor("alert", "issue_type"))
	want := "      " + it.BusinessLabel + "; fix owner: " + it.FixOwner + "\n"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("%s: want line %q\n%s", labelled, want, buf.String())
	}
}

func TestPrintFieldTextListsTheActionCatalogForActionsList(t *testing.T) {
	for _, tc := range []struct {
		resource, field string
		cat             api.ActionCatalog
	}{
		{"alert", "alert_actions_list", api.AlertActionCatalog},
		{"warning", "warning_actions_list", api.WarningActionCatalog},
	} {
		t.Run(tc.resource, func(t *testing.T) {
			var buf bytes.Buffer
			info := &schema.FieldInfo{Name: tc.field, Type: "array"}
			printFieldText(&buf, info, 0, decorationFor(tc.resource, tc.field))
			out := buf.String()
			if !strings.Contains(out, "\nACTIONS:\n") {
				t.Fatalf("no ACTIONS section\n%s", out)
			}
			for name, entry := range tc.cat.Actions {
				if !strings.Contains(out, "\n  "+name+"\n"+alerts.CatalogNote(entry)) {
					t.Errorf("action %s is not followed by its catalog note", name)
				}
			}
		})
	}
}

func TestOnlyTheActionsListGetsTheActionCatalog(t *testing.T) {
	for _, tc := range []struct{ resource, field string }{
		{"alert", "issue_type"},
		{"alert", "warning_actions_list"},
		{"warning", "alert_actions_list"},
		{"table", "alert_actions_list"},
	} {
		if d := decorationFor(tc.resource, tc.field); len(d.actions) != 0 {
			t.Errorf("%s.%s must not print an ACTIONS section", tc.resource, tc.field)
		}
	}
}
