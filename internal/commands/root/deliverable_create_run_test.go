package root

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const createRunTestPath = deliverableTestPath + "/" + liveDeliverableID + "/runs"

// createRunServer is the revision test server with a configurable listing of
// the live deliverable's runs and a create-run route that records each body.
type createRunServer struct {
	*deliverableTestServer
	runQueries []string
	bodies     []map[string]any
}

func resetCreateRunFlags(t *testing.T) {
	t.Helper()
	cmd, _, err := GetRootCmd().Find([]string{"create", "deliverable-run"})
	if err != nil {
		t.Fatal(err)
	}
	reset := func() {
		for _, name := range []string{"params-file", "period", "allow-empty", "use-enabled-revision", "render", "wait", "wait-timeout"} {
			f := cmd.Flags().Lookup(name)
			if err := f.Value.Set(f.DefValue); err != nil {
				t.Fatal(err)
			}
			f.Changed = false
		}
	}
	reset()
	t.Cleanup(reset)
}

// newCreateRunServer answers the live deliverable's run listing with runs and
// its create-run POST with status and response.
func newCreateRunServer(t *testing.T, runs []string, status int, response string) *createRunServer {
	t.Helper()
	resetCreateRunFlags(t)
	s := &createRunServer{deliverableTestServer: revisionTestServer(t)}
	listing := fmt.Sprintf(`{"results":[%s],"total_rows":%d,"page":1}`, strings.Join(runs, ","), len(runs))
	s.RegisterHandler("GET", createRunTestPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		s.runQueries = append(s.runQueries, r.URL.RawQuery)
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(listing))
	})
	s.RegisterHandler("POST", createRunTestPath, func(w http.ResponseWriter, r *http.Request) {
		checkDeliverableToken(t, r)
		data, _ := io.ReadAll(r.Body)
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Errorf("body %q: %v", data, err)
		}
		s.bodies = append(s.bodies, body)
		testutil.RespondJSON(w, status, json.RawMessage(response))
	})
	return s
}

func (s *createRunServer) posts() int {
	n := 0
	for _, request := range s.Requests() {
		if strings.HasPrefix(request, "POST") {
			n++
		}
	}
	return n
}

// runRecordJSON is a run of the live deliverable with typed run parameters.
func runRecordJSON(id, sequence int, state, mode, revisionID, typed string) string {
	return fmt.Sprintf(`{"id":%d,"deliverable_id":%q,"revision_id":%q,"revision_mode":%q,"state":%q,`+
		`"attempt_id":0,"sequence":%d,"allow_empty":false,"msg":"The deliverable run is open.",`+
		`"created_at":"2026-10-01T12:30:00Z","updated_at":"2026-10-01T12:30:00Z","run_params":{},"run_params_typed":%s}`,
		id, liveDeliverableID, revisionID, mode, state, sequence, typed)
}

func stringParam(name, text string) string {
	return fmt.Sprintf(`{%q:{"type":"string","text":%q}}`, name, text)
}

var createdNewPeriodRun = runRecordJSON(57, 1, "open", "new_period", enabledRevisionID, stringParam("period", "2026-09"))

func runCreateRun(t *testing.T, input string, args ...string) (string, string, error) {
	t.Helper()
	cmd := GetRootCmd()
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"create", "deliverable-run"}, args...))
	t.Cleanup(func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetIn(nil)
		cmd.SetArgs(nil)
		resetPersistentFlag(t, cmd, "output", "table")
	})
	_, err := executeC()
	return out.String(), stderr.String(), err
}

