package alert

import (
	"bytes"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	migrationSchemaPath = "/api/orgs/" + testOrgID + "/datasets/42/migration-details"
	migrationRulesPath  = "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules"
	migrationBuiltinID  = "11111111-1111-4111-8111-111111111111"
	migrationCustomID   = "22222222-2222-4222-8222-222222222222"
)

func deletionDraft() api.MigrationDetailResponse {
	return api.MigrationDetailResponse{
		DatasetID: 42, MigrationID: 3, State: api.MigrationStateDraft, MigrationState: api.DraftWithCurrent,
		Fields: map[string]api.FieldMigrationInfo{
			"amount": {Current: &api.FieldDatabaseDetailDict{}, New: &api.FieldDatabaseDetailDict{IsDeleted: testutil.BoolPtr(true)}},
			"absent": {Current: &api.FieldDatabaseDetailDict{}},
			"kept":   {Current: &api.FieldDatabaseDetailDict{}, New: &api.FieldDatabaseDetailDict{}},
		},
	}
}

func registerMigrationDraft(mock *testutil.MockAPIServer, detail api.MigrationDetailResponse) {
	mock.RegisterHandler("GET", migrationSchemaPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, detail)
	})
}

func migrationResolveArgs(t *testing.T, file bool) []string {
	t.Helper()
	if file {
		return []string{"alert", "resolve", "-f", writeManifest(t,
			"apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n")}
	}
	return []string{"alert", "resolve", "9", "--action", "apply_migration"}
}

func migrationFilterRule(id, name, column string) api.DatasetRuleTinySchema {
	return api.DatasetRuleTinySchema{
		ID: uuid.MustParse(id), InstanceName: name, State: api.DatasetRuleStateEnabled,
		RowFilter: &api.RowFilter{Conditions: &[]api.RowFilterCondition{{ColumnName: column, Operator: "is_blank"}}},
	}
}

func registerMigrationRuleDetail(mock *testutil.MockAPIServer, id string, builtin bool) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+id, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleDetailSchema{
			ID: uuid.MustParse(id), IsBuiltin: builtin, State: api.DatasetRuleStateEnabled,
		})
	})
}

func TestApplyMigrationDeletedColumnRulesPreflight(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(format+map[bool]string{false: "-action", true: "-file"}[file], func(t *testing.T) {
				mock := newAlertTestEnv(t)
				registerResolvingGetAlert(mock, migrationAlert())
				registerEmptySuggestions(mock)
				registerMigrationDraft(mock, deletionDraft())
				builtin := migrationFilterRule(migrationBuiltinID, "builtin_check", "amount")
				builtin.DatasetColumns = &[]string{"amount"}
				custom := migrationFilterRule(migrationCustomID, "custom_check\x1b[31m", "kept")
				custom.RowFilter.Predicate = &api.RowFilterPredicate{Source: "def applies(row): return True", Columns: []string{"absent"}}
				paused := migrationFilterRule("33333333-3333-4333-8333-333333333333", "paused", "amount")
				paused.State = api.DatasetRuleStateDisabled
				gone := migrationFilterRule("44444444-4444-4444-8444-444444444444", "gone", "amount")
				gone.State = api.DatasetRuleStateDeleted
				pages := 0
				total := 4
				mock.RegisterHandler("GET", migrationRulesPath, func(w http.ResponseWriter, r *http.Request) {
					pages++
					if r.URL.Query().Get("page") == "2" {
						testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleListSchema{Results: []api.DatasetRuleTinySchema{custom, paused, gone}, TotalRows: &total})
						return
					}
					testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleListSchema{
						Results: []api.DatasetRuleTinySchema{builtin}, TotalRows: &total,
					})
				})
				registerMigrationRuleDetail(mock, migrationBuiltinID, true)
				registerMigrationRuleDetail(mock, migrationCustomID, false)
				var out, errOut bytes.Buffer
				sent := false
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
					sent = true
					for _, want := range []string{
						"builtin_check", "retires (deletes) this enabled builtin rule", "custom_check\\x1b[31m", "validation_problem blocker",
						"its row filter reads absent", "paused", "not enabled", "a table rule maps", "maps amount",
					} {
						if !strings.Contains(errOut.String(), want) {
							t.Errorf("POST preceded warning %q: %s", want, errOut.String())
						}
					}
					testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
				})
				cmd := setupAlertTestCommand()
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs(append(migrationResolveArgs(t, file), "--yes", "-o", format))
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if !sent || pages != 2 || strings.Contains(out.String(), "Warning:") || strings.Contains(errOut.String(), "gone") || strings.ContainsRune(errOut.String(), '\x1b') {
					t.Fatalf("sent = %v, pages = %d, stdout = %s, stderr = %s", sent, pages, out.String(), errOut.String())
				}
				requests := mock.Requests()
				for _, id := range []string{migrationBuiltinID, migrationCustomID} {
					request := "GET /api/orgs/" + testOrgID + "/dataset-rules/" + id
					n := 0
					for _, got := range requests {
						if got == request {
							n++
						}
					}
					if n != 1 {
						t.Errorf("detail reads of %s = %d, want 1", id, n)
					}
				}
			})
		}
	}
}

