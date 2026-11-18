package apply

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func TestApplyCloudSourceRejectsNullStringListBeforeRequest(t *testing.T) {
	doc := manifestDoc{Data: []byte(`apiVersion: qluster.ai/v1
kind: CloudSource
metadata: {name: s3-orders}
spec:
  dataset_id: 11
  data_source_type: s3
  settings_overrides:
    null_values: [NULL]
`)}
	_, err := applyCloudSource(&cobra.Command{}, doc)
	if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "spec.settings_overrides.null_values[0]") {
		t.Fatalf("applyCloudSource error = %v, want exit 2 naming the null list element", err)
	}
}

func sampleCloudSourceFull() *cloud_sources.CloudSourceFull {
	now := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	state := api.DataSourceState("active")
	dsType := api.DataSourceType("s3")
	id := 42
	datasetID := 11
	versionID := 3
	name := "s3-orders"
	datasetName := "orders"
	bucket := "raw-orders"
	region := "us-east-1"

	return &cloud_sources.CloudSourceFull{
		ID:              &id,
		Name:            &name,
		DatasetID:       &datasetID,
		DatasetName:     &datasetName,
		DataSourceType:  &dsType,
		SettingsModelID: func() *int { v := 2; return &v }(),
		VersionID:       &versionID,
		S3Bucket:        &bucket,
		S3RegionName:    &region,
		State:           &state,
		CreatedAt:       &now,
	}
}

func TestApplyCloudSourceCreate(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 2},
		})
	})

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results:   []cloud_sources.CloudSourceTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})

	var received api.DataSourceModelPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	t.Setenv("S3_ACCESS_KEY", "ak")

	manifestPath := env.CreateFile("cloud-source.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  settings_model_id: 2
  s3_bucket: raw-orders
  s3_region_name: us-east-1
  s3_access_key: ${S3_ACCESS_KEY}
  s3_secret_key: pa$$w0rd
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if received.Name != "s3-orders" {
		t.Fatalf("expected name s3-orders, got %s", received.Name)
	}
	if received.S3AccessKey == nil || *received.S3AccessKey != "ak" {
		t.Fatalf("expected S3 access key to be expanded, got %+v", received.S3AccessKey)
	}
	if received.S3SecretKey == nil || *received.S3SecretKey != "pa$$w0rd" {
		t.Fatalf("expected literal S3 secret key, got %+v", received.S3SecretKey)
	}
	if stdout.String() != "cloud-source/s3-orders created\n" {
		t.Fatalf("unexpected output: %s", stdout.String())
	}
}

func TestApplyCloudSourceRequiresCredentialsOnlyOnCreate(t *testing.T) {
	tests := []struct {
		name, sourceType, fields, missingField string
	}{
		{"sftp", "sftp", "  sftp_host: sftp.example.com\n  sftp_user: user\n", "spec.sftp_password"},
		{"dropbox", "dropbox", "  dropbox_folder: /Exports\n", "spec.dropbox_authorization_code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, extra := range []string{"", "  " + strings.TrimPrefix(tc.missingField, "spec.") + ": '**********'\n"} {
				server := newCloudSourceServer(t, nil)
				manifest := "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: source\nspec:\n  dataset_id: 11\n  data_source_type: " + tc.sourceType + "\n" + tc.fields + extra
				_, err := server.apply(t, manifest)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Fatalf("missing create credential exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
				}
				if !strings.Contains(err.Error(), tc.missingField) {
					t.Errorf("missing create credential error = %q, want %s", err, tc.missingField)
				}
				if writes := server.writes(); len(writes) != 0 {
					t.Errorf("create without credential sent %v", writes)
				}
			}
		})
	}
}