func TestCreateDeliverableRunNewPeriodSendsTypedParams(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		args        []string
		typed       map[string]any
	}{
		{"period flag", "", []string{"--period", "2026-09"}, map[string]any{"type": "string", "text": "2026-09"}},
		{"yaml float", "period: 2026.0\n", []string{"--params-file", "-"}, map[string]any{"type": "float", "text": "2026.0"}},
		{"yaml exponent", "period: 1e6\n", []string{"--params-file", "-"}, map[string]any{"type": "float", "text": "1000000.0"}},
		{"yaml integer", "period: 2026\n", []string{"--params-file", "-"}, map[string]any{"type": "integer", "text": "2026"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
			out, stderr, err := runCreateRun(t, tc.input, append([]string{"monthly-bordereau"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"run_params_typed": map[string]any{"period": tc.typed}}
			if len(s.bodies) != 1 || !reflect.DeepEqual(s.bodies[0], want) {
				t.Errorf("bodies = %v, want %v", s.bodies, want)
			}
			wantOut := "deliverable-run/57 created\n" +
				"  issue   : #1\n" +
				"  mode    : new period\n" +
				"  release : 2026.1 (revision " + enabledRevisionID + ")\n" +
				"  params  : period=2026-09\n" +
				"Next: qctl describe deliverable-run 57\n"
			if out != wantOut || stderr != "" {
				t.Errorf("out = %q\nstderr = %q", out, stderr)
			}
			wantRequests := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "runs"),
				"GET " + revisionTestPath + enabledRevisionID, "POST " + createRunTestPath}
			if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
				t.Errorf("requests = %v, want %v", got, wantRequests)
			}
			if got := strings.Join(s.runQueries, " "); got != "limit=500&order_by=id&page=1&reverse=false" {
				t.Errorf("runs query = %q", got)
			}
		})
	}
}

func TestCreateDeliverableRunSendsFlagsOnlyWhenGiven(t *testing.T) {
	for _, tc := range []struct {
		flag, key string
	}{
		{"--allow-empty", "allow_empty"},
		{"--use-enabled-revision", "use_enabled_revision"},
	} {
		t.Run(tc.flag, func(t *testing.T) {
			s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
			if _, _, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-09", tc.flag); err != nil {
				t.Fatal(err)
			}
			if len(s.bodies) != 1 || s.bodies[0][tc.key] != true || len(s.bodies[0]) != 2 {
				t.Errorf("bodies = %v, want %s alone beside the params", s.bodies, tc.key)
			}
		})
	}
}

func TestCreateDeliverableRunRefusesMissingRequiredParamBeforePOST(t *testing.T) {
	for _, input := range []string{"carrier: X\n", "period: null\n", "period: ' '\n", "Period: '2026-09'\n"} {
		t.Run(input, func(t *testing.T) {
			s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
			out, _, err := runCreateRun(t, input, "monthly-bordereau", "--params-file", "-")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || out != "" || !strings.Contains(err.Error(), `"period"`) {
				t.Fatalf("out = %q; exit = %d; %v", out, code, err)
			}
			if n := s.posts(); n != 0 {
				t.Errorf("POSTs = %d, want 0", n)
			}
		})
	}
}

func TestCreateDeliverableRunRefusesNoEnabledRevisionBeforePOST(t *testing.T) {
	for _, tc := range []struct {
		name string
		runs []string
		args []string
	}{
		{"new period", nil, []string{"--period", "2026-09"}},
		{"re-issue on the enabled revision", []string{runRecordJSON(40, 1, "delivered", "new_period", draftRevisionID, stringParam("period", "2026-09"))},
			[]string{"--period", "2026-09", "--use-enabled-revision"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCreateRunServer(t, tc.runs, http.StatusOK, createdNewPeriodRun)
			s.RegisterHandler("GET", deliverableTestPath+"/"+liveDeliverableID, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(strings.ReplaceAll(liveDeliverableRecord,
					`"enabled_revision_id":"`+enabledRevisionID+`"`, `"enabled_revision_id":null`)))
			})
			_, _, err := runCreateRun(t, "", append([]string{"monthly-bordereau"}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "qctl enable deliverable monthly-bordereau") {
				t.Fatalf("exit = %d; %v", code, err)
			}
			if n := s.posts(); n != 0 {
				t.Errorf("POSTs = %d, want 0", n)
			}
		})
	}
}

