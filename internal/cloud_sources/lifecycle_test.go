package cloud_sources

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func tinySource(state api.DataSourceState, schedule *string) CloudSourceTiny {
	return CloudSourceTiny{
		ID:               42,
		Name:             "s3-orders",
		DatasetID:        11,
		DatasetName:      "orders",
		DataSourceType:   api.DataSourceTypeS3,
		State:            state,
		Schedule:         schedule,
		UnresolvedAlerts: 2,
	}
}

// /tiny is the one read that can see a deleted source, so every lifecycle
// command starts there. Registering the handler on the /tiny path and nothing
// else is the assertion: a client that reached for GET /data-sources/42 would
// get the mock transport's 404.
func TestGetCloudSourceTinyReadsTheTinyRoute(t *testing.T) {
	c, mock := newCSClient(t)
	sched := "@hourly"
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+csOrgID+"/data-sources/42/tiny",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, tinySource(api.DataSourceStateDeleted, &sched))
		})

	got, err := c.GetCloudSourceTiny("tok", 42)
	if err != nil {
		t.Fatalf("GetCloudSourceTiny: %v", err)
	}
	if got.State != api.DataSourceStateDeleted || got.Name != "s3-orders" {
		t.Fatalf("got %+v, want the deleted s3-orders row", got)
	}
	if ScheduleOf(got) != "@hourly" {
		t.Errorf("ScheduleOf = %q, want @hourly", ScheduleOf(got))
	}
}

// Each call must POST to its own route and to nothing else. run-sensor-cronjob
// is here twice over, so to speak: it is also the whole of `undelete`, since
// there is no undelete route -- if this path ever moves, both commands break.
func TestLifecycleWritesPostToTheirOwnRoutes(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		call func(*Client) error
	}{
		{"enable", "/api/orgs/" + csOrgID + "/data-sources/42/run-sensor-cronjob",
			func(c *Client) error { return c.EnableCloudSource("tok", 42) }},
		{"disable", "/api/orgs/" + csOrgID + "/data-sources/42/disable-sensor-cronjob",
			func(c *Client) error { return c.DisableCloudSource("tok", 42) }},
		{"run sensor-job", "/api/orgs/" + csOrgID + "/data-sources/42/run-sensor-job",
			func(c *Client) error { return c.RunSensorJob("tok", 42) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newCSClient(t)
			mock.RegisterHandler(http.MethodPost, tc.path, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.SensorRunSchema{Result: "ok"})
			})
			if err := tc.call(c); err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			want := "POST " + tc.path
			if reqs := mock.Requests(); len(reqs) != 1 || reqs[0] != want {
				t.Errorf("requests = %v, want exactly [%s]", reqs, want)
			}
		})
	}
}

// The name is the server's "type the name to confirm" gate. It must ride in the
// query string; a request without it is a 422 the user cannot act on.
func TestDeleteCloudSourceSendsTheExactNameAsAQueryParameter(t *testing.T) {
	c, mock := newCSClient(t)
	var gotQuery string
	mock.RegisterHandler(http.MethodDelete, "/api/orgs/"+csOrgID+"/data-sources/42",
		func(w http.ResponseWriter, r *http.Request) {
			gotQuery = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, api.DataSourceDeleteResponseSchema{Result: "ok"})
		})

	if err := c.DeleteCloudSource("tok", 42, "s3 orders"); err != nil {
		t.Fatalf("DeleteCloudSource: %v", err)
	}
	if gotQuery != "name=s3+orders" {
		t.Errorf("query = %q, want name=s3+orders", gotQuery)
	}
}

// A duplicate sensor answers 409 ERR_QUEUE_KEY_CONFLICT and exits 2.
func TestRunSensorJobOnAQueuedSensorExitsTwo(t *testing.T) {
	c, mock := newCSClient(t)
	mock.RegisterHandler(http.MethodPost, "/api/orgs/"+csOrgID+"/data-sources/42/run-sensor-job",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]any{
				"detail": map[string]any{
					"msg":        "Sensor is already queued to be run.<br>We have moved it near the top of the queue so it runs sooner.",
					"error_code": "ERR_QUEUE_KEY_CONFLICT",
				},
			})
		})

	err := c.RunSensorJob("tok", 42)
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

