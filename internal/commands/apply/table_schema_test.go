package apply

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const tableSchemaDetailJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "amount":   {"current": {"type": "String", "args": [64], "is_nullable": true}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`

// tableSchemaDeletedAmountJSON is tableSchemaDetailJSON with amount already
// deleted, which is the shape the deleted-column refusal reacts to.
const tableSchemaDeletedAmountJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "amount":   {"current": {"type": "String", "args": [64], "is_nullable": true, "is_deleted": true}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`

// schemaPatchResponse is the PATCH answer with the two fields that decide the
// outcome line made explicit. `redirect` is the frontend route capi returns
// ONLY when this patch opened a needs_review_migration alert
// (patch_migration, capi/capi/views/migration_view.py) -- pass "" for the null
// the other three outcomes send. `confirmed` is MigrationModel.confirmed, which
// separates a draft the backend put on the migrate queue from one it merely
// wrote into.
func schemaPatchResponse(redirect string, confirmed bool) string {
	redirectJSON := "null"
	if redirect != "" {
		redirectJSON = strconv.Quote(redirect)
	}
	return fmt.Sprintf(`{
  "settings_version_id": 8, "draft_version_id": 13,
  "redirect_post_submit_to": %s,
  "migration": {"id": 91, "confirmed": %t, "created_at": "2026-07-11T07:00:00Z",
    "dataset_id": 42, "dataset_name": "orders", "down_id": null,
    "incompatible_overrides": null, "model": {}, "settings_model_id": 5,
    "state": "draft", "suggested_field_validation_with_allowed_types": null, "updated_at": null}
}`, redirectJSON, confirmed)
}

// tableSchemaPatchOK is the review outcome -- the ask_user default over a
// change that is not additive-only. The redirect's shape is capi's own,
// `/datasets/{dataset}/alerts/{alert}`.
var tableSchemaPatchOK = schemaPatchResponse("/datasets/42/alerts/99", false)

func setupTableSchemaApplyEnv(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	return setupTableSchemaApplyEnvWithDetail(t, tableSchemaDetailJSON)
}

// setupTableSchemaApplyEnvWithDetail is setupTableSchemaApplyEnv with the live
// schema the GET answers made explicit, so a test can start from a schema whose
// columns are already deleted. The table has no rules.
func setupTableSchemaApplyEnvWithDetail(t *testing.T, detailJSON string) (*testutil.TestEnv, *testutil.MockAPIServer) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/ingestion-jobs",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.IngestionJobsListSchema{Results: []api.IngestionJobTinySchema{}})
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/migration-details",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(detailJSON))
		})
	registerTableRulesList(mock, 42)
	return env, mock
}

// registerSchemaPatch answers the PATCH and captures its body.
func registerSchemaPatch(t *testing.T, mock *testutil.MockAPIServer, status int, resp string) *string {
	t.Helper()
	captured := new(string)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(w http.ResponseWriter, r *http.Request) {
			// io.ReadAll, not a single Read: one Read may return a partial body,
			// and a truncated body neither unmarshals nor substring-matches.
			if r.Body != nil {
				b, _ := io.ReadAll(r.Body)
				*captured = string(b)
			}
			testutil.RespondJSON(w, status, json.RawMessage(resp))
		})
	return captured
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

const retypeAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
`

func TestApplyTableSchema_YesAppliesAndPrintsTheLine(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)

	out, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "table-schema/orders updated") {
		t.Errorf("missing the result line; got:\n%s", out)
	}
	// The WHOLE body, not a spot-check: see wantSchemaPatchBody.
	var got map[string]any
	if err := json.Unmarshal([]byte(*body), &got); err != nil {
		t.Fatalf("the PATCH body is not JSON: %q", *body)
	}
	if !reflect.DeepEqual(got, wantSchemaPatchBody) {
		t.Errorf("the PATCH body is not what datasets.SchemaPatchBody produces\n got: %#v\nwant: %#v",
			got, wantSchemaPatchBody)
	}
}

// wantSchemaPatchBody is the COMPLETE body apply must PATCH for retypeAmount
// against tableSchemaDetailJSON -- every key, every nested override property, as
// it unmarshals from JSON (numbers are float64).
//
// Its twin is wantSchemaPatchBody in internal/commands/diff/table_schema_test.go,
// asserted the same way against the body `qctl diff -f` prints. The two fixtures
// need not be literally equal -- each is complete for its own package's fixture --
// but both are produced by datasets.SchemaPatchBody over
// datasets.BuildSchemaOverrides. That is the invariant: if either command stops
// calling those and hand-builds a body instead, that command's own package test
// goes red on the whole-body comparison, because a hand-built body drops or
// renames something. Spot-checking a few keys (which is what this assertion used
// to do) let a dropped "args" ship green through both packages.
var wantSchemaPatchBody = map[string]any{
	"settings_version_id": float64(7),
	// Sent because a draft exists; omitted entirely when migration_state is
	// current_only.
	"draft_version_id": float64(12),
	"schema_overrides": map[string]any{
		"amount": map[string]any{
			"type": "Decimal",
			// [12,2] from the manifest, is_nullable from LIVE -- the overlay
			// datasets.BuildSchemaOverrides performs. A minimal delta would
			// omit is_nullable and silently reset it server-side.
			"args":        []any{float64(12), float64(2)},
			"is_nullable": true,
		},
	},
}

const addShippedAt = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    shipped_at:
      type: DateTime
      is_nullable: true
`

