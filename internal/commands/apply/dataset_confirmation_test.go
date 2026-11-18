package apply

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func newTableUpdateApplyTest(t *testing.T) (*testutil.TestEnv, *testutil.MockAPIServer, datasets.DatasetFull) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	existing := sampleDatasetFullForApply()
	orgID := types.UUID{}
	_ = orgID.UnmarshalText([]byte(testOrgID))
	existing.OrganizationID = orgID
	existing.SettingsModel = api.SettingsSchema{ID: 7, VersionID: 2}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID:             existing.ID,
				Name:           existing.Name,
				OrganizationID: orgID,
				State:          api.DataSetStateActive,
				Users:          []api.DataSetUserInfoTinySchema{},
				VersionID:      existing.VersionID,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")
	return env, mock, existing
}

func tableUpdateManifest(env *testutil.TestEnv, anomaly int) string {
	return env.CreateFile("table.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
  anomaly_threshold: `+strconv.Itoa(anomaly)+`
`)
}

func TestApplyTableUpdateRequiresConfirmationBeforePatch(t *testing.T) {
	env, mock, _ := newTableUpdateApplyTest(t)
	cmdutil.ResetReader()
	t.Cleanup(cmdutil.ResetReader)
	manifestPath := tableUpdateManifest(env, 12)

	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "-f", manifestPath})
	err := root.Execute()
	if err == nil {
		t.Fatal("changed table update succeeded without confirmation")
	}
	if got := qerrors.GetExitCode(err); got != 2 {
		t.Errorf("exit code = %d, want 2", got)
	}
	if !strings.Contains(err.Error(), "re-run with --yes (-y)") {
		t.Errorf("error does not explain the non-interactive path: %v", err)
	}
	if !strings.Contains(stdout.String(), "Changes to apply:\n  spec.anomaly_threshold: 10 → 12") {
		t.Errorf("apply did not print the preview before refusing:\n%s", stdout.String())
	}
	if requests := mock.Requests(); !reflect.DeepEqual(requests, []string{
		"GET /api/orgs/" + testOrgID + "/datasets",
		"GET /api/orgs/" + testOrgID + "/datasets/1",
	}) {
		t.Errorf("requests = %#v; PATCH must follow confirmation", requests)
	}
}

func TestApplyTableUpdateConfirmationSendsPreparedPatch(t *testing.T) {
	env, mock, existing := newTableUpdateApplyTest(t)
	cmdutil.SetReader(strings.NewReader("yes\n"))
	t.Cleanup(cmdutil.ResetReader)
	manifestPath := tableUpdateManifest(env, 12)

	var body api.DataSetPatchRequestSchema
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode PATCH: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		existing.AnomalyThreshold = 12
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "table", "-f", manifestPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("confirmed table update: %v", err)
	}
	if body.AnomalyThreshold == nil || *body.AnomalyThreshold != 12 || body.VersionID != existing.VersionID {
		t.Errorf("PATCH body anomaly/version = %v/%d", body.AnomalyThreshold, body.VersionID)
	}
	for _, text := range []string{
		"Changes to apply:\n  spec.anomaly_threshold: 10 → 12",
		"Updating table orders...",
		"table/orders updated",
	} {
		if !strings.Contains(stdout.String(), text) {
			t.Errorf("output is missing %q:\n%s", text, stdout.String())
		}
	}
}

func TestApplyTableUpdateDeclineSkipsPatch(t *testing.T) {
	env, mock, _ := newTableUpdateApplyTest(t)
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)
	manifestPath := tableUpdateManifest(env, 12)

	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "-f", manifestPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("a declined one-document apply is a skip, got %v", err)
	}
	if !strings.Contains(stdout.String(), "Skipping "+manifestPath) {
		t.Errorf("decline output:\n%s", stdout.String())
	}
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "PATCH ") {
			t.Errorf("declined apply sent %s", request)
		}
	}
}

func TestApplyTableUnchangedDoesNotPromptOrPatch(t *testing.T) {
	env, mock, _ := newTableUpdateApplyTest(t)
	cmdutil.ResetReader()
	t.Cleanup(cmdutil.ResetReader)
	manifestPath := tableUpdateManifest(env, 10)

	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "-f", manifestPath})
	if err := root.Execute(); err != nil {
		t.Fatalf("unchanged apply: %v", err)
	}
	if got := stdout.String(); got != "table/orders unchanged\n" {
		t.Errorf("stdout = %q, want unchanged line", got)
	}
	for _, request := range mock.Requests() {
		if strings.HasPrefix(request, "PATCH ") {
			t.Errorf("unchanged apply sent %s", request)
		}
	}
}

// Under -o json and -o yaml stdout is the result, so an update that would
// prompt is refused without --yes before anything is sent or printed.
func TestApplyTableStructuredUpdateRequiresYesWithoutCorruptingStdout(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			env, mock, _ := newTableUpdateApplyTest(t)
			manifestPath := tableUpdateManifest(env, 12)

			root := setupApplyRoot()
			root.SilenceErrors = true
			root.SilenceUsage = true
			var stdout bytes.Buffer
			root.SetOut(&stdout)
			root.SetArgs([]string{"apply", "-f", manifestPath, "-o", format})
			err := root.Execute()
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("apply -o %s error = %v, want bad usage", format, err)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout was corrupted with prose: %q", stdout.String())
			}
			for _, request := range mock.Requests() {
				if strings.HasPrefix(request, "PATCH ") {
					t.Errorf("unconfirmed apply -o %s sent %s", format, request)
				}
			}
		})
	}
}

// The preview block prints the live table's name and the live side of every
// changed value. Both are the server's bytes: the block must read as text, not
// execute as a control sequence. The live backup_key_format carries U+009B
// because json.Marshal escapes C0 and leaves C1 alone, so that is the shape a
// diff value actually arrives in.
func TestApplyTableConfirmationSanitizesLiveValues(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	cmdutil.ResetReader()
	t.Cleanup(cmdutil.ResetReader)

	existing := sampleDatasetFullForApply()
	orgID := types.UUID{}
	_ = orgID.UnmarshalText([]byte(testOrgID))
	existing.OrganizationID = orgID
	existing.SettingsModel = api.SettingsSchema{ID: 7, VersionID: 2}
	existing.Name = "orders\x1b[31m"
	existing.BackupKeyFormat = "{dataset_id}\u009b31m"

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetsPage{
			Results: []datasets.DatasetTiny{{
				ID:             existing.ID,
				Name:           existing.Name,
				OrganizationID: orgID,
				State:          api.DataSetStateActive,
				Users:          []api.DataSetUserInfoTinySchema{},
				VersionID:      existing.VersionID,
			}},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(1),
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, existing)
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("table.yaml", `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: "orders\x1b[31m"
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: append
  backup_settings_id: 1
  backup_key_format: "{dataset_id}"
`)

	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs([]string{"apply", "table", "-f", manifestPath})
	if err := root.Execute(); err == nil {
		t.Fatal("a changed table update succeeded without confirmation")
	}

	got := stdout.String()
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\u009b') {
		t.Fatalf("the preview block kept a terminal control: %q", got)
	}
	for _, want := range []string{`Table        : orders\x1b[31m (id: 1)`, `{dataset_id}\x9b31m`} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing visible escape %q:\n%s", want, got)
		}
	}
}
