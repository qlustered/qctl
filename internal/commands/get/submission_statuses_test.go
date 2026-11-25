package get

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	submissionSeriesID   = "11111111-2222-4333-8444-555555555555"
	submissionOrgPrefix  = "/api/orgs/" + testOrgID
	submissionBoardPath  = submissionOrgPrefix + "/table-series/" + submissionSeriesID + "/submissions"
	submissionIntakePath = submissionOrgPrefix + "/data-sources/42/submissions"
	submissionCountsPath = submissionOrgPrefix + "/submissions/series"
	submissionSeriesPath = submissionOrgPrefix + "/table-series"
)

const submissionCountsJSON = `{"expected":1,"late":1,"on_hold":1,"received_not_loaded":0,"received":0,"clean":1,"nil":0,"waived":0,"total":4}`

const submissionLateRow = `{"data_source_model_id":42,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
 "dataset_id":7,"status":"expected","is_locked":false,"pending_file":null,"not_loaded_file":null,"declaration":null,
 "broker_name":"Acme Brokers","board_status":"late","broker_due":"2026-10-10","days_late":6,
 "last_reminded_at":"2026-10-12T09:00:00Z","latest_file":null,"file_count":0,"bad_row_count":null,
 "issues_report_sent_at":null,"issues_report_waiting_reason":null,"issues_report_target":null,"waiting_file":null}`

const submissionHeldRow = `{"data_source_model_id":43,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
 "dataset_id":7,"status":"received_not_loaded","is_locked":false,
 "pending_file":null,"not_loaded_file":{"stored_item_id":901,"file_name":"held.csv","created_at":"2026-10-03T08:00:00Z","alert_item_id":812,
  "fix_owner":"broker_file","is_on_hold":true,"hold_reasons":["row_drop","period_mismatch"],"channel":"email","sender":null,
  "sender_ask":{"asked_at":"2026-10-04T08:00:00Z","email":"ops@bolt.example","user_id":"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff"},"waiting_on":"broker"},
 "declaration":null,"broker_name":"Bolt Re","board_status":"on_hold","broker_due":"2026-10-15","days_late":1,
 "last_reminded_at":null,"latest_file":{"stored_item_id":900,"file_name":"older.csv","created_at":"2026-10-01T08:00:00Z","channel":"portal","sender":null},
 "file_count":2,"bad_row_count":null,"issues_report_sent_at":null,"issues_report_waiting_reason":"approval_required",
 "issues_report_target":{"stored_item_id":900,"file_name":"older.csv","broker_fixable_row_count":2,
  "recipient":{"id":"bbbbbbbb-cccc-4ddd-8eee-ffffffffffff","email":"ops@bolt.example","first_name":"Olive","last_name":"Bolt"},
  "recipient_refusal":null,"report_refusal":null},"waiting_file":null}`

const submissionCleanRow = `{"data_source_model_id":44,"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",
 "dataset_id":7,"status":"clean","is_locked":false,"pending_file":null,"not_loaded_file":null,"declaration":null,
 "broker_name":"Cove Partners","board_status":"clean","broker_due":"2026-10-10","days_late":null,
 "last_reminded_at":null,"latest_file":{"stored_item_id":950,"file_name":"cove-sept.csv","created_at":"2026-10-02T08:00:00Z","channel":"email",
  "sender":{"id":"cccccccc-dddd-4eee-8fff-000000000000","email":"files@cove.example","first_name":"Cora","last_name":"Vale"}},
 "file_count":1,"bad_row_count":0,"issues_report_sent_at":"2026-10-02T09:00:00Z","issues_report_waiting_reason":null,
 "issues_report_target":{"stored_item_id":950,"file_name":"cove-sept.csv","broker_fixable_row_count":0,
  "recipient":{"id":"cccccccc-dddd-4eee-8fff-000000000000","email":"files@cove.example","first_name":"Cora","last_name":"Vale"},
  "recipient_refusal":null,"report_refusal":null},"waiting_file":null}`

