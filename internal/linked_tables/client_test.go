package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"encoding/json"
	stderrors "errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func sampleManifest() *LinkedTableManifest {
	arity := "zero_or_one"
	return &LinkedTableManifest{
		APIVersion: "qluster.ai/v1",
		Kind:       "LinkedTable",
		Spec: LinkedTableSpec{
			Destination: "warehouse",
			Table:       TableSpec{Database: "analytics", Name: "orders_enriched"},
			Spine:       SpineSpec{Source: "orders", NaturalKeyColumns: []string{"order_id"}},
			Sources: map[string]SourceSpec{
				"orders": {Dataset: "raw_orders"},
				"customers": {
					Dataset:    "raw_customers",
					Arity:      &arity,
					JoinFields: map[string]JoinField{"customer_id": {Shorthand: "customer_id"}},
				},
			},
			Output: map[string]OutputMapping{
				"order_id": {Source: "orders", Field: "order_id"},
			},
		},
	}
}

func TestClientApply_SendsCompositeKind(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var received api.CompositeDatasetApplyManifest
	var gotValidateOnly string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		gotValidateOnly = r.URL.Query().Get("validate_only")
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"result": "revision_added", "dataset_id": 7, "settings_model_id": 8, "data_source_model_id": 9,
			"composite_spec_group_id":    "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10",
			"composite_spec_revision_id": "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33",
			"revision_state":             "draft", "release": "v2", "spec_sha256": "ab12",
			"desired_state": "enabled", "converged": false,
			"composite_dry_run_job_id": 812, "ingestion_job_id": 9001,
			"warnings": []any{map[string]any{"loc": []string{"revision", "output", "gooz", "field"}, "msg": "pruned", "code": "deleted_field_pruned"}},
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"

	result, raw, err := c.Apply("token", m)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if received.Kind != "CompositeDataset" {
		t.Fatalf("expected wire kind CompositeDataset, got %q", received.Kind)
	}
	if received.Spec.Spine.Source != "orders" {
		t.Fatalf("unexpected spine source: %q", received.Spec.Spine.Source)
	}
	if gotValidateOnly != "false" && gotValidateOnly != "" {
		t.Fatalf("expected validate_only false/empty, got %q", gotValidateOnly)
	}
	if result == nil {
		t.Fatal("expected a decoded answer")
	}
	if result.Result != "revision_added" || result.Release != "v2" || result.RevisionState != "draft" ||
		result.DesiredState != "enabled" || result.Converged ||
		result.CompositeSpecGroupID.String() != "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10" ||
		result.CompositeDryRunJobID == nil || *result.CompositeDryRunJobID != 812 ||
		result.IngestionJobID == nil || *result.IngestionJobID != 9001 ||
		len(result.Warnings) != 1 || result.Warnings[0].Msg != "pruned" {
		t.Fatalf("unexpected decoded answer: %+v", result)
	}
	if len(raw) == 0 {
		t.Fatalf("expected raw response body")
	}
}

// A 2xx without a result is still an apply the server accepted, so it is not
// an error; the caller gets the raw body and no decoded answer.
func TestClientApply_AnswerWithoutResultDecodesToNil(t *testing.T) {
	for name, body := range map[string]string{
		"no result":      `{"dataset_id": 7}`,
		"null":           `null`,
		"malformed uuid": `{"result": "created", "composite_spec_group_id": "nope"}`,
	} {
		t.Run(name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			})
			c, err := NewClient(mock.URL(), testOrgID, 0)
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			m := sampleManifest()
			m.Metadata.Name = "orders-enriched"
			result, raw, err := c.Apply("token", m)
			if err != nil {
				t.Fatalf("Apply: %v", err)
			}
			if result != nil {
				t.Fatalf("expected no decoded answer, got %+v", result)
			}
			if string(raw) != body {
				t.Fatalf("raw body = %q, want %q", raw, body)
			}
		})
	}
}

func TestClientValidate_SetsQueryAndDecodesTheVerdict(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotValidateOnly string
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		gotValidateOnly = r.URL.Query().Get("validate_only")
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"valid": false,
			"errors": []any{map[string]any{
				"loc": []string{"spec", "destination"}, "msg": "destination 'warehouse' not found in this organization",
				"code": "unresolved_reference",
			}},
			"warnings": []any{},
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"

	result, raw, err := c.Validate("token", m)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if gotValidateOnly != "true" {
		t.Fatalf("expected validate_only=true, got %q", gotValidateOnly)
	}
	if result.Valid || len(result.Errors) != 1 || len(raw) == 0 {
		t.Fatalf("unexpected verdict: %+v", result)
	}
	verdict := result.Err("orders-enriched")
	if code := qerrors.GetExitCode(verdict); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	want := "  - spec.destination: destination 'warehouse' not found in this organization (unresolved_reference)"
	if !strings.Contains(verdict.Error(), want) {
		t.Fatalf("verdict does not list the issue:\n%s", verdict.Error())
	}
}

