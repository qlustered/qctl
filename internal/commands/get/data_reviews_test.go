package get

import (
	"bytes"
	"encoding/json"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/data_reviews"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	dataReviewGroupID    = "11111111-2222-4333-8444-555555555555"
	dataReviewRequestor  = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	dataReviewDecider    = "99999999-8888-4777-8666-555555555555"
	dataReviewGroupsURL  = "/api/orgs/" + testOrgID + "/data-reviews/groups"
	dataReviewChangesURL = "/api/orgs/" + testOrgID + "/data-reviews/top-changes"
)

type dataReviewGetEnv struct {
	mock *testutil.MockAPIServer
}

func newDataReviewGetEnv(t *testing.T) *dataReviewGetEnv {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")
	return &dataReviewGetEnv{mock: mock}
}

func (e *dataReviewGetEnv) registerUsers(status int) {
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users", func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": "directory unavailable"}})
			return
		}
		testutil.RespondJSON(w, status, json.RawMessage(`{"results":[`+
			`{"id":"`+dataReviewRequestor+`","email":"requestor@example.com","first_name":"Rae","last_name":"Quest"},`+
			`{"id":"`+dataReviewDecider+`","email":"decider@example.com","first_name":"Dee","last_name":"Cider"}`+
			`],"next":null}`))
	})
}

func (e *dataReviewGetEnv) run(t *testing.T, sub *cobra.Command, name string, args ...string) (string, string, error) {
	t.Helper()
	root := setupTestCommandForTable(sub)
	root.SilenceUsage = true
	root.SilenceErrors = true
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"get", name}, args...))
	err := root.Execute()
	return stdout.String(), stderr.String(), err
}

func groupPageJSON(total int) json.RawMessage {
	return json.RawMessage(`{"groups":[{"approved_count":5,"created_at":"2026-09-15T12:00:00Z",` +
		`"open_row_count":3,"rejected_count":1,"requestor_id":"` + dataReviewRequestor + `",` +
		`"review_group_id":"` + dataReviewGroupID + `","row_count":8,"title":"September cleanup",` +
		`"description":"Check the corrected addresses","assigned_reviewers":[` +
		`{"id":"` + dataReviewDecider + `","first_name":"Dee","last_name":"Reviewer","email":"reviewer@example.com"},` +
		`{"id":"` + dataReviewRequestor + `","first_name":"","last_name":"","email":"email-only@example.com"},` +
		`{"id":"` + dataReviewGroupID + `","first_name":"","last_name":"","email":""}],` +
		`"undecided_count":2}],"total":` + strconv.Itoa(total) + `}`)
}

// topChangesJSON is two changes, each side carrying the unit tag atlas gives a
// null or text value.
func topChangesJSON() json.RawMessage {
	return json.RawMessage(`[` +
		`{"affected_row_count":12,"decided_by_id":null,"decision":null,"edited_value":"CA",` +
		`"field_name":"state","history_id":701,"original_value":null,` +
		`"old_value_type":"none","value_type":"str"},` +
		`{"affected_row_count":4,"decided_by_id":"` + dataReviewDecider + `","decision":"approve",` +
		`"edited_value":null,"field_name":"region","history_id":702,"original_value":"West",` +
		`"old_value_type":"str","value_type":"none"}` +
		`]`)
}

// dataReviewDecodeList decodes a -o json|yaml List envelope with JSON's value
// types, so a yaml case compares like a json one, and fails unless it carries
// exactly the envelope's keys and kind List.
func dataReviewDecodeList(t *testing.T, format, stdout string) map[string]any {
	t.Helper()
	var doc any
	var err error
	if format == "json" {
		err = json.Unmarshal([]byte(stdout), &doc)
	} else {
		err = yaml.Unmarshal([]byte(stdout), &doc)
	}
	if err != nil {
		t.Fatalf("stdout is not %s: %v\n%s", format, err, stdout)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding %s output: %v", format, err)
	}
	var list map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("%s output is not an object: %v\n%s", format, err, stdout)
	}
	want := []string{"items", "kind", "limit", "next_page", "page", "total"}
	if got := slices.Sorted(maps.Keys(list)); !slices.Equal(got, want) {
		t.Fatalf("%s keys = %v, want %v\n%s", format, got, want, stdout)
	}
	if list["kind"] != "List" {
		t.Errorf("kind = %v, want List", list["kind"])
	}
	return list
}

// dataReviewWantPaging checks a decoded List's page, limit, total and
// next_page; a nil want is a JSON null.
func dataReviewWantPaging(t *testing.T, list map[string]any, want map[string]*int) {
	t.Helper()
	for key, value := range want {
		var expected any
		if value != nil {
			expected = float64(*value)
		}
		if got := list[key]; got != expected {
			t.Errorf("%s = %v, want %v", key, got, expected)
		}
	}
}

// dataReviewWireItems is fixture decoded into T and encoded again: the form
// output.WriteRaw gives the API's objects, which drops an omitempty null.
func dataReviewWireItems[T any](t *testing.T, fixture json.RawMessage) any {
	t.Helper()
	var typed T
	if err := json.Unmarshal(fixture, &typed); err != nil {
		t.Fatalf("fixture does not decode: %v", err)
	}
	raw, err := json.Marshal(typed)
	if err != nil {
		t.Fatalf("re-encoding fixture: %v", err)
	}
	var generic any
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("decoding re-encoded fixture: %v", err)
	}
	return generic
}

func dataReviewInt(v int) *int { return &v }

