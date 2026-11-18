package copycmd_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qctlroot "github.com/qlustered/qctl/internal/commands/root"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const cloudCopyOrg = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	cloudCopyBase     = "/api/orgs/" + cloudCopyOrg
	cloudCopyTiny     = cloudCopyBase + "/data-sources/12/tiny"
	cloudCopyTemplate = cloudCopyBase + "/datasets/42/data-sources/12/copy"
	cloudCopyList     = cloudCopyBase + "/data-sources"
	cloudCopyPOST     = cloudCopyBase + "/data-sources/12/copy"
)

const cloudCopyResult = `{"id":57,"name":"orders-copy","dataset_id":42,"settings_model_id":9,"state":"draft","data_source_type":"s3","nullable":null,"future":{"integer":9007199254740993},"s3_secret_key":"•••••••"}`

type cloudCopyEnv struct {
	mock       *testutil.MockAPIServer
	tiny       map[string]any
	template   map[string]any
	postBodies []string
	postStatus int
	postBody   string
}

func newCloudCopyEnv(t *testing.T) *cloudCopyEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	for _, key := range []string{"QCTL_SERVER", "QCTL_ORG", "QCTL_KINDE_HOST", "QCTL_KINDE_CLIENT_ID"} {
		t.Setenv(key, "")
	}
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpoint, _ := config.NormalizeEndpointKey(mock.URL())
	te.SetupConfigWithOrg(mock.URL(), "", cloudCopyOrg)
	te.SetupCredential(endpoint, cloudCopyOrg, "test-token")
	e := &cloudCopyEnv{
		mock: mock,
		tiny: map[string]any{"id": 12, "name": "orders-s3", "dataset_id": 42, "dataset_name": "orders", "data_source_type": "s3", "state": "draft"},
		template: map[string]any{
			"id": nil, "name": "orders-s3abcd", "dataset_id": 42, "settings_model_id": 9,
			"dataset_name": "orders", "secrets_to_resupply": []string{"s3_secret_key"},
			"state": "active", "data_source_type": "s3", "s3_bucket": "orders", "s3_prefix": "us/",
			"s3_access_key": "credential-id", "s3_secret_key": "•••••••", "sftp_password": "•••••••",
			"schedule": nil, "infer_datetime_override": false, "delta_applied_cursor": 5,
		},
		postStatus: http.StatusOK, postBody: cloudCopyResult,
	}
	mock.RegisterHandler("GET", cloudCopyTiny, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, e.tiny)
	})
	mock.RegisterHandler("GET", cloudCopyList, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 0, "results": []any{}})
	})
	e.registerTemplate(cloudCopyTemplate)
	mock.RegisterHandler("POST", cloudCopyPOST, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		e.postBodies = append(e.postBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.postStatus)
		_, _ = io.WriteString(w, e.postBody)
	})
	return e
}

func (e *cloudCopyEnv) registerTemplate(path string) {
	e.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, e.template)
	})
}

func resetCloudCopyFlags(t *testing.T, cmd *cobra.Command) {
	t.Helper()
	reset := func(f *pflag.Flag) {
		if !f.Changed {
			return
		}
		var err error
		if value, ok := f.Value.(pflag.SliceValue); ok {
			err = value.Replace(nil)
		} else {
			err = f.Value.Set(f.DefValue)
		}
		if err != nil {
			t.Errorf("reset --%s: %v", f.Name, err)
		}
		f.Changed = false
	}
	cmd.Flags().Visit(reset)
	cmd.PersistentFlags().Visit(reset)
	for _, child := range cmd.Commands() {
		resetCloudCopyFlags(t, child)
	}
}

func runCloudCopy(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	return runCloudCopyIn(t, nil, args...)
}

// runCloudCopyIn runs copy cloud-source with args, reading stdin when it is
// not nil.
func runCloudCopyIn(t *testing.T, stdin io.Reader, args ...string) (string, string, error) {
	t.Helper()
	cmd := qctlroot.GetRootCmd()
	resetCloudCopyFlags(t, cmd)
	defer resetCloudCopyFlags(t, cmd)
	var out, stderr bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&stderr)
	if stdin != nil {
		cmd.SetIn(stdin)
	}
	cmd.SetArgs(append([]string{"copy", "cloud-source"}, args...))
	defer func() {
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		cmd.SetIn(nil)
		cmd.SetArgs(nil)
		config.SetPathOverride("")
		config.SetContextOverride("")
	}()
	err := cmd.Execute()
	return out.String(), stderr.String(), err
}

