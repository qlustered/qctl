package describe

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	rsDescribeOrgID  = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	rsDescribeRuleID = "550e8400-e29b-41d4-a716-446655440000"
)

var rsDescribeSuggestionUUID = uuid.MustParse("3f2a0000-1111-2222-3333-444444444444")

func setupRuleSuggestionDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewRuleSuggestionCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func newRuleSuggestionDescribeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", rsDescribeOrgID)
	env.SetupCredential(endpointKey, rsDescribeOrgID, "test-token")
	return mock
}

func sampleFullSuggestion(state api.SuggestionState) api.DatasetRuleSuggestionSchema {
	params := map[string]interface{}{"threshold": float64(0.95)}
	existing := uuid.MustParse(rsDescribeRuleID)
	return api.DatasetRuleSuggestionSchema{
		ID:                    rsDescribeSuggestionUUID,
		Action:                api.SuggestionActionUpdate,
		State:                 state,
		Source:                api.User,
		DatasetID:             8,
		InstanceName:          "high_confidence_dedupe",
		OrganizationID:        uuid.MustParse(rsDescribeOrgID),
		RuleFamilyID:          uuid.MustParse("ffff0000-1111-2222-3333-444444444444"),
		ExistingDatasetRuleID: &existing,
		Params:                &params,
		RuleColumnMapping:     map[string]string{"email": "email_col"},
		CreatedAt:             time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC),
		// Equal to sampleCurrentRule's severity on purpose: a suggestion staged
		// under a draft migration records the POST-APPLY value of the live rule
		// (atlas patch_dataset_rule, capi/capi/views/rule_view.py), so equal is
		// the ordinary case and the diff must stay quiet about it.
		Severity: api.RuleSeverityWarning,
	}
}

func sampleCurrentRule() dataset_rules.DatasetRuleDetail {
	return dataset_rules.DatasetRuleDetail{
		ID:             uuid.MustParse(rsDescribeRuleID),
		InstanceName:   "high_confidence_dedupe",
		DatasetID:      8,
		OrganizationID: uuid.MustParse(rsDescribeOrgID),
		State:          "enabled",
		CreatedAt:      time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 6, 2, 10, 0, 0, 0, time.UTC),
		RuleRevision: api.RuleRevisionSmallSchema{
			ID:      uuid.MustParse("aaaa0000-bbbb-cccc-dddd-eeeeeeee0001"),
			Name:    "dedupe",
			Slug:    "dedupe",
			Release: "1.0.0",
		},
		Params:            map[string]interface{}{"threshold": float64(0.9)},
		ColumnMappingDict: map[string]string{"email": "email_col"},
		Severity:          api.RuleSeverityWarning,
	}
}

func runDescribeRuleSuggestion(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupRuleSuggestionDescribeCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"describe", "rule-suggestion"}, args...))
	err := cmd.Execute()
	return buf.String(), err
}

func TestDescribeRuleSuggestionPending(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleFullSuggestion(api.SuggestionStatePending))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
		})

	out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"# RULE SUGGESTION " + rsDescribeSuggestionUUID.String(),
		"Action: update | State: pending | Source: user",
		"## STAGED VALUES",
		`{"threshold":0.9} -> {"threshold":0.95}`,
		"## NEXT ACTIONS",
		"qctl suggestion accept " + rsDescribeSuggestionUUID.String() + " --table-id 8",
		"qctl suggestion dismiss " + rsDescribeSuggestionUUID.String() + " --table-id 8",
		"qctl guide manage-suggestions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestDescribeRuleSuggestionResolvedHasNoActions(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleFullSuggestion(api.SuggestionStateApplied))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
		})

	out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "(suggestion is applied — nothing to resolve)") {
		t.Errorf("resolved footer missing\n%s", out)
	}
	if strings.Contains(out, "qctl suggestion accept") {
		t.Errorf("resolved suggestion must not offer accept\n%s", out)
	}
}

func TestDescribeRuleSuggestionRawJSON(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleFullSuggestion(api.SuggestionStatePending))
		})

	out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, `"instance_name": "high_confidence_dedupe"`) {
		t.Errorf("-o json must emit the raw object\n%s", out)
	}
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("-o json must not render the text payload\n%s", out)
	}
}