var submissionBoardJSON = `{"series_id":"` + submissionSeriesID + `","series_name":"Monthly bordereau","frequency":"monthly","files_waiting_for_period":[],
 "period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30","broker_due":"2026-10-10",
 "mga_deadline":"2026-10-20","is_locked":true,"counts":` + submissionCountsJSON + `,
 "rows":[` + submissionLateRow + `,` + submissionHeldRow + `,` + submissionCleanRow + `],
 "open_files":[{"stored_item_id":960,"file_name":"acme-oct.csv","created_at":"2026-11-02T08:00:00Z","alert_item_id":null,
  "fix_owner":null,"is_on_hold":false,"hold_reasons":[],"channel":"portal","sender":null,"sender_ask":null,
  "data_source_model_id":42,"broker_name":"Acme Brokers","period_key":"2026-10","waiting_on":"qluster"}],
 "change_proposals_awaiting_review":2,
 "deliverables":[{"deliverable_id":"dddddddd-eeee-4fff-8000-111111111111","name":"Monthly bordereau pack","receiver_label":"Lloyd's",
  "due":"2026-10-23","at_risk":"2026-10-20","state":"signed_off","signed_off_run_id":31,"signed_off_at":"2026-10-21T10:00:00Z"}]}`

const submissionIntakeJSON = `[` +
	`{"data_source_model_id":42,"period_key":"2026-10","period_start":"2026-10-01","period_end":"2026-10-31",
 "dataset_id":null,"status":"expected","is_locked":false,"pending_file":null,"not_loaded_file":null,"declaration":null,
 "broker_name":"Acme Brokers","board_status":"expected","broker_due":"2026-11-10","days_late":null,
 "last_reminded_at":null,"latest_file":null,"file_count":0,"bad_row_count":null,
 "issues_report_sent_at":null,"issues_report_waiting_reason":null,"issues_report_target":null,"waiting_file":null},` +
	`{"data_source_model_id":42,"period_key":"2026-08","period_start":"2026-08-01","period_end":"2026-08-31",
 "dataset_id":6,"status":"waived","is_locked":true,"pending_file":null,"not_loaded_file":null,
 "declaration":{"id":5,"kind":"waived","declared_at":"2026-09-12T10:00:00Z","declared_by_id":"aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee","reason":"Broker closed"},
 "broker_name":"Acme Brokers","board_status":"waived","broker_due":"2026-09-10","days_late":null,
 "last_reminded_at":null,"latest_file":null,"file_count":0,"bad_row_count":null,
 "issues_report_sent_at":null,"issues_report_waiting_reason":null,"issues_report_target":null,"waiting_file":null}]`

var submissionSeriesCountsJSON = `[{"series_id":"` + submissionSeriesID + `","series_name":"Monthly bordereau",
 "period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30","broker_due":"2026-10-10",
 "counts":` + submissionCountsJSON + `},
 {"series_id":"99999999-2222-4333-8444-555555555555","series_name":"Quarterly","period_key":"2026-Q3",
 "period_start":"2026-07-01","period_end":"2026-09-30","broker_due":"2026-10-15",
 "counts":{"expected":0,"late":0,"on_hold":0,"received_not_loaded":0,"received":0,"clean":0,"nil":0,"waived":0,"total":0}}]`

func setupSubmissionStatusesCommand() *cobra.Command {
	root := newTestRootCmd()
	root.SilenceUsage = true
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewSubmissionStatusesCommand())
	root.AddCommand(getCmd)
	return root
}