func TestGetDataReviewGroups_DefaultRequestAndTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	var query url.Values
	e.mock.RegisterHandler("GET", dataReviewGroupsURL, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, groupPageJSON(3))
	})
	e.registerUsers(http.StatusOK)

	stdout, stderr, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups", "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{"dataset_id": "42", "include_closed": "false", "limit": "50"} {
		if got := query.Get(key); got != want {
			t.Errorf("query %s = %q, want %q", key, got, want)
		}
	}
	if got := query.Get("offset"); got != "" {
		t.Errorf("query offset = %q, want omitted for zero", got)
	}
	for _, want := range []string{
		dataReviewGroupID, "September cleanup", "Check the corrected addresses",
		"Rae Quest", "Dee Reviewer", "email-only@example.com", "DESCRIPTION", "REVIEWERS", "8", "3", "2", "5", "1",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "--offset 1") {
		t.Errorf("stderr = %q, want next-offset hint", stderr)
	}
	if got := len(e.mock.Requests()); got != 2 {
		t.Errorf("requests = %d, want groups + users", got)
	}
}

func TestGetDataReviewGroups_LocalRefusalsCostNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing table", nil},
		{"both table selectors", []string{"--table", "orders", "--table-id", "42"}},
		{"zero limit", []string{"--table-id", "42", "--limit", "0"}},
		{"limit too high", []string{"--table-id", "42", "--limit", "201"}},
		{"negative offset", []string{"--table-id", "42", "--offset", "-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups", tc.args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if got := len(e.mock.Requests()); got != 0 {
				t.Errorf("requests = %d, want 0: %v", got, e.mock.Requests())
			}
		})
	}
}

func TestGetDataReviewGroups_StructuredAndNameOutputsAreRawAndSkipDirectory(t *testing.T) {
	for _, format := range []string{"json", "yaml", "name"} {
		t.Run(format, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			e.mock.RegisterHandler("GET", dataReviewGroupsURL, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, groupPageJSON(1))
			})
			stdout, stderr, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups",
				"--table-id", "42", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch format {
			case "json":
				if !strings.Contains(stdout, `"review_group_id"`) || !strings.Contains(stdout, `"total": 1`) {
					t.Errorf("JSON is not the raw GroupPage: %s", stdout)
				}
				if !strings.Contains(stdout, `"description": "Check the corrected addresses"`) ||
					!strings.Contains(stdout, `"assigned_reviewers"`) || !strings.Contains(stdout, `"email": "email-only@example.com"`) {
					t.Errorf("JSON is missing the request metadata: %s", stdout)
				}
			case "yaml":
				if !strings.Contains(stdout, "review_group_id:") || !strings.Contains(stdout, "total: 1") {
					t.Errorf("YAML is not the raw GroupPage: %s", stdout)
				}
				if !strings.Contains(stdout, "description: Check the corrected addresses") ||
					!strings.Contains(stdout, "assigned_reviewers:") || !strings.Contains(stdout, "email: email-only@example.com") {
					t.Errorf("YAML is missing the request metadata: %s", stdout)
				}
			case "name":
				if stdout != "data-review-group/"+dataReviewGroupID+"\n" {
					t.Errorf("name output = %q", stdout)
				}
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty", stderr)
			}
			if got := len(e.mock.Requests()); got != 1 {
				t.Errorf("requests = %d, want groups only: %v", got, e.mock.Requests())
			}
		})
	}
}

func TestGetDataReviewGroups_DirectoryFailureWarnsAndFallsBackToUUID(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.mock.RegisterHandler("GET", dataReviewGroupsURL, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, groupPageJSON(1))
	})
	e.registerUsers(http.StatusForbidden)

	stdout, stderr, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups", "--table-id", "42")
	if err != nil {
		t.Fatalf("directory failure should degrade, got: %v", err)
	}
	if !strings.Contains(stdout, dataReviewRequestor) || !strings.Contains(stdout, "Dee Reviewer") ||
		!strings.Contains(stdout, "email-only@example.com") {
		t.Errorf("stdout = %q, want raw requestor UUID and embedded reviewer names", stdout)
	}
	if strings.Count(stderr, "Warning:") != 1 || !strings.Contains(stderr, "user directory") {
		t.Errorf("stderr = %q, want one directory warning", stderr)
	}
}

func TestGetDataReviewGroups_NullMetadata(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.mock.RegisterHandler("GET", dataReviewGroupsURL, func(w http.ResponseWriter, _ *http.Request) {
		var page map[string]any
		if err := json.Unmarshal(groupPageJSON(1), &page); err != nil {
			t.Fatal(err)
		}
		group := page["groups"].([]any)[0].(map[string]any)
		group["description"], group["assigned_reviewers"] = nil, nil
		testutil.RespondJSON(w, http.StatusOK, page)
	})
	e.registerUsers(http.StatusOK)
	stdout, _, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups",
		"--table-id", "42", "--columns", "id,description,reviewers")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, dataReviewGroupID) || strings.Contains(stdout, "<nil>") {
		t.Errorf("unexpected row with absent request metadata: %s", stdout)
	}
}

func TestGetDataReviewGroups_MetadataColumns(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.mock.RegisterHandler("GET", dataReviewGroupsURL, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, groupPageJSON(1))
	})
	e.registerUsers(http.StatusForbidden)
	stdout, _, err := e.run(t, NewDataReviewGroupsCommand(), "data-review-groups",
		"--table-id", "42", "--columns", "description,reviewers")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Check the corrected addresses", "Dee Reviewer", "email-only@example.com", dataReviewGroupID} {
		if !strings.Contains(stdout, want) {
			t.Errorf("metadata columns are missing %q: %s", want, stdout)
		}
	}
}

