package get

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// setupTestCommandForFiles creates a root command with the global flags the real
// root registers, and adds `get files`.
func setupTestCommandForFiles() *cobra.Command {
	rootCmd := newTestRootCmd()

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewFilesCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func sampleStoredItemTiny(id int, fileName string) stored_items.StoredItemTiny {
	clean := 100
	bad := 0
	return stored_items.StoredItemTiny{
		ID:                  id,
		DatasetID:           11,
		DatasetName:         "orders",
		DataSourceModelID:   22,
		DataSourceModelName: "s3-bucket",
		FileName:            fileName,
		CleanRowsCount:      &clean,
		BadRowsCount:        &bad,
		CreatedAt:           time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC),
	}
}

// runGetFiles wires a mock server that asserts on the request's query string,
// runs `get files` with args, and returns stdout.
func runGetFiles(t *testing.T, assertQuery func(*testing.T, *http.Request), results []stored_items.StoredItemTiny, args ...string) string {
	t.Helper()

	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items", func(w http.ResponseWriter, r *http.Request) {
		assertQuery(t, r)
		testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemsPage{Results: results})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupTestCommandForFiles()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v (stderr: %s)", err, stderr.String())
	}
	return stdout.String()
}

// Unset means "send nothing" so the server's own default (active files) applies.
// Sending is_trashed=false explicitly would be equivalent today, but it would
// also lock qctl to a default the server owns.
func TestGetFilesOmitsIsTrashedWhenDeletedFlagUnset(t *testing.T) {
	out := runGetFiles(t, func(t *testing.T, r *http.Request) {
		if r.URL.Query().Has("is_trashed") {
			t.Errorf("expected no is_trashed parameter when --deleted is unset, got %q", r.URL.Query().Get("is_trashed"))
		}
	}, []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "active.csv")}, "get", "files")

	if !strings.Contains(out, "active.csv") {
		t.Errorf("expected the active file in output, got:\n%s", out)
	}
}

// atlas capi/views/storage_view.py's get_stored_items applies
// `StoredItem.is_trashed == is_trashed`, so --deleted returns deleted
// files ONLY. Without this flag a deleted file's ID is unrecoverable from
// the CLI and `undelete file` cannot be used at all.
func TestGetFilesDeletedFlagSendsIsTrashedTrue(t *testing.T) {
	out := runGetFiles(t, func(t *testing.T, r *http.Request) {
		if got := r.URL.Query().Get("is_trashed"); got != "true" {
			t.Errorf("expected is_trashed=true with --deleted, got %q", got)
		}
	}, []stored_items.StoredItemTiny{sampleStoredItemTiny(7, "deleted.csv")}, "get", "files", "--deleted")

	if !strings.Contains(out, "deleted.csv") {
		t.Errorf("expected the deleted file in output, got:\n%s", out)
	}
}

func TestGetFilesOrderByDeletedLinesCountReachesWire(t *testing.T) {
	runGetFiles(t, func(t *testing.T, r *http.Request) {
		if got := r.URL.Query().Get("order_by"); got != string(api.StoredItemOrderByDeletedLinesCount) {
			t.Errorf("order_by = %q, want %q", got, api.StoredItemOrderByDeletedLinesCount)
		}
	}, []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "a.csv")}, "get", "files", "--order-by", "deleted_lines_count")
}

// The flag's own help must not repeat bug 4's mistake on a new command.
func TestGetFilesDeletedFlagHelpIsExclusive(t *testing.T) {
	flag := NewFilesCommand().Flags().Lookup("deleted")
	if flag == nil {
		t.Fatal("get files has no --deleted flag; deleted files are unlistable and undelete file is unusable without a pre-captured ID")
	}
	usage := flag.Usage
	if strings.Contains(strings.ToLower(usage), "include") {
		t.Errorf("--deleted usage reads as additive: %q", usage)
	}
	if !strings.Contains(strings.ToLower(usage), "only") {
		t.Errorf("--deleted usage should say deleted files only, got: %q", usage)
	}
}

