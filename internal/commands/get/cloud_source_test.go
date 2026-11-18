package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/secretmask"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// getCloudSourceJSON is GET /data-sources/42 for an s3 source carrying two
// secrets and one override.
const getCloudSourceJSON = `{
  "id": 42, "name": "s3-orders", "version_id": 3, "state": "active",
  "dataset_id": 11, "dataset_name": "orders", "data_source_type": "s3",
  "settings_model_id": 2, "internal_id": "src-42", "schedule": "@hourly",
  "s3_bucket": "raw-orders", "s3_prefix": "incoming/",
  "s3_access_key": "super-secret-access", "s3_secret_key": "super-secret-key",
  "bad_rows_count": 41, "delta_applied_cursor": 1000,
  "null_values_override": ["N/A"], "value_mapping_override": null,
  "created_at": "2024-02-03T04:05:06Z"
}`

func TestGetCloudSource_PeriodCursorsAreStatus(t *testing.T) {
	const cursors = `[
  {"dataset_id":11,"settings_model_id":2,"period_key":"2026-06","delta_applied_cursor":null},
  {"dataset_id":12,"settings_model_id":3,"period_key":"2026-07","delta_applied_cursor":0},
  {"dataset_id":13,"settings_model_id":4,"period_key":"2026-08","delta_applied_cursor":84}
]`
	for _, tc := range []struct{ name, cursors string }{{"series", cursors}, {"ordinary", "[]"}} {
		t.Run(tc.name, func(t *testing.T) {
			body := strings.Replace(getCloudSourceJSON, `"id": 42`, `"period_delta_cursors": `+tc.cursors+`, "id": 42`, 1)
			if tc.name == "series" {
				body = strings.Replace(body, `"delta_applied_cursor": 1000`, `"delta_applied_cursor": null,
"table_series_id": "0b8f6a1e-3c2d-4e5f-8a9b-0c1d2e3f4a5b", "routing_mode": "uploader_chooses"`, 1)
			}
			var want any
			if err := yaml.Unmarshal([]byte(tc.cursors), &want); err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"json", "yaml"} {
				out, calls, err := serveGetCloudSource(t, body, "42", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Errorf("requests = %d, want 1", calls)
				}
				var m map[string]any
				if err := yaml.Unmarshal([]byte(out), &m); err != nil {
					t.Fatal(err)
				}
				status := m["status"].(map[string]any)
				if !reflect.DeepEqual(status["period_delta_cursors"], want) {
					t.Errorf("%s period cursors = %#v, want %#v", format, status["period_delta_cursors"], want)
				}
				if _, ok := m["spec"].(map[string]any)["period_delta_cursors"]; ok {
					t.Errorf("period cursors reached spec:\n%s", out)
				}
				if tc.name == "ordinary" && status["delta_applied_cursor"] != 1000 {
					t.Errorf("ordinary-source scalar cursor lost:\n%s", out)
				}
			}
		})
	}
}

// serveGetCloudSource serves body as cloud source 42, runs `qctl get
// cloud-source args...`, and reports how many requests reached the server.
func serveGetCloudSource(t *testing.T, body string, args ...string) (string, int, error) {
	t.Helper()
	return serveGetCloudSourceStatus(t, http.StatusOK, body, args...)
}

// serveGetCloudSourceStatus is serveGetCloudSource answering with status.
func serveGetCloudSourceStatus(t *testing.T, status int, body string, args ...string) (string, int, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources/42", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, status, json.RawMessage(body))
	})
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	cmd := setupTestCommandForTable(NewCloudSourceCommand())
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", "cloud-source"}, args...))
	err := cmd.Execute()
	return buf.String(), calls, err
}

func TestGetCloudSource_StructuredOutputIsTheManifest(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			out, _, err := serveGetCloudSource(t, getCloudSourceJSON, "42", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var m cloud_sources.CloudSourceManifest
			if format == "json" {
				err = json.Unmarshal([]byte(out), &m)
			} else {
				err = yaml.Unmarshal([]byte(out), &m)
			}
			if err != nil {
				t.Fatalf("not a manifest: %v\n%s", err, out)
			}
			if m.Kind != "CloudSource" || m.APIVersion != "qluster.ai/v1" {
				t.Errorf("kind/apiVersion = %q/%q, want CloudSource/qluster.ai/v1", m.Kind, m.APIVersion)
			}
			if m.Metadata.Name != "s3-orders" || m.Spec.DatasetID != 11 {
				t.Errorf("metadata.name/spec.dataset_id = %q/%d, want s3-orders/11", m.Metadata.Name, m.Spec.DatasetID)
			}
			if m.Spec.S3Bucket == nil || *m.Spec.S3Bucket != "raw-orders" {
				t.Errorf("spec.s3_bucket = %v, want raw-orders", m.Spec.S3Bucket)
			}
			if m.Status == nil || m.Status.ID != 42 || m.Status.VersionID != 3 || m.Status.State != api.DataSourceState("active") {
				t.Errorf("status = %+v, want id 42, version_id 3, state active", m.Status)
			}
			if strings.Contains(out, "super-secret") {
				t.Errorf("a secret reached the manifest:\n%s", out)
			}
		})
	}
}