func TestDescribeRuleSuggestionStagedState(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	suggestion := sampleFullSuggestion(api.SuggestionStatePending)
	stagedState := api.DatasetRuleStateDisabled
	suggestion.StagedState = &stagedState
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, suggestion)
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
		})

	out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"## STAGED VALUES",
		`{"threshold":0.9} -> {"threshold":0.95}`,
		`state            : "enabled" -> "disabled"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n---\n%s", want, out)
		}
	}
}

func TestDescribeRuleSuggestionTableIDFlag(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleFullSuggestion(api.SuggestionStatePending))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
		})

	if _, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8"); err != nil {
		t.Fatalf("--table-id must select the table by ID: %v", err)
	}
}

func TestDescribeRuleSuggestionTableFlagIsAName(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	var lookedUp string
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets",
		func(w http.ResponseWriter, r *http.Request) {
			lookedUp = r.URL.Query().Get("name")
			testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
				"results": []map[string]interface{}{{"id": 8, "name": "42"}},
			})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleFullSuggestion(api.SuggestionStatePending))
		})
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
		})

	// A table named "42" is the case one overloaded flag can never serve.
	if _, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table", "42"); err != nil {
		t.Fatalf("--table must be looked up as a name: %v", err)
	}
	if lookedUp != "42" {
		t.Errorf("--table 42 must query name=42, queried %q", lookedUp)
	}
}

func TestDescribeRuleSuggestionRequiresTable(t *testing.T) {
	newRuleSuggestionDescribeEnv(t)
	_, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String())
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("expected table-required error, got: %v", err)
	}
	// <id> is a suggestion UUID here, not a table.
	if strings.Contains(err.Error(), "positional") {
		t.Errorf("this command's positional slot is not a table; the error must not offer one: %v", err)
	}
}

// The rendered payload's footer points at manage-suggestions
// (RenderDetail, internal/rule_suggestions/render.go). The Long help must not
// send the reader somewhere else for the same question.
func TestDescribeRuleSuggestionLongPointsAtManageSuggestions(t *testing.T) {
	long := NewRuleSuggestionCommand().Long
	if !strings.Contains(long, "qctl guide manage-suggestions") {
		t.Errorf("Long should point at manage-suggestions; got:\n%s", long)
	}
	if strings.Contains(long, "qctl guide instantiate-rule") {
		t.Errorf("Long still points at instantiate-rule; got:\n%s", long)
	}
}

// TestDescribeRuleSuggestionSeverityIsDiffedOnlyWhenItDiffers covers both
// halves of the one rule severity follows in the staged diff. It is not the
// same rule the other staged fields follow: params, column_mapping_dict and
// state are nullable and mean "not touched" when NULL, while severity is
// required on the schema and is always SOME value.
//
// The test that matters is the quiet half. A suggestion staged under a draft
// migration carries the live rule's post-apply severity (atlas
// patch_dataset_rule, capi/capi/views/rule_view.py), so printing it
// unconditionally would announce `severity : "warning" -> "warning"` on the
// common case and read as a staged change that accepting would make. Accepting
// writes `existing.severity = suggestion.severity` (suggestion_view.py
// _apply_accept), so the value differing is exactly the condition under which
// accept changes the rule.
func TestDescribeRuleSuggestionSeverityIsDiffedOnlyWhenItDiffers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		severity  api.RuleSeverity
		wantLine  string
		wantAbsen bool
	}{
		{
			name:      "equal to the live rule is not a staged change",
			severity:  api.RuleSeverityWarning,
			wantAbsen: true,
		},
		{
			name:     "an edited severity is what accept would write",
			severity: api.RuleSeverityBlocker,
			wantLine: `severity         : "warning" -> "blocker"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRuleSuggestionDescribeEnv(t)
			suggestion := sampleFullSuggestion(api.SuggestionStatePending)
			suggestion.Severity = tc.severity
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, suggestion)
				})
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
				})

			out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantAbsen {
				if strings.Contains(out, "severity") {
					t.Errorf("severity must not be diffed when it matches the live rule\n---\n%s", out)
				}
				// The rest of the diff still renders: an absent severity line
				// must not be the diff falling over.
				if !strings.Contains(out, `{"threshold":0.9} -> {"threshold":0.95}`) {
					t.Errorf("the params diff went missing with it\n---\n%s", out)
				}
				return
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("output missing %q\n---\n%s", tc.wantLine, out)
			}
		})
	}
}

// TestDescribeRuleSuggestionCreateShowsSeverityOneSided: a create-action
// suggestion references no existing rule, so there is nothing to diff against
// and RenderDetail takes the one-sided branch. severity belongs there because
// accepting a create passes it straight to create_dataset_rule (atlas
// _apply_accept, capi/capi/views/suggestion_view.py) — it is the severity the
// new rule is born with, and no other qctl view shows it before the rule exists.
func TestDescribeRuleSuggestionCreateShowsSeverityOneSided(t *testing.T) {
	mock := newRuleSuggestionDescribeEnv(t)
	suggestion := sampleFullSuggestion(api.SuggestionStatePending)
	suggestion.Action = api.SuggestionActionCreate
	suggestion.ExistingDatasetRuleID = nil
	suggestion.Severity = api.RuleSeverityAsDeclared
	mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, suggestion)
		})

	out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "severity         : as_declared") {
		t.Errorf("a create suggestion must show the severity its rule would be created with\n---\n%s", out)
	}
	if strings.Contains(out, "->") {
		t.Errorf("nothing to diff against, so nothing may render as a diff\n---\n%s", out)
	}
}