// An answer with no 'valid' key cannot be read as a pass: absent must not
// decode to false-with-no-errors or to true.
func TestClientValidate_AnswerWithoutValidIsAnError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"
	_, _, err = c.Validate("token", m)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitAPIIncompatible {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitAPIIncompatible, err)
	}
}

// A 400/409 whose detail is an issue list renders every issue, and keeps the
// exit code the HTTP status maps to.
func TestClientApply_RefusalRendersTheIssueList(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": []any{
			map[string]any{"loc": []string{"spec", "table", "name"}, "msg": "spec.table.name is frozen after creation", "code": "immutable_field_changed"},
			map[string]any{"loc": []string{"spec", "destination"}, "msg": "spec.destination is frozen after creation", "code": "immutable_field_changed"},
		}})
	})
	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"
	_, _, err = c.Apply("token", m)
	var issuesErr *IssuesError
	if !stderrors.As(err, &issuesErr) || len(issuesErr.Issues) != 2 {
		t.Fatalf("expected an *IssuesError with 2 issues, got %T: %v", err, err)
	}
	for _, want := range []string{
		"spec.table.name: spec.table.name is frozen after creation (immutable_field_changed)",
		"spec.destination: spec.destination is frozen after creation (immutable_field_changed)",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err.Error())
		}
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestClientApply_SurfacesHTTPError(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": "bad spec"})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"

	if _, _, err := c.Apply("token", m); err == nil {
		t.Fatalf("expected error on 422, got nil")
	}
}

// Converging a manifest usually launches a composite dry run (a revision that
// is already enabled, or already has a current successful dry run, converges
// without one), so a full exploratory budget surfaces here as a 429
// (create_and_run_composite_dry_run, atlas
// capi/capi/controllers/composite_dry_run_controller.py, declared in the
// route's `responses` on apply_composite_dataset_view,
// capi/capi/views/composite_dataset_apply_view.py). Retrying is safe: the 429
// rolls the whole request back (get_session_for_fastapi,
// cettings/cettings/db.py) and apply is create-or-converge, so the identical
// request is the right retry.
// That only works if Retry-After survives to the caller.
//
// The header (9) and context.retry_after_seconds (15) disagree on purpose: with
// both at 15 this test passes even with the header dropped, because Task 2's
// body fallback supplies the same number.
func TestClientApply_ThrottleCarriesRetryAfter(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "9")
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]any{
			"detail": map[string]any{
				"msg":        "You have 3 dry run(s) in progress (limit: 3). Please wait for an existing dry run to complete before starting a new one.",
				"error_code": "DRY_RUN_RATE_LIMIT",
				"context": map[string]any{
					"current_count":       3,
					"max_allowed":         3,
					"limit_kind":          "concurrent_execution",
					"retry_after_seconds": 15,
				},
			},
		})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"

	_, _, err = c.Apply("token", m)
	var throttled *apierror.ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 9*time.Second {
		t.Errorf("RetryAfter = %s, want 9s — the response header was dropped", throttled.RetryAfter)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d: a throttle is not bad usage", code, qerrors.ExitGenericError)
	}
}

// The other shape: a 429 that never reached atlas. The generated parser hard
// fails on it, so the error arrives through Client.HandleError instead -- and
// must still be a throttle carrying the intermediary's Retry-After.
func TestClientApply_UnparseableThrottleBodyStillCarriesRetryAfter(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/composite-datasets/apply", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "6")
		testutil.RespondJSON(w, http.StatusTooManyRequests, map[string]any{"detail": "slow down"})
	})

	c, err := NewClient(mock.URL(), testOrgID, 0)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	m := sampleManifest()
	m.Metadata.Name = "orders-enriched"

	_, _, err = c.Apply("token", m)
	var throttled *apierror.ThrottleError
	if !stderrors.As(err, &throttled) {
		t.Fatalf("expected a *apierror.ThrottleError, got %T: %v", err, err)
	}
	if throttled.RetryAfter != 6*time.Second {
		t.Errorf("RetryAfter = %s, want 6s", throttled.RetryAfter)
	}
}
