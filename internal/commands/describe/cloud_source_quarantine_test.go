package describe

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

func cloudSourceJSON(body any) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, body)
	}
}

func TestDescribeCloudSource_QuarantineBreakdown(t *testing.T) {
	const counts = `{
		"passed_validation":0,"failed_validation":35,
		"on_hold":2,"approved":0,"waiting_for_approval":4,"held_for_approval":20,
		"held_for_approval_by_reason":{"table_setting":18,"future_reason":2},
		"not_loaded":1
	}`
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			calls := 0
			countHandler := func(w http.ResponseWriter, r *http.Request) {
				calls++
				if query := r.URL.Query(); len(query) != 1 || query.Get("data_source_model_id") != "42" {
					t.Errorf("counts query = %v, want only data_source_model_id=42", query)
				}
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(counts))
			}
			out, stderr, err := serveDescribeCloudSourceWithCounts(t,
				cloudSourceJSON(sampleCloudSourceFullResponse()), cloudSourceJSON(sampleCloudSourceTinyResponse()),
				countHandler, "42", "-o", format)
			if err != nil || stderr != "" || calls != 1 {
				t.Fatalf("err = %v, stderr = %q, count requests = %d", err, stderr, calls)
			}
			if format == "table" {
				for _, want := range []string{
					"Rows:         9182 clean / 41 quarantined (0 passed validation, 35 failed validation)\n",
					"Quarantine:   on_hold=2 approved=0 waiting_for_approval=4 held_for_approval=20 (table_setting=18 future_reason=2)\n",
					"Not loaded:   1 duplicate copy not loaded: a reviewer kept another copy\n",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("summary lacks %q:\n%s", want, out)
					}
				}
				return
			}
			var summary cloud_sources.Summary
			if err := yaml.Unmarshal([]byte(out), &summary); err != nil {
				t.Fatal(err)
			}
			v, h := summary.QuarantineByValidation, summary.QuarantineByHold
			if v == nil || v.PassedValidation != 0 || v.FailedValidation != 35 {
				t.Errorf("validation = %+v", v)
			}
			if h == nil || h.OnHold != 2 || h.Approved != 0 || h.WaitingForApproval == nil || *h.WaitingForApproval != 4 ||
				h.HeldForApproval != 20 || h.HeldForApprovalByReason["table_setting"] != 18 || h.HeldForApprovalByReason["future_reason"] != 2 {
				t.Errorf("holds = %+v", h)
			}
			if n := summary.QuarantineNotLoaded; n == nil || *n != 1 {
				t.Errorf("not loaded = %v, want 1", n)
			}
		})
	}
}

func TestDescribeCloudSource_SkipsQuarantineBreakdown(t *testing.T) {
	zero := 0
	series := uuid.MustParse("0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b")
	for _, name := range []string{"zero rows", "unknown rows", "series without bindings", "series with bindings"} {
		t.Run(name, func(t *testing.T) {
			full, tiny := sampleCloudSourceFullResponse(), sampleCloudSourceTinyResponse()
			switch name {
			case "zero rows":
				tiny.BadRowsCount = &zero
			case "unknown rows":
				tiny.BadRowsCount = nil
			default:
				full.TableSeriesID = &series
				if name == "series with bindings" {
					cursors := []api.PeriodSyncCursorSchema{{DatasetID: 12, SettingsModelID: 3, PeriodKey: "2026-07"}}
					full.PeriodDeltaCursors = &cursors
				}
			}
			counts := func(w http.ResponseWriter, r *http.Request) {
				t.Errorf("unexpected count request: %s", r.URL)
				respondStatus(http.StatusInternalServerError)(w, r)
			}
			for _, format := range []string{"table", "json", "yaml"} {
				out, stderr, err := serveDescribeCloudSourceWithCounts(t, cloudSourceJSON(full), cloudSourceJSON(tiny), counts, "42", "-o", format)
				if err != nil || stderr != "" {
					t.Fatalf("%s: err = %v, stderr = %q", format, err, stderr)
				}
				assertNoCloudSourceBreakdown(t, out)
			}
		})
	}
}

func assertNoCloudSourceBreakdown(t *testing.T, out string) {
	t.Helper()
	for _, unwanted := range []string{"passed validation", "failed validation", "Quarantine:", "quarantine_by_validation", "quarantine_by_hold",
		"Not loaded:", "quarantine_not_loaded"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("unexpected %q in summary:\n%s", unwanted, out)
		}
	}
}

func TestDescribeCloudSource_QuarantineFailureWarns(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		for _, format := range []string{"table", "json", "yaml"} {
			out, stderr, err := serveDescribeCloudSourceWithCounts(t,
				cloudSourceJSON(sampleCloudSourceFullResponse()), cloudSourceJSON(sampleCloudSourceTinyResponse()),
				respondStatus(status), "42", "-o", format)
			if err != nil || !strings.Contains(stderr, "warning: quarantine counts unavailable:") {
				t.Fatalf("status %d, %s: err = %v, stderr = %q", status, format, err, stderr)
			}
			if !strings.Contains(out, "s3-orders") {
				t.Errorf("summary missing after count failure:\n%s", out)
			}
			assertNoCloudSourceBreakdown(t, out)
		}
	}
}