// newSubmissionsEnv wires config, credentials and a mock server serving the
// three board routes and the series listing.
func newSubmissionsEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	mock.RegisterHandler("GET", submissionSeriesPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[{"id":"`+submissionSeriesID+`","name":"Monthly bordereau","members":[]}]`))
	})
	mock.RegisterHandler("GET", submissionBoardPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionBoardJSON))
	})
	mock.RegisterHandler("GET", submissionIntakePath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionIntakeJSON))
	})
	mock.RegisterHandler("GET", submissionCountsPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionSeriesCountsJSON))
	})
	return mock
}

func runGetSubmissionStatuses(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupSubmissionStatusesCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "submission-statuses"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

func assertContainsAll(t *testing.T, label, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", label, want, got)
		}
	}
}

func TestGetSubmissionStatusesScopes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		args     []string
		requests []string
		query    string
		stdout   []string
		stderr   []string
	}{
		{
			name:     "series by name, period being collected",
			args:     []string{"--series", "Monthly bordereau"},
			requests: []string{"GET " + submissionSeriesPath, "GET " + submissionBoardPath},
			stdout: []string{"CLOUD-SOURCE-ID", "STATUS", "DAYS-LATE", "ALERT-ID", "LAST-REMINDED",
				"Acme Brokers", "late", "Bolt Re", "on_hold", "held.csv", "812", "Cove Partners", "clean", "cove-sept.csv"},
			stderr: []string{
				"Series:        Monthly bordereau (" + submissionSeriesID + "), monthly\n",
				"Period:        2026-09 (2026-09-01 to 2026-09-30), locked\n",
				"Broker due:    2026-10-10\n", "MGA deadline:  2026-10-20\n",
				"4 intakes: 1 late, 1 on_hold, 1 expected, 1 clean\n",
				"Next: qctl describe alert 812",
			},
		},
		{
			name:     "series by UUID with a period",
			args:     []string{"--series", "table-series/" + submissionSeriesID, "--period", "2026-Q3"},
			requests: []string{"GET " + submissionBoardPath},
			query:    "period_key=2026-Q3",
			stdout:   []string{"Acme Brokers"},
		},
		{
			name:     "intake",
			args:     []string{"--cloud-source-id", "cloud-source/42"},
			requests: []string{"GET " + submissionIntakePath},
			stdout:   []string{"PERIOD-KEY", "IS-LOCKED", "2026-10", "expected", "2026-11-10", "2026-08", "waived", "true"},
			stderr:   []string{"Cloud source: Acme Brokers (id: 42)\n"},
		},
		{
			name:     "per-series counts",
			requests: []string{"GET " + submissionCountsPath},
			stdout: []string{"SERIES", "RECEIVED-NOT-LOADED", "TOTAL", "Monthly bordereau", "2026-09", "2026-10-10",
				"Quarterly", "2026-Q3"},
			stderr: []string{"Next: qctl get submission-statuses --series <name-or-id>"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newSubmissionsEnv(t)
			var gotQuery string
			if tc.query != "" {
				mock.RegisterHandler("GET", submissionBoardPath, func(w http.ResponseWriter, r *http.Request) {
					gotQuery = r.URL.RawQuery
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionBoardJSON))
				})
			}
			out, errOut, err := runGetSubmissionStatuses(t, append(tc.args, "--max-column-width", "0")...)
			if err != nil {
				t.Fatalf("Execute: %v\n%s", err, errOut)
			}
			if !reflect.DeepEqual(mock.Requests(), tc.requests) {
				t.Errorf("requests = %v, want %v", mock.Requests(), tc.requests)
			}
			if gotQuery != tc.query {
				t.Errorf("query = %q, want %q", gotQuery, tc.query)
			}
			assertContainsAll(t, "stdout", out, tc.stdout...)
			assertContainsAll(t, "stderr", errOut, tc.stderr...)
			if strings.Contains(out, "Series:") || strings.Contains(out, "Next:") || strings.Contains(out, "intakes:") {
				t.Errorf("prose reached stdout:\n%s", out)
			}
		})
	}
}

// The series board puts the server's period default to work: no period_key
// is sent unless --period names one.
func TestGetSubmissionStatusesSendsNoDefaultPeriod(t *testing.T) {
	mock := newSubmissionsEnv(t)
	mock.RegisterHandler("GET", submissionBoardPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("query = %q, want none", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionBoardJSON))
	})
	if _, errOut, err := runGetSubmissionStatuses(t, "--series", submissionSeriesID); err != nil {
		t.Fatalf("Execute: %v\n%s", err, errOut)
	}
}

func TestGetSubmissionStatusesFile(t *testing.T) {
	newSubmissionsEnv(t)
	out, _, err := runGetSubmissionStatuses(t, "--series", submissionSeriesID, "--no-headers",
		"--columns", "cloud_source_id,file,file_id,alert_id,fix_owner,hold_reasons,days_late,bad_rows")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	want := [][]string{
		{"42", "-", "-", "-", "-", "-", "6", "-"},
		{"43", "held.csv", "901", "812", "broker_file", "row_drop,period_mismatch", "1", "-"},
		{"44", "cove-sept.csv", "950", "-", "-", "-", "-", "0"},
	}
	if len(lines) != len(want) {
		t.Fatalf("rows = %q", out)
	}
	for i, line := range lines {
		if got := strings.Fields(line); !reflect.DeepEqual(got, want[i]) {
			t.Errorf("row %d = %v, want %v", i, got, want[i])
		}
	}
}

func decodeJSONValue(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode: %v\n%s", err, s)
	}
	return v
}

func TestGetSubmissionStatusesStructuredOutput(t *testing.T) {
	list := func(items string) string {
		return fmt.Sprintf(`{"kind":"List","items":%s,"page":null,"limit":null,"total":null,"next_page":null}`, items)
	}
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"series board object", []string{"--series", submissionSeriesID}, submissionBoardJSON},
		{"intake list", []string{"--cloud-source-id", "42"}, list(submissionIntakeJSON)},
		{"counts list", nil, list(submissionSeriesCountsJSON)},
	} {
		for _, format := range []string{"json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				newSubmissionsEnv(t)
				out, errOut, err := runGetSubmissionStatuses(t, append(tc.args, "-o", format)...)
				if err != nil {
					t.Fatalf("Execute: %v\n%s", err, errOut)
				}
				if errOut != "" {
					t.Errorf("stderr under -o %s: %q", format, errOut)
				}
				if format == "yaml" {
					testutil.AssertYAMLReadsAsJSON(t, out, tc.want)
					return
				}
				if !reflect.DeepEqual(decodeJSONValue(t, out), decodeJSONValue(t, tc.want)) {
					t.Errorf("-o json is not the API record:\n%s", out)
				}
			})
		}
	}
}

func TestGetSubmissionStatusesNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"series board", []string{"--series", submissionSeriesID}, "cloud-source/42\ncloud-source/43\ncloud-source/44\n"},
		{"intake board skips a period with no table", []string{"--cloud-source-id", "42"}, "table/6\n"},
		{"counts", nil, "table-series/" + submissionSeriesID + "\ntable-series/99999999-2222-4333-8444-555555555555\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newSubmissionsEnv(t)
			out, errOut, err := runGetSubmissionStatuses(t, append(tc.args, "-o", "name")...)
			if err != nil {
				t.Fatalf("Execute: %v\n%s", err, errOut)
			}
			if out != tc.want {
				t.Errorf("names = %q, want %q", out, tc.want)
			}
		})
	}
}

func TestGetSubmissionStatusesEmpty(t *testing.T) {
	for _, format := range []string{"table", "json"} {
		t.Run(format, func(t *testing.T) {
			mock := newSubmissionsEnv(t)
			mock.RegisterHandler("GET", submissionIntakePath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[]`))
			})
			out, errOut, err := runGetSubmissionStatuses(t, "--cloud-source-id", "42", "-o", format)
			if err != nil {
				t.Fatalf("Execute: %v\n%s", err, errOut)
			}
			if format == "table" {
				if out != "" || !strings.Contains(errOut, "No submission statuses") {
					t.Errorf("stdout %q, stderr %q", out, errOut)
				}
				return
			}
			want := `{"kind":"List","items":[],"page":null,"limit":null,"total":null,"next_page":null}`
			if !reflect.DeepEqual(decodeJSONValue(t, out), decodeJSONValue(t, want)) {
				t.Errorf("empty list = %s", out)
			}
		})
	}
}

