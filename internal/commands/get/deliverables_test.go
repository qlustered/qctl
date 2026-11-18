package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const deliverablesRoute = "/api/orgs/" + testOrgID + "/deliverables"

// deliverablesBody is a page of two deliverables as atlas sends it: one with
// an enabled revision and a rendered run, and one in the trash with neither.
const deliverablesBody = `{
  "next": null, "previous": null, "page": 1, "total_rows": 2, "msg": "",
  "results": [
    {
      "id": "dddd0000-1111-2222-3333-000000000001",
      "name": "Monthly Bordereau", "slug": "monthly-bordereau",
      "receiver_label": "Acme Re", "filename_pattern": "bdx-{period}.xlsx",
      "period_param": "period", "archived_at": null,
      "created_at": "2025-06-01T10:00:00Z", "updated_at": "2025-06-15T10:00:00Z",
      "enabled_revision_id": "eeee0000-1111-2222-3333-000000000001",
      "latest_run": {
        "id": 7, "state": "rendered", "sequence": 3, "updated_at": "2025-06-15T10:00:00Z",
        "run_params": {"period": "2025-05", "version": 2},
        "run_params_typed": {
          "period": {"type": "string", "text": "2025-05"},
          "version": {"type": "integer", "text": "2"}
        }
      }
    },
    {
      "id": "dddd0000-1111-2222-3333-000000000002",
      "name": "Quarterly Pack", "slug": "quarterly-pack",
      "receiver_label": "", "filename_pattern": "pack.xlsx",
      "period_param": null, "archived_at": "2025-06-20T09:00:00Z",
      "created_at": "2025-05-01T10:00:00Z", "updated_at": "2025-06-20T09:00:00Z",
      "enabled_revision_id": null, "latest_run": null
    }
  ]
}`

const emptyDeliverablesBody = `{"next": null, "previous": null, "page": 1, "total_rows": 0, "msg": "", "results": []}`

func setupDeliverablesTestCommand() *cobra.Command {
	root := setupTableKindsTestCommand()
	getCmd, _, _ := root.Find([]string{"get"})
	getCmd.AddCommand(NewDeliverablesCommand())
	return root
}

// deliverablesRun is what one get deliverables invocation did.
type deliverablesRun struct {
	requests       []string
	query          url.Values
	stdout, stderr string
	err            error
}

// runGetDeliverables serves body on the list route and runs get deliverables
// with args. query is the last list request's query, or nil when none came.
func runGetDeliverables(t *testing.T, body string, args ...string) deliverablesRun {
	t.Helper()
	mock := newTableKindsTestEnv(t)
	var run deliverablesRun
	mock.RegisterHandler("GET", deliverablesRoute, func(w http.ResponseWriter, r *http.Request) {
		run.query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
	cmd := setupDeliverablesTestCommand()
	cmd.SetArgs(append([]string{"get", "deliverables"}, args...))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	run.err = cmd.Execute()
	run.requests = mock.Requests()
	run.stdout, run.stderr = stdout.String(), stderr.String()
	return run
}

// TestGetDeliverablesFlagsOnWire pins the whole query each flag sends, so a
// flag that leaks into another parameter fails as surely as one that is lost.
func TestGetDeliverablesFlagsOnWire(t *testing.T) {
	defaults := func(extra ...string) url.Values {
		values := url.Values{"page": {"1"}, "limit": {"20"}, "order_by": {"updated_at"}}
		for i := 0; i < len(extra); i += 2 {
			values.Set(extra[i], extra[i+1])
		}
		return values
	}
	tests := []struct {
		name string
		args []string
		want url.Values
	}{
		{name: "no flags", want: defaults()},
		{name: "--search", args: []string{"--search", "abc"}, want: defaults("search_query", "abc")},
		{name: "--archived", args: []string{"--archived"}, want: defaults("only_archived", "true")},
		{name: "--include-archived", args: []string{"--include-archived"}, want: defaults("include_archived", "true")},
		{name: "--order-by name", args: []string{"--order-by", "name"}, want: defaults("order_by", "name")},
		{name: "--order-by created_at", args: []string{"--order-by", "created_at"}, want: defaults("order_by", "created_at")},
		// The server defaults to reverse=true, so --reverse turns it off.
		{name: "--reverse", args: []string{"--reverse"}, want: defaults("reverse", "false")},
		{name: "--page and --limit", args: []string{"--page", "3", "--limit", "5"}, want: defaults("page", "3", "limit", "5")},
		{name: "--table-id", args: []string{"--table-id", "42"}, want: defaults("member_dataset_id", "42")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := runGetDeliverables(t, emptyDeliverablesBody, tt.args...)
			if run.err != nil {
				t.Fatalf("Execute() error = %v", run.err)
			}
			if want := []string{"GET " + deliverablesRoute}; !reflect.DeepEqual(run.requests, want) {
				t.Fatalf("requests = %v, want %v", run.requests, want)
			}
			if !reflect.DeepEqual(run.query, tt.want) {
				t.Errorf("query = %v\nwant    %v", run.query, tt.want)
			}
		})
	}
}

func TestGetDeliverablesRefusesBeforeAnyRequest(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "both trash flags", args: []string{"--archived", "--include-archived"}},
		{name: "search under 3 characters", args: []string{"--search", "ab"}},
		{name: "order by a field the server cannot sort", args: []string{"--order-by", "slug"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := runGetDeliverables(t, deliverablesBody, tt.args...)
			if code := qerrors.GetExitCode(run.err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; error: %v", code, qerrors.ExitBadUsage, run.err)
			}
			if len(run.requests) != 0 {
				t.Errorf("requests = %v, want none", run.requests)
			}
		})
	}
}

