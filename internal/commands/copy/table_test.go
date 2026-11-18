package copycmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

const copyTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	copyTestPath     = "/api/orgs/" + copyTestOrgID + "/datasets/42/copy"
	copyTestGET      = "GET " + copyTestPath
	copyTestPOST     = "POST " + copyTestPath
	copyTestListPath = "/api/orgs/" + copyTestOrgID + "/datasets"
)

// copyTestOptions is the GET body in the backend's shape: settings always
// copied, every option a default, one active and one draft data source, and
// null distinguishing fields.
const copyTestOptions = `{
  "field_the_client_does_not_know": {"kept": true},
  "source_dataset_id": 42,
  "name": "Orders (2)",
  "table_name": "orders_2",
  "destination_id": 3,
  "database_name": "analytics",
  "schema_name": "public",
  "options": [
    {"option": "settings", "count": 12, "default": true, "always": true},
    {"option": "value_maps", "count": 2, "default": true, "always": false},
    {"option": "rules", "count": 5, "default": true, "always": false},
    {"option": "learned_values", "count": 0, "default": true, "always": false},
    {"option": "anomaly_model", "count": 1, "default": true, "always": false},
    {"option": "access", "count": 3, "default": true, "always": false}
  ],
  "data_sources": [
    {"id": 12, "name": "orders-s3", "data_source_type": "s3", "state": "active", "schedule": "0 * * * *",
     "location_field": "s3_bucket", "location": "acme-drop",
     "distinguishing_fields": {"pattern": "*.csv", "s3_prefix": "2026-09/", "archive_pattern": null, "excel_sheet_name": null}},
    {"id": 13, "name": "orders-sftp", "data_source_type": "sftp", "state": "draft", "schedule": null,
     "location_field": "sftp_host", "location": "sftp.example.com",
     "distinguishing_fields": {"pattern": "*.csv", "sftp_folder": "/in", "archive_pattern": null, "excel_sheet_name": null}}
  ]
}`

// copyTestResult is the POST body: the new table, with a null field, and the
// copy record, whose notes carry markdown and a line break.
const copyTestResult = `{
  "id": 57,
  "name": "Orders EU",
  "table_name": "orders_eu",
  "state": "active",
  "deleted_at": null,
  "dataset_copy": {
    "id": 5, "kind": "new_dataset", "state": "applied",
    "source_dataset_id": 42, "target_dataset_id": 57,
    "created_at": "2026-09-25T10:00:00Z",
    "options": {"include": ["access", "anomaly_model", "rules", "settings", "value_maps"]},
    "items": [
      {"id": 1, "item_type": "settings", "action": "created",
       "source_object_id": "9", "target_object_id": "31", "note": null},
      {"id": 2, "item_type": "migration_model", "action": "created",
       "source_object_id": "4", "target_object_id": "8", "note": "Rule output fields left out: **score**."},
      {"id": 3, "item_type": "data_source", "action": "created",
       "source_object_id": "12", "target_object_id": "40", "note": "Changed: s3_prefix,\nstate."}
    ]
  }
}`

// setupCopyTestCommand builds the root the real one builds, minus what this
// package does not exercise. The FlagErrorFunc and TypeArgsErrors are the real
// root's: without them an unknown flag or an extra positional exits 1 here and
// 2 in the shipped binary.
func setupCopyTestCommand() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	root.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(root)
	return root
}

// runCopy runs `qctl copy table args...` and returns its stdout and stderr.
func runCopy(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	return runCopyWithStdin(t, "", args...)
}