// Every local mistake exits 2 before config or credentials are read.
func TestGetSubmissionStatusesBadUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"series and cloud source", []string{"--series", submissionSeriesID, "--cloud-source-id", "42"}},
		{"period without series", []string{"--period", "2026-09"}},
		{"period with cloud source", []string{"--cloud-source-id", "42", "--period", "2026-09"}},
		{"blank series", []string{"--series", "  "}},
		{"blank period", []string{"--series", submissionSeriesID, "--period", ""}},
		{"padded period", []string{"--series", submissionSeriesID, "--period", " 2026-09"}},
		{"zero cloud source", []string{"--cloud-source-id", "0"}},
		{"counts column on a board", []string{"--series", submissionSeriesID, "--columns", "late"}},
		{"board column on the counts", []string{"--columns", "cloud_source"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			_, _, err := runGetSubmissionStatuses(t, tc.args...)
			if got := qerrors.GetExitCode(err); got != 2 {
				t.Fatalf("exit = %d (%v), want 2", got, err)
			}
		})
	}
}

func TestGetSubmissionStatusesServerErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   string
		status int
		body   string
		args   []string
		exit   int
	}{
		{"unseen series", submissionBoardPath, http.StatusNotFound,
			`{"detail":{"msg":"Table series can not be found in this organization."}}`,
			[]string{"--series", submissionSeriesID}, 3},
		{"period key not in the frequency", submissionBoardPath, http.StatusUnprocessableEntity,
			`{"detail":{"msg":"period_key is not in the format of the series frequency.","error_code":"SERIES_PERIOD_KEY_INVALID"}}`,
			[]string{"--series", submissionSeriesID, "--period", "2026-Q3"}, 2},
		{"unknown intake", submissionIntakePath, http.StatusNotFound,
			`{"detail":{"msg":"Data source 42 can not be found in this organization."}}`,
			[]string{"--cloud-source-id", "42"}, 3},
		{"counts forbidden", submissionCountsPath, http.StatusForbidden,
			`{"detail":{"msg":"Missing permission dataset:read"}}`, nil, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newSubmissionsEnv(t)
			mock.RegisterHandler("GET", tc.path, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			})
			out, _, err := runGetSubmissionStatuses(t, tc.args...)
			if got := qerrors.GetExitCode(err); got != tc.exit {
				t.Fatalf("exit = %d (%v), want %d", got, err, tc.exit)
			}
			if out != "" {
				t.Errorf("stdout on failure: %q", out)
			}
		})
	}
}