// TestGetDeliverablesTableShowsDefaultColumns pins the default table header
// and the row projection of an enabled deliverable with a run.
func TestGetDeliverablesTableShowsDefaultColumns(t *testing.T) {
	run := runGetDeliverables(t, deliverablesBody)
	if run.err != nil {
		t.Fatalf("Execute() error = %v", run.err)
	}
	header := rowFields(t, run.stdout, "SLUG")
	want := []string{"NAME", "SLUG", "RECEIVER", "ENABLED", "LATEST-RUN", "LATEST-RUN-PARAMS", "UPDATED-AT"}
	if !reflect.DeepEqual(header, want) {
		t.Errorf("header = %v, want %v", header, want)
	}
	line := strings.Join(rowFields(t, run.stdout, "monthly-bordereau"), " ")
	for _, cell := range []string{"Acme Re", "yes", "rendered", "period=2025-05, version=2"} {
		if !strings.Contains(line, cell) {
			t.Errorf("row %q lacks %q", line, cell)
		}
	}
	trashed := strings.Join(rowFields(t, run.stdout, "quarterly-pack"), " ")
	if !strings.Contains(trashed, "quarterly-pack - no - -") {
		t.Errorf("row %q, want no receiver, enabled no, and no run", trashed)
	}
}

// TestGetDeliverablesTrashListingAddsArchivedAt pins that a listing reaching
// into the trash, and only such a listing, shows archived_at.
func TestGetDeliverablesTrashListingAddsArchivedAt(t *testing.T) {
	base := []string{"NAME", "SLUG", "RECEIVER", "ENABLED", "LATEST-RUN", "LATEST-RUN-PARAMS", "UPDATED-AT"}
	trash := append(append([]string{}, base...), "ARCHIVED-AT")
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{name: "plain listing", want: base},
		{name: "--archived", args: []string{"--archived"}, want: trash},
		{name: "--include-archived", args: []string{"--include-archived"}, want: trash},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := runGetDeliverables(t, deliverablesBody, tt.args...)
			if run.err != nil {
				t.Fatalf("Execute() error = %v", run.err)
			}
			if header := rowFields(t, run.stdout, "SLUG"); !reflect.DeepEqual(header, tt.want) {
				t.Errorf("header = %v, want %v", header, tt.want)
			}
		})
	}
}

// TestGetDeliverablesJSONIsAListOfAPIRecords pins that -o json items are the
// API records: every key the server sent and none of the table's projection.
func TestGetDeliverablesJSONIsAListOfAPIRecords(t *testing.T) {
	run := runGetDeliverables(t, deliverablesBody, "-o", "json")
	if run.err != nil {
		t.Fatalf("Execute() error = %v", run.err)
	}
	var list output.List
	if err := json.Unmarshal([]byte(run.stdout), &list); err != nil {
		t.Fatalf("-o json did not decode as a List: %v\n%s", err, run.stdout)
	}
	assertCatalogListPaging(t, list, 1, 20, 2, nil)

	var got []map[string]interface{}
	decodeListItems(t, []byte(run.stdout), &got)
	var sent struct {
		Results []map[string]interface{} `json:"results"`
	}
	if err := json.Unmarshal([]byte(deliverablesBody), &sent); err != nil {
		t.Fatal(err)
	}
	// latest_run is omitempty in the generated type, so a null run is left out.
	delete(sent.Results[1], "latest_run")
	if !reflect.DeepEqual(got, sent.Results) {
		t.Errorf("items = %v\nwant    %v", got, sent.Results)
	}
	if run.stderr != "" {
		t.Errorf("stderr = %q, want nothing under -o json", run.stderr)
	}
}