// A column the live schema does not have is ADDED, and the body carries the
// block exactly as stated: no live definition exists to overlay, so any extra
// property here is one qctl invented on the caller's behalf. Whole-body
// DeepEqual for the same reason wantSchemaPatchBody is.
func TestApplyTableSchema_AddsAColumn(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)

	out, err := execApply(t, writeManifest(t, addShippedAt), "--yes")
	if err != nil {
		t.Fatalf("adding a column is what the schema route is for: %v", err)
	}
	if !strings.Contains(out, "table-schema/orders updated") {
		t.Errorf("missing the result line; got:\n%s", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(*body), &got); err != nil {
		t.Fatalf("the PATCH body is not JSON: %q", *body)
	}
	want := map[string]any{
		"settings_version_id": float64(7),
		"draft_version_id":    float64(12),
		"schema_overrides": map[string]any{
			"shipped_at": map[string]any{"type": "DateTime", "is_nullable": true},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the PATCH body must be the block as stated\n got: %#v\nwant: %#v", got, want)
	}
}

// The prompt is where a mistyped column name is now caught, so it has to say
// "add column" rather than render the addition as an ordinary edit.
func TestApplyTableSchema_PromptShowsAnAddition(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	confirmYes(t)

	out, err := execApply(t, writeManifest(t, addShippedAt))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "shipped_at: add column") {
		t.Errorf("the prompt must name the addition as one; got:\n%s", out)
	}
	for _, want := range []string{"type: DateTime", "is_nullable: true"} {
		if !strings.Contains(out, want) {
			t.Errorf("the prompt is missing %q; got:\n%s", want, out)
		}
	}
}

// is_nullable: false on a column being added is refused before anything is
// sent.
func TestApplyTableSchema_AddedColumnCannotBeNotNull(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(http.ResponseWriter, *http.Request) { t.Error("a refused manifest must send nothing") })

	_, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    shipped_at:
      type: DateTime
      is_nullable: false
`), "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "always added nullable") {
		t.Errorf("message %q does not state the rule", err)
	}
}

func TestApplyTableSchema_OverwrittenIndexFlagSendsNothing(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(http.ResponseWriter, *http.Request) { t.Error("a refused manifest must send nothing") })

	_, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: Date
      indexed: false
`), "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "indexes every Date column") {
		t.Errorf("message %q does not state the rule", err)
	}
}

func TestApplyTableSchema_NoChangeSendsNothing(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(http.ResponseWriter, *http.Request) {
			t.Error("apply must not send a request when nothing changed")
		})

	out, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    currency:
      type: String
      args: [3]
      is_nullable: false
`), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "table-schema/orders unchanged") {
		t.Errorf("missing the unchanged line; got:\n%s", out)
	}
}

func TestApplyTableSchema_PromptAcceptedAndDeclined(t *testing.T) {
	t.Run("accepted", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnv(t)
		registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
		confirmYes(t)
		out, err := execApply(t, writeManifest(t, retypeAmount))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// "migration_policy is frozen" is the fourth outcome the prompt has to
		// name: a locked table is refused at schema_format_override_processor.py's
		// process_migration_patch_request before ensure_draft, the job kill or the
		// alert, so the three side effects above it are all skipped and nothing at
		// all is written.
		for _, want := range []string{"orders", "amount", "type: String → Decimal", "training job", "alert",
			"migration_policy is frozen"} {
			if !strings.Contains(out, want) {
				t.Errorf("the prompt is missing %q; got:\n%s", want, out)
			}
		}
		if !strings.Contains(out, "table-schema/orders updated") {
			t.Errorf("missing the result line; got:\n%s", out)
		}
	})

	t.Run("declined", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnv(t)
		mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
			func(http.ResponseWriter, *http.Request) { t.Error("a declined apply must send nothing") })
		confirmNo(t)
		out, err := execApply(t, writeManifest(t, retypeAmount))
		// A decline is a skip, not a failure: single-document files return the
		// handler's error, and errDocumentDeclined must not become a non-zero
		// exit code.
		if err != nil {
			t.Fatalf("declining must exit 0, got: %v", err)
		}
		if !strings.Contains(out, "Skipping") {
			t.Errorf("missing the Skipping line; got:\n%s", out)
		}
	})
}

// The prompt is the last thing a human reads before a schema write, and until
// qctl-vpt.14 it promised one that every change gets a review step:
// "raises a migration alert for someone to approve (needs_review_migration
// under the ask_user default)". Under that very default, an additive-only
// patch that clones a fresh draft applies itself instead
// (AUTO_APPLY_POLICIES, schema_format_override_processor.py), and under
// apply_asap nothing ever waits. No test pinned the wording, so nothing
// caught it.
//
// This pins the shape, not the prose: each of the three migration_policy values
// has to be named with its own outcome, migration_window (gone from the enum)
// must not come back, and the promise that regressed has to stay gone.
func TestApplyTableSchema_PromptStatesEveryPolicyOutcome(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	confirmYes(t)

	out, err := execApply(t, writeManifest(t, retypeAmount))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Everything below asserts about the copy the human reads BEFORE deciding.
	// The question itself is not in `out` because it goes to stderr. Cut on the
	// line confirmSchemaOverrides prints once it is answered, which keeps the
	// post-apply outcome line out of the sample.
	prompt, _, ok := strings.Cut(out, "Updating the schema of")
	if !ok {
		t.Fatalf("the confirmed-apply line is missing; got:\n%s", out)
	}

	for _, want := range []string{
		// ask_user, both halves of it.
		"ask_user, the default",
		"only ADDS columns",
		"applies itself with no review step",
		"needs_review_migration and waits for approval",
		// apply_asap, which never waits and which a client cannot set.
		"apply_asap: applied with no review step, additive or not",
		"a client cannot set it",
		// frozen, which writes nothing at all.
		"migration_policy is frozen",
		"refused with a 409",
		// And the honest bit: qctl does not know which one you get.
		"qctl cannot tell which one you",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt is missing %q; got:\n%s", want, prompt)
		}
	}

	// The defect itself. Both spellings are unbounded claims about a review
	// step that two of the three policies do not take.
	for _, gone := range []string{
		"raises a migration alert for someone to approve",
		"needs_review_migration under the ask_user default",
	} {
		if strings.Contains(prompt, gone) {
			t.Errorf("the prompt still promises a review step: %q\n%s", gone, prompt)
		}
	}
	if strings.Contains(prompt, "migration_window") {
		t.Errorf("the prompt names migration_window, which the backend no longer has; got:\n%s", prompt)
	}
}

// The prompt states the matrix; the line after the write says which row it hit.
// Both halves are the fix for qctl-vpt.14, and this pins the second: the three
// ends a PATCH can reach, plus the fallback for a redirect qctl cannot parse.
func TestApplyTableSchema_ReportsWhichOutcomeHappened(t *testing.T) {
	cases := []struct {
		name   string
		resp   string
		want   []string
		absent []string
	}{
		{
			// ask_user over a change that is not additive-only: capi answers a
			// redirect because this patch opened the review alert.
			name: "a reviewer has to approve it",
			resp: tableSchemaPatchOK,
			want: []string{
				"Waiting for review: this change opened a needs_review_migration alert.",
				"qctl describe alert 99",
				"qctl alert resolve 99 --action apply_migration",
			},
			// The redirect is a cosmosview route. A CLI caller cannot follow it,
			// so only the alert id inside it survives into the output.
			absent: []string{"/datasets/42/alerts/99", "Applied with no review step"},
		},
		{
			// The case the ticket is about: an additive-only patch under the
			// ask_user default. No redirect, and the backend confirmed the draft.
			name: "it applied itself with nobody approving",
			resp: schemaPatchResponse("", true),
			want: []string{
				"Applied with no review step",
				"its migrate job is",
				"queued",
			},
			absent: []string{"Waiting for review", "apply_migration", "no migrate job"},
		},
		{
			// draft_only: the existing draft is reused, so there is no job kill,
			// no synthetic job and no alert -- and nothing to approve either.
			name: "the draft was reused and nothing was queued",
			resp: schemaPatchResponse("", false),
			want: []string{
				"No review step and no migrate job",
				"qctl describe table-schema 42",
			},
			absent: []string{"Waiting for review", "Applied with no review step"},
		},
		{
			// The route belongs to the frontend. If it moves, qctl must not
			// print an id it guessed out of a shape it does not recognise.
			name: "a redirect shape qctl does not recognise",
			resp: schemaPatchResponse("/some/other/place", false),
			want: []string{
				"Waiting for review: this change opened a needs_review_migration alert.",
				"qctl get alerts --table-id 42 --issue-type needs_review_migration",
				"qctl alert resolve <id> --action apply_migration",
			},
			absent: []string{"/some/other/place", "No review step and no migrate job"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			registerSchemaPatch(t, mock, http.StatusOK, tc.resp)

			out, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("the outcome line is missing %q; got:\n%s", want, out)
				}
			}
			for _, gone := range tc.absent {
				if strings.Contains(out, gone) {
					t.Errorf("the outcome line must not contain %q; got:\n%s", gone, out)
				}
			}
		})
	}
}

// The review outcome hands over an apply_migration line, and apply_migration
// prompts: without a terminal the line carries --yes, at one it does not. Both
// the line naming the parsed alert id and the <id> fallback, and no other line
// in the block.
func TestSchemaPatchOutcomeAddsYesOnlyWithoutATerminal(t *testing.T) {
	for name, tc := range map[string]struct {
		redirect string
		line     string
	}{
		"alert id parsed":         {"/datasets/42/alerts/99", "  qctl alert resolve 99 --action apply_migration"},
		"alert id not recognised": {"/some/other/place", "  qctl alert resolve <id> --action apply_migration"},
	} {
		var resp api.MigrationPatchResponse
		if err := json.Unmarshal([]byte(schemaPatchResponse(tc.redirect, false)), &resp); err != nil {
			t.Fatalf("fixture does not decode: %v", err)
		}
		for mode, c := range map[string]struct {
			canPrompt bool
			suffix    string
		}{
			"at a terminal": {true, ""},
			"no terminal":   {false, " --yes"},
		} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				out := schemaPatchOutcome(&resp, 42, c.canPrompt, nil)
				var resolveLines []string
				for _, line := range strings.Split(out, "\n") {
					if strings.Contains(line, "alert resolve") {
						resolveLines = append(resolveLines, line)
					}
				}
				if len(resolveLines) != 1 || resolveLines[0] != tc.line+c.suffix {
					t.Errorf("resolve lines = %q, want exactly [%q]:\n%s", resolveLines, tc.line+c.suffix, out)
				}
				if c.suffix == "" && strings.Contains(out, "--yes") {
					t.Errorf("a terminal reader's block must carry no --yes:\n%s", out)
				}
			})
		}
	}
}

func TestSchemaPatchOutcomeAddsKeyColumnsAtTheRequiredStep(t *testing.T) {
	var resp api.MigrationPatchResponse
	if err := json.Unmarshal([]byte(schemaPatchResponse("/datasets/42/alerts/99", false)), &resp); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	required, optional := api.Required, api.Optional
	for name, tc := range map[string]struct {
		step *api.KeyStep
		line string
	}{
		"required": {&required, "  qctl alert resolve 99 --action apply_migration --key-columns <a,b>\n"},
		"optional": {&optional, "  qctl alert resolve 99 --action apply_migration\n"},
		"null":     {nil, "  qctl alert resolve 99 --action apply_migration\n"},
	} {
		if out := schemaPatchOutcome(&resp, 42, true, tc.step); !strings.Contains(out, tc.line) {
			t.Errorf("%s: want line %q in:\n%s", name, tc.line, out)
		}
	}
}

// The outcome line follows the terminal the apply itself ran at.
func TestApplyTableSchema_ReviewLineFollowsTheTerminal(t *testing.T) {
	const line = "qctl alert resolve 99 --action apply_migration"
	t.Run("at a terminal", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnv(t)
		registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
		cmdutil.SetReader(strings.NewReader(""))
		t.Cleanup(cmdutil.ResetReader)
		out, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, line+"\n") {
			t.Errorf("want %q with no --yes in:\n%s", line, out)
		}
	})
	t.Run("no terminal", func(t *testing.T) {
		cmdutil.ResetReader()
		t.Cleanup(cmdutil.ResetReader)
		if cmdutil.CanPrompt() {
			t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
		}
		_, mock := setupTableSchemaApplyEnv(t)
		registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
		out, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(out, line+" --yes\n") {
			t.Errorf("want %q in:\n%s", line+" --yes", out)
		}
	})
}

func TestApplyTableSchema_NonInteractiveExitsTwo(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
	}
	_, mock := setupTableSchemaApplyEnv(t)
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
		func(http.ResponseWriter, *http.Request) { t.Error("must not send without confirmation") })

	_, err := execApply(t, writeManifest(t, retypeAmount))
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the message must name --yes; got %q", err)
	}
}

func TestApplyTableSchema_ErrorPaths(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantExit int
		wantIn   string
	}{
		{"migration conflict names no fake action", http.StatusConflict,
			`{"detail":{"msg":"There is already a confirmed draft schema change.","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "qctl get alerts --table-id 42"},
		{"locked table", http.StatusConflict,
			`{"detail":{"msg":"This table's schema is locked and cannot be changed.","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "qctl get table-config 42"},
		// From atlas #1020 a stale token carries ERR_MIGRATION_CONFLICT like
		// every other conflict on this route; a server older than that sends
		// the same mismatch with no code. Both must reach the re-read.
		{"version mismatch says re-read", http.StatusConflict,
			`{"detail":{"msg":"SettingsModel version mismatch. Expected 7, got 9","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "qctl get table-schema"},
		{"draft version mismatch says re-read", http.StatusConflict,
			`{"detail":{"msg":"Draft migration version mismatch. Expected 7, got 9","error_code":"ERR_MIGRATION_CONFLICT"}}`,
			2, "qctl get table-schema"},
		{"version mismatch on a pre-#1020 server says re-read", http.StatusConflict,
			`{"detail":{"msg":"SettingsModel version mismatch. Expected 7, got 9"}}`,
			2, "qctl get table-schema"},
		{"forbidden", http.StatusForbidden,
			`{"detail":{"msg":"You don't have access to run schema changes."}}`, 4, ""},
		{"not found", http.StatusNotFound,
			`{"detail":{"msg":"Your data is currently being analyzed"}}`, 3, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			registerSchemaPatch(t, mock, tc.status, tc.body)
			_, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Errorf("GetExitCode = %d, want %d", code, tc.wantExit)
			}
			// ERR_MIGRATION_CONFLICT must never send the reader to an action the
			// blocking alert does not offer: discard_migration belongs to
			// needs_review_migration, and this refusal comes from
			// confirmed_migration / migration_ready_to_run, which offer try_again.
			if strings.Contains(err.Error(), "discard_migration") {
				t.Errorf("the message names an action the blocking alert does not offer: %q", err)
			}
			if tc.wantIn != "" && !strings.Contains(err.Error(), tc.wantIn) {
				t.Errorf("message %q does not mention %q", err, tc.wantIn)
			}
		})
	}
}

// TestApplyTableSchema_MigrationConflictGuidanceHoldsForEveryProducer pins the
// one property qctl can actually guarantee about ERR_MIGRATION_CONFLICT: the
// guidance must be true whichever of the route's producers raised it.
//
// All seven are represented here with the backend's own sentences. A locked
// table (schema_format_override_processor.py) writes NOTHING -- no draft, no
// training job, no alert -- so the original copy, which announced "a schema
// change is already pending" and sent the reader to `get alerts` and
// `describe table-schema`, described the wrong situation and pointed at two
// views with nothing in them. The two version mismatches are the later half of
// the same defect: atlas #1020 gave them this code, which dropped them into
// guidance that never names the re-read that is their whole fix. The seventh,
// an edit of a deleted column (refuse_deleted_column_edits), is one qctl
// refuses before sending when its read already saw the column deleted; it
// reaches this guidance only for a column deleted between that read and the
// write, and re-reading cannot clear it, so the guidance names the restore.
//
// The cases must discriminate. Asserting only the exit code and the three
// old commands passes against both pre-fix versions, which is exactly how the
// defect survived twice: the harness could not go red on it. So the checks
// below are (a) the guidance never asserts a pending change, (b) it names
// migration_policy, (c) it names the re-read command, and (d) it is IDENTICAL
// for every body -- qctl does not guess which producer raised, because nothing
// but the backend's untyped English separates them.
func TestApplyTableSchema_MigrationConflictGuidanceHoldsForEveryProducer(t *testing.T) {
	cases := []struct {
		name string
		msg  string
	}{
		{"locked", "This table's schema is locked and cannot be changed."},
		{"confirmed draft", "There is already a confirmed schema change that needs to be applied " +
			"before any new draft schema changes can be made."},
		{"apply in flight", "A schema change is currently being applied to the database. Please wait for it to finish."},
		{"stranded by a failed apply", "The previous schema change for this table failed to apply and is still blocking it. " +
			"There is no draft schema change left to review, so the table's schema cannot be edited."},
		{"stale settings token", "SettingsModel version mismatch. Expected 7, got 9"},
		{"stale draft token", "Draft migration version mismatch. Expected 7, got 9"},
		{"edit of a deleted column", "Restore column 'amount' before editing it."},
	}
	var guidance string
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			body, err := json.Marshal(map[string]any{
				"detail": map[string]any{"msg": tc.msg, "error_code": "ERR_MIGRATION_CONFLICT"},
			})
			if err != nil {
				t.Fatalf("encode body: %v", err)
			}
			registerSchemaPatch(t, mock, http.StatusConflict, string(body))

			_, err = execApply(t, writeManifest(t, retypeAmount), "--yes")
			if err == nil {
				t.Fatal("expected an error")
			}
			// The code, not the text: a %v wrap would still read perfectly here
			// while silently degrading the exit code to 1.
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			full := err.Error()
			if !strings.Contains(full, tc.msg) {
				t.Errorf("the backend's own sentence must survive the wrap; got:\n%s", full)
			}
			cut := strings.LastIndex(full, "\n\n")
			if cut < 0 {
				t.Fatalf("no guidance block appended; got:\n%s", full)
			}
			got := full[cut+2:]

			for _, claim := range []string{"already pending", "so a new one cannot be started"} {
				if strings.Contains(got, claim) {
					t.Errorf("guidance asserts %q, which is false on a locked table; got:\n%s", claim, got)
				}
			}
			if !strings.Contains(got, "migration_policy") {
				t.Errorf("guidance never names migration_policy, the remedy for the locked producer; got:\n%s", got)
			}
			for _, want := range []string{
				"qctl get table-schema orders -o yaml",
				"qctl get table-config 42",
				"qctl get alerts --table-id 42",
				"qctl describe table-schema 42",
				"seven different situations",
				"apply a block whose only change is\n`is_deleted: false`, then make the change in a second apply",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("guidance does not name %q; got:\n%s", want, got)
				}
			}
			// got starts after the LAST blank line, so its first line ("seven
			// different situations") and its last (describe table-schema) both
			// being in it, checked above, is what shows the block has none.
			if !strings.HasPrefix(got, "This table refused the schema change.") {
				t.Errorf("the guidance block does not start where it should, so it carries a blank line:\n%s", got)
			}
			if strings.Contains(got, "--action") {
				t.Errorf("guidance names an --action:\n%s", got)
			}
			// discard_migration belongs to needs_review_migration, which is not
			// what is blocking under any of these.
			if strings.Contains(got, "discard_migration") {
				t.Errorf("guidance names an action no blocking alert offers:\n%s", got)
			}
			if guidance == "" {
				guidance = got
			} else if got != guidance {
				t.Errorf("guidance differs between producers, so qctl is guessing which one raised:\n%s\n---\n%s", guidance, got)
			}
		})
	}
}

const deleteAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: true
`

// fieldInUseBody is the 409 _field_in_use_error builds (atlas
// cettings/cettings/controllers/composite_controller/composite_field_references.py),
// with the frontend links it carries in `actions`.
func fieldInUseBody(t *testing.T, msg string, dependents ...map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"detail": map[string]any{
			"msg":        msg,
			"error_code": "ERR_FIELD_IN_USE_BY_COMPOSITE",
			"loc":        []string{"schema_overrides", "amount"},
			"context": map[string]any{
				"dataset_id": 42, "field_name": "amount", "dependents": dependents,
			},
			"actions": []map[string]string{{
				"label": "Edit setup: orders_enriched",
				"url":   "/orgs/" + testOrgID + "/datasets/12/composite/edit?step=output_columns",
			}},
		},
	})
	if err != nil {
		t.Fatalf("encode body: %v", err)
	}
	return string(body)
}

func fieldInUseDependent(tableID int, name, revision, state string, usages ...string) map[string]any {
	return map[string]any{
		"downstream_dataset_id":      tableID,
		"downstream_dataset_name":    name,
		"composite_spec_group_id":    "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
		"composite_spec_revision_id": revision,
		"revision_state":             state,
		"usages":                     usages,
	}
}

// TestApplyTableSchema_FieldInUseByCompositeNamesTheLinkedTables: a delete a
// linked table still reads is not a stale token, so the re-read advice the
// other 409s get would send the reader round in a circle. The guidance names
// each linked table from context.dependents instead, and the natural-key line
// appears exactly when a dependent reads the column as one.
func TestApplyTableSchema_FieldInUseByCompositeNamesTheLinkedTables(t *testing.T) {
	const enabledRev = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	const reviewRev = "7a1e9c55-2b3d-4f60-8e21-9c0d3b4a5f16"
	cases := []struct {
		name       string
		msg        string
		dependents []map[string]any
		wantLines  []string
		naturalKey bool
	}{
		{
			name: "one table",
			msg:  "Column 'amount' is used by composite table 'orders_enriched'. Remove it from that table's output columns first.",
			dependents: []map[string]any{
				fieldInUseDependent(12, "orders_enriched", enabledRev, "enabled", "output", "join"),
			},
			wantLines: []string{
				"  orders_enriched (table 12), revision " + enabledRev + " (enabled): output column, matching key\n",
			},
		},
		{
			name: "two tables, one on its natural key",
			msg:  "Column 'amount' is used by composite tables 'claims_rollup' and 'orders_enriched'. Remove it from their setup first.",
			dependents: []map[string]any{
				fieldInUseDependent(14, "claims_rollup", reviewRev, "in_review", "natural_key"),
				fieldInUseDependent(12, "orders_enriched", enabledRev, "enabled", "output"),
			},
			wantLines: []string{
				"  claims_rollup (table 14), revision " + reviewRev + " (in_review): natural key\n",
				"  orders_enriched (table 12), revision " + enabledRev + " (enabled): output column\n",
			},
			naturalKey: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnv(t)
			registerSchemaPatch(t, mock, http.StatusConflict, fieldInUseBody(t, tc.msg, tc.dependents...))

			_, err := execApply(t, writeManifest(t, deleteAmount), "--yes")
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			full := err.Error()
			if !strings.Contains(full, tc.msg) {
				t.Errorf("the backend's own sentence must survive the wrap; got:\n%s", full)
			}
			for _, want := range tc.wantLines {
				if !strings.Contains(full, want) {
					t.Errorf("guidance lacks %q; got:\n%s", want, full)
				}
			}
			for _, stale := range []string{"stale", "qctl get table-schema", "Another write"} {
				if strings.Contains(full, stale) {
					t.Errorf("guidance gives the stale-token advice (%q), which cannot clear this refusal:\n%s", stale, full)
				}
			}
			if !strings.Contains(full, "qctl apply -f") {
				t.Errorf("guidance does not say how to change the linked table; got:\n%s", full)
			}
			if got := strings.Contains(full, "refuses to change a linked\ntable's natural keys"); got != tc.naturalKey {
				t.Errorf("natural-key line present = %v, want %v; got:\n%s", got, tc.naturalKey, full)
			}
		})
	}
}

// TestApplyTableSchema_OtherConflictsKeepTheReread: the plain-409 arm still
// catches the route's lost-update 409, which a re-read does clear.
func TestApplyTableSchema_OtherConflictsKeepTheReread(t *testing.T) {
	_, mock := setupTableSchemaApplyEnv(t)
	registerSchemaPatch(t, mock, http.StatusConflict,
		`{"detail":{"msg":"Your request could not be completed due to a temporary conflict with other operations. Please try again.","error_code":"ERR_DB_CONCURRENCY_CONFLICT"}}`)
	_, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
	if err == nil {
		t.Fatal("expected an error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "qctl get table-schema orders -o yaml") {
		t.Errorf("a lost update must still get the re-read; got:\n%s", err)
	}
}

// TestApplyTableSchema_DeletedColumnRefusal: an edit or a `null` reset of a
// column the live schema has deleted is refused with exit 2 before anything is
// sent -- under --yes and on the prompt path alike, and for the whole file
// when one column of several is refused, because the backend would write
// nothing from that PATCH. The restore itself, `is_deleted: false` alone, is
// sent.
func TestApplyTableSchema_DeletedColumnRefusal(t *testing.T) {
	const clearAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount: null
`
	// retypeBoth edits the live currency beside the deleted amount.
	const retypeBoth = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
    currency:
      type: String
      args: [8]