// filesEnv wires every endpoint `get files` can touch and counts calls to each.
// The counters are the point of several tests: "--table-id makes no extra
// request" and "an unresolvable name never reaches /stored-items" are both
// assertions about calls that must NOT happen.
type filesEnv struct {
	t     *testing.T
	mock  *testutil.MockAPIServer
	calls map[string]int

	storedItemsQuery  url.Values
	datasetsQuery     url.Values
	cloudSourcesQuery url.Values
}

func newFilesEnv(t *testing.T) *filesEnv {
	t.Helper()

	env := testutil.NewTestEnv(t)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(func() {
		mock.Close()
		env.Cleanup()
	})

	fe := &filesEnv{t: t, mock: mock, calls: map[string]int{}}

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items", func(w http.ResponseWriter, r *http.Request) {
		fe.calls["stored-items"]++
		fe.storedItemsQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemsPage{
			Results: []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "orders.csv")},
		})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	return fe
}

// withDatasets registers the table list endpoint that --table NAME hits.
func (fe *filesEnv) withDatasets(results ...datasets.DatasetTiny) *filesEnv {
	if results == nil {
		results = []datasets.DatasetTiny{}
	}
	fe.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		fe.calls["datasets"]++
		fe.datasetsQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": results, "total_rows": len(results), "page": 1,
		})
	})
	return fe
}

// withDataset registers the table detail endpoint, which --table-id combined
// with --cloud-source NAME hits to learn the table's real name.
func (fe *filesEnv) withDataset(id int, name string) *filesEnv {
	path := fmt.Sprintf("/api/orgs/%s/datasets/%d", testOrgID, id)
	fe.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		fe.calls["dataset-detail"]++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": id, "name": name})
	})
	return fe
}

// withCloudSources registers the cloud source list endpoint that
// --cloud-source NAME hits.
func (fe *filesEnv) withCloudSources(results ...cloud_sources.CloudSourceTiny) *filesEnv {
	if results == nil {
		results = []cloud_sources.CloudSourceTiny{}
	}
	fe.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		fe.calls["data-sources"]++
		fe.cloudSourcesQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": results, "total_rows": len(results), "page": 1,
		})
	})
	return fe
}

// run executes `get files` with args and returns the command error.
func (fe *filesEnv) run(args ...string) error {
	fe.t.Helper()
	rootCmd := setupTestCommandForFiles()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"get", "files"}, args...))
	return rootCmd.Execute()
}