func TestCreateDeliverableRunReissueSkipsTheEnabledRevisionsParams(t *testing.T) {
	delivered := runRecordJSON(40, 1, "delivered", "new_period", draftRevisionID, stringParam("carrier", "X"))
	created := runRecordJSON(58, 2, "open", "reissue_as_delivered", draftRevisionID, stringParam("carrier", "X"))
	s := newCreateRunServer(t, []string{delivered}, http.StatusOK, created)
	out, stderr, err := runCreateRun(t, "carrier: X\n", "monthly-bordereau", "--params-file", "-")
	if err != nil {
		t.Fatal(err)
	}
	wantOut := "deliverable-run/58 created\n" +
		"  issue   : #2\n" +
		"  mode    : re-issue as delivered\n" +
		"  release : 2026.2 (revision " + draftRevisionID + ")\n" +
		"  params  : carrier=X\n" +
		"Next: qctl describe deliverable-run 58\n"
	if out != wantOut || stderr != "" {
		t.Errorf("out = %q\nstderr = %q", out, stderr)
	}
	wantRequests := []string{deliverableRequest(), deliverableRequest(liveDeliverableID), deliverableRequest(liveDeliverableID, "runs"),
		"POST " + createRunTestPath, "GET " + revisionTestPath + draftRevisionID}
	if got := s.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %v, want %v", got, wantRequests)
	}
}

func TestCreateDeliverableRunNamesTheRevisionWhenItsReadFails(t *testing.T) {
	delivered := runRecordJSON(40, 1, "delivered", "new_period", draftRevisionID, stringParam("period", "2026-08"))
	created := runRecordJSON(58, 2, "open", "reissue_as_delivered", draftRevisionID, stringParam("period", "2026-08"))
	s := newCreateRunServer(t, []string{delivered}, http.StatusOK, created)
	s.RegisterHandler("GET", revisionTestPath+draftRevisionID, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusInternalServerError, json.RawMessage(`{"detail":"boom"}`))
	})
	out, _, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-08")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "  release : revision "+draftRevisionID+"\n") {
		t.Errorf("out = %q", out)
	}
}

func TestCreateDeliverableRunNotes(t *testing.T) {
	deliveredAugust := runRecordJSON(40, 1, "delivered", "new_period", draftRevisionID, stringParam("carrier", "X"))
	for _, tc := range []struct {
		name, input string
		args        []string
		runs        []string
		created     string
		want        string
		notWant     string
	}{
		{
			name: "unused parameter", input: "period: '2026-09'\nextra: 1\n", args: []string{"--params-file", "-"},
			created: createdNewPeriodRun,
			want:    `Note: release 2026.1 does not use parameter "extra"; it still counts toward the run's period identity`,
		},
		{
			name: "re-issue lacks the enabled revision's names", input: "carrier: X\n",
			args: []string{"--params-file", "-", "--use-enabled-revision"}, runs: []string{deliveredAugust},
			created: runRecordJSON(58, 2, "open", "reissue_enabled", enabledRevisionID, stringParam("carrier", "X")),
			want:    `Note: the enabled release 2026.1 also uses "period"; adding it would make the run a new period instead of a re-issue`,
		},
		{
			name: "use-enabled-revision on a new period", args: []string{"--period", "2026-09", "--use-enabled-revision"},
			created: createdNewPeriodRun,
			want:    "Note: --use-enabled-revision has no effect: these parameters match no delivered run",
		},
		{
			name: "pending run with the same parameters", input: "carrier: X\n", args: []string{"--params-file", "-"},
			runs: []string{deliveredAugust,
				runRecordJSON(45, 2, "rendered", "reissue_as_delivered", draftRevisionID, stringParam("carrier", "X")),
				runRecordJSON(46, 3, "cancelled", "reissue_as_delivered", draftRevisionID, stringParam("carrier", "X")),
				runRecordJSON(47, 1, "open", "new_period", enabledRevisionID, stringParam("carrier", "Y"))},
			created: runRecordJSON(58, 4, "open", "reissue_as_delivered", draftRevisionID, stringParam("carrier", "X")),
			want:    "Note: run #2 (deliverable-run/45, Draft ready) already has these parameters\n",
			notWant: "deliverable-run/46",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newCreateRunServer(t, tc.runs, http.StatusOK, tc.created)
			out, stderr, err := runCreateRun(t, tc.input, append([]string{"monthly-bordereau"}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(stderr, tc.want) || strings.Contains(out, "Note:") {
				t.Errorf("stderr = %q\nout = %q", stderr, out)
			}
			if strings.Count(stderr, "Note:") != 1 || (tc.notWant != "" && strings.Contains(stderr, tc.notWant)) {
				t.Errorf("stderr = %q, want exactly one note", stderr)
			}
			if n := s.posts(); n != 1 {
				t.Errorf("POSTs = %d, want 1", n)
			}
		})
	}
}

func TestCreateDeliverableRunMachineFormats(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
		out, stderr, err := runCreateRun(t, "period: '2026-09'\nextra: 1\n", "monthly-bordereau", "--params-file", "-", "-o", "json")
		if err != nil {
			t.Fatal(err)
		}
		assertSeriesJSON(t, out, createdNewPeriodRun)
		if !strings.Contains(stderr, `parameter "extra"`) || strings.Contains(out, "Note:") {
			t.Errorf("stderr = %q", stderr)
		}
	})
	t.Run("yaml", func(t *testing.T) {
		newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
		out, _, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-09", "-o", "yaml")
		if err != nil {
			t.Fatal(err)
		}
		testutil.AssertYAMLReadsAsJSON(t, out, createdNewPeriodRun)
	})
	t.Run("name", func(t *testing.T) {
		newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
		out, _, err := runCreateRun(t, "", "monthly-bordereau", "--period", "2026-09", "-o", "name")
		if err != nil || out != "deliverable-run/57\n" {
			t.Fatalf("out = %q, err = %v", out, err)
		}
	})
}