// The applied cursor is status, and absent until the first incremental job
// finishes.
func TestGetCloudSource_TheAppliedCursorIsStatus(t *testing.T) {
	out, _, err := serveGetCloudSource(t, getCloudSourceJSON, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	spec, status, ok := strings.Cut(out, "\nstatus:\n")
	if !ok {
		t.Fatalf("no status block:\n%s", out)
	}
	if !strings.Contains(status, "  delta_applied_cursor: 1000\n") {
		t.Errorf("status lacks delta_applied_cursor: 1000:\n%s", out)
	}
	if strings.Contains(spec, "delta_applied_cursor") {
		t.Errorf("the cursor reached the spec:\n%s", out)
	}

	never := strings.Replace(getCloudSourceJSON, `"delta_applied_cursor": 1000`, `"delta_applied_cursor": null`, 1)
	out, _, err = serveGetCloudSource(t, never, "42", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(out, "delta_applied_cursor") {
		t.Errorf("a never-advanced cursor is shown:\n%s", out)
	}
}

// cloudSourceReadWithSettings is GET /data-sources/42 for an s3 source with
// overrides and CSV settings and every credential set to "**********". It
// returns the body and the credential keys.
func cloudSourceReadWithSettings(t *testing.T) (string, []string) {
	t.Helper()
	body := map[string]any{
		"id": 42, "name": "s3-orders", "version_id": 3, "state": "active",
		"dataset_id": 11, "data_source_type": "s3", "s3_bucket": "raw-orders",
		"delta_applied_cursor": 1000,

		"schedule": "", "pattern": "*.csv", "csv_delimiter": ";", "csv_quotechar": "",

		"null_values_override":                 []string{"N/A"},
		"infer_datetime_override":              false,
		"datetime_allowed_characters_override": "",
		"value_mapping_per_column_override":    map[string]map[string]string{"country": {"UK": "GB"}},
		"value_mapping_override":               nil,
		"excluded_row_signatures_override":     []string{"sig-1"},
	}
	var secrets []string
	sm := secretmask.New()
	typ := reflect.TypeOf(api.DataSourceModelAllOptionalSchema{})
	for i := range typ.NumField() {
		key, _, _ := strings.Cut(typ.Field(i).Tag.Get("json"), ",")
		if sm.IsSecret(key) {
			body[key] = "**********"
			secrets = append(secrets, key)
		}
	}
	if len(secrets) == 0 {
		t.Fatal("no credential fields found on DataSourceModelAllOptionalSchema")
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), secrets
}

// treeKeys returns every map key in a decoded JSON or YAML document.
func treeKeys(v any) []string {
	var keys []string
	switch v := v.(type) {
	case map[string]any:
		for key, child := range v {
			keys = append(keys, key)
			keys = append(keys, treeKeys(child)...)
		}
	case []any:
		for _, child := range v {
			keys = append(keys, treeKeys(child)...)
		}
	}
	return keys
}

// The export is what apply reads back: overrides nested without their suffix,
// nulls, secrets and an empty keep-when-omitted field left out.
func TestGetCloudSource_ManifestCarriesTheSourceSettings(t *testing.T) {
	body, secrets := cloudSourceReadWithSettings(t)
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			out, _, err := serveGetCloudSource(t, body, "42", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			decode := yaml.Unmarshal
			if format == "json" {
				decode = json.Unmarshal
			}
			var tree map[string]any
			if err := decode([]byte(out), &tree); err != nil {
				t.Fatalf("not a manifest: %v\n%s", err, out)
			}
			spec, _ := tree["spec"].(map[string]any)

			var want map[string]any
			if err := decode([]byte(`{"null_values": ["N/A"], "infer_datetime": false,
				"datetime_allowed_characters": "", "value_mapping_per_column": {"country": {"UK": "GB"}}}`), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(spec["settings_overrides"], want) {
				t.Errorf("spec.settings_overrides = %#v, want %#v\n%s", spec["settings_overrides"], want, out)
			}
			for _, key := range treeKeys(tree) {
				if strings.HasSuffix(key, "_override") {
					t.Errorf("%s reached the manifest:\n%s", key, out)
				}
			}

			for key, value := range map[string]string{"csv_delimiter": ";", "csv_quotechar": "", "pattern": "*.csv"} {
				if got, ok := spec[key]; !ok || got != value {
					t.Errorf("spec.%s = %#v (present %t), want %q\n%s", key, got, ok, value, out)
				}
			}
			if got, ok := spec["schedule"]; ok {
				t.Errorf("spec.schedule = %#v, want it left out\n%s", got, out)
			}

			for _, key := range secrets {
				if strings.Contains(out, key) {
					t.Errorf("secret %s reached the manifest:\n%s", key, out)
				}
			}
			if strings.Contains(out, "**********") {
				t.Errorf("a masked secret reached the manifest:\n%s", out)
			}

			var m cloud_sources.CloudSourceManifest
			if err := pkgmanifest.StrictUnmarshal([]byte(out), &m); err != nil {
				t.Errorf("apply's strict parse refuses the export: %v\n%s", err, out)
			}
		})
	}
}

// The default view is the row `get cloud-sources` prints for the same source.
func TestGetCloudSource_TableIsTheListRow(t *testing.T) {
	out, _, err := serveGetCloudSource(t, getCloudSourceJSON, "42")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := tableRows(out)

	ce := newCloudSourcesEnv(t)
	ce.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{
			"results": []json.RawMessage{json.RawMessage(getCloudSourceJSON)}, "total_rows": 1, "page": 1,
		})
	})
	rootCmd := setupCloudSourcesTestCommand()
	var list bytes.Buffer
	rootCmd.SetOut(&list)
	rootCmd.SetArgs([]string{"get", "cloud-sources"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("get cloud-sources: %v", err)
	}
	want := tableRows(list.String())

	// Two empty tables would agree, so pin the list to the shared columns first.
	if n := len(strings.Fields(want["ID"])) + 1; n != len(strings.Split(cloudSourceDefaultColumns, ",")) {
		t.Fatalf("list header %q is not the %d columns of %q:\n%s", want["ID"], n, cloudSourceDefaultColumns, list.String())
	}
	if _, ok := want["42"]; !ok {
		t.Fatalf("the list printed no row for source 42:\n%s", list.String())
	}
	for _, key := range []string{"ID", "42"} {
		if got[key] != want[key] {
			t.Errorf("line %s = %q, want the list's %q\nget cloud-source:\n%s\nget cloud-sources:\n%s",
				key, got[key], want[key], out, list.String())
		}
	}
}

