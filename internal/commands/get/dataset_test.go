package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func TestTableNameMissesStayInScopeAndStopBeforeFetching(t *testing.T) {
	for _, args := range [][]string{{"get", "table", "trips", "-o", "json"}, {"get", "files", "--table", "trips", "-o", "json"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				rows := []datasets.DatasetTiny{}
				if r.URL.Query().Has("search_query") {
					rows = []datasets.DatasetTiny{{ID: 8, Name: "Trips"}}
				}
				testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{Results: rows})
			})
			root := setupTestCommandForTable(NewDatasetCommand())
			root.Commands()[0].AddCommand(NewFilesCommand())
			root.SilenceUsage = true
			root.SilenceErrors = true
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(args)
			err := root.Execute()
			if qerrors.GetExitCode(err) != qerrors.ExitNotFound {
				t.Fatalf("err = %v, want exit 3", err)
			}
			for _, want := range []string{`no table named "trips"`, `Did you mean "Trips" (id 8)?`, "Scoped to organization " + testOrgID} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if stdout.Len() != 0 || len(mock.Requests()) != 2 {
				t.Errorf("stdout = %q, requests = %v", stdout.String(), mock.Requests())
			}
		})
	}
}

const getTableDatasetJSON = `{
  "id": 12, "name": "trips", "state": "active", "version_id": 4,
  "organization_id": "123e4567-e89b-12d3-a456-426614174000",
  "destination_id": 1, "destination_name": "postgres-prod",
  "database_name": "analytics", "schema_name": "public", "table_name": "trips",
  "migration_policy": "ask_user", "data_loading_process": "append_skip_identical_rows",
  "backup_settings_id": 9, "backup_key_format": "{source}/{date}",
  "anomaly_threshold": 5, "max_retry_count": 3, "max_tries_to_fix_json": 2,
  "not_anomalies_per_column": {"fare": [12.5, 99], "status": ["N/A"]},
  "clean_rows_count": 9182, "bad_rows_count": 41,
  "settings_model": {"id": 7, "version_id": 2, "array_delimiters": [",", "|"], "null_values": ["NULL"]},
  "backup_settings_list": [], "destinations_list": []
}`

// getTableNameLookupJSON answers GET /datasets?name=trips, the path
// datasets.ResolveID and cmdutil.ResolveTable both take for a name.
const getTableNameLookupJSON = `{"total_rows": 1, "page": 1,
  "results": [{"id": 12, "name": "trips", "state": "active"}]}`

func setupTestCommandForTable(sub *cobra.Command) *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")

	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(sub)
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// runGetTableCmd wires env + mock, registers the dataset fixture on table 12
// and the name lookup, and runs `qctl get <sub> args...`. Task 5's
// table-config tests reuse it.
func runGetTableCmd(t *testing.T, sub *cobra.Command, subName string, args ...string) (string, error) {
	t.Helper()
	return runGetTableCmdServing(t, getTableDatasetJSON, sub, subName, args...)
}

// runGetTableCmdServing is runGetTableCmd serving body as table 12.
func runGetTableCmdServing(t *testing.T, body string, sub *cobra.Command, subName string, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/12", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(getTableNameLookupJSON))
	})

	cmd := setupTestCommandForTable(sub)
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(append([]string{"get", subName}, args...))
	// Execute first: `return buf.String(), cmd.Execute()` would evaluate the
	// buffer before the command wrote to it.
	err := cmd.Execute()
	return buf.String(), err
}

func runGetTable(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return runGetTableCmd(t, NewDatasetCommand(), "table", args...)
}

// getTableCastingDefaultJSON is getTableDatasetJSON with a 7-digit casting
// default in the settings model.
var getTableCastingDefaultJSON = strings.Replace(getTableDatasetJSON,
	`"null_values": ["NULL"]}`,
	`"null_values": ["NULL"], "default_value_for_field_when_casting_error": {"fare": 1234567}}`, 1)

// The manifest prints a 7-digit casting default as an integer.
func TestGetTable_SettingsModelKeepsIntegers(t *testing.T) {
	out, err := runGetTableCmdServing(t, getTableCastingDefaultJSON, NewDatasetCommand(), "table", "12", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "fare: 1234567\n") {
		t.Errorf("-o yaml should print fare: 1234567, got:\n%s", out)
	}
}

// A casting default past int64 that encoding/json writes for a float64 prints
// as a YAML float, and -o json keeps its digits.
func TestGetTable_SettingsModelPrintsAnIntegerFloatAsAFloat(t *testing.T) {
	body := strings.Replace(getTableCastingDefaultJSON, `{"fare": 1234567}`, `{"fare": 10000000000000000000}`, 1)
	for format, want := range map[string]string{
		"yaml": "fare: 1e+19\n",
		"json": `"fare": 10000000000000000000` + "\n",
	} {
		out, err := runGetTableCmdServing(t, body, NewDatasetCommand(), "table", "12", "-o", format)
		if err != nil {
			t.Fatalf("-o %s: unexpected error: %v", format, err)
		}
		if !strings.Contains(out, want) {
			t.Errorf("-o %s should print %q, got:\n%s", format, want, out)
		}
	}
}

func TestGetTable_StructuredOutputIsTheManifest(t *testing.T) {
	for _, format := range []string{"yaml", "json"} {
		t.Run(format, func(t *testing.T) {
			out, err := runGetTable(t, "12", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var m datasets.TableManifestWithStatus
			switch format {
			case "json":
				if err := json.Unmarshal([]byte(out), &m); err != nil {
					t.Fatalf("not a manifest: %v\n%s", err, out)
				}
			default:
				if err := yaml.Unmarshal([]byte(out), &m); err != nil {
					t.Fatalf("not a manifest: %v\n%s", err, out)
				}
			}
			if m.Kind != "Table" || m.APIVersion != "qluster.ai/v1" {
				t.Errorf("kind/apiVersion = %q/%q, want Table/qluster.ai/v1", m.Kind, m.APIVersion)
			}
			if m.Metadata.Name != "trips" {
				t.Errorf("metadata.name = %q, want trips", m.Metadata.Name)
			}
			if m.Spec.DestinationID != 1 {
				t.Errorf("spec.destination_id = %d, want 1", m.Spec.DestinationID)
			}
			// The old shape put status counters at the top level, interleaved
			// with configuration. They belong under status.
			if strings.Contains(out, "\nbad_rows_count") || strings.Contains(out, "\"bad_rows_count\": 41,\n  \"clean") {
				t.Errorf("status counters must not sit at the top level; got:\n%s", out)
			}
		})
	}
}

// A name argument goes through ResolveID's name-lookup path (GET
// /datasets?name=trips) before the GET /datasets/12 the other tests exercise
// directly. getTableNameLookupJSON and the /datasets handler registered in
// runGetTableCmd existed for this and were otherwise dead.
func TestGetTable_ResolvesByName(t *testing.T) {
	out, err := runGetTable(t, "trips", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var m datasets.TableManifestWithStatus
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not a manifest: %v\n%s", err, out)
	}
	if m.Metadata.Name != "trips" {
		t.Errorf("metadata.name = %q, want trips", m.Metadata.Name)
	}
}

// -o table and -o name still render the response through the printer.
func TestGetTable_TableAndNameUnchanged(t *testing.T) {
	out, err := runGetTable(t, "12", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "table/12\n" {
		t.Errorf("-o name = %q, want %q", out, "table/12\n")
	}
	if strings.Contains(out, "apiVersion") {
		t.Errorf("-o name must not emit the manifest; got:\n%s", out)
	}
}
