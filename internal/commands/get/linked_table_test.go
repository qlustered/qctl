package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

const (
	ltRevision      = "5d0c2a4e-8b1f-4e3a-a6c9-7f2d1b0e4c33"
	ltOlderRevision = "7a1e9c02-3b4d-4f5e-8a6b-9c0d1e2f3a4b"
	ltGroup         = "0b6f4a36-2f7e-4c61-9d7a-3c1f1e2a9b10"
)

var (
	ltOrgPath            = "/api/orgs/" + testOrgID
	ltBackupSettingsPath = ltOrgPath + "/backup-settings/4"
)

func ltRevisionPath(rev string) string {
	return ltOrgPath + "/composite-spec-revisions/" + rev
}

func ltDryRunPath(id int) string {
	return ltOrgPath + "/composite-dry-runs/" + strconv.Itoa(id)
}

func ltDryRunDetail(id int, state string, autoEnable any) map[string]any {
	return map[string]any{
		"id": id, "composite_spec_revision_id": ltOlderRevision, "composite_spec_group_id": ltGroup,
		"downstream_dataset_id": 12, "organization_id": testOrgID, "state": state,
		"max_spine_rows": 1000, "created_at": "2026-09-22T10:00:00Z",
		"auto_enable_state": autoEnable, "structured_summary": nil,
	}
}

// ltDataset is table id as GET /datasets/{id} sends it.
func ltDataset(id int, name string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "organization_id": testOrgID, "state": "active", "version_id": 1,
		"database_name": "analytics", "schema_name": "public", "table_name": name,
		"destination_id": 3, "destination_name": "warehouse", "backup_settings_id": 4,
	}
}

// ltRevisionDetail is revision rev of table 12 in state: orders (11, the
// spine) joined to customers (13).
func ltRevisionDetail(rev, release, state string) map[string]any {
	return map[string]any{
		"composite_spec_revision_id": rev,
		"composite_spec_group_id":    ltGroup,
		"state":                      state,
		"spec_sha256":                "ab12",
		"created_at":                 "2026-09-20T10:00:00Z",
		"created_by_user_id":         nil,
		"is_rerunnable":              true,
		"revision": map[string]any{
			"release": release, "description": nil,
			"sources": map[string]any{
				"orders": map[string]any{"dataset_id": 11, "join_fields": map[string]any{}},
				"customers": map[string]any{"dataset_id": 13, "arity": "zero_or_one", "join_fields": map[string]any{
					"customer_id": "customer_id",
					"tier":        map[string]any{"spine_fields": []string{}, "source_fields": []string{"tier"}, "constant": []any{1, 2.5}},
				}},
			},
			"output": map[string]any{
				"order_id":  map[string]any{"source": "orders", "field": "order_id"},
				"cust_name": map[string]any{"source": "customers", "field": "name"},
			},
		},
		"group": map[string]any{
			"name": "orders_enriched", "slug": "orders-enriched", "spine_dataset_id": 11,
			"natural_key_columns": []string{"order_id"}, "natural_key_normalization": map[string]any{},
			"identity_hash_version": 1, "replaced_group_id": nil, "is_retired": false,
		},
	}
}

// linkedTableGetEnv serves linked table 12 (orders_enriched), whose latest
// revision is v3, enabled; its sources 11 and 13; and backup settings 4,
// answered with backupStatus.
func linkedTableGetEnv(t *testing.T, backupStatus int) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	mock.RegisterHandler(http.MethodGet, ltOrgPath+"/datasets/12/latest-composite-spec-revision", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"composite_spec_revision_id": ltRevision, "composite_spec_group_id": ltGroup,
			"state": "enabled", "release": "v3", "description": nil, "spec_sha256": "ab12",
			"created_at": "2026-09-22T10:00:00Z", "updated_at": "2026-09-22T10:00:00Z",
		})
	})
	mock.RegisterHandler(http.MethodGet, ltRevisionPath(ltRevision), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ltRevisionDetail(ltRevision, "v3", "enabled"))
	})
	for id, name := range map[int]string{12: "orders_enriched", 11: "orders", 13: "customers"} {
		dataset := ltDataset(id, name)
		mock.RegisterHandler(http.MethodGet, ltOrgPath+"/datasets/"+strconv.Itoa(id), func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, dataset)
		})
	}
	mock.RegisterHandler(http.MethodGet, ltBackupSettingsPath, func(w http.ResponseWriter, _ *http.Request) {
		if backupStatus != http.StatusOK {
			testutil.RespondJSON(w, backupStatus, map[string]any{"detail": map[string]any{"msg": "backup:list required"}})
			return
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"id": 4, "name": "nightly", "version_id": 1})
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return mock
}