func TestGetDataReviewChanges_DefaultsAndRendering(t *testing.T) {
	e := newDataReviewGetEnv(t)
	var listQuery, countQuery url.Values
	e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, r *http.Request) {
		listQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, topChangesJSON())
	})
	e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, r *http.Request) {
		countQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"count":9}`))
	})
	e.registerUsers(http.StatusOK)

	stdout, stderr, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes", "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{"dataset_id": "42", "decision_state": "unresolved", "limit": "20"} {
		if got := listQuery.Get(key); got != want {
			t.Errorf("list query %s = %q, want %q", key, got, want)
		}
	}
	if got := countQuery.Get("decision_state"); got != "unresolved" {
		t.Errorf("count decision_state = %q, want unresolved", got)
	}
	for _, query := range []url.Values{listQuery, countQuery} {
		if _, ok := query["review_group_id"]; ok {
			t.Errorf("default query unexpectedly scopes to a group: %v", query)
		}
		if _, ok := query["offset"]; ok {
			t.Errorf("default query unexpectedly sends offset: %v", query)
		}
	}
	for _, want := range []string{"701", "state", "CA", "∅", "702", "West", "approve", "Dee Cider"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "Showing 2 of up to 9 unresolved change(s).") {
		t.Errorf("stderr = %q, want upper-bound footer", stderr)
	}
	if got := len(e.mock.Requests()); got != 3 {
		t.Errorf("requests = %d, want list + count + users: %v", got, e.mock.Requests())
	}
}

// The unit tags are reachable through --columns, off by default. They are
// metadata about a value, so the ranked table keeps printing the values the
// server recorded and nothing else.
func TestGetDataReviewChanges_ExposesTheUnitTagsAsColumns(t *testing.T) {
	e := newDataReviewGetEnv(t)
	// A percent cell whose source text never converted, overwritten with a
	// number: the two sides carry different tags and the same shape.
	e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[`+
			`{"affected_row_count":4,"decided_by_id":null,"decision":null,"edited_value":"0.13",`+
			`"field_name":"rate","history_id":703,"original_value":"150",`+
			`"old_value_type":"str","value_type":"decimal"}]`))
	})
	e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"count":1}`))
	})
	e.registerUsers(http.StatusOK)

	stdout, _, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes",
		"--table-id", "42", "--columns", "history_id,before,before_type,after,after_type")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if got := strings.Fields(lines[len(lines)-1]); len(got) != 5 ||
		got[1] != "150" || got[2] != "str" || got[3] != "0.13" || got[4] != "decimal" {
		t.Errorf("row = %q, want each value beside its own tag\n%s", lines[len(lines)-1], stdout)
	}

	stdout, _, err = e.run(t, NewDataReviewChangesCommand(), "data-review-changes", "--table-id", "42")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	if strings.Contains(stdout, "decimal") || strings.Contains(stdout, "BEFORE-TYPE") {
		t.Errorf("the unit tags reached the default columns\n%s", stdout)
	}
}

// FLAGGED is a default column: "yes" for a change whose has_open_flag is true,
// blank for one whose has_open_flag is false.
func TestGetDataReviewChanges_FlaggedMarksAChangeWithAnOpenFlag(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`[`+
			`{"affected_row_count":12,"decided_by_id":null,"decision":null,"edited_value":"CA",`+
			`"field_name":"state","history_id":701,"original_value":"NV","has_open_flag":true},`+
			`{"affected_row_count":4,"decided_by_id":null,"decision":null,"edited_value":"East",`+
			`"field_name":"region","history_id":702,"original_value":"West","has_open_flag":false}]`))
	})
	e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"count":2}`))
	})
	e.registerUsers(http.StatusOK)

	stdout, _, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes", "--table-id", "42")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	rows := dataReviewChangeTableRows(stdout)
	if len(rows) != 3 || !slices.Contains(rows[0], "FLAGGED") {
		t.Fatalf("want a header with a default FLAGGED column and two rows\n%s", stdout)
	}
	flagged := slices.Index(rows[0], "FLAGGED")
	if rows[1][0] != "701" || len(rows[1]) <= flagged || rows[1][flagged] != "yes" {
		t.Errorf("flagged row = %q, want 701 marked yes under FLAGGED\n%s", rows[1], stdout)
	}
	if rows[2][0] != "702" || slices.Contains(rows[2], "yes") {
		t.Errorf("unflagged row = %q, want 702 with no marker\n%s", rows[2], stdout)
	}

	stdout, _, err = e.run(t, NewDataReviewChangesCommand(), "data-review-changes",
		"--table-id", "42", "--columns", "history_id,flagged")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	want := [][]string{{"HISTORY-ID", "FLAGGED"}, {"701", "yes"}, {"702"}}
	if got := dataReviewChangeTableRows(stdout); !reflect.DeepEqual(got, want) {
		t.Errorf("rows = %q, want %q\n%s", got, want, stdout)
	}
}

// dataReviewChangeTableRows splits table output into the fields of each
// non-blank line.
func dataReviewChangeTableRows(stdout string) [][]string {
	var rows [][]string
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			rows = append(rows, fields)
		}
	}
	return rows
}