// runCopyWithStdin runs `qctl copy table args...` reading stdin, and returns
// its stdout and stderr.
func runCopyWithStdin(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupCopyTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"copy", "table"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// writeDataSourcesFile writes content to a file under t.TempDir and returns
// its path.
func writeDataSourcesFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sources.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type copyEnv struct {
	mock *testutil.MockAPIServer
	// postBodies holds each POST body the server received.
	postBodies []string
	// postStatus and postBody answer the POST.
	postStatus int
	postBody   string
}

// newCopyEnv serves the copy routes of table 42 and the list that resolves
// the name orders to it.
func newCopyEnv(t *testing.T) *copyEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", copyTestOrgID)
	te.SetupCredential(key, copyTestOrgID, "test-token")

	e := &copyEnv{mock: mock, postStatus: http.StatusOK, postBody: copyTestResult}
	mock.RegisterHandler("GET", copyTestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(copyTestOptions))
	})
	mock.RegisterHandler("POST", copyTestPath, func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		e.postBodies = append(e.postBodies, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.postStatus)
		_, _ = io.WriteString(w, e.postBody)
	})
	mock.RegisterHandler("GET", copyTestListPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	return e
}

// assertRequests fails unless the server saw exactly want, in order.
func (e *copyEnv) assertRequests(t *testing.T, want ...string) {
	t.Helper()
	got := e.mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
}

// assertJSONEqual fails unless got and want decode to the same value.
func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("decode %q: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode want %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s\nwant   %s", got, want)
	}
}

// requireNonInteractiveStdin skips unless prompts read a stdin that is not a
// terminal, as under go test.
func requireNonInteractiveStdin(t *testing.T) {
	t.Helper()
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	cmdutil.ResetReader()
}

// The plan in full, with the copy column of two data sources: the layout and
// the order are the contract a reader scans.
func TestCopyTable_DryRunPrintsThePlanAndPostsNothing(t *testing.T) {
	e := newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "orders", "--dry-run", "--schema-name", "eu",
		"--data-sources-file", writeDataSourcesFile(t, `
- source_id: 12
  state: draft
  s3_prefix: 2026-10/
- source_id: 13
  pattern: in,out*.csv
  name: orders-sftp-eu
`))
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	want := `Copy table orders (id 42) into a new table:
  Name:            "Orders (2)"  (suggested)
  Table name:      orders_2      (suggested)
  Destination id:  3             (source's)
  Database:        analytics     (source's)
  Schema:          eu            (--schema-name)

Options:
  OPTION          INCLUDED  DEFAULT  ALWAYS  ITEMS
  settings        yes       yes      yes     12
  value_maps      yes       yes      no      2
  rules           yes       yes      no      5
  learned_values  yes       yes      no      0
  anomaly_model   yes       yes      no      1
  access          yes       yes      no      3

Data sources:
  ID  NAME         TYPE  STATE   LOCATION                    DISTINGUISHING FIELDS                                                                 SECRETS  COPY
  12  orders-s3    s3    active  s3_bucket=acme-drop         archive_pattern=(unset), excel_sheet_name=(unset), pattern=*.csv, s3_prefix=2026-09/  -        yes: s3_prefix=2026-10/, state=draft
  13  orders-sftp  sftp  draft   sftp_host=sftp.example.com  archive_pattern=(unset), excel_sheet_name=(unset), pattern=*.csv, sftp_folder=/in     -        yes: name=orders-sftp-eu, pattern="in,out*.csv"

Dry run: nothing was copied.
`
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	e.assertRequests(t, "GET "+copyTestListPath, copyTestGET)
}

func TestCopyTable_PlanSaysWhenNoDataSourceIsCopied(t *testing.T) {
	newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--dry-run", "--include", "rules")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"Copy table 42 into a new table:",
		"  value_maps      no        yes      no      2",
		"  rules           yes       yes      no      5",
		"No data source is copied: a data source is copied only when --data-sources-file lists it.",
		"  12  orders-s3    s3    active",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q\n%s", want, stdout)
		}
	}
}

