package diff

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/slug"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	ltTableID    = 42
	ltRevisionID = "c0ffee00-0000-4000-8000-000000000003"
	ltGroupID    = "c0ffee00-0000-4000-8000-0000000000aa"
)

var (
	ltApplyPath    = "/api/orgs/" + testOrgID + "/composite-datasets/apply"
	ltDatasetsPath = "/api/orgs/" + testOrgID + "/datasets"
	ltLatestPath   = "/api/orgs/" + testOrgID + "/datasets/42/latest-composite-spec-revision"
	ltRevisionPath = "/api/orgs/" + testOrgID + "/composite-spec-revisions/" + ltRevisionID
)

// linkedTableManifest is orders_enriched: orders joined with customers, whose
// arity is zero_or_one.
func linkedTableManifest(kind string) string {
	return `apiVersion: qluster.ai/v1
kind: ` + kind + `
metadata:
  name: orders_enriched
spec:
  destination: warehouse
  table:
    database: analytics
    name: orders_enriched
  spine:
    source: orders
    natural_key_columns: [order_id]
  sources:
    orders:
      dataset: orders
    customers:
      dataset: customers
      arity: zero_or_one
      join_fields:
        customer_id: id
  output:
    order_id: {source: orders, field: order_id}
    customer_name: {source: customers, field: name}
`
}

// liveLinkedTableSpec is the revision block the server stores for
// linkedTableManifest with orders as table 11 and customers as table 12.
func liveLinkedTableSpec(customersArity string) map[string]any {
	return map[string]any{
		"release":     "v3",
		"description": nil,
		"sources": map[string]any{
			"orders": map[string]any{"dataset_id": 11, "arity": nil},
			"customers": map[string]any{
				"dataset_id": 12, "arity": customersArity,
				"join_fields": map[string]any{"customer_id": "id"},
			},
		},
		"output": map[string]any{
			"order_id":      map[string]any{"source": "orders", "field": "order_id"},
			"customer_name": map[string]any{"source": "customers", "field": "name"},
		},
	}
}

func ltIssue(code, msg string, loc ...string) map[string]any {
	return map[string]any{"loc": loc, "msg": msg, "code": code}
}

func ltVerdict(valid bool, errs, warnings []map[string]any) map[string]any {
	if errs == nil {
		errs = []map[string]any{}
	}
	if warnings == nil {
		warnings = []map[string]any{}
	}
	return map[string]any{"valid": valid, "errors": errs, "warnings": warnings}
}

// registerLinkedTableValidate answers the apply route with verdict and
// returns the query string of every request it received.
func registerLinkedTableValidate(mock *testutil.MockAPIServer, verdict map[string]any) *[]string {
	var queries []string
	mock.RegisterHandler(http.MethodPost, ltApplyPath, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		testutil.RespondJSON(w, http.StatusOK, verdict)
	})
	return &queries
}

// registerTablesByName lists the table named by the name filter when tables
// has it, and nothing otherwise. It returns the name filter of every request.
func registerTablesByName(mock *testutil.MockAPIServer, tables map[string]int) *[]string {
	var names []string
	mock.RegisterHandler(http.MethodGet, ltDatasetsPath, func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		if !r.URL.Query().Has("name") {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}, "total_rows": 0})
			return
		}
		names = append(names, name)
		results := []map[string]any{}
		if id, ok := tables[name]; ok {
			results = append(results, map[string]any{
				"id": id, "name": name, "organization_id": testOrgID,
				"state": "active", "users": []any{}, "version_id": 1,
			})
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results": results, "page": 1, "total_rows": len(results),
		})
	})
	return &names
}

// registerLatestRevision serves tableID's latest revision, enabled at v3 with spec.
func registerLatestRevision(mock *testutil.MockAPIServer, tableID string, spec map[string]any) {
	groupName := "orders_enriched"
	if tableID == "77" {
		groupName = "2024"
	}
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets/"+tableID+"/latest-composite-spec-revision",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, map[string]any{
				"composite_spec_revision_id": ltRevisionID,
				"composite_spec_group_id":    ltGroupID,
				"state":                      "enabled",
				"release":                    "v3",
				"description":                nil,
				"spec_sha256":                "ab12",
				"created_at":                 "2026-09-22T10:00:00Z",
				"updated_at":                 "2026-09-22T10:00:00Z",
			})
		})
	mock.RegisterHandler(http.MethodGet, ltRevisionPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_revision_id": ltRevisionID,
			"composite_spec_group_id":    ltGroupID,
			"state":                      "enabled",
			"spec_sha256":                "ab12",
			"created_at":                 "2026-09-20T10:00:00Z",
			"created_by_user_id":         nil,
			"is_rerunnable":              true,
			"revision":                   spec,
			"group": map[string]any{
				"name": groupName, "slug": slug.Generate(groupName, 63), "spine_dataset_id": 11,
				"identity_hash_version": 1, "natural_key_columns": []string{"order_id"},
				"natural_key_normalization": map[string]any{},
			},
		})
	})
}