func execGetLinkedTable(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := newTestRootCmd()
	root.PersistentFlags().String("columns", "", "")
	getCmd := NewCommand()
	root.AddCommand(getCmd)
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "linked-table"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func loadExport(t *testing.T, data string) *linked_tables.LinkedTableManifest {
	t.Helper()
	m, err := linked_tables.LoadManifest([]byte(data))
	if err != nil {
		t.Fatalf("output is not a manifest apply accepts: %v\n%s", err, data)
	}
	return m
}

func TestGetLinkedTableYAMLIsAManifestApplyAccepts(t *testing.T) {
	mock := linkedTableGetEnv(t, http.StatusOK)

	stdout, stderr, err := execGetLinkedTable(t, "12", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
	m := loadExport(t, stdout)
	if m.Metadata.Name != "orders_enriched" || m.Spec.Destination != "warehouse" ||
		m.Spec.BackupSettings == nil || *m.Spec.BackupSettings != "nightly" {
		t.Errorf("metadata/destination/backup_settings = %q %q %v", m.Metadata.Name, m.Spec.Destination, m.Spec.BackupSettings)
	}
	if m.Spec.Spine.Source != "orders" || m.Spec.Sources["orders"].Dataset != "orders" || m.Spec.Sources["customers"].Dataset != "customers" {
		t.Errorf("spine/sources = %q %+v", m.Spec.Spine.Source, m.Spec.Sources)
	}
	if m.Spec.Release != nil || m.Spec.DesiredState == nil || *m.Spec.DesiredState != "enabled" {
		t.Errorf("release/desired_state = %v %v", m.Spec.Release, m.Spec.DesiredState)
	}
	for _, want := range []string{"status:\n", "  slug: orders-enriched\n", "  release: v3\n", "  revision_id: " + ltRevision + "\n", "            - 2.5\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
	for _, request := range mock.Requests() {
		if !strings.HasPrefix(request, "GET ") {
			t.Errorf("sent %s; get must only read", request)
		}
	}
}

func TestGetLinkedTableJSONIsAManifestApplyAccepts(t *testing.T) {
	linkedTableGetEnv(t, http.StatusOK)

	stdout, _, err := execGetLinkedTable(t, "12", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("output is not JSON:\n%s", stdout)
	}
	if m := loadExport(t, stdout); m.Spec.Sources["customers"].JoinFields["customer_id"].Shorthand != "customer_id" {
		t.Errorf("join_fields.customer_id = %+v", m.Spec.Sources["customers"].JoinFields["customer_id"])
	}
	if !strings.Contains(stdout, `"constant": [`) {
		t.Errorf("clause constant is not a JSON array:\n%s", stdout)
	}
}

// The agent role can apply a linked table but may not read backup settings.
func TestGetLinkedTableForbiddenBackupSettingsAreLeftOutWithAWarning(t *testing.T) {
	linkedTableGetEnv(t, http.StatusForbidden)

	stdout, stderr, err := execGetLinkedTable(t, "12", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m := loadExport(t, stdout); m.Spec.BackupSettings != nil {
		t.Errorf("spec.backup_settings = %q, want it left out", *m.Spec.BackupSettings)
	}
	if !strings.Contains(stdout, "  backup_settings_id: 4\n") {
		t.Errorf("status lacks backup_settings_id:\n%s", stdout)
	}
	if !strings.HasPrefix(stderr, "Warning: spec.backup_settings is left out") || !strings.Contains(stderr, "refused; it needs backup:list") {
		t.Errorf("stderr = %q, want the backup settings warning", stderr)
	}
}

// Only the manifest names the backup settings, so no other view reads them.
func TestGetLinkedTableSummaryDoesNotReadBackupSettings(t *testing.T) {
	for _, format := range []string{"table", "name"} {
		mock := linkedTableGetEnv(t, http.StatusForbidden)

		_, stderr, err := execGetLinkedTable(t, "12", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		if stderr != "" {
			t.Errorf("-o %s: stderr = %q, want nothing", format, stderr)
		}
		for _, request := range mock.Requests() {
			if request == "GET "+ltBackupSettingsPath {
				t.Errorf("-o %s read the backup settings", format)
			}
		}
	}
}

func TestGetLinkedTableOtherBackupSettingsErrorsFail(t *testing.T) {
	linkedTableGetEnv(t, http.StatusNotFound)

	_, _, err := execGetLinkedTable(t, "12", "-o", "yaml")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code %d (err %v), want %d", code, err, qerrors.ExitNotFound)
	}
}

func TestGetLinkedTableDefaultIsOneRow(t *testing.T) {
	linkedTableGetEnv(t, http.StatusOK)

	stdout, _, err := execGetLinkedTable(t, "12")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want a header and one row, got:\n%s", stdout)
	}
	for _, want := range []string{"NAME", "TABLE-ID", "RELEASE", "STATE", "SPINE", "SOURCES", "OUTPUT-COLUMNS", "DESTINATION"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("header lacks %s: %q", want, lines[0])
		}
	}
	if got := strings.Fields(lines[1]); strings.Join(got, " ") != "orders_enriched 12 v3 enabled orders 2 2 warehouse" {
		t.Errorf("row = %q", lines[1])
	}
}

func TestGetLinkedTableName(t *testing.T) {
	linkedTableGetEnv(t, http.StatusOK)

	stdout, _, err := execGetLinkedTable(t, "12", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "linked-table/12\n" {
		t.Errorf("stdout = %q", stdout)
	}
}

func TestGetLinkedTableRevisionFlagExportsThatRevision(t *testing.T) {
	mock := linkedTableGetEnv(t, http.StatusOK)
	mock.RegisterHandler(http.MethodGet, ltRevisionPath(ltOlderRevision), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ltRevisionDetail(ltOlderRevision, "v2", "disabled"))
	})
	mock.RegisterHandler(http.MethodGet, ltOrgPath+"/composite-dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{}})
	})

	stdout, _, err := execGetLinkedTable(t, "--table-id", "12", "--revision", ltOlderRevision, "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m := loadExport(t, stdout); *m.Spec.DesiredState != "draft" {
		t.Errorf("desired_state = %s, want draft for a disabled revision", *m.Spec.DesiredState)
	}
	for _, want := range []string{"  revision_id: " + ltOlderRevision + "\n", "  release: v2\n", "  state: disabled\n"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q:\n%s", want, stdout)
		}
	}
}