func TestGetDataReviewChanges_LocalRefusalsCostNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing table", nil},
		{"bad state", []string{"--table-id", "42", "--state", "bogus"}},
		{"zero limit", []string{"--table-id", "42", "--limit", "0"}},
		{"limit too high", []string{"--table-id", "42", "--limit", "21"}},
		{"negative offset", []string{"--table-id", "42", "--offset", "-1"}},
		{"empty group", []string{"--table-id", "42", "--group", ""}},
		{"malformed group", []string{"--table-id", "42", "--group", "not-a-uuid"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes", tc.args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if got := len(e.mock.Requests()); got != 0 {
				t.Errorf("requests = %d, want 0: %v", got, e.mock.Requests())
			}
		})
	}
}

// The count is an upper bound, so page, total and next_page remain null.
func TestGetDataReviewChanges_JSONIsRawAndSkipsPresentationRequests(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			var query url.Values
			e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, r *http.Request) {
				query = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, topChangesJSON())
			})
			e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"count":9}`))
			})

			stdout, stderr, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes",
				"--table-id", "42", "--group", dataReviewGroupID, "--offset", "20", "--limit", "5", "-o", format)
			if err != nil {
				t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
			}
			list := dataReviewDecodeList(t, format, stdout)
			dataReviewWantPaging(t, list, map[string]*int{
				"page": nil, "limit": dataReviewInt(5), "total": nil, "next_page": nil,
			})
			want := dataReviewWireItems[[]data_reviews.TopChange](t, topChangesJSON())
			if !reflect.DeepEqual(list["items"], want) {
				t.Errorf("items = %#v\nwant the changes as sent: %#v", list["items"], want)
			}
			// want derives from the generated type, so the fields it could lose
			// are checked against the fixture itself: the unit tags, and a null
			// before side written as null rather than dropped.
			items, _ := list["items"].([]any)
			if len(items) != 2 {
				t.Fatalf("items = %#v, want the two changes", list["items"])
			}
			for i, tags := range []map[string]string{
				{"old_value_type": "none", "value_type": "str"},
				{"old_value_type": "str", "value_type": "none"},
			} {
				item, _ := items[i].(map[string]any)
				for key, tag := range tags {
					if got := item[key]; got != tag {
						t.Errorf("change %d %s = %v, want %q", i, key, got, tag)
					}
				}
			}
			if first, _ := items[0].(map[string]any); first == nil {
				t.Errorf("first item = %#v, want an object", items[0])
			} else if value, present := first["original_value"]; !present || value != nil {
				t.Errorf("first item original_value = %v (present %v), want null", value, present)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, structured output should have no human footer", stderr)
			}
			// The count handler is registered and deliberately left uncalled: it
			// feeds the stderr footer, which structured output does not print.
			if got := len(e.mock.Requests()); got != 1 {
				t.Errorf("requests = %d, want the list only: %v", got, e.mock.Requests())
			}
			for key, want := range map[string]string{"review_group_id": dataReviewGroupID, "offset": "20", "limit": "5"} {
				if got := query.Get(key); got != want {
					t.Errorf("query %s = %q, want %q", key, got, want)
				}
			}
		})
	}
}

func TestGetDataReviewChanges_GroupPaginationAndContinuation(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		offset, returned, total int
		next                    string
	}{
		{"first full page", 0, 20, 43, "--offset 20"},
		{"second full page", 20, 20, 43, "--offset 40"},
		{"last full page", 20, 20, 40, ""},
		{"short refined page", 20, 2, 43, ""},
		{"empty refined page", 20, 0, 43, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			var listQuery, countQuery url.Values
			e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, r *http.Request) {
				listQuery = r.URL.Query()
				changes := make([]data_reviews.TopChange, tc.returned)
				for i := range changes {
					changes[i].HistoryID = 701 + tc.offset + i
					changes[i].FieldName = "state"
					changes[i].AffectedRowCount = 1
				}
				testutil.RespondJSON(w, http.StatusOK, changes)
			})
			e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, r *http.Request) {
				countQuery = r.URL.Query()
				testutil.RespondJSON(w, http.StatusOK, map[string]int{"count": tc.total})
			})
			e.registerUsers(http.StatusOK)
			stdout, stderr, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes",
				"--table-id", "42", "--state", "all", "--group", "data-review-group/"+dataReviewGroupID,
				"--offset", strconv.Itoa(tc.offset))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, query := range []url.Values{listQuery, countQuery} {
				for key, want := range map[string]string{
					"dataset_id": "42", "decision_state": "all", "review_group_id": dataReviewGroupID,
				} {
					if got := query.Get(key); got != want {
						t.Errorf("query %s = %q, want %q", key, got, want)
					}
				}
			}
			if tc.offset > 0 && listQuery.Get("offset") != strconv.Itoa(tc.offset) {
				t.Errorf("list offset = %q, want %d", listQuery.Get("offset"), tc.offset)
			}
			if _, ok := countQuery["offset"]; ok {
				t.Errorf("count must cover the entire group: %v", countQuery)
			}
			if tc.returned > 0 && !strings.Contains(stdout, strconv.Itoa(701+tc.offset)) {
				t.Errorf("stdout does not contain this page's first change: %s", stdout)
			}
			if !strings.Contains(stderr, "Showing "+strconv.Itoa(tc.returned)+" of up to "+strconv.Itoa(tc.total)+" all change(s).") {
				t.Errorf("stderr lacks the group's upper-bound count: %s", stderr)
			}
			if tc.next == "" {
				if strings.Contains(stderr, "continue with") {
					t.Errorf("unexpected continuation hint on a finished page: %s", stderr)
				}
			} else if !strings.Contains(stderr, tc.next) || !strings.Contains(stderr, "may be available") {
				t.Errorf("stderr lacks a qualified next-offset hint %q: %s", tc.next, stderr)
			}
		})
	}
}

func TestGetDataReviewChanges_DirectoryFailureWarnsAndFallsBackToUUID(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.mock.RegisterHandler("GET", dataReviewChangesURL, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, topChangesJSON())
	})
	e.mock.RegisterHandler("GET", dataReviewChangesURL+"/count", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"count":9}`))
	})
	e.registerUsers(http.StatusInternalServerError)

	stdout, stderr, err := e.run(t, NewDataReviewChangesCommand(), "data-review-changes", "--table-id", "42")
	if err != nil {
		t.Fatalf("directory failure should degrade, got: %v", err)
	}
	if !strings.Contains(stdout, dataReviewDecider) {
		t.Errorf("stdout = %q, want raw decider UUID", stdout)
	}
	if strings.Count(stderr, "Warning:") != 1 || !strings.Contains(stderr, "Showing 2 of up to 9") {
		t.Errorf("stderr = %q, want one warning and one footer", stderr)
	}
}