// TestDescribeRuleSuggestionRowFilterIsShownOnlyWhenItDiffers: accepting an
// update writes the suggestion's row filter onto the live rule (atlas
// apply_staged_rule_update, cettings/cettings/controllers/dataset_rule_state.py),
// so a filter that differs is a staged change, and one equal to the live
// rule's is not.
func TestDescribeRuleSuggestionRowFilterIsShownOnlyWhenItDiffers(t *testing.T) {
	filter := func(minAmount string) *api.RowFilter {
		description := "Big orders"
		conditions := []api.RowFilterCondition{{ColumnName: "amount", Operator: api.Gte, Value: &minAmount}}
		return &api.RowFilter{Description: &description, Conditions: &conditions}
	}
	for _, tc := range []struct {
		name      string
		staged    *api.RowFilter
		wantBlock string
	}{
		{name: "equal to the live rule is not a staged change", staged: filter("10")},
		{
			name:   "a differing filter is what accept would write",
			staged: filter("100"),
			wantBlock: "row_filter       : the rows kept differ\n" +
				"  live:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"10\"\n" +
				"  staged:\n" +
				"    description: Big orders\n" +
				"    conditions (a row must meet every one):\n" +
				"      - amount gte \"100\"\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRuleSuggestionDescribeEnv(t)
			suggestion := sampleFullSuggestion(api.SuggestionStatePending)
			suggestion.RowFilter = tc.staged
			rule := sampleCurrentRule()
			rule.RowFilter = filter("10")
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, suggestion)
				})
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, rule)
				})

			out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantBlock == "" {
				if strings.Contains(out, "row_filter") {
					t.Errorf("row_filter must not render when it matches the live rule\n---\n%s", out)
				}
				return
			}
			if !strings.Contains(out, tc.wantBlock) {
				t.Errorf("output missing block:\n%s\n---\n%s", tc.wantBlock, out)
			}
		})
	}
}

// A candidate with no run and no queued request reads its table, and names
// the period when it is locked. A run-linked candidate never reads it, and a
// failed read renders the candidate as validating.
func TestDescribeRuleSuggestionCandidateOnALockedPeriod(t *testing.T) {
	const seriesID = "0b8c3f9e-1d2a-4e5f-9a6b-7c8d9e0f1a2b"
	tablePath := "/api/orgs/" + rsDescribeOrgID + "/datasets/8"
	for _, tc := range []struct {
		name      string
		jobID     *int
		table     int
		wantPhase string
		wantReads int
	}{
		{"held", nil, http.StatusOK, "held (table period locked)", 1},
		{"run linked", ptrInt(8814), http.StatusOK, "validating", 0},
		{"table read fails", nil, http.StatusInternalServerError, "validating", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRuleSuggestionDescribeEnv(t)
			yes, no := true, false
			suggestion := sampleFullSuggestion(api.SuggestionStatePending)
			suggestion.IsRevalidationCandidate = &yes
			suggestion.ValidationQueued = &no
			suggestion.ValidationDryRunJobID = tc.jobID
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/datasets/8/rule-suggestions/"+rsDescribeSuggestionUUID.String(),
				func(w http.ResponseWriter, r *http.Request) { testutil.RespondJSON(w, http.StatusOK, suggestion) })
			mock.RegisterHandler("GET", "/api/orgs/"+rsDescribeOrgID+"/dataset-rules/"+rsDescribeRuleID,
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, sampleCurrentRule())
				})
			reads := 0
			mock.RegisterHandler("GET", tablePath, func(w http.ResponseWriter, r *http.Request) {
				reads++
				if tc.table != http.StatusOK {
					testutil.RespondJSON(w, tc.table, map[string]any{"detail": "boom"})
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"id": 8, "name": "orders",
					"series": map[string]any{"id": seriesID, "name": "Monthly bordereau"},
					"period": map[string]any{"period_key": "2026-09", "period_start": "2026-09-01", "period_end": "2026-09-30", "is_locked": true},
				})
			})

			out, err := runDescribeRuleSuggestion(t, rsDescribeSuggestionUUID.String(), "--table-id", "8")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, "Phase            : "+tc.wantPhase+"\n") {
				t.Errorf("output lacks phase %q:\n%s", tc.wantPhase, out)
			}
			if reads != tc.wantReads {
				t.Errorf("table reads = %d, want %d", reads, tc.wantReads)
			}
			if tc.wantPhase == "held (table period locked)" &&
				!strings.Contains(out, "qctl table-series unlock "+seriesID+" --period 2026-09 --yes") {
				t.Errorf("held candidate lacks the unlock command:\n%s", out)
			}
		})
	}
}

func ptrInt(v int) *int { return &v }