func assertCloudCopyRequests(t *testing.T, e *cloudCopyEnv, want ...string) {
	t.Helper()
	if got := e.mock.Requests(); !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %v, want %v", got, want)
	}
}

// cloudCopySecret is the secret value the cloud copy tests supply.
const cloudCopySecret = "supplied-secret-value"

// cloudCopySecretsFile is a --secrets-file supplying the template's only
// secret to resupply.
const cloudCopySecretsFile = "s3_secret_key: " + cloudCopySecret + "\n"

// writeCloudSecretsFile writes content to a new --secrets-file and returns its
// path.
func writeCloudSecretsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// cloudCopyPOSTBody decodes the one POST body e received.
func cloudCopyPOSTBody(t *testing.T, e *cloudCopyEnv) map[string]json.RawMessage {
	t.Helper()
	var body map[string]json.RawMessage
	if len(e.postBodies) != 1 || json.Unmarshal([]byte(e.postBodies[0]), &body) != nil {
		t.Fatalf("got %d POST bodies, want one JSON object", len(e.postBodies))
	}
	return body
}

// assertNoCloudCopySecret fails when a value appears in the output or the
// error.
func assertNoCloudCopySecret(t *testing.T, out, stderr string, err error, values ...string) {
	t.Helper()
	text := out + stderr
	if err != nil {
		text += err.Error()
	}
	for _, value := range append([]string{cloudCopySecret}, values...) {
		if strings.Contains(text, value) {
			t.Fatal("a secret value appeared in the output or the error")
		}
	}
}

// checkTemplateControl reruns a success case without its template route.
func checkTemplateControl(t *testing.T, e *cloudCopyEnv, path string, args ...string) {
	t.Helper()
	handler := e.mock.Handlers["GET "+path]
	delete(e.mock.Handlers, "GET "+path)
	defer e.mock.RegisterHandler("GET", path, handler)
	posts := len(e.postBodies)
	out, _, err := runCloudCopy(t, args...)
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Fatalf("unregistered template: error = %v, want exit 3", err)
	}
	if out != "" || len(e.postBodies) != posts {
		t.Fatalf("unregistered template produced output or sent a POST: %q, %v", out, e.postBodies)
	}
}

func TestCopyCloudSourceLocalRefusals(t *testing.T) {
	cases := [][]string{
		nil, {"12", "13"}, {""}, {"orders-s3"}, {"0"}, {"-1"}, {"table/12"},
		{"12", "--to-table", "orders", "--to-table-id", "42"},
		{"12", "--to-table", ""}, {"12", "--to-table-id", "0"}, {"12", "--to-table-id", "-1"},
		{"12", "--name", ""}, {"12", "--state", ""}, {"12", "--state", "deleted"}, {"12", "--state", "bogus"},
		{"12", "--set", ""}, {"12", "--set", "pattern"}, {"12", "--set", "unknown=x"},
		{"12", "--set", "sftp_port=oops"}, {"12", "--set", "sftp_port="},
		{"12", "--set", "s3-prefix=a", "--set", "s3_prefix=b"},
		{"12", "--set", "name=new"}, {"12", "--set", "settings_model_id=9"},
		{"12", "--set", "s3_secret_key=sensitive-value"}, {"12", "--set", "s3_access_key=sensitive-value"},
		{"12", "--dry-run", "--yes"}, {"12", "--dry-run", "-o", "name"},
		{"12", "--secrets-file", ""},
		{"12", "-o", "bogus"}, {"12", "--no-such-flag"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newCloudCopyEnv(t)
			// No configured server or credential: local errors must win over Bootstrap.
			configPath := t.TempDir() + "/absent"
			args = append(append([]string(nil), args...), "--config", configPath)
			out, stderr, err := runCloudCopy(t, args...)
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			assertCloudCopyRequests(t, e)
			if strings.Contains(err.Error()+out+stderr, "sensitive-value") {
				t.Fatal("credential appeared in diagnostic")
			}
		})
	}
}

func TestCopyCloudSourceSourceRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, state, kind, want string
		args                    []string
	}{
		{"deleted", "deleted", "s3", "deleted", nil},
		{"upload", "draft", "qluster_ui", "qluster_ui", nil},
		{"composite", "disabled", "composite", "composite", nil},
		{"unchanged active", "active", "s3", "--set", nil},
		{"active state override", "active", "s3", "--set", []string{"--state", "draft", "--name", "new"}},
		{"active different target", "active", "s3", "--set", []string{"--to-table", "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"], e.tiny["data_source_type"] = tc.state, tc.kind
			out, _, err := runCloudCopy(t, append([]string{"12", "--yes"}, tc.args...)...)
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want exit 2 containing %q", err, tc.want)
			}
			if out != "" {
				t.Errorf("refusal stdout = %q", out)
			}
			assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny)
		})
	}
}

func TestCopyCloudSourceSameTable(t *testing.T) {
	for _, state := range []string{"active", "draft", "disabled"} {
		t.Run(state, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = state
			args := []string{"cloud-source/12", "--set", "s3-prefix=eu/", "--set", "schedule=@hourly", "--set", "sftp-port=2222",
				"--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile), "--yes"}
			out, stderr, err := runCloudCopy(t, args...)
			if err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			assertNoCloudCopySecret(t, out, stderr, nil)
			if !strings.HasPrefix(out, "Copy cloud source orders-s3 (12)") || !strings.HasSuffix(out, "Created cloud source 57 (orders-copy).\nNext: qctl describe cloud-source 57\n") || stderr != "" {
				t.Errorf("stdout = %q, stderr = %q", out, stderr)
			}
			wantRequests := []string{"GET " + cloudCopyTiny, "GET " + cloudCopyTemplate}
			if state == "active" {
				wantRequests = append(wantRequests, "GET "+cloudCopyList)
			}
			assertCloudCopyRequests(t, e, append(wantRequests, "POST "+cloudCopyPOST)...)
			body := cloudCopyPOSTBody(t, e)
			for key, want := range map[string]string{
				"dataset_id": "42", "settings_model_id": "9", "name": `"orders-s3abcd"`,
				"state": strconv.Quote(state), "s3_prefix": `"eu/"`, "schedule": `"@hourly"`,
				"sftp_port": "2222", "infer_datetime_override": "false",
				// The template's access-key ID is forwarded; the secret is the file's.
				"s3_access_key": `"credential-id"`,
			} {
				if string(body[key]) != want {
					t.Errorf("body[%s] = %s, want %s", key, body[key], want)
				}
			}
			if string(body["s3_secret_key"]) != strconv.Quote(cloudCopySecret) {
				t.Error("body s3_secret_key is not the supplied secret")
			}
			for _, key := range []string{"id", "sftp_password", "delta_applied_cursor"} {
				if _, exists := body[key]; exists {
					t.Errorf("body contains %s", key)
				}
			}
			checkTemplateControl(t, e, cloudCopyTemplate, args...)
		})
	}
}

