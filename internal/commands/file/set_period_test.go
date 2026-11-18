package file

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

const (
	periodPath  = "/api/orgs/" + testOrgID + "/stored-items/123/reporting-period"
	datasetPath = "/api/orgs/" + testOrgID + "/datasets/42"
)

// setupTestCommand builds a root that behaves like the shipped one for what
// these tests assert: TypeArgsErrors makes an arity error exit 2, the
// PersistentPreRunE refuses an undeclared -o, and StripKindRefs takes the
// file/ prefix off the id.
func setupTestCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "")
	rootCmd.PersistentFlags().String("columns", "", "")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "")
	rootCmd.PersistentFlags().CountP("verbose", "v", "")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return cmdutil.ValidateOutputFormat(cmd)
	}
	rootCmd.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(rootCmd)
	cmdutil.StripKindRefs(rootCmd)
	return rootCmd
}

func newTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return mock
}

func execSetPeriod(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"file", "set-period"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// registerAssign answers the PUT with the file moved to table 42 and records
// the request body.
func registerAssign(t *testing.T, mock *testutil.MockAPIServer) *string {
	t.Helper()
	body := new(string)
	mock.RegisterHandler(http.MethodPut, periodPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		*body = string(raw)
		testutil.RespondJSON(w, http.StatusOK,
			json.RawMessage(`{"id":123,"dataset_id":42,"routing_period_key":"2026-07"}`))
	})
	return body
}

func registerDataset(mock *testutil.MockAPIServer, status int) {
	mock.RegisterHandler(http.MethodGet, datasetPath, func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			testutil.RespondJSON(w, status, json.RawMessage(`{"detail":{"msg":"no access"}}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":42,"name":"orders_2026_07"}`))
	})
}

func TestSetPeriodSendsThePeriodAndNamesTheTable(t *testing.T) {
	mock := newTestEnv(t)
	body := registerAssign(t, mock)
	registerDataset(mock, http.StatusOK)

	out, _, err := execSetPeriod(t, "file/123", "2026-07")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *body != `{"period_key":"2026-07"}` {
		t.Errorf("request body = %s, want {\"period_key\":\"2026-07\"}", *body)
	}
	for _, want := range []string{"file/123 sent to period 2026-07", "table: orders_2026_07 (ID 42)", "Next: qctl describe file 123"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestSetPeriodReportsANewlyCreatedTable(t *testing.T) {
	mock := newTestEnv(t)
	mock.RegisterHandler(http.MethodPut, periodPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != `{"period_key":"2026-08"}` {
			t.Errorf("assignment body = %s", raw)
		}
		testutil.RespondJSON(w, http.StatusOK,
			json.RawMessage(`{"id":123,"dataset_id":84,"routing_period_key":"2026-08"}`))
	})
	newTablePath := "/api/orgs/" + testOrgID + "/datasets/84"
	mock.RegisterHandler(http.MethodGet, newTablePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":84,"name":"orders_2026_08"}`))
	})
	out, _, err := execSetPeriod(t, "123", "2026-08", "-o", "json")
	if err != nil {
		t.Fatalf("assignment failed: %v", err)
	}
	var got setPeriodResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v: %s", err, out)
	}
	if got.DatasetID != 84 || got.DatasetName == nil || *got.DatasetName != "orders_2026_08" || got.RoutingPeriodKey == nil || *got.RoutingPeriodKey != "2026-08" {
		t.Errorf("assigned result = %+v", got)
	}
	if reqs := mock.Requests(); len(reqs) != 2 || reqs[0] != "PUT "+periodPath || reqs[1] != "GET "+newTablePath {
		t.Errorf("requests = %v, want assignment before new table lookup", reqs)
	}
}

// A caller who cannot read the table still gets the move reported, at exit 0.
func TestSetPeriodSurvivesAnUnreadableTable(t *testing.T) {
	mock := newTestEnv(t)
	registerAssign(t, mock)
	registerDataset(mock, http.StatusForbidden)

	out, _, err := execSetPeriod(t, "123", "2026-07")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "table: ID 42 (name unavailable)") {
		t.Errorf("output %q does not report the unavailable name", out)
	}
}