// linkedTableServer serves orders_enriched (42), orders (11) and customers
// (12), with table 42's latest revision enabled and holding live.
func linkedTableServer(mock *testutil.MockAPIServer, live map[string]any) *[]string {
	names := registerTablesByName(mock, map[string]int{"orders_enriched": ltTableID, "orders": 11, "customers": 12})
	registerLatestRevision(mock, "42", live)
	return names
}

// changeLines returns the lines stdout prints before its first blank line.
func changeLines(stdout string) []string {
	block, _, _ := strings.Cut(stdout, "\n\n")
	return strings.Split(block, "\n")
}

func requestBody(t *testing.T, stdout string) map[string]any {
	t.Helper()
	_, raw, ok := strings.Cut(stdout, "\nrequest body:\n")
	if !ok {
		t.Fatalf("diff printed no request body:\n%s", stdout)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &body); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, raw)
	}
	return body
}

func TestDiffLinkedTableInvalidManifestExitsTwoWithWarningsOnStderr(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	queries := registerLinkedTableValidate(mock, ltVerdict(false,
		[]map[string]any{ltIssue("unresolved_reference", "dataset 'customers' not found in this organization", "spec", "sources", "customers", "dataset")},
		[]map[string]any{ltIssue("empty_source", "source orders has no rows", "revision", "sources", "orders")},
	))
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable"))

	stdout, stderr, err := execDiff(t, path)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", got, err, qerrors.ExitBadUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if want := "Warning: revision.sources.orders: source orders has no rows (empty_source)\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"POST " + ltApplyPath}) {
		t.Errorf("requests = %#v, want only the validate call", got)
	}
	if !reflect.DeepEqual(*queries, []string{"validate_only=true"}) {
		t.Errorf("apply queries = %#v, want validate_only=true", *queries)
	}
}

// A join constant YAML cannot decode as the integer written fails the load,
// before the validate_only check is sent.
func TestDiffLinkedTableRefusesAConstantItCannotSendAsWritten(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	path := env.CreateFile("linked-table.yaml", strings.Replace(linkedTableManifest("LinkedTable"), "        customer_id: id\n",
		"        customer_id: id\n        region: {source_fields: [region_code], constant: 18446744073709551616}\n", 1))

	stdout, _, err := execDiff(t, path)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", got, err, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "spec.sources.customers.join_fields.region.constant") {
		t.Fatalf("error does not name the join's constant: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if got := mock.Requests(); len(got) != 0 {
		t.Errorf("requests = %#v, want none", got)
	}
}