func TestGetLinkedTableExportPreservesPendingAutoEnable(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			mock := linkedTableGetEnv(t, http.StatusOK)
			mock.RegisterHandler(http.MethodGet, ltRevisionPath(ltOlderRevision), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, ltRevisionDetail(ltOlderRevision, "v2", "draft"))
			})
			mock.RegisterHandler(http.MethodGet, ltOrgPath+"/composite-dry-runs", func(w http.ResponseWriter, r *http.Request) {
				query := r.URL.Query()
				if query.Get("revision_id") != ltOlderRevision || query.Get("limit") != "100" ||
					strings.Join(query["state"], ",") != "created,running" {
					t.Errorf("list query = %v", query)
				}
				page, _ := strconv.Atoi(query.Get("page"))
				if page < 1 || page > 3 {
					t.Errorf("unexpected page %d", page)
				}
				rows := []any{}
				for id := (page-1)*100 + 1; id <= min(page*100, 205); id++ {
					rows = append(rows, map[string]any{"id": id})
				}
				response := map[string]any{"results": rows}
				if page == 1 {
					response["total_rows"] = 205
					response["next"] = map[string]any{"start": 100}
				}
				testutil.RespondJSON(w, http.StatusOK, response)
			})
			for id := 1; id <= 205; id++ {
				mock.RegisterHandler(http.MethodGet, ltDryRunPath(id), func(w http.ResponseWriter, _ *http.Request) {
					var autoEnable any
					if id == 205 {
						autoEnable = "pending"
					}
					testutil.RespondJSON(w, http.StatusOK, ltDryRunDetail(id, "running", autoEnable))
				})
			}

			stdout, stderr, err := execGetLinkedTable(t, "12", "--revision", ltOlderRevision, "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			m := loadExport(t, stdout)
			var withStatus linked_tables.LinkedTableManifestWithStatus
			if err := yaml.Unmarshal([]byte(stdout), &withStatus); err != nil {
				t.Fatal(err)
			}
			if *m.Spec.DesiredState != "enabled" || withStatus.Status.State != "draft" {
				t.Errorf("desired_state/status.state = %s/%s, want enabled/draft", *m.Spec.DesiredState, withStatus.Status.State)
			}
			if stderr != "" {
				t.Errorf("stderr = %q", stderr)
			}
		})
	}
}