func TestCopyCloudSourceTargetAndOverrides(t *testing.T) {
	for _, target := range [][]string{{"--to-table", "orders_eu"}, {"--to-table", "42"}, {"--to-table-id", "table/84"}} {
		t.Run(strings.Join(target, " "), func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.template["dataset_id"], e.template["settings_model_id"] = 84, 31
			path := cloudCopyBase + "/datasets/84/data-sources/12/copy"
			e.registerTemplate(path)
			name := target[1]
			e.mock.RegisterHandler("GET", cloudCopyBase+"/datasets", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("name") != name {
					t.Errorf("table lookup query = %v", r.URL.Query())
				}
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 1, "page": 1, "results": []any{map[string]any{"id": 84, "name": name}}})
			})
			args := append([]string{"12", "--name", "eu-copy", "--state", "disabled", "--set", "pattern=",
				"--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile), "--yes"}, target...)
			if _, stderr, err := runCloudCopy(t, args...); err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			wantRequests := []string{"GET " + cloudCopyTiny}
			if target[0] == "--to-table" {
				wantRequests = append(wantRequests, "GET "+cloudCopyBase+"/datasets")
			}
			assertCloudCopyRequests(t, e, append(wantRequests, "GET "+path, "POST "+cloudCopyPOST)...)
			var body map[string]any
			if len(e.postBodies) != 1 || json.Unmarshal([]byte(e.postBodies[0]), &body) != nil {
				t.Fatalf("POST bodies = %v", e.postBodies)
			}
			for key, want := range map[string]any{"dataset_id": float64(84), "settings_model_id": float64(31), "name": "eu-copy", "state": "disabled", "pattern": ""} {
				if body[key] != want {
					t.Errorf("body[%s] = %v, want %v", key, body[key], want)
				}
			}
			checkTemplateControl(t, e, path, args...)
		})
	}
}

// cloudCopyResultWithKey is a copy response carrying an access-key ID in
// clear, as the server returns one, and a secret nested in an unknown member.
const cloudCopyResultWithKey = `{"id":57,"name":"orders-copy","state":"draft","data_source_type":"s3","nullable":null,` +
	`"future":{"integer":9007199254740993,"ratio":1.50,"password":"nested-secret"},` +
	`"s3_access_key":"returned-key-id","s3_secret_key":"","simple_url":"","secrets_to_resupply":[]}`

// After the copy, -o json|yaml prints the response with every secret field
// masked, unless --allow-plaintext-secrets is set; -o name prints its id.
func TestCopyCloudSourceStructuredAndNameOutput(t *testing.T) {
	masked := strings.NewReplacer(`"returned-key-id"`, `"***"`, `"s3_secret_key":""`, `"s3_secret_key":"***"`,
		`"nested-secret"`, `"***"`).Replace(cloudCopyResultWithKey)
	for _, tc := range []struct {
		format    string
		plaintext bool
	}{{"json", false}, {"json", true}, {"yaml", false}, {"yaml", true}, {"name", false}} {
		t.Run(fmt.Sprintf("%s/plaintext=%v", tc.format, tc.plaintext), func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.postBody = cloudCopyResultWithKey
			args := []string{"12", "--yes", "-o", tc.format}
			if tc.plaintext {
				args = append(args, "--allow-plaintext-secrets")
			}
			out, stderr, err := runCloudCopy(t, args...)
			if err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			want := masked
			if tc.plaintext {
				want = cloudCopyResultWithKey
			}
			switch tc.format {
			case "name":
				if out != "cloud-source/57\n" {
					t.Errorf("name output = %q", out)
				}
			case "yaml":
				testutil.AssertYAMLReadsAsJSON(t, out, want)
			case "json":
				if !reflect.DeepEqual(decodeJSONNumbers(t, out), decodeJSONNumbers(t, want)) {
					t.Errorf("JSON output = %s, want %s", out, want)
				}
			}
			// Harness-alive: the response holds the access key in clear, and
			// only --allow-plaintext-secrets shows it.
			if strings.Contains(out, "returned-key-id") != tc.plaintext {
				t.Errorf("access key shown = %v, want %v: %s", !tc.plaintext, tc.plaintext, out)
			}
			if !strings.HasPrefix(stderr, "Copy cloud source orders-s3 (12)") {
				t.Errorf("plan stderr = %q", stderr)
			}
			checkTemplateControl(t, e, cloudCopyTemplate, args...)
		})
	}
}