func TestSetPeriodJSONDocument(t *testing.T) {
	for _, tc := range []struct {
		name          string
		datasetStatus int
		wantName      any
	}{
		{"named", http.StatusOK, "orders_2026_07"},
		{"name unavailable", http.StatusForbidden, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			registerAssign(t, mock)
			registerDataset(mock, tc.datasetStatus)

			out, errOut, err := execSetPeriod(t, "123", "2026-07", "-o", "json")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatalf("stdout is not JSON: %v\n%s", err, out)
			}
			want := map[string]any{"id": float64(123), "dataset_id": float64(42), "dataset_name": tc.wantName, "routing_period_key": "2026-07"}
			for k, v := range want {
				if doc[k] != v {
					t.Errorf("%s = %v, want %v", k, doc[k], v)
				}
			}
			if len(doc) != len(want) {
				t.Errorf("document %v has keys beyond %v", doc, want)
			}
			if !strings.Contains(errOut, "sent to period 2026-07") {
				t.Errorf("stderr %q does not carry the prose", errOut)
			}
		})
	}
}

// -o name needs no table name, so it costs exactly the PUT.
func TestSetPeriodNameOutput(t *testing.T) {
	mock := newTestEnv(t)
	registerAssign(t, mock)
	registerDataset(mock, http.StatusOK)

	out, _, err := execSetPeriod(t, "123", "2026-07", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "file/123\n" {
		t.Errorf("stdout = %q, want file/123", out)
	}
	if reqs := mock.Requests(); len(reqs) != 1 || reqs[0] != "PUT "+periodPath {
		t.Errorf("requests = %v, want only the PUT", reqs)
	}
}

func TestSetPeriodUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"non-numeric id":    {"abc", "2026-07"},
		"zero id":           {"0", "2026-07"},
		"negative id":       {"-5", "2026-07"},
		"wrong kind prefix": {"table/123", "2026-07"},
		"empty key":         {"123", ""},
		"leading space":     {"123", " 2026-07"},
		"two lines":         {"123", "2026\n07"},
		"too long":          {"123", strings.Repeat("x", 201)},
		"no period key":     {"123"},
		"three args":        {"123", "2026-07", "extra"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newTestEnv(t)
			registerAssign(t, mock)

			_, _, err := execSetPeriod(t, append([]string{"--"}, args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("a local refusal made requests: %v", reqs)
			}
		})
	}
}

