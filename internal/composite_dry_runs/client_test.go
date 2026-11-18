package composite_dry_runs //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

// runningDetail is a detail body for a job that has not produced metrics yet:
// the route sends structured_summary null.
const runningDetail = `{
  "id": 5512,
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "downstream_dataset_id": 12,
  "organization_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "state": "running",
  "max_spine_rows": 1000,
  "created_at": "2026-09-22T10:00:00Z",
  "ingestion_job_id": 9921,
  "user_message": null,
  "wizard_step": null,
  "structured_summary": null
}`

// erroredDetail is a failed job: the error fields the view computes, an
// error_context integer past 2^53, and a summary with skipped output columns.
const erroredDetail = `{
  "id": 5513,
  "composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
  "composite_spec_group_id": "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
  "downstream_dataset_id": 12,
  "organization_id": "b2c3d4e5-f6a7-8901-bcde-f23456789012",
  "state": "error",
  "max_spine_rows": 1000,
  "created_at": "2026-09-22T10:00:00Z",
  "error_type": "CompositeRunnerSpecError",
  "error_message": "field policy_ref is not on claims",
  "error_loc": ["revision", "output", "policy_ref", "field"],
  "error_context": {"column": "policy_ref", "table": "claims", "spine_row_id": 9007199254740993},
  "user_message": "The column policy_ref no longer exists on claims.",
  "wizard_step": "output_columns",
  "structured_summary": {
    "mode": "composite",
    "activation_blocker_count": 0,
    "skipped_output_columns": [{"downstream_field": "policy_ref", "source_alias": "claims",
      "source_dataset_id": 13, "source_dataset_name": "claims", "field": "policy_ref"}]
  }
}`

func serveDetail(t *testing.T, body string) *Client {
	t.Helper()
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/composite-dry-runs/5512", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGet_NullStructuredSummaryStaysAbsent(t *testing.T) {
	d, err := serveDetail(t, runningDetail).Get("token", 5512)
	if err != nil {
		t.Fatal(err)
	}
	if d.StructuredSummary != nil {
		t.Errorf("StructuredSummary = %#v, want nil for a null summary", d.StructuredSummary)
	}

	generated, err := json.Marshal(d.Job)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), `"structured_summary":null`) {
		t.Fatalf("generated type lost a null summary:\n%s", generated)
	}

	out, err := yaml.Marshal(ToManifest(d))
	if err != nil {
		t.Fatal(err)
	}
	for _, absent := range []string{"structured_summary", "activation_blocker_count"} {
		if strings.Contains(string(out), absent) {
			t.Errorf("manifest carries %q for a null summary:\n%s", absent, out)
		}
	}
}

// TestGet_ErrorFieldsReachTheManifest: the error fields the view computes and
// the summary's skipped output columns all reach the manifest, and an
// error_context integer keeps every digit. The control shows the generated
// type rounds that integer, so the overlay for error_context is not vacuous.
func TestGet_ErrorFieldsReachTheManifest(t *testing.T) {
	d, err := serveDetail(t, erroredDetail).Get("token", 5512)
	if err != nil {
		t.Fatal(err)
	}
	out, err := yaml.Marshal(ToManifest(d))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"user_message: The column policy_ref no longer exists on claims.",
		"wizard_step: output_columns",
		"- revision\n",
		"table: claims",
		"spine_row_id: 9007199254740993",
		"skipped_output_columns:",
		"source_dataset_name: claims",
		"error_type: CompositeRunnerSpecError",
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("manifest lacks %q:\n%s", want, out)
		}
	}

	if d.Job.ErrorContext == nil {
		t.Fatal("control: the generated type no longer decodes error_context")
	}
	if got, ok := (*d.Job.ErrorContext)["spine_row_id"].(float64); !ok || got != 9007199254740992 {
		t.Fatalf("control: the generated type decodes spine_row_id as %#v, not the rounded float64 it did;\n"+
			"the body overlay for error_context may be removable", (*d.Job.ErrorContext)["spine_row_id"])
	}
}

func TestGet_NullWizardStepStaysAbsent(t *testing.T) {
	d, err := serveDetail(t, runningDetail).Get("token", 5512)
	if err != nil {
		t.Fatal(err)
	}
	if d.Job.WizardStep != nil {
		t.Errorf("WizardStep = %q, want nil for a null wizard_step", *d.Job.WizardStep)
	}
	out, err := yaml.Marshal(ToManifest(d))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "wizard_step") {
		t.Errorf("manifest carries wizard_step for a null one:\n%s", out)
	}
}

// TestToRawManifest_IsTheBodysKeySet: -vv shows the keys the server sent and
// no others. The generated type would add every key it declares, as null.
func TestToRawManifest_IsTheBodysKeySet(t *testing.T) {
	d, err := serveDetail(t, runningDetail).Get("token", 5512)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := ToRawManifest(d)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := raw.RawResponse.(map[string]interface{})
	if !ok {
		t.Fatalf("raw_response is %T", raw.RawResponse)
	}
	var want map[string]interface{}
	if err := json.Unmarshal([]byte(runningDetail), &want); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Errorf("raw_response has %d keys, the body %d: %v", len(got), len(want), got)
	}
	if _, present := got["revision_state"]; present {
		t.Error("raw_response gained revision_state, which the body did not carry")
	}
}

// TestGet_NotFoundExitsThree: another organization's job and a missing one
// are the same 404, and it must keep the not-found exit code.
func TestGet_NotFoundExitsThree(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Get("token", 5512)
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d; err = %v", got, qerrors.ExitNotFound, err)
	}
}

// TestExamples_ReturnsTheBodyAndSendsItsFilters: join values are data cells,
// so the body has to come back byte for byte -- a float64 round trip would
// round this BigInteger key.
func TestExamples_ReturnsTheBodyAndSendsItsFilters(t *testing.T) {
	const body = `{"results":[{"id":1024,"composite_dry_run_job_id":5512,"spine_dataset_id":11,` +
		`"spine_row_id":7821,"bucket":"blocked_zero_match","source_alias":"claims",` +
		`"join_values":{"policy_id":12345678901234567890},"matched_upstream_row_ids":{"claims":[]},` +
		`"quarantined_upstream_row_ids":{"claims":[]},"created_at":"2026-09-22T10:01:34Z"}],"page":2,"total_rows":59}`

	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	var query map[string][]string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/composite-dry-runs/5512/examples", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	c, err := NewClient(mock.Server.URL, testOrgID, 0)
	if err != nil {
		t.Fatal(err)
	}

	page, raw, err := c.Examples("token", 5512, ExamplesParams{Bucket: "blocked_zero_match", SourceAlias: "claims", Page: 2, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != body {
		t.Errorf("body changed on the way through:\n got %s\nwant %s", raw, body)
	}
	if len(page.Results) != 1 || page.Results[0].SpineRowID != 7821 {
		t.Errorf("decoded page = %+v", page)
	}
	for param, want := range map[string]string{"bucket": "blocked_zero_match", "source_alias": "claims", "page": "2", "limit": "5"} {
		if got := query[param]; len(got) != 1 || got[0] != want {
			t.Errorf("%s = %v, want %q", param, got, want)
		}
	}
	if _, sent := query["reverse"]; sent {
		t.Error("reverse was sent; the route's default order must be left alone")
	}
}
