package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const correctedLinesRoute = "/api/orgs/" + testOrgID + "/stored-items/311/corrected-lines"

const correctedLinesPage = `{"page":1,"total_rows":3,"results":[` +
	`{"row_number":5,"field_name":"brand","value_sent":"Rolx","value_used":"Rolex"},` +
	`{"row_number":5,"field_name":"email","value_sent":"a***@x.com","value_used":"b***@x.com"},` +
	`{"row_number":9,"field_name":"note","value_sent":null,"value_used":"two\nlines"}]}`

type correctedLinesEnv struct {
	mock    *testutil.MockAPIServer
	queries []url.Values
	status  int
	resp    string
}

func newCorrectedLinesEnv(t *testing.T) *correctedLinesEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")

	e := &correctedLinesEnv{mock: mock, status: http.StatusOK, resp: correctedLinesPage}
	mock.RegisterHandler("GET", correctedLinesRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		if e.status == http.StatusUnprocessableEntity {
			testutil.RespondJSON(w, e.status, json.RawMessage(`{"detail":{"msg":"invalid request"}}`))
			return
		}
		if e.status != http.StatusOK {
			testutil.RespondError(w, e.status, "refused")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.resp))
	})
	return e
}

func runGetCorrectedLines(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewCorrectedLinesCommand())
	root.AddCommand(getCmd)
	cmdutil.TypeArgsErrors(root)
	cmdutil.StripKindRefs(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "corrected-lines"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestGetCorrectedLines_Table(t *testing.T) {
	e := newCorrectedLinesEnv(t)

	out, stderr, err := runGetCorrectedLines(t, "--file", "311")
	if err != nil {
		t.Fatalf("get corrected-lines: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"LINE", "FIELD", "VALUE-SENT", "VALUE-USED",
		"brand", "Rolx", "Rolex", "email", "a***@x.com", "b***@x.com", "two lines",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(e.queries) != 1 || e.queries[0].Get("page") != "1" || e.queries[0].Get("limit") != "50" {
		t.Errorf("queries = %v, want one with page 1 and limit 50", e.queries)
	}
}

func TestGetCorrectedLines_FileRefAndPaging(t *testing.T) {
	e := newCorrectedLinesEnv(t)

	_, stderr, err := runGetCorrectedLines(t, "file/311", "--page", "3", "--limit", "100")
	if err != nil {
		t.Fatalf("get corrected-lines: %v\n%s", err, stderr)
	}
	if len(e.queries) != 1 || e.queries[0].Get("page") != "3" || e.queries[0].Get("limit") != "100" {
		t.Errorf("queries = %v, want page 3 and limit 100", e.queries)
	}
}

// The route sends no next link, so the following page comes from total_rows.
func TestGetCorrectedLines_HintsTheNextPageFromTheTotal(t *testing.T) {
	newCorrectedLinesEnv(t)

	_, stderr, err := runGetCorrectedLines(t, "311", "--limit", "1")
	if err != nil {
		t.Fatalf("get corrected-lines: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "use --page 2.") {
		t.Errorf("stderr = %q, want a --page 2 hint", stderr)
	}
}

func TestGetCorrectedLines_BadArgsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"name":           {"orders.csv"},
		"zero":           {"0"},
		"negative":       {"-1"},
		"table ref":      {"table/311"},
		"limit too high": {"--file", "311", "--limit", "101"},
		"zero limit":     {"--file", "311", "--limit", "0"},
		"zero page":      {"--file", "311", "--page", "0"},
		"zero flag":      {"--file", "0"},
		"negative flag":  {"--file=-1"},
		"name flag":      {"--file", "orders.csv"},
		"wrong ref flag": {"--file", "table/311"},
		"both":           {"311", "--file", "311"},
		"extra arg":      {"311", "312"},
		"empty arg":      {""},
		"no file":        {},
	} {
		t.Run(name, func(t *testing.T) {
			e := newCorrectedLinesEnv(t)
			_, _, err := runGetCorrectedLines(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("sent %v, want no request", reqs)
			}
		})
	}
}

func TestGetCorrectedLines_JSONAndYAMLPassTheRecordsThrough(t *testing.T) {
	newCorrectedLinesEnv(t)
	out, stderr, err := runGetCorrectedLines(t, "--file", "file/311", "--limit", "3", "-o", "json")
	if err != nil {
		t.Fatalf("get corrected-lines: %v\n%s", err, stderr)
	}
	var list struct {
		Kind               string          `json:"kind"`
		Items              json.RawMessage `json:"items"`
		Page, Limit, Total int
		NextPage           *int `json:"next_page"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("decode list: %v\n%s", err, out)
	}
	if list.Kind != "List" || list.Page != 1 || list.Limit != 3 || list.Total != 3 || list.NextPage != nil {
		t.Fatalf("list = %+v", list)
	}
	var fixture struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal([]byte(correctedLinesPage), &fixture); err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(list.Items, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixture.Results, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("items = %v, want unprojected API records %v", got, want)
	}
	yamlOut, _, err := runGetCorrectedLines(t, "--file", "311", "--limit", "3", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertYAMLReadsAsJSON(t, yamlOut, out)
}

func TestGetCorrectedLines_ErrorStatusesExitWithTheirCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   int
	}{
		{"unknown file", http.StatusNotFound, qerrors.ExitNotFound},
		{"not granted", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"invalid request", http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCorrectedLinesEnv(t)
			e.status = tc.status
			_, _, err := runGetCorrectedLines(t, "311")
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit %d, want %d (err %v)", code, tc.want, err)
			}
		})
	}
}

func TestGetCorrectedLines_EmptyPageSaysSo(t *testing.T) {
	e := newCorrectedLinesEnv(t)
	e.resp = `{"page":1,"total_rows":0,"results":[]}`
	out, stderr, err := runGetCorrectedLines(t, "311")
	if err != nil {
		t.Fatalf("get corrected-lines: %v", err)
	}
	if out != "" || !strings.Contains(stderr, "No corrected cells") {
		t.Errorf("stdout %q, stderr %q", out, stderr)
	}
}

func TestGetCorrectedLines_StructuredPaging(t *testing.T) {
	e := newCorrectedLinesEnv(t)
	e.resp = `{"page":2,"total_rows":3,"results":[{"row_number":5,"field_name":"email","value_sent":null,"value_used":null}]}`
	out, _, err := runGetCorrectedLines(t, "311", "--page", "2", "--limit", "1", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatal(err)
	}
	if list["page"] != float64(2) || list["limit"] != float64(1) || list["total"] != float64(3) || list["next_page"] != float64(3) {
		t.Fatalf("list = %v", list)
	}
}

func TestGetCorrectedLines_EmptyStructuredList(t *testing.T) {
	e := newCorrectedLinesEnv(t)
	e.resp = `{"page":1,"total_rows":0,"results":[]}`
	jsonOut, _, err := runGetCorrectedLines(t, "--file", "311", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	var list map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &list); err != nil {
		t.Fatal(err)
	}
	items, ok := list["items"].([]any)
	if !ok || len(items) != 0 || list["total"] != float64(0) || list["next_page"] != nil {
		t.Fatalf("empty list = %v", list)
	}
	yamlOut, _, err := runGetCorrectedLines(t, "311", "-o", "yaml")
	if err != nil {
		t.Fatal(err)
	}
	testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)
}

func TestGetCorrectedLines_EmptyPageStillValidatesColumns(t *testing.T) {
	e := newCorrectedLinesEnv(t)
	e.resp = `{"page":1,"total_rows":0,"results":[]}`
	_, _, err := runGetCorrectedLines(t, "311", "--columns", "invalid")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit %d, want 2: %v", code, err)
	}
}

func TestGetCorrectedLines_ValidatesBeforeCredentials(t *testing.T) {
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	for _, args := range [][]string{nil, {"0"}, {"--file", "0"}, {"311", "--file", "311"}, {"311", "--limit", "101"}, {"311", "--page", "0"}} {
		_, _, err := runGetCorrectedLines(t, args...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("args %v: exit %d, want 2: %v", args, code, err)
		}
	}
}