// The default view names the new source. A draft whose response lists secrets
// to resupply gets the commands that add them and enable it; any other copy
// gets the secrets it lacks, if any, and the describe command. The same copy
// gets each view from its response alone.
func TestCopyCloudSourceResultView(t *testing.T) {
	for _, tc := range []struct{ name, response, want string }{
		{
			name:     "lacks nothing",
			response: `{"id":57,"name":"orders\u001bcopy","state":"draft","secrets_to_resupply":[]}`,
			want:     "Created cloud source 57 (orders\\x1bcopy).\nNext: qctl describe cloud-source 57\n",
		},
		{
			name:     "no list",
			response: `{"id":57,"name":"orders-copy","state":"draft"}`,
			want:     "Created cloud source 57 (orders-copy).\nNext: qctl describe cloud-source 57\n",
		},
		{
			name:     "draft lacking secrets",
			response: `{"id":57,"name":"orders-copy","state":"draft","secrets_to_resupply":["s3_secret_key","sftp_password"]}`,
			want: "Created cloud source 57 (orders-copy).\nNext: cloud source 57 is a draft without s3_secret_key, sftp_password: " +
				"add them to the spec of 'qctl get cloud-source 57 -o yaml', apply that with 'qctl apply -f', then run 'qctl enable cloud-source 57'\n",
		},
		{
			name:     "disabled lacking a secret",
			response: `{"id":57,"name":"orders-copy","state":"disabled","secrets_to_resupply":["s3_secret_key\u001b[2J"]}`,
			want: "Created cloud source 57 (orders-copy).\nSecrets to resupply: s3_secret_key\\x1b[2J\n" +
				"Next: qctl describe cloud-source 57\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.postBody = tc.response
			out, stderr, err := runCloudCopy(t, "12", "--set", "s3_prefix=eu/", "--yes")
			if err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			if !strings.HasPrefix(out, "Copy cloud source orders-s3 (12)") || !strings.HasSuffix(out, tc.want) {
				t.Errorf("stdout = %q, want a plan ending in %q", out, tc.want)
			}
		})
	}
}

func TestCopyCloudSourceConfirmation(t *testing.T) {
	for _, answer := range []string{"yes\n", "no\n", "non-terminal"} {
		t.Run(answer, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			if answer == "non-terminal" {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				original := os.Stdin
				os.Stdin = reader
				t.Cleanup(func() { os.Stdin = original; reader.Close(); writer.Close() })
				cmdutil.ResetReader()
			} else {
				cmdutil.SetReader(strings.NewReader(answer))
			}
			t.Cleanup(cmdutil.ResetReader)
			out, stderr, err := runCloudCopy(t, "12")
			if answer == "yes\n" {
				if err != nil || !strings.Contains(out, "Created cloud source 57") || !strings.Contains(stderr, "[y/N]") {
					t.Fatalf("confirmed: output = %q, stderr = %q, error = %v", out, stderr, err)
				}
				cmdutil.SetReader(strings.NewReader(answer))
				checkTemplateControl(t, e, cloudCopyTemplate, "12")
				return
			}
			want := qerrors.ExitGenericError
			if answer == "non-terminal" {
				want = qerrors.ExitBadUsage
			}
			if err == nil || qerrors.GetExitCode(err) != want || !strings.HasPrefix(out, "Copy cloud source orders-s3 (12)") {
				t.Fatalf("confirmation: output = %q, error = %v, want exit %d", out, err, want)
			}
			assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny, "GET "+cloudCopyTemplate)
			if len(e.postBodies) != 0 {
				t.Fatal("unconfirmed copy sent a POST")
			}
		})
	}
}

func TestCopyCloudSourceServerRefusalPreservesExitCode(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = "active"
			e.postStatus = status
			e.postBody = `{"detail":{"msg":"Another active source already reads s3_bucket orders with the same pattern and s3_prefix","loc":["body","s3_prefix"]}}`
			out, _, err := runCloudCopy(t, "12", "--set", "schedule=@hourly",
				"--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile), "--yes")
			want := map[int]int{400: qerrors.ExitBadUsage, 403: qerrors.ExitUnauthorized, 404: qerrors.ExitNotFound}[status]
			if err == nil || qerrors.GetExitCode(err) != want || !strings.HasPrefix(out, "Copy cloud source orders-s3 (12)") {
				t.Fatalf("server refusal: output = %q, error = %v, want exit %d", out, err, want)
			}
			assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny, "GET "+cloudCopyTemplate, "GET "+cloudCopyList, "POST "+cloudCopyPOST)
		})
	}
}