// Every flag the request carries reaches it, the data sources with exactly
// the fields given, and the result names the new table and what it carried.
func TestCopyTable_YesPostsTheRequestAndPrintsTheResult(t *testing.T) {
	e := newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "orders", "--yes",
		"--name", "Orders EU", "--table-name", "orders_eu", "--database-name", "warehouse",
		"--schema-name", "eu", "--destination-id", "7", "--exclude", "learned-values",
		"--data-sources-file", writeDataSourcesFile(t, `
- {source_id: 12, s3_prefix: eu/, state: draft}
- {source_id: 13, pattern: "in,out*.csv"}
`))
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	e.assertRequests(t, "GET "+copyTestListPath, copyTestGET, copyTestPOST)
	if len(e.postBodies) != 1 {
		t.Fatalf("POST bodies = %q, want one", e.postBodies)
	}
	assertJSONEqual(t, e.postBodies[0], `{
	  "name": "Orders EU", "table_name": "orders_eu",
	  "database_name": "warehouse", "schema_name": "eu", "destination_id": 7,
	  "include": ["settings", "value_maps", "rules", "anomaly_model", "access"],
	  "data_sources": [
	    {"source_id": 12, "s3_prefix": "eu/", "state": "draft", "name": null, "schedule": null,
	     "pattern": null, "archive_pattern": null, "sftp_folder": null, "gs_prefix": null, "excel_sheet_name": null,
	     "s3_secret_key": null, "sftp_password": null, "sftp_ssh_key": null, "sftp_ssh_key_passphrase": null,
	     "gpg_private_key": null, "gpg_passphrase": null, "file_password": null, "gs_service_account_key": null,
	     "dropbox_access_token": null, "dropbox_authorization_code": null, "simple_url": null},
	    {"source_id": 13, "pattern": "in,out*.csv", "state": null, "name": null, "schedule": null,
	     "s3_prefix": null, "archive_pattern": null, "sftp_folder": null, "gs_prefix": null, "excel_sheet_name": null,
	     "s3_secret_key": null, "sftp_password": null, "sftp_ssh_key": null, "sftp_ssh_key_passphrase": null,
	     "gpg_private_key": null, "gpg_passphrase": null, "file_password": null, "gs_service_account_key": null,
	     "dropbox_access_token": null, "dropbox_authorization_code": null, "simple_url": null}
	  ]
	}`)
	for _, want := range []string{
		`Created table "Orders EU" (id 57), a copy of table orders (id 42).`,
		"  ITEM TYPE        ACTION   SOURCE  TARGET  NOTE",
		"  settings         created  9       31      -",
		"  migration_model  created  4       8       Rule output fields left out: score.",
		"  data_source      created  12      40      Changed: s3_prefix, state.",
		"Next: qctl describe table 57\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "Copy table orders (id 42) into a new table:") {
		t.Errorf("the plan is not on stdout:\n%s", stdout)
	}
}