func TestApplyCloudSourceCreateAcceptsEitherSFTPCredentialAndDropboxCode(t *testing.T) {
	tests := []struct {
		sourceType, fields, credential string
	}{
		{"sftp", "  sftp_host: sftp.example.com\n  sftp_user: user\n", "sftp_password"},
		{"sftp", "  sftp_host: sftp.example.com\n  sftp_user: user\n", "sftp_ssh_key"},
		{"dropbox", "  dropbox_folder: /Exports\n", "dropbox_authorization_code"},
	}
	for _, tc := range tests {
		t.Run(tc.credential, func(t *testing.T) {
			server := newCloudSourceServer(t, nil)
			manifest := "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: source\nspec:\n  dataset_id: 11\n  data_source_type: " + tc.sourceType + "\n" + tc.fields + "  " + tc.credential + ": credential\n"
			if _, err := server.apply(t, manifest); err != nil {
				t.Fatalf("create with %s: %v", tc.credential, err)
			}
			if writes := server.writes(); !reflect.DeepEqual(writes, []string{"POST /api/orgs/" + testOrgID + "/data-sources"}) {
				t.Fatalf("writes = %v, want one POST", writes)
			}
			if got := string(server.bodies[0][tc.credential]); got != `"credential"` {
				t.Errorf("POST %s = %s", tc.credential, got)
			}
		})
	}
}

func TestApplyCloudSourceUpdatesWithoutCredentials(t *testing.T) {
	tests := []struct {
		name, sourceType, fields string
	}{
		{"sftp", "sftp", "  sftp_host: sftp.example.com\n  sftp_user: user\n"},
		{"dropbox", "dropbox", "  dropbox_folder: /Exports\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			live := sampleCloudSourceFull()
			*live.Name = "source"
			*live.DataSourceType = api.DataSourceType(tc.sourceType)
			server := newCloudSourceServer(t, live)
			manifest := "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: source\nspec:\n  dataset_id: 11\n  data_source_type: " + tc.sourceType + "\n" + tc.fields + "  schedule: '@hourly'\n"
			if _, err := server.apply(t, manifest, "--yes"); err != nil {
				t.Fatalf("update without credential: %v", err)
			}
			if writes := server.writes(); !reflect.DeepEqual(writes, []string{"PATCH /api/orgs/" + testOrgID + "/data-sources/42"}) {
				t.Fatalf("writes = %v, want one PATCH", writes)
			}
			for _, key := range []string{"sftp_password", "sftp_ssh_key", "dropbox_authorization_code"} {
				if _, ok := server.bodies[0][key]; ok {
					t.Errorf("PATCH contains %s", key)
				}
			}
		})
	}
}

// With --yes an update asks nothing and sends one PATCH: the keys that change,
// with the credentials expanded, plus id and the version_id apply read.
func TestApplyCloudSourceUpdate(t *testing.T) {
	server := newCloudSourceServer(t, sampleCloudSourceFull())
	forbidCloudSourcePrompt(t)
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	stdout, err := server.apply(t, s3OrdersManifest(`  settings_model_id: 2
  s3_access_key: ${S3_ACCESS_KEY}
  s3_secret_key: ${S3_SECRET_KEY}
  schedule: "@hourly"
`), "--yes")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if writes := server.writes(); !reflect.DeepEqual(writes, []string{"PATCH /api/orgs/" + testOrgID + "/data-sources/42"}) {
		t.Fatalf("writes = %v, want exactly one PATCH of the source", writes)
	}
	got := map[string]string{}
	for key, value := range server.bodies[0] {
		got[key] = string(value)
	}
	want := map[string]string{
		"id":            `42`,
		"version_id":    `3`,
		"s3_access_key": `"ak"`,
		"s3_secret_key": `"sk"`,
		"schedule":      `"@hourly"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PATCH body =\n  %v\nwant\n  %v", got, want)
	}
	if stdout != "cloud-source/s3-orders updated\n" {
		t.Errorf("stdout = %q, want the updated line alone", stdout)
	}
}

// The field a user cannot know is now optional: apply reads the table named by
// spec.dataset_id and fills it in.
func TestApplyCloudSourceResolvesSettingsModelIDFromTheTable(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 7},
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results: []cloud_sources.CloudSourceTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
		})
	})
	var received api.DataSourceModelPostRequestSchema
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatalf("decode: %v", err)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("cs.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  s3_bucket: raw-orders
`)
	rootCmd := setupApplyRoot()
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if received.SettingsModelID != 7 {
		t.Errorf("settings_model_id = %d, want the table's 7", received.SettingsModelID)
	}
}