// A malformed --secrets-file exits 2 before any request and shows no value.
// The same copy with a well-formed file reaches the POST.
func TestCopyCloudSourceSecretsFileRefusals(t *testing.T) {
	unsetEnv, emptyEnv := "QCTL_CLOUD_COPY_TEST_UNSET", "QCTL_CLOUD_COPY_TEST_EMPTY"
	t.Setenv(unsetEnv, "")
	if err := os.Unsetenv(unsetEnv); err != nil {
		t.Fatal(err)
	}
	t.Setenv(emptyEnv, "")
	for _, tc := range []struct{ name, file string }{
		{"empty file", ""},
		{"empty mapping", "{}\n"},
		{"two documents", cloudCopySecretsFile + "---\nsftp_password: " + cloudCopySecret + "\n"},
		{"a list", "- " + cloudCopySecretsFile},
		{"a scalar", cloudCopySecret + "\n"},
		{"a non-credential field", "s3_prefix: eu/\n" + cloudCopySecretsFile},
		{"dropbox_access_token", "dropbox_access_token: " + cloudCopySecret + "\n"},
		{"duplicate key", cloudCopySecretsFile + cloudCopySecretsFile},
		{"a mapping value", "s3_secret_key: {value: " + cloudCopySecret + "}\n"},
		{"empty value", `s3_secret_key: ""` + "\n"},
		{"mask character", "s3_secret_key: " + cloudCopySecret + "•\n"},
		{"unset ${VAR}", "s3_secret_key: '${" + unsetEnv + "}'\n"},
		{"empty ${VAR}", "s3_secret_key: '${" + emptyEnv + "}'\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = "active"
			args := []string{"12", "--set", "s3_prefix=eu/", "--yes"}
			out, stderr, err := runCloudCopy(t, append(args, "--secrets-file", writeCloudSecretsFile(t, tc.file))...)
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if out != "" {
				t.Errorf("refusal stdout = %q", out)
			}
			assertNoCloudCopySecret(t, out, stderr, err)
			assertCloudCopyRequests(t, e)
			if _, stderr, err := runCloudCopy(t, append(args, "--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile))...); err != nil {
				t.Fatalf("control: %v; stderr: %s", err, stderr)
			}
			if len(e.postBodies) != 1 {
				t.Fatalf("control sent %d POSTs, want 1", len(e.postBodies))
			}
		})
	}
}

// --secrets-file - reads the mapping from stdin, and refuses a malformed one
// before any request.
func TestCopyCloudSourceSecretsFileFromStdin(t *testing.T) {
	e := newCloudCopyEnv(t)
	e.tiny["state"] = "disabled"
	args := []string{"12", "--secrets-file", "-", "--yes"}
	out, stderr, err := runCloudCopyIn(t, strings.NewReader("s3_prefix: "+cloudCopySecret+"\n"), args...)
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("malformed stdin: error = %v, want exit 2", err)
	}
	assertNoCloudCopySecret(t, out, stderr, err)
	assertCloudCopyRequests(t, e)
	out, stderr, err = runCloudCopyIn(t, strings.NewReader(cloudCopySecretsFile), args...)
	if err != nil {
		t.Fatalf("copy: %v; stderr: %s", err, stderr)
	}
	assertNoCloudCopySecret(t, out, stderr, nil)
	if string(cloudCopyPOSTBody(t, e)["s3_secret_key"]) != strconv.Quote(cloudCopySecret) {
		t.Error("the POST does not carry the secret read from stdin")
	}
}