func TestDiffLinkedTableCreateListsEverySpecFieldAsAnAddition(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	queries := registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
	names := registerTablesByName(mock, map[string]int{"orders": 11, "customers": 12})
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable")+`  description: joined orders
  dry_run: {max_spine_rows: 50}
`)

	stdout, stderr, err := execDiff(t, path)
	if !errors.Is(err, qerrors.ErrDiffDifferences) || qerrors.GetExitCode(err) != 1 {
		t.Fatalf("err = %v (exit %d), want ErrDiffDifferences (exit 1)", err, qerrors.GetExitCode(err))
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	want := []string{
		`spec.destination: <absent> → "warehouse"`,
		`spec.table.database: <absent> → "analytics"`,
		`spec.table.name: <absent> → "orders_enriched"`,
		`spec.spine.source: <absent> → "orders"`,
		`spec.spine.natural_key_columns: <absent> → ["order_id"]`,
		`spec.sources.customers.dataset: <absent> → "customers"`,
		`spec.sources.customers.arity: <absent> → "zero_or_one"`,
		`spec.sources.customers.join_fields.customer_id: <absent> → "id"`,
		`spec.sources.orders.dataset: <absent> → "orders"`,
		`spec.output.customer_name: <absent> → {"source":"customers","field":"name"}`,
		`spec.output.order_id: <absent> → {"source":"orders","field":"order_id"}`,
		`spec.description: <absent> → "joined orders"`,
	}
	if got := changeLines(stdout); !reflect.DeepEqual(got, want) {
		t.Errorf("change lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	body := requestBody(t, stdout)
	if body["kind"] != "CompositeDataset" {
		t.Errorf("request body kind = %v, want CompositeDataset", body["kind"])
	}
	spec, _ := body["spec"].(map[string]any)
	if dryRun, _ := spec["dry_run"].(map[string]any); dryRun["max_spine_rows"] != float64(50) {
		t.Errorf("request body spec.dry_run = %#v, want the manifest's", spec["dry_run"])
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, []string{"POST " + ltApplyPath, "GET " + ltDatasetsPath, "GET " + ltDatasetsPath}) {
		t.Errorf("requests = %#v, want validation, a name lookup and the table scan", got)
	}
	if !reflect.DeepEqual(*names, []string{"orders_enriched"}) {
		t.Errorf("name lookups = %#v, want only metadata.name", *names)
	}
	if !reflect.DeepEqual(*queries, []string{"validate_only=true"}) {
		t.Errorf("apply queries = %#v, want validate_only=true", *queries)
	}
}

func TestDiffLinkedTableUpdateComparesWithTheLatestRevision(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	queries := registerLinkedTableValidate(mock, ltVerdict(true, nil,
		[]map[string]any{ltIssue("empty_source", "source orders has no rows", "revision", "sources", "orders")}))
	names := linkedTableServer(mock, liveLinkedTableSpec("exactly_one"))
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable"))

	stdout, stderr, err := execDiff(t, path)
	if !errors.Is(err, qerrors.ErrDiffDifferences) || qerrors.GetExitCode(err) != 1 {
		t.Fatalf("err = %v (exit %d), want ErrDiffDifferences (exit 1)", err, qerrors.GetExitCode(err))
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty on exit 1", stderr)
	}
	wantPrefix := "linked-table/orders_enriched: compared with its latest revision, v3 (enabled)\n" +
		`spec.sources.customers.arity: "exactly_one" → "zero_or_one"` + "\n" +
		"\n" +
		"Warning: revision.sources.orders: source orders has no rows (empty_source)\n" +
		"\nrequest body:\n"
	if !strings.HasPrefix(stdout, wantPrefix) {
		t.Errorf("stdout =\n%s\nwant it to start with\n%s", stdout, wantPrefix)
	}
	if body := requestBody(t, stdout); body["kind"] != "CompositeDataset" {
		t.Errorf("request body kind = %v, want CompositeDataset", body["kind"])
	}

	wantRequests := []string{
		"POST " + ltApplyPath,
		"GET " + ltDatasetsPath,
		"GET " + ltLatestPath,
		"GET " + ltRevisionPath,
		"GET " + ltDatasetsPath,
		"GET " + ltDatasetsPath,
	}
	if got := mock.Requests(); !reflect.DeepEqual(got, wantRequests) {
		t.Errorf("requests = %#v, want %#v", got, wantRequests)
	}
	if !reflect.DeepEqual(*queries, []string{"validate_only=true"}) {
		t.Errorf("apply queries = %#v; the only POST must be validate_only=true", *queries)
	}
	if !reflect.DeepEqual(*names, []string{"orders_enriched", "customers", "orders"}) {
		t.Errorf("name lookups = %#v", *names)
	}
}

func TestDiffLinkedTableNoDifferencesIsSilent(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerLinkedTableValidate(mock, ltVerdict(true, nil,
		[]map[string]any{ltIssue("empty_source", "source orders has no rows", "revision", "sources", "orders")}))
	linkedTableServer(mock, liveLinkedTableSpec("zero_or_one"))
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable"))

	stdout, stderr, err := execDiff(t, path)
	if err != nil {
		t.Fatalf("unchanged linked table diff: %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	if stdout != "" || stderr != "" {
		t.Errorf("unchanged diff must be silent, warnings included; stdout=%q stderr=%q", stdout, stderr)
	}
}

func TestDiffCompositeDatasetKindDispatchesToLinkedTable(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	queries := registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
	linkedTableServer(mock, liveLinkedTableSpec("exactly_one"))
	path := env.CreateFile("composite.yaml", linkedTableManifest("CompositeDataset"))

	stdout, _, err := execDiff(t, path)
	if !errors.Is(err, qerrors.ErrDiffDifferences) {
		t.Fatalf("err = %v (exit %d), want ErrDiffDifferences", err, qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, `spec.sources.customers.arity: "exactly_one" → "zero_or_one"`) {
		t.Errorf("diff is missing the arity change:\n%s", stdout)
	}
	if len(*queries) != 1 {
		t.Errorf("validate calls = %d, want 1", len(*queries))
	}
}

func TestDiffLinkedTableLooksUpAllDigitNamesByName(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
	names := registerTablesByName(mock, map[string]int{"2024": 77, "2025": 88})
	registerLatestRevision(mock, "77", map[string]any{
		"release":     "v3",
		"description": nil,
		"sources":     map[string]any{"orders": map[string]any{"dataset_id": 88, "arity": nil}},
		"output":      map[string]any{"order_id": map[string]any{"source": "orders", "field": "order_id"}},
	})
	path := env.CreateFile("linked-table.yaml", `apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: "2024"
spec:
  destination: warehouse
  table:
    database: analytics
    name: orders_2024
  spine:
    source: orders
    natural_key_columns: [order_id]
  sources:
    orders:
      dataset: "2025"
  output:
    order_id: {source: orders, field: order_id}
`)

	stdout, stderr, err := execDiff(t, path)
	if err != nil {
		t.Fatalf("diff: %v (exit %d)\nstdout:\n%s", err, qerrors.GetExitCode(err), stdout)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("unchanged diff must be silent; stdout=%q stderr=%q", stdout, stderr)
	}
	if !reflect.DeepEqual(*names, []string{"2024", "2025"}) {
		t.Errorf("name lookups = %#v, want both all-digit names looked up by name", *names)
	}
	for _, request := range mock.Requests() {
		if strings.Contains(request, "/datasets/2024") || strings.Contains(request, "/datasets/2025") {
			t.Errorf("request %q used an all-digit name as a table id", request)
		}
	}
}

func TestDiffLinkedTableSkipsAPrunedOutputColumn(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	pruned := ltIssue("deleted_field_pruned", "output column 'customer_name' reads deleted field 'name'",
		"revision", "output", "customer_name", "field")
	registerLinkedTableValidate(mock, ltVerdict(true, nil, []map[string]any{pruned}))
	live := liveLinkedTableSpec("exactly_one")
	delete(live["output"].(map[string]any), "customer_name")
	linkedTableServer(mock, live)
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable"))

	stdout, stderr, err := execDiff(t, path)
	if !errors.Is(err, qerrors.ErrDiffDifferences) {
		t.Fatalf("err = %v (exit %d), want ErrDiffDifferences", err, qerrors.GetExitCode(err))
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
	want := []string{
		"linked-table/orders_enriched: compared with its latest revision, v3 (enabled)",
		`spec.sources.customers.arity: "exactly_one" → "zero_or_one"`,
	}
	if got := changeLines(stdout); !reflect.DeepEqual(got, want) {
		t.Errorf("change lines =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !strings.Contains(stdout, "Warning: revision.output.customer_name.field: ") {
		t.Errorf("diff does not print the prune warning:\n%s", stdout)
	}
}

func TestDiffLinkedTableNamesALiveSourceTheManifestDropped(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		wantLine string
		wantExit int
	}{
		{"read", http.StatusOK, `spec.sources.customers.dataset: "customers" → <absent>`, 1},
		{"not found prints the id", http.StatusNotFound, `spec.sources.customers.dataset: 12 → <absent>`, 1},
		{"forbidden fails", http.StatusForbidden, "", qerrors.ExitUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
			linkedTableServer(mock, liveLinkedTableSpec("zero_or_one"))
			mock.RegisterHandler(http.MethodGet, ltDatasetsPath+"/12", func(w http.ResponseWriter, _ *http.Request) {
				if tc.status != http.StatusOK {
					testutil.RespondJSON(w, tc.status, map[string]any{"detail": "no"})
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": 12, "name": "customers"})
			})
			path := env.CreateFile("linked-table.yaml", `apiVersion: qluster.ai/v1
kind: LinkedTable
metadata:
  name: orders_enriched
spec:
  destination: warehouse
  table:
    database: analytics
    name: orders_enriched
  spine:
    source: orders
    natural_key_columns: [order_id]
  sources:
    orders:
      dataset: orders
  output:
    order_id: {source: orders, field: order_id}
`)

			stdout, _, err := execDiff(t, path)
			if got := qerrors.GetExitCode(err); got != tc.wantExit {
				t.Fatalf("exit code = %d (err %v), want %d", got, err, tc.wantExit)
			}
			if tc.wantLine != "" && !strings.Contains(stdout, tc.wantLine+"\n") {
				t.Errorf("diff is missing %q:\n%s", tc.wantLine, stdout)
			}
		})
	}
}

func TestDiffLinkedTableUnknownSourceIsNotFound(t *testing.T) {
	env, mock := newDiffTestEnv(t)
	registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
	registerTablesByName(mock, map[string]int{"orders_enriched": ltTableID, "orders": 11})
	registerLatestRevision(mock, "42", liveLinkedTableSpec("zero_or_one"))
	path := env.CreateFile("linked-table.yaml", linkedTableManifest("LinkedTable"))

	stdout, _, err := execDiff(t, path)
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (err %v), want %d", got, err, qerrors.ExitNotFound)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
}

func TestDiffLinkedTableSeriesExportFindsItsGroupAcrossPages(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			queries := registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
			registerLatestRevision(mock, "42", liveLinkedTableSpec("zero_or_one"))
			var detail linked_tables.RevisionDetail
			raw, err := json.Marshal(map[string]any{
				"composite_spec_revision_id": ltRevisionID, "composite_spec_group_id": ltGroupID,
				"state": "enabled", "revision": liveLinkedTableSpec("zero_or_one"),
				"group": map[string]any{
					"name": "orders_enriched", "slug": "orders_enriched", "spine_dataset_id": 11,
					"identity_hash_version": 1, "natural_key_columns": []string{"order_id"},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &detail); err != nil {
				t.Fatal(err)
			}
			manifest, err := linked_tables.ExportManifest(linked_tables.LiveTable{
				ID: 42, Name: "Orders · Nov 2026", Destination: "warehouse", Database: "analytics", TableName: "orders_enriched",
			}, &detail, map[int]string{11: "orders", 12: "customers"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Metadata.Name != "orders_enriched" {
				t.Fatalf("export metadata.name = %q", manifest.Metadata.Name)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if format == "yaml" {
				var buf bytes.Buffer
				if err := jsonnum.NewYAMLEncoder(&buf).Encode(manifest); err != nil {
					t.Fatal(err)
				}
				data = buf.Bytes()
			}
			var pages []string
			mock.RegisterHandler(http.MethodGet, ltDatasetsPath, func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				results := []map[string]any{}
				if q.Has("name") {
					id := map[string]int{"orders_enriched": 99, "orders": 11, "customers": 12}[q.Get("name")]
					results = append(results, map[string]any{"id": id, "name": q.Get("name")})
				} else {
					pages = append(pages, q.Get("page"))
					if q.Get("order_by") != "id" || q.Get("limit") != "100" {
						t.Errorf("scan query = %s", r.URL.RawQuery)
					}
					if q.Get("page") == "1" {
						results = append(results, map[string]any{"id": 99, "name": "orders_enriched"}, map[string]any{"id": 98, "name": "plain table"})
					} else {
						results = append(results, map[string]any{"id": 42, "name": "Orders · Nov 2026"})
					}
				}
				// No total: the next link must keep the scan going.
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "next": map[string]any{"page": 2}})
			})
			otherRevision := "c0ffee00-0000-4000-8000-000000000099"
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets/99/latest-composite-spec-revision", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"composite_spec_revision_id": otherRevision})
			})
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/composite-spec-revisions/"+otherRevision, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{
					"composite_spec_revision_id": otherRevision, "revision": liveLinkedTableSpec("exactly_one"),
					"group": map[string]any{"name": "other", "slug": "other"},
				})
			})
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/datasets/98/latest-composite-spec-revision", func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "not a linked table"})
			})
			path := env.CreateFile("export."+format, string(data))
			stdout, stderr, err := execDiff(t, path)
			if err != nil || stdout != "" || stderr != "" {
				t.Fatalf("diff export: err=%v (exit %d), stdout=%q stderr=%q", err, qerrors.GetExitCode(err), stdout, stderr)
			}
			if !reflect.DeepEqual(pages, []string{"1", "2"}) {
				t.Errorf("scan pages = %v", pages)
			}
			if !reflect.DeepEqual(*queries, []string{"validate_only=true"}) {
				t.Errorf("apply queries = %v", *queries)
			}
		})
	}
}

func TestDiffLinkedTableSlugScanReadErrorsKeepExitCode(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			registerLinkedTableValidate(mock, ltVerdict(true, nil, nil))
			mock.RegisterHandler(http.MethodGet, ltDatasetsPath, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Has("name") {
					testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}})
					return
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []map[string]any{{"id": 42}}, "total_rows": 1})
			})
			mock.RegisterHandler(http.MethodGet, ltLatestPath, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, status, map[string]any{"detail": "read failed"})
			})
			stdout, _, err := execDiff(t, env.CreateFile("linked.yaml", linkedTableManifest("LinkedTable")))
			want := qerrors.ExitGenericError
			if status == http.StatusForbidden {
				want = qerrors.ExitUnauthorized
			}
			if qerrors.GetExitCode(err) != want || stdout != "" {
				t.Fatalf("err=%v (exit %d), want %d; stdout=%q", err, qerrors.GetExitCode(err), want, stdout)
			}
		})
	}
}