`
	cases := []struct {
		name     string
		manifest string
		yes      bool
		wantIn   string
	}{
		{"an edit under --yes", retypeAmount, true, "spec.fields.amount (MANIFEST) changes type, args on a deleted column"},
		{"an edit on the prompt path", retypeAmount, false, "spec.fields.amount (MANIFEST) changes type, args on a deleted column"},
		{"a null reset", clearAmount, true, "spec.fields.amount (MANIFEST) is null on a deleted column"},
		{"one column of several", retypeBoth, true, "spec.fields.amount (MANIFEST) changes type, args on a deleted column"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mock := setupTableSchemaApplyEnvWithDetail(t, tableSchemaDeletedAmountJSON)
			mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
				func(http.ResponseWriter, *http.Request) { t.Error("a refused manifest must send nothing") })
			confirmYes(t)

			var extra []string
			if tc.yes {
				extra = []string{"--yes"}
			}
			path := writeManifest(t, tc.manifest)
			out, err := execApply(t, path, extra...)
			if code := qerrors.GetExitCode(err); err == nil || code != 2 {
				t.Fatalf("GetExitCode = %d (err %v), want 2", code, err)
			}
			if want := strings.Replace(tc.wantIn, "MANIFEST", path, 1); !strings.Contains(err.Error(), want) {
				t.Errorf("message does not say %q:\n%s", want, err)
			}
			// Refused before the prompt, so the reader is never asked to
			// approve a change that cannot land.
			if strings.Contains(out, "Changes to apply:") {
				t.Errorf("the refusal came after the prompt; got:\n%s", out)
			}
		})
	}

	t.Run("is_deleted: false alone is sent", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnvWithDetail(t, tableSchemaDeletedAmountJSON)
		body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)

		out, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: false
`), "--yes")
		if code := qerrors.GetExitCode(err); code != 0 {
			t.Fatalf("GetExitCode = %d (err %v), want 0", code, err)
		}
		if !strings.Contains(out, "table-schema/orders updated") {
			t.Errorf("missing the result line; got:\n%s", out)
		}
		if !strings.Contains(*body, `"is_deleted":false`) {
			t.Errorf("the restore must reach the wire; body:\n%s", *body)
		}
	})

	t.Run("an unedited block sends nothing and is not refused", func(t *testing.T) {
		_, mock := setupTableSchemaApplyEnvWithDetail(t, tableSchemaDeletedAmountJSON)
		mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/42/migrations",
			func(http.ResponseWriter, *http.Request) { t.Error("an unedited block must send nothing") })

		out, err := execApply(t, writeManifest(t, `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: true
`), "--yes")
		if code := qerrors.GetExitCode(err); code != 0 {
			t.Fatalf("GetExitCode = %d (err %v), want 0", code, err)
		}
		if !strings.Contains(out, "table-schema/orders unchanged") {
			t.Errorf("missing the unchanged line; got:\n%s", out)
		}
	})
}