// Each supplied value reaches the POST as given, or as read from the
// environment for an exact ${VAR}, and no credential is sent that the file
// leaves out, other than the template's s3_access_key.
func TestCopyCloudSourceSuppliedSecretsReachThePOST(t *testing.T) {
	const env = "QCTL_CLOUD_COPY_TEST_SECRET"
	t.Setenv(env, cloudCopySecret)
	for _, tc := range []struct {
		name, file string
		want       map[string]string
	}{
		{
			name: "replaced access key",
			file: "s3_secret_key: '${" + env + "}'\ns3_access_key: replacement-key-id\ngpg_passphrase: literal-passphrase\nsftp_password: null\n",
			want: map[string]string{"s3_secret_key": cloudCopySecret, "s3_access_key": "replacement-key-id", "gpg_passphrase": "literal-passphrase"},
		},
		{
			name: "template's access key",
			file: "s3_secret_key: '${" + env + "}'\ngpg_passphrase: literal-passphrase\n",
			want: map[string]string{"s3_secret_key": cloudCopySecret, "s3_access_key": "credential-id", "gpg_passphrase": "literal-passphrase"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = "active"
			e.template["secrets_to_resupply"] = []string{"s3_secret_key", "gpg_passphrase"}
			out, stderr, err := runCloudCopy(t, "12", "--set", "s3_prefix=eu/", "--secrets-file", writeCloudSecretsFile(t, tc.file), "--yes")
			if err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			assertNoCloudCopySecret(t, out, stderr, nil, "replacement-key-id", "literal-passphrase", "credential-id")
			body := cloudCopyPOSTBody(t, e)
			for _, key := range cloud_sources.CopyRequestSecretKeys() {
				want, supplied := tc.want[key]
				got, sent := body[key]
				switch {
				case sent != supplied:
					t.Errorf("body has %s: %v, want %v", key, sent, supplied)
				case supplied && string(got) != strconv.Quote(want):
					t.Errorf("body %s is not the value wanted", key)
				}
			}
			if _, sent := body["sftp_password"]; sent {
				t.Error("body has sftp_password, which the file gives as null")
			}
		})
	}
}

// An active or disabled copy lacking a secret the source holds is refused
// with exit 2 after the plan and before the prompt, the --dry-run document
// and the POST. Supplying that secret lets the same copy through.
func TestCopyCloudSourceRefusesMissingSecrets(t *testing.T) {
	for _, origin := range []struct {
		name, state string
		args        []string
	}{
		{"active", "active", nil},
		{"disabled", "disabled", nil},
		{"draft original, --state disabled", "draft", []string{"--state", "disabled"}},
	} {
		for _, mode := range [][]string{{"--yes"}, {"--yes", "-o", "json"}, {"--dry-run"}, {"--dry-run", "-o", "json"}, {"--dry-run", "-o", "yaml"}} {
			t.Run(origin.name+" "+strings.Join(mode, " "), func(t *testing.T) {
				e := newCloudCopyEnv(t)
				e.tiny["state"] = origin.state
				e.template["secrets_to_resupply"] = []string{"s3_secret_key", "sftp_password"}
				args := slices.Concat([]string{"12", "--set", "s3_prefix=eu/"}, origin.args, mode)
				partial := writeCloudSecretsFile(t, "sftp_password: "+cloudCopySecret+"\n")
				out, stderr, err := runCloudCopy(t, append(args, "--secrets-file", partial)...)
				if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
					t.Fatalf("error = %v, want exit 2", err)
				}
				for _, want := range []string{"lacks s3_secret_key;", "--secrets-file", "--state draft"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("refusal %q lacks %q", err, want)
					}
				}
				if strings.Contains(err.Error(), "sftp_password") {
					t.Errorf("refusal names a supplied secret as missing: %q", err)
				}
				assertNoCloudCopySecret(t, out, stderr, err)
				wantRequests := []string{"GET " + cloudCopyTiny, "GET " + cloudCopyTemplate}
				if origin.state == "active" {
					wantRequests = append(wantRequests, "GET "+cloudCopyList)
				}
				assertCloudCopyRequests(t, e, wantRequests...)
				plan := out
				if len(mode) > 1 {
					plan = stderr
					if out != "" {
						t.Errorf("stdout = %q, want nothing", out)
					}
				}
				if !strings.HasPrefix(plan, "Copy cloud source orders-s3 (12)") || strings.Contains(out, "Dry run:") || strings.Contains(stderr, "[y/N]") {
					t.Errorf("want the plan and nothing after it: stdout %q, stderr %q", out, stderr)
				}
				full := writeCloudSecretsFile(t, "sftp_password: x\ns3_secret_key: y\n")
				if _, stderr, err := runCloudCopy(t, append(args, "--secrets-file", full)...); err != nil {
					t.Fatalf("control: %v; stderr: %s", err, stderr)
				}
				wantPOSTs := 1
				if mode[0] == "--dry-run" {
					wantPOSTs = 0
				}
				if len(e.postBodies) != wantPOSTs {
					t.Fatalf("control sent %d POSTs, want %d", len(e.postBodies), wantPOSTs)
				}
			})
		}
	}
}