const (
	dataReviewDataURL   = "/api/orgs/" + testOrgID + "/datasets/42/data"
	dataReviewByRowsURL = "/api/orgs/" + testOrgID + "/datasets/42/data-reviews/by-rows"
	dataReviewRow5      = "55555555-0000-4000-8000-000000000005"
	dataReviewRow7      = "77777777-0000-4000-8000-000000000007"
	dataReviewRequester = "0190a0a0-1111-4222-8333-444444444444"
	dataReviewGroupA    = "11111111-2222-4333-8444-555555555555"
	dataReviewGroupB    = "22222222-2222-4333-8444-555555555555"
)

// quarantinePageJSON is a GET /data page holding the given row ids, in the
// order given.
func quarantinePageJSON(totalPages int, rowIDs ...int) json.RawMessage {
	rows := make([]string, 0, len(rowIDs))
	for _, id := range rowIDs {
		rows = append(rows, `[`+strconv.Itoa(id)+`,"value-`+strconv.Itoa(id)+`"]`)
	}
	return json.RawMessage(`{"dataset_name":"orders","fields":["id","name"],"labels":["id","name"],` +
		`"field_types":["integer","text"],"field_display_formats":[null,null],` +
		`"results":[` + strings.Join(rows, ",") + `],` +
		`"total_page_count":` + strconv.Itoa(totalPages) + `,"total_rows":` + strconv.Itoa(len(rowIDs)*totalPages) + `}`)
}

// rowOverlaysJSON covers rows 5 and 7 of a page that also holds row 3, which
// has no active review. Row 5 names its requester and two requests; row 7
// carries one change of every decision shape, and no current_approvals,
// requester or request ids.
func rowOverlaysJSON() json.RawMessage {
	return json.RawMessage(`{"reviews":[` +
		`{"id":"` + dataReviewRow5 + `","row_id":5,"status":"open","comment_count":0,` +
		`"requester":{"id":"` + dataReviewRequester + `","email":"ada@example.com","first_name":"Ada","last_name":""},` +
		`"review_group_ids":["` + dataReviewGroupA + `","` + dataReviewGroupB + `"],` +
		`"current_approvals":1,"required_approvals":2,` +
		`"release_block_reason":"meter_short","changes":[]},` +
		`{"id":"` + dataReviewRow7 + `","row_id":7,"status":"changes_requested","comment_count":3,` +
		`"required_approvals":1,` +
		`"release_block_reason":"cell_veto","changes":[` +
		`{"field_name":"a","history_id":1,"decided_at":null,"decision":null},` +
		`{"field_name":"b","history_id":2,"decided_at":null,"decision":"comment_only"},` +
		`{"field_name":"c","history_id":3,"decided_at":null,"decision":"approve"},` +
		`{"field_name":"d","history_id":4,"decided_at":null,"decision":"reject"},` +
		`{"field_name":"e","history_id":5,"decided_at":null,"decision":"request_changes"}` +
		`]}]}`)
}

type dataReviewRowsCall struct {
	dataQuery url.Values
	rowIDs    []int
}