// Each supplied secret reaches the POST as given, a ${VAR} one with the
// variable's value, and every other secret goes out as null.
func TestCopyTable_YesPostsSuppliedSecrets(t *testing.T) {
	t.Setenv("QCTL_COPY_TEST_SFTP_PASSWORD", "sftp-password-from-env")
	e := newCopyEnv(t)
	_, stderr, err := runCopy(t, "--table-id", "42", "--yes", "--data-sources-file", writeDataSourcesFile(t, `
- source_id: 12
  s3_prefix: eu/
  s3_secret_key: literal-s3-secret
- source_id: 13
  sftp_password: ${QCTL_COPY_TEST_SFTP_PASSWORD}
  gpg_passphrase: gpg-passphrase
`))
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	var body struct {
		DataSources json.RawMessage `json:"data_sources"`
	}
	if err := json.Unmarshal([]byte(e.postBodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, string(body.DataSources), `[
	  {"source_id": 12, "s3_prefix": "eu/", "state": null, "name": null, "schedule": null,
	   "pattern": null, "archive_pattern": null, "sftp_folder": null, "gs_prefix": null, "excel_sheet_name": null,
	   "s3_secret_key": "literal-s3-secret", "sftp_password": null, "sftp_ssh_key": null, "sftp_ssh_key_passphrase": null,
	   "gpg_private_key": null, "gpg_passphrase": null, "file_password": null, "gs_service_account_key": null,
	   "dropbox_access_token": null, "dropbox_authorization_code": null, "simple_url": null},
	  {"source_id": 13, "s3_prefix": null, "state": null, "name": null, "schedule": null,
	   "pattern": null, "archive_pattern": null, "sftp_folder": null, "gs_prefix": null, "excel_sheet_name": null,
	   "s3_secret_key": null, "sftp_password": "sftp-password-from-env", "sftp_ssh_key": null, "sftp_ssh_key_passphrase": null,
	   "gpg_private_key": null, "gpg_passphrase": "gpg-passphrase", "file_password": null, "gs_service_account_key": null,
	   "dropbox_access_token": null, "dropbox_authorization_code": null, "simple_url": null}
	]`)
}

// A --dry-run shows no supplied secret, given literally or through ${VAR}, in
// any format: the plan marks it (supplied), and the document masks it with
// one fixed value whatever --allow-plaintext-secrets says.
func TestCopyTable_DryRunNeverShowsASecret(t *testing.T) {
	const literal = "literal-s3-secret-value"
	const fromEnv = "sftp-password-from-env-value"
	t.Setenv("QCTL_COPY_TEST_SFTP_PASSWORD", fromEnv)
	file := `
- source_id: 12
  s3_prefix: eu/
  s3_secret_key: ` + literal + `
- source_id: 13
  sftp_password: ${QCTL_COPY_TEST_SFTP_PASSWORD}
`
	for _, args := range [][]string{
		{"-o", "table"},
		{"-o", "json"},
		{"-o", "yaml"},
		{"-o", "json", "--allow-plaintext-secrets"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			newCopyEnv(t)
			root := setupCopyTestCommand()
			root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
			var out, errOut bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&errOut)
			root.SetArgs(append([]string{"copy", "table", "--table-id", "42", "--dry-run",
				"--data-sources-file", writeDataSourcesFile(t, file)}, args...))
			err := root.Execute()
			stdout, stderr := out.String(), errOut.String()
			for _, secret := range []string{literal, fromEnv} {
				if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) ||
					(err != nil && strings.Contains(err.Error(), secret)) {
					t.Fatal("a secret value reached the output")
				}
			}
			if err != nil {
				t.Fatalf("copy table: %v\n%s", err, stderr)
			}
			plan := stdout + stderr
			for _, want := range []string{
				"yes: s3_prefix=eu/, s3_secret_key=(supplied)",
				"yes: sftp_password=(supplied)",
			} {
				if !strings.Contains(plan, want) {
					t.Errorf("the plan lacks %q:\n%s", want, plan)
				}
			}
			if args[1] == "table" {
				return
			}
			var doc struct {
				Request struct {
					DataSources []map[string]any `json:"data_sources" yaml:"data_sources"`
				} `json:"request" yaml:"request"`
			}
			if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
				t.Fatalf("stdout is not one document: %v\n%s", err, stdout)
			}
			sources := doc.Request.DataSources
			if len(sources) != 2 || sources[0]["s3_secret_key"] != dryRunSecretMask ||
				sources[1]["sftp_password"] != dryRunSecretMask || sources[1]["s3_secret_key"] != nil {
				t.Errorf("request.data_sources = %v, want the two supplied secrets masked and the rest null", sources)
			}
		})
	}
}

// With no flag the request still names every option: the defaults and the
// always-copied ones, never null; the names are the suggested ones, and what
// defaults to the source's is left out.
func TestCopyTable_DefaultsSendTheSuggestedNamesAndAnExplicitInclude(t *testing.T) {
	e := newCopyEnv(t)
	if _, stderr, err := runCopy(t, "--table-id", "42", "-y"); err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	e.assertRequests(t, copyTestGET, copyTestPOST)
	assertJSONEqual(t, e.postBodies[0], `{
	  "name": "Orders (2)", "table_name": "orders_2",
	  "database_name": null, "schema_name": null, "destination_id": null,
	  "include": ["settings", "value_maps", "rules", "learned_values", "anomaly_model", "access"]
	}`)
}

