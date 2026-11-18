package alert

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const resolvePath = "/api/orgs/" + testOrgID + "/alerts/resolve/9"

// keyStepDraft is a draft migration of table 42 at key step step, with one
// key candidate and keys as its stored key columns.
func keyStepDraft(step *api.KeyStep, keys ...string) api.MigrationDetailResponse {
	draft := api.MigrationDetailResponse{
		DatasetID: 42, MigrationID: 3, State: api.MigrationStateDraft, MigrationState: api.DraftOnly,
		Fields:  map[string]api.FieldMigrationInfo{"order_id": {New: &api.FieldDatabaseDetailDict{}}},
		KeyStep: step,
	}
	if len(keys) > 0 {
		draft.KeyColumns = &keys
	}
	if step != nil {
		draft.KeyColumnCandidates = &api.KeyColumnCandidatesSchema{
			RowsChecked: 500, WholeFile: true,
			Candidates: []api.KeyColumnSetCountsSchema{{Columns: []string{"order_id"}}},
		}
	}
	return draft
}

func stepPtr(step api.KeyStep) *api.KeyStep { return &step }

// keyStepEnv serves the migration alert, no suggestions and draft, and
// records each resolve body.
func keyStepEnv(t *testing.T, draft *api.MigrationDetailResponse) (*testutil.MockAPIServer, *[]map[string]any) {
	t.Helper()
	mock := newAlertTestEnv(t)
	registerResolvingGetAlert(mock, migrationAlert())
	registerEmptySuggestions(mock)
	if draft != nil {
		registerMigrationDraft(mock, *draft)
	}
	var bodies []map[string]any
	mock.RegisterHandler("POST", resolvePath, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		okResolve(w, r)
	})
	return mock, &bodies
}

func applyMigrationManifest(t *testing.T, extra string) string {
	t.Helper()
	return writeManifest(t, "apiVersion: qluster.ai/v1\nkind: AlertResolution\nmetadata:\n  id: 9\nspec:\n  action: apply_migration\n"+extra)
}

func TestKeyFlagMatrixRefusesBeforeAnyRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"both flags":       {"alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "id", "--no-key-columns"},
		"empty key list":   {"alert", "resolve", "9", "--action", "apply_migration", "--key-columns", ""},
		"another action":   {"alert", "resolve", "9", "--action", "discard_migration", "--key-columns", "id"},
		"keyless on other": {"alert", "resolve", "9", "--action", "rerun_training", "--no-key-columns"},
		"with dry-run":     {"alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "id", "--dry-run"},
		"with -f":          {"alert", "resolve", "-f", "x.yaml", "--no-key-columns"},
		"propose both":     {"alert", "propose", "9", "--action", "apply_migration", "--key-columns", "id", "--no-key-columns"},
		"propose with -f":  {"alert", "propose", "-f", "x.yaml", "--key-columns", "id"},
	} {
		t.Run(name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			_, err := runAlertCommand(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || len(mock.Requests()) != 0 {
				t.Fatalf("exit = %d, requests = %v, err = %v; want exit 2 with no request", code, mock.Requests(), err)
			}
		})
	}
}

