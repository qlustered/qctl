package deliverables

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const createRunPath = testPath + "/" + monthlyID + "/runs"

func createRunResponse() map[string]any {
	return map[string]any{
		"id": 42, "deliverable_id": monthlyID, "revision_id": monthlyID, "revision_mode": "reissue_as_delivered",
		"state": "open", "attempt_id": 0, "sequence": 2, "allow_empty": false, "msg": "The deliverable run is open.",
		"run_params":       map[string]any{"period": "2026-09"},
		"run_params_typed": map[string]any{"period": map[string]any{"type": "string", "text": "2026-09"}},
	}
}

func TestCreateRunSendsTypedParamsAndOnlyTheFlagsGiven(t *testing.T) {
	text := "2026-09"
	for _, tc := range []struct {
		name string
		req  CreateRunRequest
		want string
	}{
		{name: "nil params", want: `{"run_params_typed":{}}`},
		{name: "empty params", req: CreateRunRequest{Params: map[string]api.RunParameterValue{}}, want: `{"run_params_typed":{}}`},
		{name: "params and flags", req: CreateRunRequest{
			Params: map[string]api.RunParameterValue{
				"period": {Type: api.String, Text: &text},
				"note":   {Type: api.Null},
			},
			AllowEmpty: true, UseEnabledRevision: true,
		}, want: `{"allow_empty":true,"run_params_typed":{"note":{"text":null,"type":"null"},"period":{"text":"2026-09","type":"string"}},"use_enabled_revision":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			var body map[string]any
			mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				if err := json.Unmarshal(data, &body); err != nil {
					t.Errorf("body %q: %v", data, err)
				}
				testutil.RespondJSON(w, http.StatusOK, createRunResponse())
			})
			if _, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), tc.req); err != nil {
				t.Fatal(err)
			}
			if got := mock.Requests(); !slices.Equal(got, []string{"POST " + createRunPath}) {
				t.Errorf("requests = %v", got)
			}
			got, _ := json.Marshal(body)
			if string(got) != tc.want {
				t.Errorf("body = %s, want %s", got, tc.want)
			}
			if _, ok := body["run_params"]; ok {
				t.Errorf("body sends run_params: %s", got)
			}
		})
	}
}

func TestCreateRunDecodesTheRunAndKeepsTheBody(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":42,"deliverable_id":"` + monthlyID + `","revision_id":"` + monthlyID + `",
			"revision_mode":"new_period","state":"open","attempt_id":0,"sequence":3,"allow_empty":false,
			"run_params":{"year":2026.0},"run_params_typed":{"year":{"type":"float","text":"2026.0"}},"extra":"kept"}`))
	})
	result, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	run := result.Run
	if run.ID != 42 || run.Sequence != 3 || run.RevisionMode != api.NewPeriod || run.State != api.DeliverableRunStateOpen {
		t.Errorf("run = %#v", run)
	}
	if got, ok := run.RunParams["year"].(float64); !ok || got != 2026 {
		t.Errorf("year = %#v, want float64 2026", run.RunParams["year"])
	}
	if run.RunParamsTyped == nil {
		t.Fatal("run_params_typed missing")
	}
	if year := (*run.RunParamsTyped)["year"]; year.Text == nil || *year.Text != "2026.0" || year.Type != api.Float {
		t.Errorf("run_params_typed = %#v", *run.RunParamsTyped)
	}
	if !strings.Contains(string(result.Body), `"extra":"kept"`) {
		t.Errorf("body = %s", result.Body)
	}
}

func TestCreateRunConflictsKeepExitTwoAndNameTheNextCommand(t *testing.T) {
	for _, tc := range []struct {
		code string
		hint string
	}{
		{"NEW_PERIOD_IN_FLIGHT", "qctl get deliverable-runs monthly-bordereau --state open (likewise rendered, stale, signed_off)"},
		{"REISSUE_NOT_LATEST", "qctl get deliverable-runs monthly-bordereau --state delivered"},
		{"NO_ENABLED_REVISION", "qctl enable deliverable monthly-bordereau (its only draft), or choose one with --release <release>"},
		{"", ""},
	} {
		t.Run(tc.code, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			detail := map[string]any{"msg": "Cannot create a run for period 2026-09."}
			if tc.code != "" {
				detail["error_code"] = tc.code
			}
			mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": detail})
			})
			_, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{Ref: "monthly-bordereau"})
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2; error = %v", code, err)
			}
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != tc.code {
				t.Errorf("API metadata lost: %#v", apiErr)
			}
			if !strings.Contains(err.Error(), "Cannot create a run for period 2026-09.") {
				t.Errorf("error = %q, want the server message", err)
			}
			if tc.hint == "" {
				if strings.Contains(err.Error(), "qctl ") {
					t.Errorf("error = %q, want no hint", err)
				}
				return
			}
			if !strings.HasSuffix(err.Error(), tc.hint) {
				t.Errorf("error = %q, want it to end with %q", err, tc.hint)
			}
		})
	}
}

func TestCreateRunHintShellQuotesTheRef(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"msg": "not latest", "error_code": "REISSUE_NOT_LATEST"}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{Ref: "Monthly Bordereau"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2; error = %v", code, err)
	}
	if want := "qctl get deliverable-runs 'Monthly Bordereau' --state delivered"; !strings.HasSuffix(err.Error(), want) {
		t.Errorf("error = %q, want it to end with %q", err, want)
	}
}

func TestCreateRunHintNamesTheUUIDWithoutARef(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"msg": "no enabled revision", "error_code": "NO_ENABLED_REVISION"}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{})
	if !strings.Contains(err.Error(), "qctl enable deliverable "+monthlyID+" ") {
		t.Errorf("error = %q", err)
	}
}

func TestCreateRunScopeErrorIsReadable(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
			"msg": "Run parameter 'period' is required.", "error_code": "SCOPE_ERROR", "loc": []any{"body", "run_params", "period"},
		}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{Ref: "monthly-bordereau"})
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit = %d, want 2; error = %v", code, err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity || apiErr.Code != "SCOPE_ERROR" {
		t.Errorf("API metadata lost: %#v", apiErr)
	}
	if !strings.Contains(err.Error(), "Run parameter 'period' is required.") || strings.Contains(err.Error(), "qctl ") {
		t.Errorf("error = %q", err)
	}
}

func TestCreateRunForbiddenNamesThePermission(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("POST", createRunPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"msg": "Permission denied", "error_code": "ACCESS_DENIED"}})
	})
	_, err := NewClient(mock.URL(), testOrgID, 0).CreateRun("token", uuid.MustParse(monthlyID), CreateRunRequest{})
	assertPermissionRefusal(t, err)
}
