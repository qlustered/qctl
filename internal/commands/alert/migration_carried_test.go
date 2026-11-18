package alert

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	carriedFitID    = "11111111-1111-1111-1111-111111111111"
	carriedMisfitID = "22222222-2222-2222-2222-222222222222"
)

func registerCarriedSuggestions(mock *testutil.MockAPIServer) api.MigrationDetailResponse {
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	fit := suggestionDetail(carriedFitID, api.SuggestionActionCreate, "Carried fit")
	fit.Source = api.RecreatedPeriod
	misfit := suggestionDetail(carriedMisfitID, api.SuggestionActionCreate, "Carried misfit")
	misfit.Source = api.RecreatedPeriod
	registerPendingSuggestions(mock, fit, misfit)
	draft := deletionDraft()
	draft.Fields = nil
	draft.RecreatedPeriodRules = &[]api.RecreatedPeriodRuleSchema{
		{SuggestionID: fit.ID, Fits: true},
		{SuggestionID: misfit.ID, Fits: false, MissingColumns: []string{"old_column"}},
	}
	return draft
}

func TestApplyMigrationPreviewsCarriedFitsAndMisfits(t *testing.T) {
	for _, file := range []bool{false, true} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(format+map[bool]string{false: "-action", true: "-file"}[file], func(t *testing.T) {
				mock := newAlertTestEnv(t)
				registerMigrationDraft(mock, registerCarriedSuggestions(mock))
				var out, errOut bytes.Buffer
				posts := 0
				mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
					posts++
					preview := out.String()
					if format != "table" {
						preview = errOut.String()
					}
					for _, want := range []string{carriedFitID, "Carried fit (carried from recreated period)", "1 create suggestion(s) will be accepted", carriedMisfitID, "1 carried suggestion(s) do not fit and will be dismissed", "missing columns: old_column"} {
						if !strings.Contains(preview, want) {
							t.Errorf("POST preceded preview %q: %s", want, preview)
						}
					}
					okResolve(w, r)
				})
				cmd := setupAlertTestCommand()
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				cmd.SetArgs(append(migrationResolveArgs(t, file), "--yes", "-o", format))
				if err := cmd.Execute(); err != nil || posts != 1 {
					t.Fatalf("resolve: %v, posts = %d", err, posts)
				}
				if format != "table" && strings.Contains(out.String(), "Carried") {
					t.Fatalf("preview polluted structured stdout: %s", out.String())
				}
				reads := 0
				for _, request := range mock.Requests() {
					if request == "GET "+migrationSchemaPath {
						reads++
					}
				}
				if reads != 1 {
					t.Fatalf("migration reads = %d, want shared key-step read", reads)
				}
			})
		}
	}
}

func TestApplyMigrationCarriedDecisions(t *testing.T) {
	for _, decision := range []string{"accept misfit", "accept fit", "dismiss misfit"} {
		t.Run(decision, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerMigrationDraft(mock, registerCarriedSuggestions(mock))
			field, id := "accept_suggestion_ids", carriedMisfitID
			if decision == "accept fit" {
				id = carriedFitID
			}
			if decision == "dismiss misfit" {
				field = "dismiss_suggestion_ids"
			}
			posts := 0
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", func(w http.ResponseWriter, r *http.Request) {
				posts++
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				var ids []uuid.UUID
				if err := json.Unmarshal(body[field], &ids); err != nil || len(ids) != 1 || ids[0].String() != id {
					t.Errorf("decision payload %s: %s (%v)", field, body[field], err)
				}
				okResolve(w, r)
			})
			doc := "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n  " + field + ": [" + id + "]\n"
			out, err := runAlertCommand(t, "alert", "resolve", "-f", writeManifest(t, doc), "--yes")
			if decision == "accept misfit" {
				if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || posts != 0 {
					t.Fatalf("accept misfit: %v, posts = %d; want exit 2 before POST", err, posts)
				}
				for _, want := range []string{carriedMisfitID, "old_column", "spec.accept_suggestion_ids"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error missing %q: %v", want, err)
					}
				}
			} else if err != nil || posts != 1 {
				t.Fatalf("resolve: %v, posts = %d, output = %s", err, posts, out)
			}
		})
	}
}

func TestApplyMigrationDoesNotTrustUnavailableCarriedFits(t *testing.T) {
	for _, problem := range []string{"read failed", "different draft", "different table", "current schema", "missing fit"} {
		t.Run(problem, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			draft := registerCarriedSuggestions(mock)
			switch problem {
			case "different draft":
				draft.MigrationID++
			case "different table":
				draft.DatasetID++
			case "current schema":
				draft.State, draft.MigrationState = api.MigrationStateCurrent, api.CurrentOnly
			case "missing fit":
				draft.RecreatedPeriodRules = nil
			}
			registerMigrationDraft(mock, draft)
			if problem == "read failed" {
				mock.RegisterHandler("GET", migrationSchemaPath, testutil.MockUnauthorizedHandler())
			}
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/alerts/resolve/9", okResolve)
			out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--yes")
			if err != nil || !strings.Contains(out, "Could not check the fit of 2 carried suggestion(s)") || strings.Contains(out, "will be accepted") {
				t.Fatalf("unavailable fit must not promise acceptance: %v, output = %s", err, out)
			}
		})
	}
}