// A draft copy saves without the secrets the source holds.
func TestCopyCloudSourceDraftWithoutSecretsIsPosted(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		args        []string
	}{
		{"draft original", "draft", nil},
		{"active original, --state draft", "active", []string{"--state", "draft"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = tc.state
			args := append([]string{"12", "--set", "s3_prefix=eu/", "--yes"}, tc.args...)
			if _, stderr, err := runCloudCopy(t, args...); err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			body := cloudCopyPOSTBody(t, e)
			if string(body["state"]) != `"draft"` {
				t.Errorf("body state = %s, want draft", body["state"])
			}
			if _, sent := body["s3_secret_key"]; sent {
				t.Error("body has s3_secret_key, which nothing supplied")
			}
		})
	}
}

// An active URL source copies without --set, because its copy's URL starts
// empty; the exemption is the type's, not the secret's.
func TestCopyCloudSourceActiveSimpleURLNeedsNoSet(t *testing.T) {
	const url = "https://example.com/orders.csv"
	e := newCloudCopyEnv(t)
	e.tiny["state"], e.tiny["data_source_type"] = "active", "simple_url"
	for _, key := range []string{"s3_bucket", "s3_prefix", "s3_access_key", "s3_secret_key"} {
		delete(e.template, key)
	}
	e.template["data_source_type"], e.template["secrets_to_resupply"] = "simple_url", []string{"simple_url"}
	args := []string{"12", "--secrets-file", writeCloudSecretsFile(t, "simple_url: "+url+"\n"), "--yes"}
	out, stderr, err := runCloudCopy(t, args...)
	if err != nil {
		t.Fatalf("copy: %v; stderr: %s", err, stderr)
	}
	assertNoCloudCopySecret(t, out, stderr, nil, url)
	assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny, "GET "+cloudCopyTemplate, "GET "+cloudCopyList, "POST "+cloudCopyPOST)
	if string(cloudCopyPOSTBody(t, e)["simple_url"]) != strconv.Quote(url) {
		t.Error("the POST does not carry the supplied URL")
	}

	// Without the URL, the active copy is refused for lacking it.
	_, _, err = runCloudCopy(t, "12", "--yes")
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "lacks simple_url") {
		t.Fatalf("copy without the URL: error = %v, want exit 2 naming simple_url", err)
	}
	if len(e.postBodies) != 1 {
		t.Fatal("the copy without the URL sent a POST")
	}

	// The same copy of an S3 source is refused for changing nothing.
	e.tiny["data_source_type"] = "s3"
	_, _, err = runCloudCopy(t, args...)
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--set") {
		t.Fatalf("S3 control: error = %v, want exit 2 naming --set", err)
	}
	if len(e.postBodies) != 1 {
		t.Fatal("the S3 control sent a POST")
	}
}

// Supplying a credential is not a location change: an active S3 original
// copied with --secrets-file and no --set is refused before its template is
// read. The same copy with --set reaches the POST.
func TestCopyCloudSourceActiveNeedsSetEvenWithSecrets(t *testing.T) {
	e := newCloudCopyEnv(t)
	e.tiny["state"] = "active"
	args := []string{"12", "--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile+"s3_access_key: other-key-id\n"), "--yes"}
	out, stderr, err := runCloudCopy(t, args...)
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--set") {
		t.Fatalf("error = %v, want exit 2 naming --set", err)
	}
	if out != "" {
		t.Errorf("refusal stdout = %q", out)
	}
	assertNoCloudCopySecret(t, out, stderr, err, "other-key-id")
	assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny)
	if _, stderr, err := runCloudCopy(t, append(args, "--set", "s3_prefix=eu/")...); err != nil {
		t.Fatalf("control: %v; stderr: %s", err, stderr)
	}
	if len(e.postBodies) != 1 {
		t.Fatalf("control sent %d POSTs, want 1", len(e.postBodies))
	}
}
