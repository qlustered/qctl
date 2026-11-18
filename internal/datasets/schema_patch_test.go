package datasets

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const migrationPatchOK = `{
  "settings_version_id": 8,
  "draft_version_id": 13,
  "redirect_post_submit_to": "/orgs/x/datasets/42/alerts/99",
  "migration": {"id": 91, "confirmed": false, "created_at": "2026-07-11T07:00:00Z",
    "dataset_id": 42, "dataset_name": "orders", "down_id": null,
    "incompatible_overrides": null, "model": {}, "settings_model_id": 5,
    "state": "draft", "suggested_field_validation_with_allowed_types": null,
    "updated_at": null}
}`

func TestPatchSchemaOverrides_SendsTheBodyVerbatim(t *testing.T) {
	c, mock := newRemClient(t)
	got := recordJSON(t, mock, "PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		http.StatusOK, migrationPatchOK)

	body := map[string]any{
		"settings_version_id": 7,
		"draft_version_id":    12,
		"schema_overrides": map[string]any{
			"amount": map[string]any{"type": "Decimal", "args": []int{12, 2}, "is_nullable": true},
			"legacy": nil,
		},
	}
	resp, err := c.PatchSchemaOverrides("tok", 42, body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Migration.ID != 91 {
		t.Errorf("migration id = %d, want 91", resp.Migration.ID)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(got.body), &sent); err != nil {
		t.Fatalf("request body is not JSON: %q", got.body)
	}
	overrides, _ := sent["schema_overrides"].(map[string]any)
	amount, _ := overrides["amount"].(map[string]any)

	// THE omitempty TRAP (spec §6.3). Marshaling api.FieldOverrideDict -- the
	// type the request's schema_overrides map carries -- would send
	// "indexed":null,"trgm_indexed":null,… merge_schema_overrides stores the
	// field entry verbatim and schema_overrides_merge then splats those nulls
	// over the inferred definition, wiping properties the caller never
	// touched. So the
	// assertion is that this function sends the caller's map VERBATIM: no
	// property is null, and no key appears that the caller's map above did not
	// carry.
	//
	// Do not read the list below as "never send these". Real traffic sends
	// indexed, trgm_indexed, is_dollar, is_percent, display_decimals and datetime_formats
	// whenever the live definition has them -- liveOverrideBase copies them in,
	// and that overlay is what makes editing one line of the emitted YAML safe.
	// They are unwanted HERE only because the map built above does not have
	// them.
	for k, v := range amount {
		if v == nil {
			t.Errorf("schema_overrides.amount.%s is null; the server would store that null and merge it over the inferred definition", k)
		}
	}
	for _, unwanted := range []string{"indexed", "trgm_indexed", "is_dollar", "is_percent", "display_decimals", "is_deleted", "original_name", "datetime_formats"} {
		if _, ok := amount[unwanted]; ok {
			t.Errorf("schema_overrides.amount carries %q, which the caller's map did not: PatchSchemaOverrides must send the map verbatim, not marshal it through api.FieldOverrideDict", unwanted)
		}
	}
	// A cleared field must reach the wire as JSON null, which is the reset.
	raw, ok := overrides["legacy"]
	if !ok {
		t.Fatal("schema_overrides.legacy is missing; the null reset must be sent, not dropped")
	}
	if raw != nil {
		t.Errorf("schema_overrides.legacy = %v, want null", raw)
	}
}

func TestPatchSchemaOverrides_ErrorPaths(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantExit int
		wantCode string
	}{
		{"migration conflict", http.StatusConflict,
			`{"detail":{"msg":"There is already a confirmed draft schema change.","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "ERR_MIGRATION_CONFLICT"},
		// From atlas #1020 the route raises both version mismatches with
		// ERR_MIGRATION_CONFLICT; a server older than that sends the same
		// mismatch with no code, and the envelope must carry either through.
		{"version mismatch", http.StatusConflict,
			`{"detail":{"msg":"SettingsModel version mismatch. Expected 7, got 9","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "ERR_MIGRATION_CONFLICT"},
		{"draft version mismatch", http.StatusConflict,
			`{"detail":{"msg":"Draft migration version mismatch. Expected 7, got 9","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "ERR_MIGRATION_CONFLICT"},
		{"version mismatch on a pre-#1020 server", http.StatusConflict,
			`{"detail":{"msg":"SettingsModel version mismatch. Expected 7, got 9"}}`,
			2, ""},
		{"bad request", http.StatusBadRequest,
			`{"detail":{"msg":"No draft migration version id is provided."}}`,
			2, ""},
		{"forbidden", http.StatusForbidden,
			`{"detail":{"msg":"You don't have access to run schema changes."}}`,
			4, ""},
		{"not found", http.StatusNotFound,
			`{"detail":{"msg":"Your data is currently being analyzed"}}`,
			3, ""},
		// PatchMigrationResponse declares 422, but its generated shape expects
		// detail to be a list. Atlas sends this object envelope (_reject_override
		// in cettings/cettings/controllers/migration_model_controller.py), so the
		// generated parser returns an error and wrapAPIError must recover the
		// captured body.
		{"object-shaped validation error", http.StatusUnprocessableEntity,
			`{"detail":{"msg":"Field 'amount' has invalid display_decimals: it has to be a whole number from 0 to 16383, got -1","error_code":"ERR_VALIDATION","loc":["body","schema_overrides","amount"]}}`,
			2, "ERR_VALIDATION"},
		{"server error", http.StatusInternalServerError,
			`{"detail":"Internal Server Error"}`,
			1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newRemClient(t)
			recordJSON(t, mock, "PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations", tc.status, tc.body)

			_, err := c.PatchSchemaOverrides("tok", 42, map[string]any{"settings_version_id": 7})
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Errorf("GetExitCode = %d, want %d", code, tc.wantExit)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error is not a *datasets.APIError: %T", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tc.wantCode)
			}
		})
	}
}