func TestSetPeriodRefusals(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		wantExit int
		wantHint string
	}{
		{"no open routing alert", http.StatusConflict,
			`{"detail":{"msg":"This file has no open alert asking for its reporting period.","error_code":"NO_OPEN_ROUTING_ALERT"}}`,
			qerrors.ExitBadUsage, "Hint: file 123 is not waiting for its period"},
		{"period locked", http.StatusConflict,
			`{"detail":{"msg":"Period 2026-07 is locked. Unlock it before changing its data.","error_code":"PERIOD_LOCKED"}}`,
			qerrors.ExitBadUsage, "\n\nInstead: choose an unlocked period and run 'qctl file set-period 123 <period-key>' again. " +
				"Period '2026-07', the one you named, takes no data while it is locked."},
		{"period has a later file", http.StatusConflict,
			`{"detail":{"msg":"A later file of this source has already loaded into 2026-07, so this file cannot go there.","error_code":"PERIOD_HAS_LATER_FILE","context":{"later_stored_item_id":456}}}`,
			qerrors.ExitBadUsage, "\n\nInstead: choose another period and run 'qctl file set-period 123 <period-key>' again. " +
				"A later file of the same cloud source has already loaded into period '2026-07'."},
		{"assignment conflict", http.StatusConflict,
			`{"detail":{"msg":"A previously loaded file cannot be moved to another reporting period.","error_code":"REPORTING_PERIOD_ASSIGNMENT_CONFLICT"}}`,
			qerrors.ExitBadUsage, "Hint: the file's state or its ingestion jobs do not allow this move"},
		{"inactive period", http.StatusBadRequest,
			`{"detail":{"msg":"The table series has no active period '2026-07'.","loc":["body","period_key"]}}`,
			qerrors.ExitBadUsage, ""},
		{"no grant", http.StatusForbidden,
			`{"detail":{"msg":"User is not authorized to access cloud source 9"}}`,
			qerrors.ExitUnauthorized, ""},
		{"unknown file", http.StatusNotFound,
			`{"detail":{"msg":"file id 123 can not be found."}}`,
			qerrors.ExitNotFound, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler(http.MethodPut, periodPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, tc.status, json.RawMessage(tc.body))
			})

			_, _, err := execSetPeriod(t, "123", "2026-07")
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("exit code = %d (err %v), want %d", code, err, tc.wantExit)
			}
			if tc.wantHint != "" && !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("error %q does not contain %q", err, tc.wantHint)
			}
			if tc.wantHint == "" && strings.Contains(err.Error(), "Hint:") {
				t.Errorf("error %q carries a hint it should not", err)
			}
			if tc.name == "period locked" {
				testutil.AssertOnePeriodUnlockHint(t, err)
			}
		})
	}
}

func TestSetPeriodChangeReviewRefusals(t *testing.T) {
	const routingKey = `[["series_file_routing","assign_reporting_period"]]`
	cases := []struct {
		name    string
		body    string
		want    string
		notWant string
	}{
		{"proposal required",
			`{"detail":{"msg":"This table's change review policy requires a change proposal for this change.","error_code":"CHANGE_PROPOSAL_REQUIRED",` +
				`"context":{"operation":"alert_resolve","verb":"resolve","denied_keys":` + routingKey + `}}}`,
			"\n\nInstead: propose the period for review on the file's series_file_routing alert. " +
				"'qctl get alerts --issue-type series_file_routing --table-id <table-id>' lists the open ones " +
				"('qctl describe file 123' shows the file's dataset_id); " +
				"'qctl alert propose <alert-id> --action assign_reporting_period --dry-run > period.yaml' writes the manifest. " +
				"Set its spec.new_value to '2026-07', then run 'qctl alert propose -f period.yaml'.",
			""},
		{"proposal required under another operation",
			`{"detail":{"msg":"This table's change review policy requires a change proposal for this change.","error_code":"CHANGE_PROPOSAL_REQUIRED",` +
				`"context":{"operation":"dataset_rule_patch","verb":"resolve","denied_keys":[["params"]]}}}`,
			"\nHint: Propose the params change for review instead",
			"Instead:"},
		{"proposal required without context",
			`{"detail":{"msg":"This table's change review policy requires a change proposal for this change.","error_code":"CHANGE_PROPOSAL_REQUIRED"}}`,
			"\nHint: Propose the change for review instead",
			"Instead:"},
		{"policy denied",
			`{"detail":{"msg":"This table's change review policy does not let the user role resolve this change.","error_code":"CHANGE_REVIEW_POLICY_DENIED",` +
				`"context":{"operation":"alert_resolve","verb":"resolve","denied_keys":` + routingKey + `}}}`,
			"\nHint: The policy does not let your role resolve alert_resolve key series_file_routing/assign_reporting_period. " +
				"`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key.",
			"Instead:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newTestEnv(t)
			mock.RegisterHandler(http.MethodPut, periodPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, json.RawMessage(tc.body))
			})

			_, _, err := execSetPeriod(t, "123", "2026-07")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
				t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitUnauthorized)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			if tc.notWant != "" && strings.Contains(err.Error(), tc.notWant) {
				t.Errorf("error %q contains %q", err, tc.notWant)
			}
		})
	}
}