func TestCreateDeliverableRunConflictHint(t *testing.T) {
	// The hint names the deliverable by its slug however the argument named it.
	for _, ref := range []string{"monthly-bordereau", liveDeliverableID} {
		t.Run(ref, func(t *testing.T) {
			s := newCreateRunServer(t, nil, http.StatusConflict,
				`{"detail":{"msg":"Cannot create a run for period 2026-09.","error_code":"NEW_PERIOD_IN_FLIGHT"}}`)
			out, _, err := runCreateRun(t, "", ref, "--period", "2026-09")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || out != "" {
				t.Fatalf("out = %q; exit = %d; %v", out, code, err)
			}
			if !strings.Contains(err.Error(), "qctl get deliverable-runs monthly-bordereau --state open") {
				t.Errorf("err = %v", err)
			}
			if n := s.posts(); n != 1 {
				t.Errorf("POSTs = %d, want 1", n)
			}
		})
	}
}

func TestCreateDeliverableRunRefusesATrashedDeliverable(t *testing.T) {
	s := newCreateRunServer(t, nil, http.StatusOK, createdNewPeriodRun)
	_, _, err := runCreateRun(t, "", "weekly-claims")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "qctl undelete deliverable weekly-claims") {
		t.Fatalf("exit = %d; %v", code, err)
	}
	if got, want := s.Requests(), []string{deliverableRequest(), deliverableRequest(trashedDeliverableID)}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

func TestCreateDeliverableRunLocalErrorsBeforeCredentials(t *testing.T) {
	for _, args := range [][]string{
		{""}, {" "}, {"table/42"}, {liveDeliverableID, "--period", ""}, {liveDeliverableID, "--period", " "},
		{liveDeliverableID, "--params-file", ""}, {liveDeliverableID, "--period", "2026-09", "--params-file", "unused.yaml"},
		{liveDeliverableID, "--params-file", "/nonexistent/params.yaml"}, {liveDeliverableID, "-o", "wide"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			resetCreateRunFlags(t)
			err := runOutsideGetWithoutContext(t, append([]string{"create", "deliverable-run"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit = %d, want 2: %v", code, err)
			}
		})
	}
}