func (e *dataReviewGetEnv) registerDataReviewRows(page, overlays json.RawMessage) *dataReviewRowsCall {
	call := &dataReviewRowsCall{}
	e.mock.RegisterHandler("GET", dataReviewDataURL, func(w http.ResponseWriter, r *http.Request) {
		call.dataQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, page)
	})
	e.mock.RegisterHandler("POST", dataReviewByRowsURL, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RowIDs []int `json:"row_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			testutil.RespondJSON(w, http.StatusBadRequest, map[string]any{"detail": err.Error()})
			return
		}
		call.rowIDs = body.RowIDs
		testutil.RespondJSON(w, http.StatusOK, overlays)
	})
	return call
}

// tableLine returns the stdout line whose second cell is row, so an assertion
// about one row cannot be satisfied by another.
func tableLine(t *testing.T, stdout string, row int) []string {
	t.Helper()
	for _, line := range strings.Split(stdout, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[1] == strconv.Itoa(row) {
			return fields
		}
	}
	t.Fatalf("no table line for row %d\n%s", row, stdout)
	return nil
}

func TestGetDataReviews_DefaultRequestAndJoin(t *testing.T) {
	e := newDataReviewGetEnv(t)
	call := e.registerDataReviewRows(quarantinePageJSON(2, 7, 3, 5), rowOverlaysJSON())

	stdout, stderr, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{
		"is_bad_data": "true", "review_status": "awaiting_decision", "page": "1", "limit": "20", "order_by": "id",
	} {
		if got := call.dataQuery.Get(key); got != want {
			t.Errorf("data query %s = %q, want %q", key, got, want)
		}
	}
	if call.dataQuery.Has("review_group_id") {
		t.Errorf("data query carries review_group_id without --group: %v", call.dataQuery)
	}
	if got := len(call.rowIDs); got != 3 {
		t.Errorf("by-rows row_ids = %v, want the page's three rows", call.rowIDs)
	}

	wantLines := map[int][]string{
		3: {"-", "3", "-", "-", "-", "-", "-", "-", "-", "-"},
		5: {dataReviewRow5, "5", "open", "Ada", "1/2", "meter_short", "0", "0", "0", "-"},
		// comment_only and no decision are both undecided; approve is neither.
		7: {dataReviewRow7, "7", "changes_requested", "-", "-", "cell_veto", "2", "2", "3", "-"},
	}
	for row, want := range wantLines {
		if got := tableLine(t, stdout, row); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("row %d = %q, want %q", row, got, want)
		}
	}
	if strings.Contains(stdout, "value-") {
		t.Errorf("stdout leaks data values:\n%s", stdout)
	}
	// The page arrived as 7, 3, 5.
	var order []string
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 1 && fields[0] != "ID" {
			order = append(order, fields[1])
		}
	}
	if got := strings.Join(order, ","); got != "3,5,7" {
		t.Errorf("row order = %s, want ascending 3,5,7\n%s", got, stdout)
	}
	if strings.Count(stderr, "\n") != 1 || !strings.Contains(stderr, "--page 2") {
		t.Errorf("stderr = %q, want exactly one next-page hint", stderr)
	}
	if got := e.mock.Requests(); len(got) != 2 {
		t.Errorf("requests = %v, want data + by-rows", got)
	}
}

func TestGetDataReviews_DefaultColumnOrder(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.registerDataReviewRows(quarantinePageJSON(1, 5), rowOverlaysJSON())

	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	header := dataReviewTableFields(t, stdout)[0]
	want := []string{"ID", "ROW", "STATUS", "REQUESTER", "APPROVALS", "HELD", "UNDECIDED", "BLOCKING", "COMMENTS", "CANNOT-APPROVE"}
	if !slices.Equal(header, want) {
		t.Errorf("header = %v, want %v\n%s", header, want, stdout)
	}
}

func TestGetDataReviews_SelfExclusionFollowsRowsBeforeSorting(t *testing.T) {
	e := newDataReviewGetEnv(t)
	page := strings.TrimSuffix(string(quarantinePageJSON(1, 7, 3, 5)), "}") + `,"sent_or_changed_by_me":[true,true,false]}`
	e.registerDataReviewRows(json.RawMessage(page), rowOverlaysJSON())
	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42", "--columns", "row,cannot_approve")
	if err != nil {
		t.Fatal(err)
	}
	got := dataReviewTableFields(t, stdout)
	want := [][]string{{"ROW", "CANNOT-APPROVE"}, {"3", "self"}, {"5", "-"}, {"7", "self"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("table = %v, want %v", got, want)
	}
	if got := len(e.mock.Requests()); got != 2 {
		t.Errorf("requests = %d, want data + by-rows", got)
	}
}

func TestGetDataReviews_RejectsMisalignedSelfExclusion(t *testing.T) {
	e := newDataReviewGetEnv(t)
	page := strings.TrimSuffix(string(quarantinePageJSON(1, 7, 3, 5)), "}") + `,"sent_or_changed_by_me":[true]}`
	e.registerDataReviewRows(json.RawMessage(page), rowOverlaysJSON())
	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42")
	if err == nil || !strings.Contains(err.Error(), "sent_or_changed_by_me") {
		t.Errorf("err = %v, want sidecar alignment error", err)
	}
	if stdout != "" {
		t.Errorf("rendered an unalignable page: %s", stdout)
	}
}

func TestGetDataReviews_RequestsColumnListsEveryRequest(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.registerDataReviewRows(quarantinePageJSON(1, 7, 3, 5), rowOverlaysJSON())
	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42", "--columns", "row,requests")
	if err != nil {
		t.Fatal(err)
	}
	got := dataReviewTableFields(t, stdout)
	want := [][]string{{"ROW", "REQUESTS"}, {"3", "-"}, {"5", dataReviewGroupA + "," + dataReviewGroupB}, {"7", "-"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("table = %v, want %v", got, want)
	}
}

// dataReviewTableFields is stdout's non-blank lines split into fields, header
// first; it fails the test on an empty table.
func dataReviewTableFields(t *testing.T, stdout string) [][]string {
	t.Helper()
	var rows [][]string
	for _, line := range strings.Split(stdout, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			rows = append(rows, fields)
		}
	}
	if len(rows) == 0 {
		t.Fatalf("no table in stdout %q", stdout)
	}
	return rows
}

// approvalsCells maps each row of a --columns row,approvals table to its
// approvals cell, which may hold spaces.
func approvalsCells(t *testing.T, stdout string) map[string]string {
	t.Helper()
	rows := dataReviewTableFields(t, stdout)
	if !slices.Equal(rows[0], []string{"ROW", "APPROVALS"}) {
		t.Fatalf("header = %v, want [ROW APPROVALS]\n%s", rows[0], stdout)
	}
	cells := map[string]string{}
	for _, fields := range rows[1:] {
		cells[fields[0]] = strings.Join(fields[1:], " ")
	}
	return cells
}

func TestGetDataReviews_ApprovalsColumn(t *testing.T) {
	overlay := func(row int, counts string) string {
		return `{"id":"00000000-0000-4000-8000-00000000000` + strconv.Itoa(row) + `","row_id":` +
			strconv.Itoa(row) + `,"status":"open"` + counts + `,"changes":[]}`
	}
	overlays := json.RawMessage(`{"reviews":[` + strings.Join([]string{
		overlay(1, `,"current_approvals":1,"required_approvals":2`),
		overlay(2, `,"current_approvals":0,"required_approvals":1`),
		overlay(3, `,"required_approvals":2`),
		overlay(4, `,"current_approvals":1`),
		overlay(5, ``),
	}, ",") + `]}`)
	e := newDataReviewGetEnv(t)
	e.registerDataReviewRows(quarantinePageJSON(1, 1, 2, 3, 4, 5, 6), overlays)

	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42",
		"--columns", "row,approvals")
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	want := map[string]string{
		"1": "1/2",
		"2": "0/1", // a zero count is a count, not an absent one
		"3": "-",   // no current_approvals
		"4": "-",   // no required_approvals
		"5": "-",   // neither
		"6": "-",   // no active review
	}
	if got := approvalsCells(t, stdout); !maps.Equal(got, want) {
		t.Errorf("approvals = %v, want %v\n%s", got, want, stdout)
	}
}

// The overlays are re-encoded from the generated type, so a field it lacks
// would vanish from both this output and dataReviewWireItems' expectation.
func TestGetDataReviews_StructuredOutputCarriesTheApprovalCounts(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			e.registerDataReviewRows(quarantinePageJSON(1, 3, 5, 7), rowOverlaysJSON())

			stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42", "-o", format)
			if err != nil {
				t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
			}
			items, ok := dataReviewDecodeList(t, format, stdout)["items"].([]any)
			if !ok || len(items) != 2 {
				t.Fatalf("items = %#v, want the two overlays", items)
			}
			byRow := map[float64]map[string]any{}
			for _, item := range items {
				overlay := item.(map[string]any)
				byRow[overlay["row_id"].(float64)] = overlay
			}
			if got := byRow[5]; got["current_approvals"] != float64(1) || got["required_approvals"] != float64(2) {
				t.Errorf("row 5 = %v, want current_approvals 1 and required_approvals 2", got)
			}
			if got := byRow[7]; got["required_approvals"] != float64(1) {
				t.Errorf("row 7 = %v, want required_approvals 1", got)
			}
			if _, present := byRow[7]["current_approvals"]; present {
				t.Errorf("row 7 = %v, want current_approvals absent as sent", byRow[7])
			}
		})
	}
}

func TestGetDataReviews_StateGroupAndPagination(t *testing.T) {
	e := newDataReviewGetEnv(t)
	call := e.registerDataReviewRows(quarantinePageJSON(3, 5), rowOverlaysJSON())

	_, stderr, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42",
		"--state", "rejected", "--group", dataReviewGroupID, "--page", "3", "--limit", "5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{
		"is_bad_data": "true", "review_status": "rejected", "review_group_id": dataReviewGroupID,
		"page": "3", "limit": "5",
	} {
		if got := call.dataQuery.Get(key); got != want {
			t.Errorf("data query %s = %q, want %q", key, got, want)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want no hint on the last page", stderr)
	}
}

func TestGetDataReviews_NameListsOnlyRowsWithAnActiveReview(t *testing.T) {
	e := newDataReviewGetEnv(t)
	e.registerDataReviewRows(quarantinePageJSON(1, 7, 3, 5), rowOverlaysJSON())

	stdout, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "data-review/" + dataReviewRow5 + "\ndata-review/" + dataReviewRow7 + "\n"; stdout != want {
		t.Errorf("name output = %q, want %q", stdout, want)
	}
}

// The items are the by-rows overlays, row 3 (no active review) absent; the
// paging is the data page's, so total counts the bucket's rows.
func TestGetDataReviews_StructuredOutputIsAListOfTheOverlays(t *testing.T) {
	cases := []struct {
		name string
		page string
		want map[string]*int
	}{
		{"middle page", "1", map[string]*int{
			"page": dataReviewInt(1), "limit": dataReviewInt(3), "total": dataReviewInt(6), "next_page": dataReviewInt(2),
		}},
		{"last page", "2", map[string]*int{
			"page": dataReviewInt(2), "limit": dataReviewInt(3), "total": dataReviewInt(6), "next_page": nil,
		}},
	}
	for _, tc := range cases {
		for _, format := range []string{"json", "yaml"} {
			t.Run(tc.name+"/"+format, func(t *testing.T) {
				e := newDataReviewGetEnv(t)
				e.registerDataReviewRows(quarantinePageJSON(2, 7, 3, 5), rowOverlaysJSON())

				stdout, stderr, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42",
					"--page", tc.page, "--limit", "3", "-o", format)
				if err != nil {
					t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
				}
				list := dataReviewDecodeList(t, format, stdout)
				dataReviewWantPaging(t, list, tc.want)
				overlay := dataReviewWireItems[data_reviews.RowOverlayPage](t, rowOverlaysJSON())
				if want := overlay.(map[string]any)["reviews"]; !reflect.DeepEqual(list["items"], want) {
					t.Errorf("items = %#v\nwant the overlays as sent: %#v", list["items"], want)
				}
				if stderr != "" {
					t.Errorf("stderr = %q, want no hint with structured output", stderr)
				}
				if got := e.mock.Requests(); len(got) != 2 {
					t.Errorf("requests = %v, want data + by-rows", got)
				}
			})
		}
	}
}

func TestGetDataReviews_EmptyPageIsOneRequest(t *testing.T) {
	for _, format := range []string{"", "json", "yaml"} {
		t.Run("format="+format, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			e.registerDataReviewRows(quarantinePageJSON(0), rowOverlaysJSON())
			args := []string{"--table-id", "42"}
			if format != "" {
				args = append(args, "-o", format)
			}

			stdout, stderr, err := e.run(t, NewDataReviewsCommand(), "data-reviews", args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := e.mock.Requests(); len(got) != 1 {
				t.Errorf("requests = %v, want GET /data only", got)
			}
			switch format {
			case "json", "yaml":
				list := dataReviewDecodeList(t, format, stdout)
				dataReviewWantPaging(t, list, map[string]*int{
					"page": dataReviewInt(1), "limit": dataReviewInt(20), "total": dataReviewInt(0), "next_page": nil,
				})
				if items, ok := list["items"].([]any); !ok || len(items) != 0 {
					t.Errorf("items = %#v, want []", list["items"])
				}
				if stderr != "" {
					t.Errorf("stderr = %q, want nothing with structured output", stderr)
				}
			default:
				if stdout != "" {
					t.Errorf("stdout = %q, want nothing", stdout)
				}
				if !strings.Contains(stderr, "No data-review rows matched") ||
					!strings.Contains(stderr, "awaiting_decision bucket") {
					t.Errorf("stderr = %q, want the empty-result line and the bucket hint", stderr)
				}
			}
		})
	}
}

func TestGetDataReviews_LocalRefusalsCostNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"missing table", nil},
		{"both table selectors", []string{"--table", "orders", "--table-id", "42"}},
		{"bad state", []string{"--table-id", "42", "--state", "bogus"}},
		{"backend state spelled differently", []string{"--table-id", "42", "--state", "open"}},
		{"malformed group", []string{"--table-id", "42", "--group", "1234"}},
		{"empty group", []string{"--table-id", "42", "--group", ""}},
		{"zero page", []string{"--table-id", "42", "--page", "0"}},
		{"zero limit", []string{"--table-id", "42", "--limit", "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", tc.args...)
			if err == nil {
				t.Fatal("expected usage error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want 2 (%v)", got, err)
			}
			if got := e.mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestGetDataReviews_UnknownColumnIsBadUsage(t *testing.T) {
	for _, page := range []json.RawMessage{quarantinePageJSON(1, 5), quarantinePageJSON(0)} {
		e := newDataReviewGetEnv(t)
		e.registerDataReviewRows(page, rowOverlaysJSON())
		_, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42", "--columns", "id,bogus")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want 2 (%v)", got, err)
		}
	}
}

func TestGetDataReviews_EndpointFailuresKeepTheirExitCodes(t *testing.T) {
	refuse := func(status int, msg string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, status, map[string]any{"detail": map[string]any{"msg": msg}})
		}
	}
	cases := []struct {
		name     string
		data     http.HandlerFunc
		byRows   http.HandlerFunc
		wantCode int
		wantText string
	}{
		{
			name:     "data 403",
			data:     refuse(http.StatusForbidden, "User is not authorized to access table 42"),
			wantCode: qerrors.ExitUnauthorized,
			wantText: "data endpoint",
		},
		{
			name:     "data 400",
			data:     refuse(http.StatusBadRequest, "The following parameters can only be used when is_bad_data=True: review_status"),
			wantCode: qerrors.ExitBadUsage,
			wantText: "can only be used when is_bad_data=True",
		},
		{
			name:     "by-rows 403",
			byRows:   refuse(http.StatusForbidden, "Permission denied"),
			wantCode: qerrors.ExitUnauthorized,
			wantText: "by-rows endpoint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			e.registerDataReviewRows(quarantinePageJSON(1, 5), rowOverlaysJSON())
			if tc.data != nil {
				e.mock.RegisterHandler("GET", dataReviewDataURL, tc.data)
			}
			if tc.byRows != nil {
				e.mock.RegisterHandler("POST", dataReviewByRowsURL, tc.byRows)
			}
			_, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42")
			if got := qerrors.GetExitCode(err); got != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (%v)", got, tc.wantCode, err)
			}
			// The one sanctioned substring check: which endpoint refused is
			// this command's own framing, and it is the point of the wrap.
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantText)
			}
			if tc.wantCode == qerrors.ExitUnauthorized && !strings.Contains(err.Error(), "source:data:read") {
				t.Errorf("error = %q, want the source data read permission", err)
			}
		})
	}
}

func TestGetDataReviews_ForbiddenUsesOperationMetadata(t *testing.T) {
	for _, operation := range []string{"get_data", "data_reviews_by_rows"} {
		t.Run(operation, func(t *testing.T) {
			original := api.OperationPermissions[operation]
			api.OperationPermissions[operation] = "test:rows:read"
			t.Cleanup(func() { api.OperationPermissions[operation] = original })
			e := newDataReviewGetEnv(t)
			e.registerDataReviewRows(quarantinePageJSON(1, 5), rowOverlaysJSON())
			method, path := "GET", dataReviewDataURL
			if operation == "data_reviews_by_rows" {
				method, path = "POST", dataReviewByRowsURL
			}
			e.mock.RegisterHandler(method, path, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "Source not assigned"})
			})
			_, _, err := e.run(t, NewDataReviewsCommand(), "data-reviews", "--table-id", "42")
			if qerrors.GetExitCode(err) != qerrors.ExitUnauthorized {
				t.Fatalf("exit code lost: %v", err)
			}
			if !strings.Contains(err.Error(), "test:rows:read") || !strings.Contains(err.Error(), "Source not assigned") {
				t.Errorf("operation metadata or server cause lost: %v", err)
			}
		})
	}
}