// A missing source is exit 3 from every call in this file.
func TestLifecycleCallsMapA404ToExitThree(t *testing.T) {
	c, _ := newCSClient(t) // no handlers: the transport 404s everything
	for name, call := range map[string]func() error{
		"tiny":     func() error { _, err := c.GetCloudSourceTiny("tok", 42); return err },
		"enable":   func() error { return c.EnableCloudSource("tok", 42) },
		"activate": func() error { return c.ActivateCloudSource("tok", 42) },
		"disable":  func() error { return c.DisableCloudSource("tok", 42) },
		"sensor":   func() error { return c.RunSensorJob("tok", 42) },
		"delete":   func() error { return c.DeleteCloudSource("tok", 42, "s3-orders") },
	} {
		err := call()
		if err == nil {
			t.Fatalf("%s: expected an error on 404", name)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
			t.Errorf("%s: exit code = %d, want %d", name, code, qerrors.ExitNotFound)
		}
	}
}

func TestRequireNotDeleted(t *testing.T) {
	if err := RequireNotDeleted(ptrTiny(tinySource(api.DataSourceStateDisabled, nil)), "enabled"); err != nil {
		t.Fatalf("a disabled source must pass: %v", err)
	}
	err := RequireNotDeleted(ptrTiny(tinySource(api.DataSourceStateDeleted, nil)), "enabled")
	if err == nil {
		t.Fatal("a deleted source must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl undelete cloud-source 42") {
		t.Errorf("the refusal must name the remedy, got: %v", err)
	}
}

func TestRequireDeleted(t *testing.T) {
	if err := RequireDeleted(ptrTiny(tinySource(api.DataSourceStateDeleted, nil))); err != nil {
		t.Fatalf("a deleted source must pass: %v", err)
	}
	err := RequireDeleted(ptrTiny(tinySource(api.DataSourceStateDisabled, nil)))
	if err == nil {
		t.Fatal("a live source must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl enable cloud-source 42") {
		t.Errorf("the refusal must name the remedy, got: %v", err)
	}
}

// Enabling an active source changes nothing, so advising it would send the
// user on a no-op.
func TestRequireDeletedGivesAnActiveSourceNoEnableAdvice(t *testing.T) {
	err := RequireDeleted(ptrTiny(tinySource(api.DataSourceStateActive, nil)))
	if err == nil {
		t.Fatal("an active source must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "is active, not deleted") {
		t.Errorf("the refusal must say the source is active, got: %v", err)
	}
	if strings.Contains(err.Error(), "enable") {
		t.Errorf("an active source must get no enable advice, got: %v", err)
	}
}

func TestRequireActivePicksTheRemedyFromTheState(t *testing.T) {
	if err := RequireActive(ptrTiny(tinySource(api.DataSourceStateActive, nil)), "check for new files"); err != nil {
		t.Fatalf("an active source must pass: %v", err)
	}
	for state, wantRemedy := range map[api.DataSourceState]string{
		api.DataSourceStateDisabled: "qctl enable cloud-source 42",
		api.DataSourceStateDraft:    "qctl enable cloud-source 42",
		api.DataSourceStateDeleted:  "qctl undelete cloud-source 42",
	} {
		err := RequireActive(ptrTiny(tinySource(state, nil)), "check for new files")
		if err == nil {
			t.Fatalf("state %s must be refused", state)
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("state %s: exit code = %d, want %d", state, code, qerrors.ExitBadUsage)
		}
		if !strings.Contains(err.Error(), wantRemedy) {
			t.Errorf("state %s: want remedy %q, got: %v", state, wantRemedy, err)
		}
		if state == api.DataSourceStateDeleted && !strings.Contains(err.Error(), "Recovery depends on server support") {
			t.Errorf("a deleted source must explain the recovery limitation, got: %v", err)
		}
		if mentionsApply := strings.Contains(err.Error(), "qctl apply -f"); mentionsApply != (state == api.DataSourceStateDraft) {
			t.Errorf("state %s: the secrets-then-enable remedy belongs to drafts only, got: %v", state, err)
		}
	}
}

func ptrTiny(v CloudSourceTiny) *CloudSourceTiny { return &v }

const activatePath = "/api/orgs/" + csOrgID + "/data-sources/42"

// activateServer answers the full read with version 7 and records every PATCH
// body, replying with status and reply.
func activateServer(t *testing.T, status int, reply string) (*Client, *testutil.MockAPIServer, *[][]byte) {
	t.Helper()
	c, mock := newCSClient(t)
	mock.RegisterHandler(http.MethodGet, activatePath, func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":42,"version_id":7,"name":"uploads",`+
			`"dataset_id":11,"dataset_name":"orders","data_source_type":"qluster_ui","state":"disabled","schedule":""}`))
	})
	bodies := &[][]byte{}
	mock.RegisterHandler(http.MethodPatch, activatePath, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read PATCH body: %v", err)
		}
		*bodies = append(*bodies, b)
		testutil.RespondJSON(w, status, json.RawMessage(reply))
	})
	return c, mock, bodies
}