func TestGetLinkedTableAutoEnableScanStops(t *testing.T) {
	for _, tc := range []struct {
		name     string
		empty    bool
		status   int
		wantExit int
	}{
		{name: "no pending intent"},
		{name: "empty page", empty: true},
		{name: "later page forbidden", status: http.StatusForbidden, wantExit: qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := linkedTableGetEnv(t, http.StatusOK)
			mock.RegisterHandler(http.MethodGet, ltRevisionPath(ltOlderRevision), func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, ltRevisionDetail(ltOlderRevision, "v2", "draft"))
			})
			pages := 0
			mock.RegisterHandler(http.MethodGet, ltOrgPath+"/composite-dry-runs", func(w http.ResponseWriter, r *http.Request) {
				pages++
				if r.URL.Query().Get("page") != strconv.Itoa(pages) || pages > 2 {
					t.Errorf("unexpected query %v at request %d", r.URL.Query(), pages)
				}
				if pages > 1 && tc.status != 0 {
					testutil.RespondError(w, tc.status, "denied")
					return
				}
				rows := []any{}
				if pages == 1 || !tc.empty {
					rows = append(rows, map[string]any{"id": 5512 + pages})
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": rows, "total_rows": 2})
			})
			for id := 5513; id <= 5514; id++ {
				mock.RegisterHandler(http.MethodGet, ltDryRunPath(id), func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, ltDryRunDetail(id, "running", nil))
				})
			}
			stdout, _, err := execGetLinkedTable(t, "12", "--revision", ltOlderRevision, "-o", "yaml")
			if code := qerrors.GetExitCode(err); code != tc.wantExit {
				t.Fatalf("exit = %d, want %d: %v", code, tc.wantExit, err)
			}
			if pages != 2 {
				t.Errorf("pages = %d, want 2", pages)
			}
			if err == nil {
				if m := loadExport(t, stdout); *m.Spec.DesiredState != "draft" {
					t.Errorf("desired_state = %s, want draft", *m.Spec.DesiredState)
				}
			}
		})
	}
}

func TestGetLinkedTableDryRunReadErrorKeepsExitCode(t *testing.T) {
	mock := linkedTableGetEnv(t, http.StatusOK)
	mock.RegisterHandler(http.MethodGet, ltRevisionPath(ltOlderRevision), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, ltRevisionDetail(ltOlderRevision, "v2", "draft"))
	})
	mock.RegisterHandler(http.MethodGet, ltOrgPath+"/composite-dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": []any{map[string]any{"id": 5512}}})
	})
	mock.RegisterHandler(http.MethodGet, ltDryRunPath(5512), func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "forbidden"})
	})

	_, _, err := execGetLinkedTable(t, "12", "--revision", ltOlderRevision, "-o", "yaml")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code %d (err %v), want %d", code, err, qerrors.ExitUnauthorized)
	}
}

func TestGetLinkedTableNotALinkedTableIsNotFound(t *testing.T) {
	mock := linkedTableGetEnv(t, http.StatusOK)
	mock.RegisterHandler(http.MethodGet, ltOrgPath+"/datasets/12/latest-composite-spec-revision", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "latest composite spec revision for dataset 12 can not be found."})
	})

	_, _, err := execGetLinkedTable(t, "12")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code %d (err %v), want %d", code, err, qerrors.ExitNotFound)
	}
}

func TestGetLinkedTableBadRevisionIsBadUsageBeforeAnyRequest(t *testing.T) {
	mock := linkedTableGetEnv(t, http.StatusOK)

	_, _, err := execGetLinkedTable(t, "12", "--revision", "v2")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if requests := mock.Requests(); len(requests) != 0 {
		t.Errorf("sent %v before rejecting --revision", requests)
	}
}

func TestGetLinkedTableSeriesMemberKeepsGroupIdentity(t *testing.T) {
	for _, format := range []string{"json", "yaml", "table"} {
		t.Run(format, func(t *testing.T) {
			mock := linkedTableGetEnv(t, http.StatusOK)
			mock.RegisterHandler(http.MethodGet, ltOrgPath+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
				table := ltDataset(12, "Orders · Nov 2026")
				testutil.RespondJSON(w, http.StatusOK, table)
			})
			stdout, stderr, err := execGetLinkedTable(t, "--table-id", "12", "-o", format)
			if err != nil {
				t.Fatalf("get: %v; stderr=%s", err, stderr)
			}
			if format == "table" {
				if !strings.Contains(stdout, "Orders · Nov 2026") {
					t.Fatalf("display name missing: %s", stdout)
				}
				return
			}
			manifest, err := linked_tables.LoadManifest([]byte(stdout))
			if err != nil {
				t.Fatal(err)
			}
			if manifest.Metadata.Name != "orders_enriched" {
				t.Errorf("metadata.name = %q", manifest.Metadata.Name)
			}
		})
	}
}