// A series name that matches no series exits 3 before the board is read.
func TestGetSubmissionStatusesUnknownSeriesName(t *testing.T) {
	mock := newSubmissionsEnv(t)
	_, _, err := runGetSubmissionStatuses(t, "--series", "No such series")
	if got := qerrors.GetExitCode(err); got != 3 {
		t.Fatalf("exit = %d (%v), want 3", got, err)
	}
	if want := []string{"GET " + submissionSeriesPath}; !reflect.DeepEqual(mock.Requests(), want) {
		t.Errorf("requests = %v, want %v", mock.Requests(), want)
	}
}

var submissionEmptyBoardJSON = `{"series_id":"` + submissionSeriesID + `","series_name":"Monthly bordereau","frequency":"monthly","files_waiting_for_period":[],
 "period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30","broker_due":"2026-10-10",
 "mga_deadline":"2026-10-20","is_locked":false,
 "counts":{"expected":0,"late":0,"on_hold":0,"received_not_loaded":0,"received":0,"clean":0,"nil":0,"waived":0,"total":0},
 "rows":[],"open_files":[],"change_proposals_awaiting_review":0,"deliverables":[]}`

// A board with no row still names its series and period in a table, keeps
// rows: [] in JSON, and prints no reference under -o name.
func TestGetSubmissionStatusesEmptySeriesBoard(t *testing.T) {
	for _, format := range []string{"table", "json", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := newSubmissionsEnv(t)
			mock.RegisterHandler("GET", submissionBoardPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(submissionEmptyBoardJSON))
			})
			out, errOut, err := runGetSubmissionStatuses(t, "--series", submissionSeriesID, "-o", format)
			if err != nil {
				t.Fatalf("Execute: %v\n%s", err, errOut)
			}
			switch format {
			case "table":
				if out != "" {
					t.Errorf("stdout = %q, want empty", out)
				}
				assertContainsAll(t, "stderr", errOut, "Period:        2026-09 (2026-09-01 to 2026-09-30)\n", "No submission statuses")
			case "json":
				if !reflect.DeepEqual(decodeJSONValue(t, out), decodeJSONValue(t, submissionEmptyBoardJSON)) {
					t.Errorf("-o json is not the API record:\n%s", out)
				}
			case "name":
				if out != "" {
					t.Errorf("names = %q, want none", out)
				}
			}
		})
	}
}

func TestGetSubmissionStatusesEmptyCounts(t *testing.T) {
	for _, format := range []string{"table", "json", "name"} {
		t.Run(format, func(t *testing.T) {
			mock := newSubmissionsEnv(t)
			mock.RegisterHandler("GET", submissionCountsPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[]`))
			})
			out, errOut, err := runGetSubmissionStatuses(t, "-o", format)
			if err != nil {
				t.Fatalf("Execute: %v\n%s", err, errOut)
			}
			switch format {
			case "table":
				if out != "" || !strings.Contains(errOut, "No table series") || strings.Contains(errOut, "Next:") {
					t.Errorf("stdout %q, stderr %q", out, errOut)
				}
			case "json":
				want := `{"kind":"List","items":[],"page":null,"limit":null,"total":null,"next_page":null}`
				if !reflect.DeepEqual(decodeJSONValue(t, out), decodeJSONValue(t, want)) {
					t.Errorf("empty list = %s", out)
				}
			case "name":
				if out != "" {
					t.Errorf("names = %q, want none", out)
				}
			}
		})
	}
}

// The intake board renders a waiver's declaration time and a reminder time
// through the relative formatter; neither is "-" when the server sent one.
func TestGetSubmissionStatusesTimes(t *testing.T) {
	newSubmissionsEnv(t)
	out, errOut, err := runGetSubmissionStatuses(t, "--cloud-source-id", "42", "--no-headers",
		"--columns", "period_key,declared_at,last_reminded")
	if err != nil {
		t.Fatalf("Execute: %v\n%s", err, errOut)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("rows = %q", out)
	}
	if got := strings.Fields(lines[0]); !reflect.DeepEqual(got, []string{"2026-10", "-", "-"}) {
		t.Errorf("expected row = %v", got)
	}
	if !strings.HasPrefix(strings.TrimSpace(lines[1]), "2026-08") || !strings.Contains(lines[1], "ago") {
		t.Errorf("waived row lacks a relative declaration time: %q", lines[1])
	}
}