// atlas capi/views/storage_view.py's get_stored_items has no exact name
// filter — only a fuzzy search_query OR'ed with a substring match on the file
// name — so --table NAME has to become an ID before the listing request goes
// out.
func TestGetFilesTableNameResolvesToDatasetID(t *testing.T) {
	fe := newFilesEnv(t).withDatasets(
		datasets.DatasetTiny{ID: 11, Name: "orders", DestinationName: "postgres_main"},
	)

	if err := fe.run("--table", "orders"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fe.datasetsQuery.Get("name"); got != "orders" {
		t.Errorf("expected the table lookup to filter on name=orders, got %q", got)
	}
	if got := fe.storedItemsQuery.Get("dataset_id"); got != "11" {
		t.Errorf("expected dataset_id=11 on the files request, got %q", got)
	}
}

// The ID path must stay a single request.
func TestGetFilesTableIDSkipsNameResolution(t *testing.T) {
	fe := newFilesEnv(t).withDatasets(datasets.DatasetTiny{ID: 42, Name: "orders"})

	if err := fe.run("--table-id", "42"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fe.calls["datasets"] != 0 {
		t.Errorf("expected no table lookup when the ID is already known, got %d call(s)", fe.calls["datasets"])
	}
	if got := fe.storedItemsQuery.Get("dataset_id"); got != "42" {
		t.Errorf("expected dataset_id=42, got %q", got)
	}
}

// The bug being fixed: an unresolvable name used to return every file in the
// org, which reads as a legitimate answer. It must fail before /stored-items.
func TestGetFilesTableNameNotFound(t *testing.T) {
	fe := newFilesEnv(t).withDatasets()

	err := fe.run("--table", "nope")
	if err == nil {
		t.Fatal("expected an error for a table name that matches nothing")
	}
	for _, want := range []string{"nope", "--table-id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if fe.calls["stored-items"] != 0 {
		t.Errorf("expected /stored-items never to be called, got %d call(s)", fe.calls["stored-items"])
	}
}

// Table names are not unique across destinations, so an exact-name match can
// return two rows. Picking one would filter by the wrong table silently.
func TestGetFilesTableNameAmbiguous(t *testing.T) {
	fe := newFilesEnv(t).withDatasets(
		datasets.DatasetTiny{ID: 11, Name: "dup", DestinationName: "postgres_main"},
		datasets.DatasetTiny{ID: 12, Name: "dup", DestinationName: "snowflake"},
	)

	err := fe.run("--table", "dup")
	if err == nil {
		t.Fatal("expected an error for a table name that matches two destinations")
	}
	for _, want := range []string{"ambiguous", "--table-id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	if fe.calls["stored-items"] != 0 {
		t.Errorf("expected /stored-items never to be called, got %d call(s)", fe.calls["stored-items"])
	}
}

// --deleted and the table filter are independent query parameters; resolving a
// name must not drop the one the user also asked for.
func TestGetFilesDeletedComposesWithTableFilter(t *testing.T) {
	fe := newFilesEnv(t).withDatasets(datasets.DatasetTiny{ID: 11, Name: "orders"})

	if err := fe.run("--deleted", "--table", "orders"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fe.storedItemsQuery.Get("is_trashed"); got != "true" {
		t.Errorf("expected is_trashed=true, got %q", got)
	}
	if got := fe.storedItemsQuery.Get("dataset_id"); got != "11" {
		t.Errorf("expected dataset_id=11 alongside --deleted, got %q", got)
	}
}

// Cloud source names are unique only within a table and /data-sources has no
// name filter, so an org-wide lookup would be a guess. Fail before any request.
func TestGetFilesCloudSourceNameRequiresTable(t *testing.T) {
	fe := newFilesEnv(t).
		withDatasets(datasets.DatasetTiny{ID: 11, Name: "orders"}).
		withCloudSources(cloud_sources.CloudSourceTiny{ID: 22, Name: "s3-bucket", DatasetID: 11})

	err := fe.run("--cloud-source", "s3-bucket")
	if err == nil {
		t.Fatal("expected an error for --cloud-source without a table")
	}
	for _, want := range []string{"--cloud-source requires --table or --table-id", "--cloud-source-id"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
	assertBadUsageExit(t, err)
	for _, endpoint := range []string{"stored-items", "datasets", "data-sources"} {
		if fe.calls[endpoint] != 0 {
			t.Errorf("expected no %s request, got %d", endpoint, fe.calls[endpoint])
		}
	}
}

func TestGetFilesCloudSourceNameResolvesWithinTable(t *testing.T) {
	fe := newFilesEnv(t).
		withDatasets(datasets.DatasetTiny{ID: 11, Name: "orders"}).
		withCloudSources(cloud_sources.CloudSourceTiny{ID: 22, Name: "s3-bucket", DatasetID: 11, DatasetName: "orders"})

	if err := fe.run("--table", "orders", "--cloud-source", "s3-bucket"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fe.cloudSourcesQuery.Get("dataset_id"); got != "11" {
		t.Errorf("expected the cloud source lookup to be scoped to dataset_id=11, got %q", got)
	}
	if got := fe.storedItemsQuery.Get("dataset_id"); got != "11" {
		t.Errorf("expected dataset_id=11 on the files request, got %q", got)
	}
	if got := fe.storedItemsQuery.Get("data_source_model_id"); got != "22" {
		t.Errorf("expected data_source_model_id=22 on the files request, got %q", got)
	}
}

// A dataset-scoped lookup whose errors name the table needs the table's real
// name, so --table-id + --cloud-source NAME fetches the table detail first.
func TestGetFilesCloudSourceNameWithTableID(t *testing.T) {
	fe := newFilesEnv(t).
		withDataset(42, "orders").
		withCloudSources(cloud_sources.CloudSourceTiny{ID: 22, Name: "s3-bucket", DatasetID: 42, DatasetName: "orders"})

	if err := fe.run("--table-id", "42", "--cloud-source", "s3-bucket"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fe.calls["dataset-detail"] != 1 {
		t.Errorf("expected exactly one /datasets/42 fetch for the table name, got %d", fe.calls["dataset-detail"])
	}
	if got := fe.storedItemsQuery.Get("dataset_id"); got != "42" {
		t.Errorf("expected dataset_id=42, got %q", got)
	}
	if got := fe.storedItemsQuery.Get("data_source_model_id"); got != "22" {
		t.Errorf("expected data_source_model_id=22, got %q", got)
	}
}

// Regression fence: this passes before the change too. The new name branch must
// not hijack the ID path, which is valid standalone and needs no table.
func TestGetFilesCloudSourceIDStillWorksAlone(t *testing.T) {
	fe := newFilesEnv(t).withDatasets(datasets.DatasetTiny{ID: 11, Name: "orders"})

	if err := fe.run("--cloud-source-id", "22"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fe.storedItemsQuery.Get("data_source_model_id"); got != "22" {
		t.Errorf("expected data_source_model_id=22, got %q", got)
	}
	if fe.calls["datasets"] != 0 {
		t.Errorf("expected no table lookup, got %d call(s)", fe.calls["datasets"])
	}
	if fe.storedItemsQuery.Has("dataset_id") {
		t.Errorf("expected no dataset_id filter, got %q", fe.storedItemsQuery.Get("dataset_id"))
	}
}

// The flag help must state the constraint the command now enforces.
func TestGetFilesCloudSourceFlagHelpNamesTheTableRequirement(t *testing.T) {
	flag := NewFilesCommand().Flags().Lookup("cloud-source")
	if flag == nil {
		t.Fatal("get files has no --cloud-source flag")
	}
	if !strings.Contains(flag.Usage, "--table") {
		t.Errorf("--cloud-source usage should say it requires a table, got: %q", flag.Usage)
	}
}

// --table-id 0 was indistinguishable from "not passed": the dataset_id
// parameter was dropped and the whole org came back at exit code 0, reading as
// a legitimate answer. This is the ID-flag half of the name-filter fix.
func TestGetFilesRejectsNonPositiveTableID(t *testing.T) {
	for _, value := range []string{"0", "-5"} {
		t.Run(value, func(t *testing.T) {
			fe := newFilesEnv(t)

			err := fe.run("--table-id", value)
			if err == nil {
				t.Fatal("expected an error for a non-positive --table-id")
			}
			if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if fe.calls["stored-items"] != 0 {
				t.Errorf("expected /stored-items never to be called, got %d call(s)", fe.calls["stored-items"])
			}
		})
	}
}

func TestGetFilesRejectsNonPositiveCloudSourceID(t *testing.T) {
	for _, value := range []string{"0", "-1"} {
		t.Run(value, func(t *testing.T) {
			fe := newFilesEnv(t)

			err := fe.run("--cloud-source-id", value)
			if err == nil {
				t.Fatal("expected an error for a non-positive --cloud-source-id")
			}
			if !strings.Contains(err.Error(), "--cloud-source-id must be a positive integer") {
				t.Errorf("error should name the flag and the rule, got: %v", err)
			}
			if fe.calls["stored-items"] != 0 {
				t.Errorf("expected /stored-items never to be called, got %d call(s)", fe.calls["stored-items"])
			}
		})
	}
}

// --cloud-source NAME needs a table, and "--table-id 0" is not one. The ID
// error must win over the requires-a-table error: it names the thing the user
// actually got wrong.
func TestGetFilesZeroTableIDBeatsCloudSourceNameGuard(t *testing.T) {
	fe := newFilesEnv(t).
		withDatasets(datasets.DatasetTiny{ID: 11, Name: "orders"}).
		withCloudSources(cloud_sources.CloudSourceTiny{ID: 22, Name: "s3-bucket", DatasetID: 11})

	err := fe.run("--table-id", "0", "--cloud-source", "s3-bucket")
	if err == nil {
		t.Fatal("expected an error for --table-id 0")
	}
	if !strings.Contains(err.Error(), "--table-id must be a positive integer") {
		t.Errorf("error should name --table-id, got: %v", err)
	}
	for _, endpoint := range []string{"stored-items", "datasets", "data-sources"} {
		if fe.calls[endpoint] != 0 {
			t.Errorf("expected no %s request, got %d", endpoint, fe.calls[endpoint])
		}
	}
}

// --limit -5 used to fall through every `maxResults > 0` guard in
// GetAllStoredItems, behaving exactly like --limit 0 (unlimited): it fetched
// and printed every file in the organization, at exit 0.
func TestGetFilesRejectsNegativeLimit(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []interface{}{}, "total_rows": 0, "page": 1,
		})
	})

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForFiles()
	cmd.SetArgs([]string{"get", "files", "--limit", "-5"})

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error for --limit -5")
	}
	if !strings.Contains(err.Error(), "--limit cannot be negative, got -5") {
		t.Errorf("error should name the flag and the rule, got: %v", err)
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if calls != 0 {
		t.Errorf("expected /stored-items never to be called, got %d call(s)", calls)
	}
}

func TestGetFilesNoVisibleTablesIsAnEmptyResult(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, noTableBody)
		})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/users/me", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"id": testOrgID, "role": "agent"})
	})

	cmd := setupTestCommandForFiles()
	cmd.SetArgs([]string{"get", "files", "--output", "json"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("expected success, got %v (exit %d)", err, qerrors.GetExitCode(err))
	}
	want := "{\n  \"items\": [],\n  \"kind\": \"List\",\n  \"limit\": null,\n  \"next_page\": null,\n  \"page\": null,\n  \"total\": null\n}"
	if got := strings.TrimSpace(stdout.String()); got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
	for _, want := range []string{"role: agent", "table owner or admin", "re-run the same command"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr missing %q: %s", want, stderr.String())
		}
	}
}