// stampColumns is detailJSON with each named column's current definition
// carrying the rule_output stamp the server writes on a column testTableRuleID
// produces.
func stampColumns(t *testing.T, detailJSON string, columns ...string) string {
	t.Helper()
	var detail map[string]any
	if err := json.Unmarshal([]byte(detailJSON), &detail); err != nil {
		t.Fatalf("fixture detail: %v", err)
	}
	for _, column := range columns {
		current := detail["fields"].(map[string]any)[column].(map[string]any)["current"].(map[string]any)
		current["generated_by"] = testutil.RuleOutputStamp(testTableRuleID, testRuleRevID, column)
	}
	b, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("encoding the stamped detail: %v", err)
	}
	return string(b)
}

// TestApplyTableSchema_ProducedColumnWarning: retyping or deleting a column
// the server stamps as a rule's output warns, names the rule, and still sends
// the PATCH, exiting 0. The same edit to an unstamped column neither warns nor
// fetches a rule.
func TestApplyTableSchema_ProducedColumnWarning(t *testing.T) {
	const deleteAmount = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: String
      args: [64]
      is_nullable: true
      is_deleted: true
`
	named := "amount (type, args): produced by rule email_check (" + testTableRuleID + ")"
	cases := []struct {
		name      string
		stamped   bool
		manifest  string
		ruleFound bool
		yes       bool
		wantLine  string // "" when no warning is wanted
	}{
		{"retyping a produced column warns and names the rule", true, retypeAmount, true, true, named},
		{"deleting one warns", true, deleteAmount, true, true,
			"amount (is_deleted): produced by rule email_check (" + testTableRuleID + ")"},
		{"the prompt path warns too", true, retypeAmount, true, false, named},
		{"a rule the lookup cannot fetch is named by ID", true, retypeAmount, false, true,
			"amount (type, args): produced by rule " + testTableRuleID + "\n"},
		{"an unstamped column does not warn", false, retypeAmount, true, true, ""},
	}
	ruleGet := "GET /api/orgs/" + testOrgID + "/dataset-rules/" + testTableRuleID
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			detail := tableSchemaDetailJSON
			if tc.stamped {
				detail = stampColumns(t, detail, "amount")
			}
			_, mock := setupTableSchemaApplyEnvWithDetail(t, detail)
			body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
			if tc.ruleFound {
				registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
			}
			confirmYes(t)

			var extra []string
			if tc.yes {
				extra = []string{"--yes"}
			}
			out, err := execApply(t, writeManifest(t, tc.manifest), extra...)
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("GetExitCode = %d, want 0 (a warning never refuses); err: %v", code, err)
			}
			if *body == "" {
				t.Fatalf("the PATCH was not sent; got:\n%s", out)
			}
			warned := strings.Contains(out, "a rule produces")
			if warned != (tc.wantLine != "") {
				t.Fatalf("produced-column warning present = %v, want %v; got:\n%s", warned, tc.wantLine != "", out)
			}
			ruleGets := 0
			for _, r := range mock.Requests() {
				if r == ruleGet {
					ruleGets++
				}
			}
			if wantGets := map[bool]int{true: 1, false: 0}[tc.stamped]; ruleGets != wantGets {
				t.Errorf("rule detail GETs = %d, want %d", ruleGets, wantGets)
			}
			if tc.wantLine == "" {
				return
			}
			if !strings.Contains(out, tc.wantLine) {
				t.Errorf("the warning is missing %q; got:\n%s", tc.wantLine, out)
			}
			if strings.Index(out, "a rule produces") > strings.Index(out, "table-schema/orders updated") {
				t.Errorf("the warning printed after the PATCH result, not before it; got:\n%s", out)
			}
			if prompt := strings.Index(out, "Changes to apply:"); !tc.yes && (prompt < 0 || strings.Index(out, "a rule produces") > prompt) {
				t.Errorf("--yes=false: the warning did not print before the confirmation prompt; got:\n%s", out)
			}
		})
	}
}

// Two edited columns one rule produces cost one rule GET, and both are named.
func TestApplyTableSchema_ProducedColumnWarningFetchesEachRuleOnce(t *testing.T) {
	const retypeBoth = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata: {id: 42}
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
    currency:
      type: String
      args: [8]
`
	_, mock := setupTableSchemaApplyEnvWithDetail(t, stampColumns(t, tableSchemaDetailJSON, "amount", "currency"))
	body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())

	out, err := execApply(t, writeManifest(t, retypeBoth), "--yes")
	if code := qerrors.GetExitCode(err); code != 0 {
		t.Fatalf("GetExitCode = %d, want 0; err: %v", code, err)
	}
	if *body == "" {
		t.Fatalf("the PATCH was not sent; got:\n%s", out)
	}
	for _, want := range []string{
		"amount (type, args): produced by rule email_check (" + testTableRuleID + ")",
		"currency (args): produced by rule email_check (" + testTableRuleID + ")",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning is missing %q; got:\n%s", want, out)
		}
	}
	ruleGets := 0
	for _, r := range mock.Requests() {
		if r == "GET /api/orgs/"+testOrgID+"/dataset-rules/"+testTableRuleID {
			ruleGets++
		}
	}
	if ruleGets != 1 {
		t.Errorf("rule detail GETs = %d, want 1", ruleGets)
	}
}