func TestGetCloudSource_NameOutput(t *testing.T) {
	out, _, err := serveGetCloudSource(t, getCloudSourceJSON, "42", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "cloud-source/42\n" {
		t.Errorf("-o name = %q, want %q", out, "cloud-source/42\n")
	}
}

func TestGetCloudSource_NonNumericIDIsBadUsage(t *testing.T) {
	_, calls, err := serveGetCloudSource(t, getCloudSourceJSON, "s3-orders")
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitBadUsage, err)
	}
	if calls != 0 {
		t.Errorf("%d requests reached the server, want 0", calls)
	}
}

// A server refusal keeps its exit code whatever -o asks for.
func TestGetCloudSource_ServerRefusalExitCodes(t *testing.T) {
	const notFound = `{"detail": "Data source 42 not found"}`
	for _, format := range []string{"table", "yaml", "json", "name"} {
		t.Run("404/"+format, func(t *testing.T) {
			_, calls, err := serveGetCloudSourceStatus(t, http.StatusNotFound, notFound, "42", "-o", format)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitNotFound, err)
			}
			if calls != 1 {
				t.Errorf("%d requests reached the server, want 1", calls)
			}
		})
	}

	t.Run("403", func(t *testing.T) {
		_, _, err := serveGetCloudSourceStatus(t, http.StatusForbidden, `{"detail": "Not enough permissions"}`, "42")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
			t.Errorf("exit code = %d, want %d (err: %v)", got, qerrors.ExitUnauthorized, err)
		}
	})
}