func TestApplyMigrationRuleCheckUnavailableStillResolves(t *testing.T) {
	for _, problem := range []string{"schema lookup", "current schema", "different draft", "different table", "no migration id", "rule list", "rule detail"} {
		t.Run(problem, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			alert := migrationAlert()
			if problem == "no migration id" {
				alert.MigrationModelID = nil
			}
			registerResolvingGetAlert(mock, alert)
			registerEmptySuggestions(mock)
			draft := deletionDraft()
			switch problem {
			case "current schema":
				draft.State, draft.MigrationState = api.MigrationStateCurrent, api.CurrentOnly
			case "different draft":
				draft.MigrationID++
			case "different table":
				draft.DatasetID++
			}
			registerMigrationDraft(mock, draft)
			if problem == "schema lookup" {
				mock.RegisterHandler("GET", migrationSchemaPath, testutil.MockUnauthorizedHandler())
			}
			mock.RegisterHandler("GET", migrationRulesPath, func(w http.ResponseWriter, _ *http.Request) {
				if problem == "rule list" {
					testutil.RespondError(w, http.StatusForbidden, "cannot list rules")
					return
				}
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleListSchema{
					Results: []api.DatasetRuleTinySchema{migrationFilterRule(migrationBuiltinID, "unknown_check", "amount")},
				})
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dataset-rules/"+migrationBuiltinID, testutil.MockUnauthorizedHandler())
			sent := false
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
				sent = true
				testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
			})
			out, err := runAlertCommand(t, append(migrationResolveArgs(t, true), "--yes")...)
			if code := qerrors.GetExitCode(err); code != 0 || !sent || !strings.Contains(out, "Warning:") {
				t.Fatalf("exit = %d, sent = %v, err = %v, output = %s", code, sent, err, out)
			}
			if problem == "rule detail" {
				if !strings.Contains(out, "Whether the rule is builtin could not be read") || !strings.Contains(out, "if this rule stays enabled with this filter") {
					t.Fatalf("missing conditional outcomes: %s", out)
				}
			} else if problem != "rule list" && slices.Contains(mock.Requests(), "GET "+migrationRulesPath) {
				t.Fatal("read rules from an unavailable or mismatched draft")
			}
		})
	}
}

func TestMigrationRuleCheckOnlyForDraftDeletions(t *testing.T) {
	for _, mode := range []string{"no deletion", "other action", "dry-run", "confirmation required"} {
		t.Run(mode, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerResolvingGetAlert(mock, migrationAlert())
			registerEmptySuggestions(mock)
			draft := deletionDraft()
			if mode == "no deletion" {
				draft.Fields = map[string]api.FieldMigrationInfo{"kept": {New: &api.FieldDatabaseDetailDict{}}}
			}
			registerMigrationDraft(mock, draft)
			mock.RegisterHandler("GET", migrationRulesPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DatasetRuleListSchema{
					Results: []api.DatasetRuleTinySchema{migrationFilterRule(migrationBuiltinID, "builtin_check", "amount")},
				})
			})
			registerMigrationRuleDetail(mock, migrationBuiltinID, true)
			sent := false
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, _ *http.Request) {
				sent = true
				testutil.RespondJSON(w, http.StatusOK, api.AlertResolveResponseSchema{})
			})
			args := migrationResolveArgs(t, false)
			if mode == "other action" {
				args[len(args)-1] = "discard_migration"
			}
			if mode == "dry-run" {
				args = append(args, "--dry-run")
			} else if mode != "confirmation required" {
				args = append(args, "--yes")
			}
			out, err := runAlertCommand(t, args...)
			wantCode := 0
			if mode == "confirmation required" {
				wantCode = 2
			}
			if code := qerrors.GetExitCode(err); code != wantCode || sent != (mode == "no deletion" || mode == "other action") {
				t.Fatalf("exit = %d, sent = %v, err = %v, output = %s", code, sent, err, out)
			}
			if checked := slices.Contains(mock.Requests(), "GET "+migrationSchemaPath); checked != (mode != "other action") {
				t.Fatalf("schema checked = %v; requests = %v", checked, mock.Requests())
			}
			if listed := slices.Contains(mock.Requests(), "GET "+migrationRulesPath); listed != (mode == "confirmation required") {
				t.Fatalf("rules listed = %v; requests = %v", listed, mock.Requests())
			}
			if warned := strings.Contains(out, "retires (deletes) this enabled builtin rule"); warned != (mode == "confirmation required") {
				t.Fatalf("warned = %v; output = %s", warned, out)
			}
		})
	}
}