// A pair that disagrees is exit 2 and NO create: the backend derives the target
// table from settings_model_id, so this manifest would have created the source
// somewhere it does not name.
func TestApplyCloudSourceRefusesADisagreeingSettingsModelID(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 7},
		})
	})
	// The create path is registered even though nothing may reach it. Without
	// these two routes the lookup 404s on its own, so "no POST arrived" would
	// be true whatever the code did and the exit code would be 3 for a reason
	// that has nothing to do with the refusal. With them, deleting the check
	// makes this apply SUCCEED -- which is the failure the test is here for.
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results: []cloud_sources.CloudSourceTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("cs.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  settings_model_id: 99
  data_source_type: s3
  s3_bucket: raw-orders
`)
	rootCmd := setupApplyRoot()
	rootCmd.SilenceErrors = true
	rootCmd.SilenceUsage = true
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})

	err := rootCmd.Execute()
	if err == nil {
		t.Fatal("expected a refusal for a disagreeing pair")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	for _, r := range mock.Requests() {
		if r == "POST /api/orgs/"+testOrgID+"/data-sources" {
			t.Fatalf("nothing may be created for a disagreeing pair; requests = %v", mock.Requests())
		}
	}
}

// A table that reports no settings model is a malformed response, not a bad
// manifest: exit 1, and nothing created.
//
// Both manifest shapes are exercised because the guard's absence shows
// differently in each. In the absent case the filled 0 reaches PrepareApply,
// whose re-run of Validate() rejects it as a manifest error, exit 2. In the
// supplied case 5 is compared against the zero-valued 0, taking the
// disagreeing-pair branch: exit 2 with the nonsense text "table \"orders\"
// owns settings model 0".
//
// The whole create path is registered on purpose. Without the POST route the
// "no create" assertion would be true whatever the code did.
func TestApplyCloudSourceRefusesATableWithNoSettingsModel(t *testing.T) {
	tests := []struct {
		name     string
		specLine string
	}{
		{name: "settings_model_id absent", specLine: ""},
		{name: "settings_model_id supplied", specLine: "  settings_model_id: 5\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()
			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			// settings_model is absent entirely; SettingsModel is a non-pointer
			// struct, so it decodes to the zero value rather than erroring.
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"id":11,"name":"orders"}`))
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
					Results: []cloud_sources.CloudSourceTiny{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
				})
			})
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
			})

			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "token")

			manifestPath := env.CreateFile("cs.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
`+tt.specLine+`  data_source_type: s3
  s3_bucket: raw-orders
`)
			rootCmd := setupApplyRoot()
			rootCmd.SilenceErrors = true
			rootCmd.SilenceUsage = true
			rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})

			err := rootCmd.Execute()
			if err == nil {
				t.Fatal("expected a refusal when the table reports no settings model")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d (a malformed server response is not a usage error)", code, qerrors.ExitGenericError)
			}
			for _, r := range mock.Requests() {
				if r == "POST /api/orgs/"+testOrgID+"/data-sources" {
					t.Fatalf("nothing may be created from a zero settings model; requests = %v", mock.Requests())
				}
			}
		})
	}
}

// The applied line echoes the manifest's own name, which qctl did not author
// either: a name carrying an ESC sequence prints as a visible escape.
func TestApplyCloudSourceSanitizesTheAppliedName(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 2},
		})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results:   []cloud_sources.CloudSourceTiny{},
			Page:      testutil.IntPtr(1),
			TotalRows: testutil.IntPtr(0),
		})
	})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleCloudSourceFull())
	})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	manifestPath := env.CreateFile("cloud-source.yaml", `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: "s3-orders\x1b[31m"
spec:
  dataset_id: 11
  data_source_type: s3
  settings_model_id: 2
  s3_bucket: raw-orders
`)

	rootCmd := setupApplyRoot()
	var stdout bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetArgs([]string{"apply", "cloud-source", "-f", manifestPath})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if got := stdout.String(); strings.ContainsRune(got, '\x1b') ||
		got != `cloud-source/s3-orders\x1b[31m created`+"\n" {
		t.Errorf("applied name was not sanitized: %q", got)
	}
}

// cloudSourceServer is a mock API holding the table orders (id 11, settings
// model 2) and at most one cloud source. It answers every POST and PATCH and
// records its body.
type cloudSourceServer struct {
	env    *testutil.TestEnv
	mock   *testutil.MockAPIServer
	bodies []map[string]json.RawMessage
}

// newCloudSourceServer serves existing as the table's only cloud source, or no
// cloud source when existing is nil.
func newCloudSourceServer(t *testing.T, existing *cloud_sources.CloudSourceFull) *cloudSourceServer {
	t.Helper()
	s := &cloudSourceServer{env: testutil.NewTestEnv(t), mock: testutil.NewMockAPIServer()}
	t.Cleanup(s.env.Cleanup)
	t.Cleanup(s.mock.Close)
	dataSources := "/api/orgs/" + testOrgID + "/data-sources"

	s.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/11", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, datasets.DatasetFull{
			ID: 11, Name: "orders", SettingsModel: api.SettingsSchema{ID: 2},
		})
	})
	listed := []cloud_sources.CloudSourceTiny{}
	if existing != nil {
		listed = append(listed, cloud_sources.CloudSourceTiny{
			ID: *existing.ID, Name: *existing.Name, DataSourceType: *existing.DataSourceType,
			DatasetID: *existing.DatasetID, DatasetName: *existing.DatasetName, State: *existing.State,
		})
		source := dataSources + "/" + strconv.Itoa(*existing.ID)
		s.mock.RegisterHandler("GET", source, func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, existing)
		})
		s.mock.RegisterHandler("PATCH", source, s.record(t, existing))
	}
	s.mock.RegisterHandler("GET", dataSources, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, cloud_sources.CloudSourcesPage{
			Results: listed, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(len(listed)),
		})
	})
	s.mock.RegisterHandler("POST", dataSources, s.record(t, sampleCloudSourceFull()))

	s.env.SetupConfigWithOrg(s.mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(s.mock.URL())
	s.env.SetupCredential(endpointKey, testOrgID, "token")
	return s
}

// record appends the request body to s.bodies and answers with reply.
func (s *cloudSourceServer) record(t *testing.T, reply any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode %s body: %v", r.Method, err)
		}
		s.bodies = append(s.bodies, body)
		testutil.RespondJSON(w, http.StatusOK, reply)
	}
}

// apply runs `apply cloud-source -f` on manifestYAML with args appended and
// returns its stdout; stderr is discarded.
func (s *cloudSourceServer) apply(t *testing.T, manifestYAML string, args ...string) (stdout string, err error) {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(io.Discard)
	root.SetArgs(append([]string{"apply", "cloud-source", "-f", s.env.CreateFile("cs.yaml", manifestYAML)}, args...))
	err = root.Execute()
	return out.String(), err
}

// writes returns every POST and PATCH the server received, as "METHOD path",
// whatever the path.
func (s *cloudSourceServer) writes() []string {
	var writes []string
	for _, r := range s.mock.Requests() {
		if strings.HasPrefix(r, "POST ") || strings.HasPrefix(r, "PATCH ") {
			writes = append(writes, r)
		}
	}
	return writes
}

// cloudSourcePromptForbidden is a prompt reader that fails the test when read.
type cloudSourcePromptForbidden struct{ t *testing.T }

func (p cloudSourcePromptForbidden) Read([]byte) (int, error) {
	p.t.Error("apply asked for confirmation")
	return 0, io.EOF
}

// forbidCloudSourcePrompt fails the test if apply asks for confirmation. The
// reader it installs makes cmdutil.CanPrompt report true whatever stdin is.
func forbidCloudSourcePrompt(t *testing.T) {
	t.Helper()
	cmdutil.SetReader(cloudSourcePromptForbidden{t})
	t.Cleanup(cmdutil.ResetReader)
}

// s3OrdersManifest is a manifest that matches sampleCloudSourceFull, followed
// by extra, a block of spec lines.
func s3OrdersManifest(extra string) string {
	return `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: s3-orders
spec:
  dataset_id: 11
  data_source_type: s3
  s3_bucket: raw-orders
  s3_region_name: us-east-1
` + extra
}

// s3OrdersPrefixManifest changes one field of sampleCloudSourceFull: it sets
// s3_prefix.
var s3OrdersPrefixManifest = s3OrdersManifest("  s3_prefix: incoming/\n")

// The output of `get cloud-source -o yaml`, applied back. As a create, the
// POST carries exactly the manifest's own fields: its name, its identity, and
// every field and override the export set, and no null. As an update of the
// source it was exported from, it changes nothing and sends nothing, and so
// needs no --yes.
func TestApplyCloudSourceAppliesItsOwnExport(t *testing.T) {
	existing := sampleCloudSourceFull()
	mapping := map[string]string{"NA": "n/a"}
	perColumn := map[string]map[string]string{"state": {"Calif": "CA"}}
	delimiter := ";"
	port := 22
	existing.ValueMappingOverride = &mapping
	existing.ValueMappingPerColumnOverride = &perColumn
	existing.CsvDelimiter = &delimiter
	existing.SftpPort = &port

	exported, err := cloud_sources.APIResponseToManifest(existing)
	if err != nil {
		t.Fatalf("export the cloud source: %v", err)
	}
	exportedYAML, err := yaml.Marshal(exported)
	if err != nil {
		t.Fatalf("marshal the exported manifest: %v", err)
	}

	t.Run("create", func(t *testing.T) {
		server := newCloudSourceServer(t, nil)
		forbidCloudSourcePrompt(t)
		stdout, err := server.apply(t, string(exportedYAML))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if writes := server.writes(); !reflect.DeepEqual(writes, []string{"POST /api/orgs/" + testOrgID + "/data-sources"}) {
			t.Fatalf("writes = %v, want exactly one POST", writes)
		}
		var got, nulls []string
		for key, value := range server.bodies[0] {
			if string(value) == "null" {
				nulls = append(nulls, key)
			}
			got = append(got, key)
		}
		sort.Strings(got)
		sort.Strings(nulls)
		if len(nulls) > 0 {
			t.Errorf("POST sends %d keys as null: %s", len(nulls), strings.Join(nulls, ","))
		}
		want := []string{
			"csv_delimiter", "data_source_type", "dataset_id", "name", "s3_bucket", "s3_region_name",
			"settings_model_id", "sftp_port", "value_mapping_override", "value_mapping_per_column_override",
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("POST keys =\n  %s\nwant\n  %s", strings.Join(got, ","), strings.Join(want, ","))
		}
		if got := string(server.bodies[0]["value_mapping_per_column_override"]); got != `{"state":{"Calif":"CA"}}` {
			t.Errorf("value_mapping_per_column_override = %s", got)
		}
		if stdout != "cloud-source/s3-orders created\n" {
			t.Errorf("stdout = %q", stdout)
		}
	})

	t.Run("update of the source it was exported from", func(t *testing.T) {
		server := newCloudSourceServer(t, existing)
		forbidCloudSourcePrompt(t)
		stdout, err := server.apply(t, string(exportedYAML))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if writes := server.writes(); len(writes) > 0 {
			t.Errorf("re-applying an unedited export sent %v", writes)
		}
		if stdout != "cloud-source/s3-orders unchanged\n" {
			t.Errorf("stdout = %q, want the unchanged line alone", stdout)
		}
	})
}

// A manifest the source already matches sends nothing and asks nothing, so it
// needs no --yes, -o json included.
func TestApplyCloudSourceUnchangedSendsNothing(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		server := newCloudSourceServer(t, sampleCloudSourceFull())
		forbidCloudSourcePrompt(t)
		stdout, err := server.apply(t, s3OrdersManifest(""))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if stdout != "cloud-source/s3-orders unchanged\n" {
			t.Errorf("stdout = %q, want the unchanged line alone", stdout)
		}
		if writes := server.writes(); len(writes) > 0 {
			t.Errorf("an unchanged apply sent %v", writes)
		}
	})

	t.Run("json", func(t *testing.T) {
		server := newCloudSourceServer(t, sampleCloudSourceFull())
		forbidCloudSourcePrompt(t)
		stdout, err := server.apply(t, s3OrdersManifest(""), "-o", "json")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		var result cloud_sources.ApplyResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("stdout is not one JSON result: %v\n%s", err, stdout)
		}
		if result.Action != "unchanged" || result.Name != "s3-orders" || result.ID != 42 {
			t.Errorf("result = %+v, want action unchanged for s3-orders (42)", result)
		}
		if writes := server.writes(); len(writes) > 0 {
			t.Errorf("an unchanged apply sent %v", writes)
		}
	})
}

// -o json keeps stdout for the result, so an update that changes something
// is refused without --yes, even when a yes is waiting at the prompt, and
// goes through with it.
func TestApplyCloudSourceJSONUpdateRequiresYes(t *testing.T) {
	t.Run("without --yes", func(t *testing.T) {
		server := newCloudSourceServer(t, sampleCloudSourceFull())
		confirmYes(t)
		stdout, err := server.apply(t, s3OrdersPrefixManifest, "-o", "json")
		if err == nil {
			t.Fatal("a changed update under -o json succeeded without --yes")
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want nothing", stdout)
		}
		if writes := server.writes(); len(writes) > 0 {
			t.Errorf("an unconfirmed apply sent %v", writes)
		}
	})

	t.Run("with --yes", func(t *testing.T) {
		server := newCloudSourceServer(t, sampleCloudSourceFull())
		forbidCloudSourcePrompt(t)
		stdout, err := server.apply(t, s3OrdersPrefixManifest, "-o", "json", "--yes")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		var result cloud_sources.ApplyResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("stdout is not one JSON result: %v\n%s", err, stdout)
		}
		if result.Action != "updated" {
			t.Errorf("action = %q, want updated", result.Action)
		}
		if writes := server.writes(); !reflect.DeepEqual(writes, []string{"PATCH /api/orgs/" + testOrgID + "/data-sources/42"}) {
			t.Errorf("writes = %v, want exactly one PATCH of the source", writes)
		}
	})
}

// With no terminal to answer the prompt, an update that changes something
// needs --yes: exit 2, and nothing sent.
func TestApplyCloudSourceNonInteractiveUpdateRequiresYes(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("stdin is a terminal; CanPrompt() cannot be made false here")
	}
	server := newCloudSourceServer(t, sampleCloudSourceFull())
	cmdutil.ResetReader()
	t.Cleanup(cmdutil.ResetReader)

	_, err := server.apply(t, s3OrdersPrefixManifest)
	if err == nil {
		t.Fatal("a changed update succeeded with no terminal and no --yes")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the message must name --yes; got %q", err)
	}
	if writes := server.writes(); len(writes) > 0 {
		t.Errorf("an unconfirmed apply sent %v", writes)
	}
}

// A source's table and type cannot change: an existing source that differs
// from the manifest in either is refused with exit 2 and nothing is written,
// though --yes is given and the manifest changes a field.
func TestApplyCloudSourceRefusesAnIdentityChange(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*cloud_sources.CloudSourceFull)
	}{
		{"dataset_id", func(cs *cloud_sources.CloudSourceFull) {
			other := 12
			cs.DatasetID = &other
		}},
		{"data_source_type", func(cs *cloud_sources.CloudSourceFull) {
			other := api.DataSourceType("gs")
			cs.DataSourceType = &other
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			existing := sampleCloudSourceFull()
			tc.edit(existing)
			server := newCloudSourceServer(t, existing)
			forbidCloudSourcePrompt(t)

			_, err := server.apply(t, s3OrdersPrefixManifest, "--yes")
			if err == nil {
				t.Fatalf("a manifest that changes %s was applied", tc.name)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			if writes := server.writes(); len(writes) > 0 {
				t.Errorf("a refused apply sent %v", writes)
			}
		})
	}
}

// A manifest carrying spec.dropbox_access_token is refused before any request,
// in YAML and JSON alike, with a message naming the key that replaced it. The
// same manifest with the key renamed is created, so the refusal is the key's.
func TestApplyCloudSourceRefusesTheLegacyDropboxKey(t *testing.T) {
	yamlManifest := func(key string) string {
		return `
apiVersion: qluster.ai/v1
kind: CloudSource
metadata:
  name: dropbox-orders
spec:
  dataset_id: 11
  data_source_type: dropbox
  dropbox_folder: /Exports
  ` + key + `: an-oauth-code
`
	}
	jsonManifest := func(key string) string {
		return `{"apiVersion": "qluster.ai/v1", "kind": "CloudSource", "metadata": {"name": "dropbox-orders"},
 "spec": {"dataset_id": 11, "data_source_type": "dropbox", "dropbox_folder": "/Exports", "` + key + `": "an-oauth-code"}}`
	}
	for _, tc := range []struct {
		name     string
		manifest func(key string) string
	}{
		{"yaml", yamlManifest},
		{"json", jsonManifest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newCloudSourceServer(t, nil)
			_, err := server.apply(t, tc.manifest("dropbox_access_token"))
			if err == nil {
				t.Fatal("a manifest carrying spec.dropbox_access_token was applied")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			if msg := err.Error(); !strings.Contains(msg, "spec.dropbox_authorization_code") {
				t.Errorf("the refusal does not name the key that replaced spec.dropbox_access_token: %v", err)
			}
			if sent := server.mock.Requests(); len(sent) != 0 {
				t.Errorf("a refused manifest sent %v", sent)
			}

			control := newCloudSourceServer(t, nil)
			if _, err := control.apply(t, tc.manifest("dropbox_authorization_code")); err != nil {
				t.Fatalf("control: the manifest with the key renamed must apply: %v", err)
			}
			if writes := control.writes(); len(writes) != 1 {
				t.Errorf("control: want one create, got %v", writes)
			}
		})
	}
}

// applyCapturing runs `apply cloud-source -f` on manifestYAML, written to a
// file, with args appended, and returns that file's path, stdout and stderr.
func (s *cloudSourceServer) applyCapturing(t *testing.T, manifestYAML string, args ...string) (path, stdout, stderr string, err error) {
	t.Helper()
	root := setupApplyRoot()
	root.SilenceErrors = true
	root.SilenceUsage = true
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	path = s.env.CreateFile("cs.yaml", manifestYAML)
	root.SetArgs(append([]string{"apply", "cloud-source", "-f", path}, args...))
	err = root.Execute()
	return path, out.String(), errOut.String(), err
}

// draftCloudSourceServer is newCloudSourceServer with no cloud source, whose
// POST answers with a draft.
func draftCloudSourceServer(t *testing.T) *cloudSourceServer {
	t.Helper()
	server := newCloudSourceServer(t, nil)
	reply := sampleCloudSourceFull()
	draft := api.DataSourceStateDraft
	reply.State = &draft
	server.mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/data-sources", server.record(t, reply))
	return server
}

// A draft create prints the created line and then a Next: line naming the
// manifest, re-applying it and enabling the created source, in that order.
// Under -o json the Next: line goes to stderr and stdout stays one result.
// An active create prints no Next: line.
func TestApplyCloudSourceDraftCreatePrintsNext(t *testing.T) {
	draftManifest := s3OrdersManifest("  state: draft\n")
	wantNext := func(path string) string {
		return "Next: remove spec.state from " + path + ", add any secrets the source needs, and run 'qctl apply -f " +
			path + "'; then turn it on with 'qctl enable cloud-source 42'\n"
	}

	t.Run("table", func(t *testing.T) {
		server := draftCloudSourceServer(t)
		path, stdout, _, err := server.applyCapturing(t, draftManifest)
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if want := "cloud-source/s3-orders created\n" + wantNext(path); stdout != want {
			t.Errorf("stdout =\n%q\nwant\n%q", stdout, want)
		}
		if got := string(server.bodies[0]["state"]); got != `"draft"` {
			t.Errorf("POST state = %s, want \"draft\"", got)
		}
	})

	t.Run("json", func(t *testing.T) {
		server := draftCloudSourceServer(t)
		path, stdout, stderr, err := server.applyCapturing(t, draftManifest, "-o", "json")
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		var result cloud_sources.ApplyResult
		if err := json.Unmarshal([]byte(stdout), &result); err != nil {
			t.Fatalf("stdout is not one JSON result: %v\n%s", err, stdout)
		}
		if result.Action != "created" || result.ID != 42 || result.State != "draft" {
			t.Errorf("result = %+v, want a draft created as 42", result)
		}
		if !strings.Contains(stderr, "cloud-source/s3-orders created\n"+wantNext(path)) {
			t.Errorf("stderr does not carry the created and Next: lines:\n%s", stderr)
		}
	})

	t.Run("active", func(t *testing.T) {
		server := newCloudSourceServer(t, nil)
		_, stdout, stderr, err := server.applyCapturing(t, s3OrdersManifest(""))
		if err != nil {
			t.Fatalf("apply: %v", err)
		}
		if stdout != "cloud-source/s3-orders created\n" || strings.Contains(stderr, "Next:") {
			t.Errorf("an active create printed stdout %q, stderr %q; want the created line alone", stdout, stderr)
		}
		if raw, ok := server.bodies[0]["state"]; ok {
			t.Errorf("an active create sent state = %s", raw)
		}
	})
}

// A draft create needs no credentials, for an sftp source and a dropbox
// source alike; TestApplyCloudSourceRequiresCredentialsOnlyOnCreate is the
// control that the same manifests without spec.state exit 2.
func TestApplyCloudSourceDraftCreateNeedsNoCredentials(t *testing.T) {
	for _, tc := range []struct {
		sourceType, fields string
	}{
		{"sftp", "  sftp_host: sftp.example.com\n  sftp_user: user\n"},
		{"dropbox", "  dropbox_folder: /Exports\n"},
	} {
		t.Run(tc.sourceType, func(t *testing.T) {
			manifest := "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: source\nspec:\n  dataset_id: 11\n  data_source_type: " +
				tc.sourceType + "\n" + tc.fields

			control := newCloudSourceServer(t, nil)
			if _, err := control.apply(t, manifest); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("control: an active create without credentials must exit 2, got %v", err)
			}

			server := draftCloudSourceServer(t)
			if _, err := server.apply(t, manifest+"  state: draft\n"); err != nil {
				t.Fatalf("draft create without credentials: %v", err)
			}
			if writes := server.writes(); !reflect.DeepEqual(writes, []string{"POST /api/orgs/" + testOrgID + "/data-sources"}) {
				t.Fatalf("writes = %v, want one POST", writes)
			}
			if got := string(server.bodies[0]["state"]); got != `"draft"` {
				t.Errorf("POST state = %s, want \"draft\"", got)
			}
		})
	}
}

// spec.state on an existing source exits 2 and writes nothing, though --yes
// is given and the manifest changes a field: draft names the state commands,
// and any other value is a manifest error.
func TestApplyCloudSourceUpdateRefusesSpecState(t *testing.T) {
	t.Run("control", func(t *testing.T) {
		server := newCloudSourceServer(t, sampleCloudSourceFull())
		forbidCloudSourcePrompt(t)
		if _, err := server.apply(t, s3OrdersPrefixManifest, "--yes"); err != nil {
			t.Fatalf("the manifest without spec.state must update: %v", err)
		}
		if writes := server.writes(); len(writes) != 1 {
			t.Fatalf("the manifest without spec.state must send one PATCH, sent %v", writes)
		}
	})
	for _, tc := range []struct {
		state string
		want  []string
	}{
		{"draft", []string{"remove spec.state", "qctl enable cloud-source 42", "qctl disable cloud-source 42"}},
		{"active", []string{"spec.state", "qctl enable cloud-source <id>"}},
	} {
		t.Run(tc.state, func(t *testing.T) {
			server := newCloudSourceServer(t, sampleCloudSourceFull())
			forbidCloudSourcePrompt(t)
			_, err := server.apply(t, s3OrdersPrefixManifest+"  state: "+tc.state+"\n", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d: %v", code, qerrors.ExitBadUsage, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not say %q: %v", want, err)
				}
			}
			if writes := server.writes(); len(writes) > 0 {
				t.Errorf("a refused apply sent %v", writes)
			}
		})
	}
}