// A genuine failure must still fail. GetAllStoredItems also wraps mid-stream
// errors as "partial results", and swallowing those would report a truncated
// listing as complete.
func TestGetFilesRealErrorStillFails(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"detail":{"msg":"boom","error_code":"ERR_INTERNAL"}}`)
		})

	cmd := setupTestCommandForFiles()
	cmd.SetArgs([]string{"get", "files"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a failure")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
	}
}

// GetAllStoredItems returns the rows it already accumulated alongside a
// %w-wrapped error, so a NO_TABLE_MESSAGE 404 on a later page still satisfies
// apierror.IsNoVisibleRows. That must NOT be converted to an empty result: it
// would silently discard the first page's real rows and report a truncated
// listing as complete.
func TestGetFilesMidStreamNoVisibleRowsIsAFailureNotEmpty(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			// First page: real rows, plus a Next cursor so GetAllStoredItems
			// fetches a second page.
			nextStart := 2
			testutil.RespondJSON(w, http.StatusOK, stored_items.StoredItemsPage{
				Results: []stored_items.StoredItemTiny{sampleStoredItemTiny(1, "orders.csv")},
				Next:    &api.PaginationSchema{Start: &nextStart},
			})
			return
		}
		// Second page: the backend now claims the org has no visible tables.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, noTableBody)
	})

	cmd := setupTestCommandForFiles()
	cmd.SetArgs([]string{"get", "files"})

	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected a failure: the first page already returned real rows")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if calls < 2 {
		t.Fatalf("expected at least 2 requests to exercise the mid-stream path, got %d", calls)
	}
}