func TestGetDeliverablesYAMLIsAList(t *testing.T) {
	run := runGetDeliverables(t, deliverablesBody, "-o", "yaml")
	if run.err != nil {
		t.Fatalf("Execute() error = %v", run.err)
	}
	var list output.List
	if err := yaml.Unmarshal([]byte(run.stdout), &list); err != nil {
		t.Fatalf("-o yaml did not decode as a List: %v\n%s", err, run.stdout)
	}
	assertCatalogListPaging(t, list, 1, 20, 2, nil)
	var items struct {
		Items []struct {
			ID         string `yaml:"id"`
			Slug       string `yaml:"slug"`
			ArchivedAt string `yaml:"archived_at"`
			LatestRun  *struct {
				State string `yaml:"state"`
			} `yaml:"latest_run"`
		} `yaml:"items"`
	}
	if err := yaml.Unmarshal([]byte(run.stdout), &items); err != nil {
		t.Fatalf("items did not decode: %v\n%s", err, run.stdout)
	}
	if len(items.Items) != 2 {
		t.Fatalf("items = %d, want 2:\n%s", len(items.Items), run.stdout)
	}
	first, second := items.Items[0], items.Items[1]
	if first.ID != "dddd0000-1111-2222-3333-000000000001" || first.Slug != "monthly-bordereau" ||
		first.LatestRun == nil || first.LatestRun.State != "rendered" {
		t.Errorf("first item = %+v", first)
	}
	if second.ID != "dddd0000-1111-2222-3333-000000000002" || second.ArchivedAt != "2025-06-20T09:00:00Z" {
		t.Errorf("second item = %+v", second)
	}
}

func TestGetDeliverablesNamePrintsOneRefPerItem(t *testing.T) {
	run := runGetDeliverables(t, deliverablesBody, "-o", "name")
	if run.err != nil {
		t.Fatalf("Execute() error = %v", run.err)
	}
	want := "deliverable/dddd0000-1111-2222-3333-000000000001\ndeliverable/dddd0000-1111-2222-3333-000000000002\n"
	if run.stdout != want {
		t.Errorf("stdout = %q, want %q", run.stdout, want)
	}
}

func TestGetDeliverablesEmpty(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{name: "table names the org", wantStderr: `No deliverables found in org "` + testOrgID + `" (context: "default").` + "\n"},
		{name: "table names the filter", args: []string{"--archived"},
			wantStderr: `No deliverables matched --archived in org "` + testOrgID + `" (context: "default").` + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := runGetDeliverables(t, emptyDeliverablesBody, tt.args...)
			if run.err != nil {
				t.Fatalf("Execute() error = %v", run.err)
			}
			if run.stdout != "" {
				t.Errorf("stdout = %q, want empty", run.stdout)
			}
			if run.stderr != tt.wantStderr {
				t.Errorf("stderr = %q, want %q", run.stderr, tt.wantStderr)
			}
		})
	}

	t.Run("json is a List with no items", func(t *testing.T) {
		run := runGetDeliverables(t, emptyDeliverablesBody, "-o", "json")
		if run.err != nil {
			t.Fatalf("Execute() error = %v", run.err)
		}
		var list output.List
		if err := json.Unmarshal([]byte(run.stdout), &list); err != nil {
			t.Fatalf("-o json did not decode as a List: %v\n%s", err, run.stdout)
		}
		assertCatalogListPaging(t, list, 1, 20, 0, nil)
		var items []map[string]interface{}
		decodeListItems(t, []byte(run.stdout), &items)
		if items == nil || len(items) != 0 {
			t.Errorf("items = %v, want []", items)
		}
		if run.stderr != "" {
			t.Errorf("stderr = %q, want nothing under -o json", run.stderr)
		}
	})
}