func TestCopyTable_WithoutYesOnANonTerminalExitsTwoAndPostsNothing(t *testing.T) {
	requireNonInteractiveStdin(t)
	e := newCopyEnv(t)
	_, _, err := runCopy(t, "--table-id", "42")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error does not name --yes: %v", err)
	}
	e.assertRequests(t, copyTestGET)
}

func TestCopyTable_PromptDecidesTheCopy(t *testing.T) {
	cases := map[string]struct {
		answer   string
		wantCode int
		posts    int
	}{
		"yes": {"y\n", 0, 1},
		"no":  {"n\n", 1, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cmdutil.SetReader(strings.NewReader(tc.answer))
			t.Cleanup(cmdutil.ResetReader)
			e := newCopyEnv(t)
			_, stderr, err := runCopy(t, "--table-id", "42")
			if code := qerrors.GetExitCode(err); code != tc.wantCode {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, tc.wantCode, err)
			}
			if len(e.postBodies) != tc.posts {
				t.Errorf("POSTs = %d, want %d", len(e.postBodies), tc.posts)
			}
			if want := `Copy table 42 to a new table "Orders (2)"? [y/N]: `; !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks the prompt %q\n%s", want, stderr)
			}
		})
	}
}

// The dry-run document is the offer as the server sent it, nulls included,
// and the request a copy would send; the plan moves to stderr.
func TestCopyTable_DryRunJSONWritesTheOptionsAndTheRequest(t *testing.T) {
	e := newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--dry-run", "-o", "json", "--include", "rules")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	e.assertRequests(t, copyTestGET)
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(doc) != 2 || doc["options"] == nil || doc["request"] == nil {
		t.Fatalf("keys = %v, want options and request", doc)
	}
	assertJSONEqual(t, string(doc["options"]), copyTestOptions)
	assertJSONEqual(t, string(doc["request"]), `{
	  "name": "Orders (2)", "table_name": "orders_2",
	  "database_name": null, "schema_name": null, "destination_id": null,
	  "include": ["settings", "rules"]
	}`)
	if !strings.Contains(stderr, "Copy table 42 into a new table:") {
		t.Errorf("the plan is not on stderr:\n%s", stderr)
	}
}

func TestCopyTable_DryRunYAMLWritesOneDocument(t *testing.T) {
	newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--dry-run", "-o", "yaml")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if len(doc) != 2 || doc["options"] == nil || doc["request"] == nil {
		t.Fatalf("keys = %v, want options and request", doc)
	}
}