// The PATCH applies exactly the keys present in its body, so any key beyond
// these three would be a write enable never meant to make.
func TestActivateCloudSourcePatchesOnlyTheState(t *testing.T) {
	c, mock, bodies := activateServer(t, http.StatusOK, `{"id":42,"version_id":8,"state":"active"}`)

	if err := c.ActivateCloudSource("tok", 42); err != nil {
		t.Fatalf("ActivateCloudSource: %v", err)
	}
	if got, want := mock.Requests(), []string{"GET " + activatePath, "PATCH " + activatePath}; !slices.Equal(got, want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	if len(*bodies) != 1 {
		t.Fatalf("PATCH bodies = %d, want 1", len(*bodies))
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal((*bodies)[0], &got); err != nil {
		t.Fatalf("PATCH body is not a JSON object: %v\n%s", err, (*bodies)[0])
	}
	want := map[string]string{"id": "42", "version_id": "7", "state": `"active"`}
	if len(got) != len(want) {
		t.Errorf("PATCH body = %s, want exactly the keys id, version_id and state", (*bodies)[0])
	}
	for key, value := range want {
		if string(got[key]) != value {
			t.Errorf("PATCH body %s = %s, want %s", key, got[key], value)
		}
	}
}

// A 409 is a stale version_id: it keeps the server's exit code and says to
// re-run, naming the version the read saw.
func TestActivateCloudSourceConflictExitsTwoAndSaysReRun(t *testing.T) {
	c, _, _ := activateServer(t, http.StatusConflict,
		`{"detail":{"msg":"DataSourceModel version mismatch. DataSourceModel was updated separately and needs to be reloaded before being modified by the user.","error_id":""}}`)

	err := c.ActivateCloudSource("tok", 42)
	if err == nil {
		t.Fatal("expected an error on 409")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || !strings.Contains(opErr.Message, "version mismatch") {
		t.Errorf("the server's 409 must stay on the chain, got: %v", err)
	}
	for _, want := range []string{"read it at version 7", "re-run 'qctl enable cloud-source 42'"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error must contain %q, got: %v", want, err)
		}
	}
}

// The full read is enable's first request, so its failure must say enable
// failed, and a missing source must still exit 3. No GET handler is
// registered: the transport 404s it.
func TestActivateCloudSourceReadFailureSaysEnableFailed(t *testing.T) {
	c, mock := newCSClient(t)

	err := c.ActivateCloudSource("tok", 42)
	if err == nil {
		t.Fatal("expected an error on 404")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	if !strings.HasPrefix(err.Error(), "enable cloud source failed") {
		t.Errorf("the error must say enable failed, got: %v", err)
	}
	if got, want := mock.Requests(), []string{"GET " + activatePath}; !slices.Equal(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// Any other failure is the server's own error and exit code; a 403 is exit 4.
func TestActivateCloudSourceForbiddenExitsFour(t *testing.T) {
	c, _, _ := activateServer(t, http.StatusForbidden, `{"detail":{"msg":"Forbidden.","error_id":""}}`)

	err := c.ActivateCloudSource("tok", 42)
	if err == nil {
		t.Fatal("expected an error on 403")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitUnauthorized)
	}
	if strings.Contains(err.Error(), "re-run") {
		t.Errorf("only a 409 says to re-run, got: %v", err)
	}
}

func compositeSource(state api.DataSourceState) *CloudSourceTiny {
	src := tinySource(state, nil)
	src.Name = "orders-composite"
	src.DataSourceType = api.DataSourceTypeComposite
	return &src
}

// A composite source gets no enable advice: enable refuses it. The refusal
// names the linked table that owns it instead.
func TestRequireDeletedGivesACompositeSourceNoEnableAdvice(t *testing.T) {
	err := RequireDeleted(compositeSource(api.DataSourceStateDisabled))
	if err == nil {
		t.Fatal("a disabled composite source must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if strings.Contains(err.Error(), "qctl enable cloud-source 42") {
		t.Errorf("a composite source must get no enable advice, got: %v", err)
	}
	if !strings.Contains(err.Error(), "linked table orders (id=11)") {
		t.Errorf("the refusal must name the owning linked table, got: %v", err)
	}
}

func TestRequireNotCompositeRefusesOnlyACompositeSource(t *testing.T) {
	for _, typ := range []api.DataSourceType{api.DataSourceTypeS3, api.DataSourceTypeQlusterUI, ""} {
		src := tinySource(api.DataSourceStateDisabled, nil)
		src.DataSourceType = typ
		if err := RequireNotComposite(&src); err != nil {
			t.Errorf("type %q must pass: %v", typ, err)
		}
	}
	err := RequireNotComposite(compositeSource(api.DataSourceStateDisabled))
	if err == nil {
		t.Fatal("a composite source must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	for _, want := range []string{"linked table orders (id=11)", "does not turn composite sources on"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must contain %q, got: %v", want, err)
		}
	}
}

func TestRequireSensorEligible(t *testing.T) {
	for _, typ := range sensorEligibleTypes {
		src := tinySource(api.DataSourceStateDisabled, nil)
		src.DataSourceType = typ
		if err := RequireSensorEligible(&src); err != nil {
			t.Errorf("type %s must pass: %v", typ, err)
		}
	}

	upload := "qctl upload file <path> --table-id 11 --cloud-source-id 42"
	enable := "qctl enable cloud-source 42"
	for _, tc := range []struct {
		name   string
		typ    api.DataSourceType
		state  api.DataSourceState
		want   []string
		reject []string
	}{
		{"active upload source", api.DataSourceTypeQlusterUI, api.DataSourceStateActive,
			[]string{"upload source (type qluster_ui) of table orders (id=11)", upload}, []string{enable}},
		{"disabled upload source", api.DataSourceTypeQlusterUI, api.DataSourceStateDisabled,
			[]string{"upload source", "It is disabled", enable, upload}, nil},
		{"deleted upload source", api.DataSourceTypeQlusterUI, api.DataSourceStateDeleted,
			[]string{"upload source"}, []string{enable, "qctl upload file"}},
		{"composite", api.DataSourceTypeComposite, api.DataSourceStateActive,
			[]string{"linked table orders (id=11)", "no sensor"}, []string{enable}},
		{"no type", "", api.DataSourceStateActive,
			[]string{"has no source type set", SensorEligibleTypeList()}, []string{enable}},
		{"unknown type", "ftp", api.DataSourceStateActive,
			[]string{"has type ftp", SensorEligibleTypeList()}, []string{enable}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tinySource(tc.state, nil)
			src.DataSourceType = tc.typ
			err := RequireSensorEligible(&src)
			if err == nil {
				t.Fatal("must be refused")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal must contain %q, got: %v", want, err)
				}
			}
			for _, reject := range tc.reject {
				if strings.Contains(err.Error(), reject) {
					t.Errorf("refusal must not contain %q, got: %v", reject, err)
				}
			}
		})
	}
}
