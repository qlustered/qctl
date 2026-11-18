package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const droppedLinesRoute = "/api/orgs/" + testOrgID + "/stored-items/311/dropped-lines"

// droppedLinesPage holds a keyed line with a masked value, and a line whose
// delete record is gone.
const droppedLinesPage = `{"page":1,"total_rows":2,"results":[` +
	`{"row_number":5,"values":[{"field_name":"policy_id","value":"P-1"},{"field_name":"email","value":"a***@x.com"},` +
	`{"field_name":"note","value":null}],"row_delete_id":17,"deleted_at":"2026-11-12T10:00:00Z",` +
	`"deleted_by":{"id":"cccc0000-0000-0000-0000-000000000003","email":"ana@example.com","first_name":"Ana","last_name":"B"},` +
	`"comment":"duplicate\nupload","undone":true},` +
	`{"row_number":9,"values":[],"row_delete_id":null,"deleted_at":null,"deleted_by":null,"comment":null,"undone":false}]}`

type droppedLinesEnv struct {
	mock    *testutil.MockAPIServer
	queries []url.Values
	status  int
	resp    string
}

func newDroppedLinesEnv(t *testing.T) *droppedLinesEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")

	e := &droppedLinesEnv{mock: mock, status: http.StatusOK, resp: droppedLinesPage}
	mock.RegisterHandler("GET", droppedLinesRoute, func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		if e.status != http.StatusOK {
			testutil.RespondError(w, e.status, "refused")
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(e.resp))
	})
	return e
}

func runGetDroppedLines(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	root.SilenceUsage, root.SilenceErrors = true, true
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewDroppedLinesCommand())
	root.AddCommand(getCmd)
	cmdutil.TypeArgsErrors(root)
	cmdutil.StripKindRefs(root)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "dropped-lines"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestGetDroppedLines_Table(t *testing.T) {
	e := newDroppedLinesEnv(t)

	out, stderr, err := runGetDroppedLines(t, "311")
	if err != nil {
		t.Fatalf("get dropped-lines: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"LINE", "VALUES", "DELETE-ID", "DELETED", "DELETED-BY", "REASON", "UNDONE",
		"policy_id=P-1, email=a***@x.com, note=", "17", "ana@example.com", "duplicate upload",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(e.queries) != 1 || e.queries[0].Get("page") != "1" || e.queries[0].Get("limit") != "50" {
		t.Errorf("queries = %v, want one with page 1 and limit 50", e.queries)
	}
}

func TestGetDroppedLines_FileRefAndPaging(t *testing.T) {
	e := newDroppedLinesEnv(t)

	_, stderr, err := runGetDroppedLines(t, "file/311", "--page", "3", "--limit", "100")
	if err != nil {
		t.Fatalf("get dropped-lines: %v\n%s", err, stderr)
	}
	if len(e.queries) != 1 || e.queries[0].Get("page") != "3" || e.queries[0].Get("limit") != "100" {
		t.Errorf("queries = %v, want page 3 and limit 100", e.queries)
	}
}

// The route sends no next link, so the following page comes from total_rows.
func TestGetDroppedLines_HintsTheNextPageFromTheTotal(t *testing.T) {
	newDroppedLinesEnv(t)

	_, stderr, err := runGetDroppedLines(t, "311", "--limit", "1")
	if err != nil {
		t.Fatalf("get dropped-lines: %v\n%s", err, stderr)
	}
	if !strings.Contains(stderr, "use --page 2.") {
		t.Errorf("stderr = %q, want a --page 2 hint", stderr)
	}
}

func TestGetDroppedLines_BadArgsSendNothing(t *testing.T) {
	for name, args := range map[string][]string{
		"name":           {"orders.csv"},
		"zero":           {"0"},
		"table ref":      {"table/311"},
		"limit too high": {"311", "--limit", "101"},
		"no file":        {},
	} {
		t.Run(name, func(t *testing.T) {
			e := newDroppedLinesEnv(t)
			_, _, err := runGetDroppedLines(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit %d, want %d (err %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("sent %v, want no request", reqs)
			}
		})
	}
}

func TestGetDroppedLines_JSONAndYAMLPassTheRecordsThrough(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			newDroppedLinesEnv(t)
			out, stderr, err := runGetDroppedLines(t, "311", "-o", format)
			if err != nil {
				t.Fatalf("get dropped-lines -o %s: %v\n%s", format, err, stderr)
			}
			var list map[string]any
			if format == "yaml" {
				err = yaml.Unmarshal([]byte(out), &list)
			} else {
				err = json.Unmarshal([]byte(out), &list)
			}
			if err != nil {
				t.Fatalf("-o %s does not decode: %v\n%s", format, err, out)
			}
			items, _ := list["items"].([]any)
			if list["kind"] != "List" || numberOf(list["total"]) != 2 || len(items) != 2 {
				t.Fatalf("list = %v, want a List of the two records", list)
			}
			first, _ := items[0].(map[string]any)
			values, _ := first["values"].([]any)
			email, _ := values[1].(map[string]any)
			if first["comment"] != "duplicate\nupload" || email["value"] != "a***@x.com" {
				t.Errorf("first item = %v, want the API record unprojected, masked value as sent", first)
			}
			second, _ := items[1].(map[string]any)
			if second["row_delete_id"] != nil || second["deleted_by"] != nil {
				t.Errorf("second item = %v, want null delete fields", second)
			}
		})
	}
}

func TestGetDroppedLines_ErrorStatusesExitWithTheirCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   int
	}{
		{"unknown file", http.StatusNotFound, qerrors.ExitNotFound},
		{"not granted", http.StatusForbidden, qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDroppedLinesEnv(t)
			e.status = tc.status
			_, _, err := runGetDroppedLines(t, "311")
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit %d, want %d (err %v)", code, tc.want, err)
			}
		})
	}
}

func TestGetDroppedLines_EmptyPageSaysSo(t *testing.T) {
	e := newDroppedLinesEnv(t)
	e.resp = `{"page":1,"total_rows":0,"results":[]}`
	out, stderr, err := runGetDroppedLines(t, "311")
	if err != nil {
		t.Fatalf("get dropped-lines: %v", err)
	}
	if out != "" || !strings.Contains(stderr, "No dropped lines") {
		t.Errorf("stdout %q, stderr %q", out, stderr)
	}
}

func TestDroppedLineRows_ALineWithoutADeleteRecord(t *testing.T) {
	rows := droppedLineRows([]stored_items.DroppedLine{{RowNumber: 9}})
	want := droppedLineRow{Line: 9, Values: "-", DeleteID: "-", Deleted: "-", DeletedBy: "-", Reason: "-"}
	if rows[0] != want {
		t.Errorf("row = %+v, want %+v", rows[0], want)
	}
}