// -o yaml after the copy is the body as received, so a null the typed
// response would drop stays null.
func TestCopyTable_YAMLAfterTheCopyIsTheResponseAsReceived(t *testing.T) {
	e := newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--yes", "-o", "yaml")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	e.assertRequests(t, copyTestGET, copyTestPOST)
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not YAML: %v\n%s", err, stdout)
	}
	if value, present := doc["deleted_at"]; !present || value != nil {
		t.Errorf("deleted_at = %v (present %v), want a present null\n%s", value, present, stdout)
	}
	if doc["id"] != 57 {
		t.Errorf("id = %v, want 57", doc["id"])
	}
	if strings.Contains(stdout, "Options:") || !strings.Contains(stderr, "Options:") {
		t.Errorf("the plan must be on stderr only\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

func TestCopyTable_JSONAfterTheCopyIsTheResponseAsReceived(t *testing.T) {
	newCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	assertJSONEqual(t, stdout, copyTestResult)
}

func TestCopyTable_NamePrintsTheNewTable(t *testing.T) {
	cases := map[string]struct {
		args []string
		want string
	}{
		"copy": {[]string{"--yes"}, "table/57\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			newCopyEnv(t)
			stdout, stderr, err := runCopy(t, append([]string{"--table-id", "42", "-o", "name"}, tc.args...)...)
			if err != nil {
				t.Fatalf("copy table: %v\n%s", err, stderr)
			}
			if stdout != tc.want {
				t.Errorf("stdout = %q, want %q", stdout, tc.want)
			}
			if !strings.Contains(stderr, "Options:") {
				t.Errorf("the plan is not on stderr:\n%s", stderr)
			}
		})
	}
}

// Every refusal the command line alone decides exits 2 before any request.
func TestCopyTable_LocalRefusalsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"include with exclude":        {"--table-id", "42", "--include", "rules", "--exclude", "access"},
		"dry run with yes":            {"--table-id", "42", "--dry-run", "--yes"},
		"dry run with -o name":        {"--table-id", "42", "--dry-run", "-o", "name"},
		"unknown option":              {"--table-id", "42", "--include", "widgets"},
		"empty include":               {"--table-id", "42", "--include", ""},
		"no data sources file path":   {"--table-id", "42", "--data-sources-file", ""},
		"the removed --data-source":   {"--table-id", "42", "--data-source", "id=12,s3_prefix=eu/"},
		"empty name":                  {"--table-id", "42", "--name", ""},
		"empty table name":            {"--table-id", "42", "--table-name", ""},
		"empty database name":         {"--table-id", "42", "--database-name", ""},
		"empty schema name":           {"--table-id", "42", "--schema-name", ""},
		"zero destination":            {"--table-id", "42", "--destination-id", "0"},
		"negative destination":        {"--table-id", "42", "--destination-id", "-1"},
		"no table":                    {"--dry-run"},
		"two tables":                  {"orders", "--table-id", "42"},
		"two positionals":             {"orders", "shipments"},
		"unknown flag":                {"--table-id", "42", "--bogus"},
		"empty table flag":            {"--table", ""},
		"include twice, one is empty": {"--table-id", "42", "--include", "rules", "--include", ""},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCopyEnv(t)
			_, _, err := runCopy(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			e.assertRequests(t)
		})
	}
}

// A --data-sources-file the command line alone refuses exits 2 before any
// request.
func TestCopyTable_DataSourcesFileRefusalsMakeNoRequest(t *testing.T) {
	cases := map[string]string{
		"empty file":       "",
		"empty list":       "[]",
		"not a list":       "source_id: 12",
		"two documents":    "- source_id: 12\n---\n- source_id: 13\n",
		"without id":       `- pattern: "*.csv"`,
		"bad id":           "- source_id: x",
		"unknown key":      "- {source_id: 12, bucket: b}",
		"duplicate key":    "- {source_id: 12, pattern: a, pattern: b}",
		"wrong type":       "- {source_id: 12, pattern: [a]}",
		"empty name":       `- {source_id: 12, name: ""}`,
		"bad state":        "- {source_id: 12, state: deleted}",
		"empty secret":     `- {source_id: 12, s3_secret_key: ""}`,
		"masked secret":    "- {source_id: 12, s3_secret_key: ••••}",
		"unset ${VAR}":     "- {source_id: 12, s3_secret_key: '${QCTL_COPY_TEST_NEVER_SET}'}",
		"not valid YAML":   "- {source_id: 12",
		"entry not a list": "- source_id=12",
	}
	for name, file := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCopyEnv(t)
			_, _, err := runCopy(t, "--table-id", "42", "--data-sources-file", writeDataSourcesFile(t, file))
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			e.assertRequests(t)
		})
	}
}

// A file that cannot be read is an I/O failure, exit 1, and makes no request.
func TestCopyTable_UnreadableDataSourcesFileExitsOne(t *testing.T) {
	e := newCopyEnv(t)
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	_, _, err := runCopy(t, "--table-id", "42", "--data-sources-file", missing)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitGenericError, err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("error does not name the file: %v", err)
	}
	e.assertRequests(t)
}