func TestApplyMigrationSendsTheKeyChoice(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft api.MigrationDetailResponse
		args  func(t *testing.T) []string
		want  map[string]any
	}{
		{"flag key columns", keyStepDraft(stepPtr(api.Required)),
			func(*testing.T) []string {
				return []string{"alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "order_id,region"}
			},
			map[string]any{"key_columns": []any{"order_id", "region"}}},
		{"flag keyless", keyStepDraft(stepPtr(api.Required)),
			func(*testing.T) []string {
				return []string{"alert", "resolve", "9", "--action", "apply_migration", "--no-key-columns"}
			},
			map[string]any{"no_key_columns": true}},
		{"manifest key columns", keyStepDraft(stepPtr(api.Required)),
			func(t *testing.T) []string {
				return []string{"alert", "resolve", "-f", applyMigrationManifest(t, "  key_columns: [order_id]\n")}
			},
			map[string]any{"key_columns": []any{"order_id"}}},
		{"optional step keeps", keyStepDraft(stepPtr(api.Optional), "order_id"),
			func(*testing.T) []string { return []string{"alert", "resolve", "9", "--action", "apply_migration"} },
			map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, bodies := keyStepEnv(t, &tc.draft)
			out, err := runAlertCommand(t, append(tc.args(t), "--yes")...)
			if err != nil || len(*bodies) != 1 {
				t.Fatalf("err = %v, posts = %d, output = %s", err, len(*bodies), out)
			}
			body := (*bodies)[0]
			for _, key := range []string{"key_columns", "no_key_columns"} {
				if want, ok := tc.want[key]; ok != (body[key] != nil) || (ok && !jsonEqual(body[key], want)) {
					t.Errorf("%s = %v, want %v", key, body[key], tc.want[key])
				}
			}
			if !strings.Contains(out, "Key columns: ") {
				t.Errorf("the confirmation does not state the key outcome:\n%s", out)
			}
		})
	}
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func TestKeyStepPreflightRefusesAtExitTwoWithoutSending(t *testing.T) {
	for _, tc := range []struct {
		name  string
		draft api.MigrationDetailResponse
		args  func(t *testing.T) []string
	}{
		{"required, flags, neither", keyStepDraft(stepPtr(api.Required)),
			func(*testing.T) []string { return []string{"alert", "resolve", "9", "--action", "apply_migration"} }},
		{"required, manifest, neither", keyStepDraft(stepPtr(api.Required)),
			func(t *testing.T) []string { return []string{"alert", "resolve", "-f", applyMigrationManifest(t, "")} }},
		{"null step, key columns", keyStepDraft(nil, "order_id"),
			func(*testing.T) []string {
				return []string{"alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "order_id"}
			}},
		{"optional step, keyless", keyStepDraft(stepPtr(api.Optional), "order_id"),
			func(t *testing.T) []string {
				return []string{"alert", "resolve", "-f", applyMigrationManifest(t, "  no_key_columns: true\n")}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock, bodies := keyStepEnv(t, &tc.draft)
			_, err := runAlertCommand(t, append(tc.args(t), "--yes")...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || len(*bodies) != 0 {
				t.Fatalf("exit = %d, posts = %d, err = %v; want exit 2 with nothing sent", code, len(*bodies), err)
			}
			if slices.Contains(mock.Requests(), "GET /api/orgs/"+testOrgID+"/datasets/42/rule-suggestions") {
				t.Errorf("the suggestion pre-flight ran before the key refusal: %v", mock.Requests())
			}
		})
	}
}

func TestKeyStepReadFailureWarnsAndSends(t *testing.T) {
	_, bodies := keyStepEnv(t, nil)
	out, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "order_id", "--yes")
	if err != nil || len(*bodies) != 1 || !strings.Contains(out, "could not read table 42's key step") {
		t.Fatalf("err = %v, posts = %d, output = %s", err, len(*bodies), out)
	}
}

func TestKeyChoiceRefusalsFromTheServerKeepTheirExitCode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		detail map[string]any
		want   int
	}{
		{"422 at body.key_columns", http.StatusUnprocessableEntity,
			map[string]any{"msg": "key_columns entry 'x' is not a column of the draft migration.", "loc": []string{"body", "key_columns"}},
			qerrors.ExitBadUsage},
		{"422 at body.no_key_columns", http.StatusUnprocessableEntity,
			map[string]any{"msg": "This table already has key columns; remove them in table settings, not with no_key_columns.", "loc": []string{"body", "no_key_columns"}},
			qerrors.ExitBadUsage},
		{"key step changed", http.StatusConflict,
			map[string]any{"msg": "This table's key columns or loading process changed while the migration was being applied. Reload it and choose its key columns again.", "error_code": "ERR_MIGRATION_CONFLICT"},
			qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newAlertTestEnv(t)
			registerGetAlert(mock, migrationAlert())
			registerEmptySuggestions(mock)
			posts := 0
			mock.RegisterHandler("POST", resolvePath, func(w http.ResponseWriter, _ *http.Request) {
				posts++
				testutil.RespondJSON(w, tc.status, map[string]any{"detail": tc.detail})
			})
			_, err := runAlertCommand(t, "alert", "resolve", "9", "--action", "apply_migration", "--key-columns", "x", "--yes")
			if code := qerrors.GetExitCode(err); code != tc.want || posts != 1 {
				t.Fatalf("exit = %d, posts = %d, err = %v", code, posts, err)
			}
			if !strings.Contains(err.Error(), "qctl describe table-schema 42") {
				t.Errorf("the refusal is not explained: %v", err)
			}
		})
	}
}

func TestApplyMigrationDryRunWritesTheKeyStep(t *testing.T) {
	for _, verb := range []string{"resolve", "propose"} {
		t.Run(verb, func(t *testing.T) {
			draft := keyStepDraft(stepPtr(api.Required))
			_, bodies := keyStepEnv(t, &draft)
			out, err := runAlertCommand(t, "alert", verb, "9", "--action", "apply_migration", "--dry-run")
			if err != nil || len(*bodies) != 0 {
				t.Fatalf("err = %v, posts = %d", err, len(*bodies))
			}
			if !strings.Contains(out, "\n  key_columns:\n  # no_key_columns: true\n") || !strings.Contains(out, "order_id") {
				t.Errorf("dry run at the required step:\n%s", out)
			}
		})
	}
}

func TestProposeRefusesAMissingKeyChoiceLocally(t *testing.T) {
	draft := keyStepDraft(stepPtr(api.Required))
	mock, _ := keyStepEnv(t, &draft)
	proposals := 0
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/change-proposals", func(w http.ResponseWriter, _ *http.Request) {
		proposals++
		w.WriteHeader(http.StatusCreated)
	})
	_, err := runAlertCommand(t, "alert", "propose", "9", "--action", "apply_migration")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || proposals != 0 {
		t.Fatalf("exit = %d, proposals = %d, err = %v", code, proposals, err)
	}
}