// Every part of the result block is the server's own text: the table name in
// the label, and the field name, types and message of each incompatible
// override. None of it may reach the terminal as a live control sequence.
// The fixtures are built through json.Marshal so the controls go on the wire
// the way a real body carries them.
func TestApplyTableSchema_SanitizesServerText(t *testing.T) {
	var detail map[string]any
	if err := json.Unmarshal([]byte(tableSchemaDetailJSON), &detail); err != nil {
		t.Fatalf("fixture detail: %v", err)
	}
	detail["dataset_name"] = "orders\x1b[31m"
	detailJSON, err := json.Marshal(detail)
	if err != nil {
		t.Fatalf("encode detail: %v", err)
	}
	_, mock := setupTableSchemaApplyEnvWithDetail(t, string(detailJSON))

	var patch map[string]any
	if err := json.Unmarshal([]byte(schemaPatchResponse("", true)), &patch); err != nil {
		t.Fatalf("fixture patch response: %v", err)
	}
	migration, _ := patch["migration"].(map[string]any)
	migration["incompatible_overrides"] = []any{map[string]any{
		"field_name": "amount\x1b[31m",
		"from_type":  "String",
		"to_type":    "Decimal",
		"msg":        "lossy\x1b]0;pwned\x07 cast",
	}}
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		t.Fatalf("encode patch response: %v", err)
	}
	registerSchemaPatch(t, mock, http.StatusOK, string(patchJSON))

	out, err := execApply(t, writeManifest(t, retypeAmount), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("the result block kept a terminal control: %q", out)
	}
	for _, want := range []string{
		`table-schema/orders\x1b[31m updated`,
		`amount\x1b[31m: String → Decimal (lossy\x1b]0;pwned\x07 cast)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result block missing visible escape %q; got:\n%s", want, out)
		}
	}
}

func TestApplyTableSchema_KindIsRegistered(t *testing.T) {
	if _, ok := genericKinds["TableSchema"]; !ok {
		t.Fatalf("apply -f does not accept kind TableSchema; kinds = %v", applyKinds())
	}
}

// tableSchemaRoundedAmountJSON is tableSchemaDetailJSON with amount shown to 2
// places, which is the only shape a reset has anything to do.
const tableSchemaRoundedAmountJSON = `{
  "confirmed": false, "created_at": "2026-07-09T10:00:00Z", "updated_at": "2026-07-11T07:00:00Z",
  "dataset_id": 42, "dataset_name": "orders",
  "diff_summary": {"total_fields": 2},
  "fields": {
    "amount":   {"current": {"type": "Decimal", "args": [12, 2], "is_nullable": true, "display_decimals": 2}},
    "currency": {"current": {"type": "String", "args": [3], "is_nullable": false}}
  },
  "migration_id": 91, "migration_model_version_id": 12, "settings_model_version_id": 7,
  "migration_state": "draft_with_current", "state": "draft"
}`

const resetAmountDecimals = `apiVersion: qluster.ai/v1
kind: TableSchema
metadata:
  id: 42
spec:
  fields:
    amount:
      type: Decimal
      args: [12, 2]
      is_nullable: true
      display_decimals: null
`

// `display_decimals: null` has to reach the server AS a null. The server
// merges the override onto the inferred model, so a key that is simply absent
// leaves the column with whatever inference gave it -- which is the whole
// reason the property carries three states rather than two. Whole-body
// DeepEqual for the same reason wantSchemaPatchBody is: a dropped key and a
// null one are the same spot-check and different requests.
func TestApplyTableSchema_DisplayDecimalsResetIsSentAsANull(t *testing.T) {
	_, mock := setupTableSchemaApplyEnvWithDetail(t, tableSchemaRoundedAmountJSON)
	body := registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)

	out, err := execApply(t, writeManifest(t, resetAmountDecimals), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "table-schema/orders updated") {
		t.Errorf("missing the result line; got:\n%s", out)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(*body), &got); err != nil {
		t.Fatalf("the PATCH body is not JSON: %q", *body)
	}
	want := map[string]any{
		"settings_version_id": float64(7),
		"draft_version_id":    float64(12),
		"schema_overrides": map[string]any{
			"amount": map[string]any{
				"type":             "Decimal",
				"args":             []any{float64(12), float64(2)},
				"is_nullable":      true,
				"display_decimals": nil,
			},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the reset must be sent as an explicit null\n got: %#v\nwant: %#v", got, want)
	}
	// The literal bytes too. The comparison above already separates a null
	// from a missing key -- a JSON null decodes to a present nil entry -- but
	// it reads the body qctl sent through a decoder, and this is the spelling
	// the server reads.
	if !strings.Contains(*body, `"display_decimals":null`) {
		t.Errorf("the PATCH body does not carry an explicit null:\n%s", *body)
	}
}

// The prompt has to name the reset in words the reader can act on, because it
// is the last thing between the file and a migration.
func TestApplyTableSchema_PromptShowsADisplayDecimalsReset(t *testing.T) {
	_, mock := setupTableSchemaApplyEnvWithDetail(t, tableSchemaRoundedAmountJSON)
	registerSchemaPatch(t, mock, http.StatusOK, tableSchemaPatchOK)
	confirmYes(t)

	out, err := execApply(t, writeManifest(t, resetAmountDecimals))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "display_decimals: 2 → unset") {
		t.Errorf("the prompt must name the reset; got:\n%s", out)
	}
}