// "-" reads the file from stdin.
func TestCopyTable_DataSourcesFileFromStdin(t *testing.T) {
	e := newCopyEnv(t)
	_, stderr, err := runCopyWithStdin(t, "- {source_id: 13, name: from-stdin}\n",
		"--table-id", "42", "--yes", "--data-sources-file", "-")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	var body struct {
		DataSources []map[string]any `json:"data_sources"`
	}
	if err := json.Unmarshal([]byte(e.postBodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DataSources) != 1 || body.DataSources[0]["source_id"] != float64(13) ||
		body.DataSources[0]["name"] != "from-stdin" {
		t.Errorf("data_sources = %v, want the one entry stdin gave", body.DataSources)
	}
}

// Refusals that need the offer exit 2 after the GET and before any POST.
func TestCopyTable_RefusalsAfterTheOfferPostNothing(t *testing.T) {
	cases := map[string]struct {
		args []string
		file string
	}{
		"active source unchanged":        {file: "- source_id: 12"},
		"active source same value":       {file: `- {source_id: 12, pattern: "*.csv"}`},
		"source the table does not have": {file: "- {source_id: 99, pattern: x}"},
		"field the source does not list": {file: "- {source_id: 12, sftp_folder: /out}"},
		"copies reading the same files": {file: `
- {source_id: 12, s3_prefix: us/, name: first}
- {source_id: 12, s3_prefix: us/, name: second}
`},
		"copies with the same name": {file: "- source_id: 13\n- {source_id: 13, state: disabled}"},
		"upload source name":        {args: []string{"--name", "EU Orders"}, file: "- {source_id: 13, name: EU Orders Source}"},
		"always option excluded":    {args: []string{"--exclude", "settings"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCopyEnv(t)
			args := append([]string{"--table-id", "42", "--yes"}, tc.args...)
			if tc.file != "" {
				args = append(args, "--data-sources-file", writeDataSourcesFile(t, tc.file))
			}
			_, _, err := runCopy(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			e.assertRequests(t, copyTestGET)
		})
	}
}

func TestCopyTable_ServerConflictExitsTwo(t *testing.T) {
	e := newCopyEnv(t)
	e.postStatus = http.StatusConflict
	e.postBody = `{"detail": {"msg": "The name Orders (2) is already used by another dataset.", "error_code": "conflict"}}`
	_, _, err := runCopy(t, "--table-id", "42", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "already used") {
		t.Errorf("error does not carry the server's message: %v", err)
	}
	if len(e.postBodies) != 1 {
		t.Errorf("POSTs = %d, want 1", len(e.postBodies))
	}
}

// The help names the syntax and the rules a user cannot guess.
func TestCopyTable_HelpNamesTheOptionsAndTheDataSourceKeys(t *testing.T) {
	long := NewTableCommand().Long
	for _, want := range []string{
		"settings, value_maps, rules, learned_values, anomaly_model, access",
		"name, state, schedule, pattern, archive_pattern, s3_prefix, sftp_folder, gs_prefix, excel_sheet_name",
		"s3_secret_key, sftp_password, sftp_ssh_key, sftp_ssh_key_passphrase, gpg_private_key, gpg_passphrase, " +
			"file_password, gs_service_account_key, dropbox_access_token, dropbox_authorization_code, simple_url",
		"active, draft, disabled",
		"--data-sources-file eu-sources.yaml",
		"s3_secret_key: ${EU_S3_SECRET}",
		"dataset:create",
		"SECRETS column",
		"set state: draft",
		"simple_url=(withheld)",
		"Quote a secret that starts with *, &, !",
	} {
		if !strings.Contains(long, want) {
			t.Errorf("Long lacks %q", want)
		}
	}
}
