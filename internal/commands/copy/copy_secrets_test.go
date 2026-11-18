package copycmd

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// heldSource is a data source of state that holds the secrets held.
func heldSource(id int, state api.DataSourceState, held ...string) api.DatasetCopyDataSourceInfo {
	return api.DatasetCopyDataSourceInfo{ID: id, Name: "source", State: state, SecretsToResupply: held}
}

// supplying is a spec for source id that gives values and supplies secrets,
// each secret with a placeholder value.
func supplying(id int, values map[string]string, secrets ...string) dataSourceSpec {
	spec := dataSourceSpec{Entry: 1, ID: id, Values: values, Secrets: map[string]string{}}
	if spec.Values == nil {
		spec.Values = map[string]string{}
	}
	for _, key := range secrets {
		spec.Secrets[key] = "placeholder"
	}
	return spec
}

func TestMissingSecrets(t *testing.T) {
	tests := []struct {
		name     string
		held     []string
		supplied []string
		want     []string
	}{
		{name: "the source holds none", supplied: []string{"s3_secret_key"}},
		{name: "every one supplied", held: []string{"gpg_passphrase", "sftp_password"}, supplied: []string{"sftp_password", "gpg_passphrase"}},
		{
			name: "the rest, in the source's order",
			held: []string{"gpg_passphrase", "gpg_private_key", "sftp_password"}, supplied: []string{"gpg_private_key"},
			want: []string{"gpg_passphrase", "sftp_password"},
		},
		{
			name: "another secret does not stand in for one the source holds",
			held: []string{"sftp_password"}, supplied: []string{"sftp_ssh_key"},
			want: []string{"sftp_password"},
		},
		{name: "a Dropbox code supplies the code", held: []string{"dropbox_authorization_code"}, supplied: []string{"dropbox_authorization_code"}},
		{name: "a Dropbox token supplies the code", held: []string{"dropbox_authorization_code"}, supplied: []string{"dropbox_access_token"}},
		{name: "a Dropbox code missing", held: []string{"dropbox_authorization_code"}, want: []string{"dropbox_authorization_code"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := missingSecrets(heldSource(12, api.DataSourceStateActive, tt.held...), supplying(12, nil, tt.supplied...))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("missingSecrets = %v, want %v", got, tt.want)
			}
		})
	}
}

// An active or disabled copy, by its own state or its source's, is refused
// while it lacks a secret its source holds; a draft is not, and neither is a
// copy of a source that holds none.
func TestRefuseMissingSecrets(t *testing.T) {
	tests := []struct {
		name    string
		source  api.DatasetCopyDataSourceInfo
		spec    dataSourceSpec
		refused string
	}{
		{
			name:    "an active source's copy",
			source:  heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
			spec:    supplying(12, nil),
			refused: "an active copy needs every secret its source holds, and this one lacks s3_secret_key; add it",
		},
		{
			name:    "state: active on a draft source",
			source:  heldSource(12, api.DataSourceStateDraft, "s3_secret_key"),
			spec:    supplying(12, map[string]string{"state": "active"}),
			refused: "an active copy needs",
		},
		{
			name:    "state: disabled on an active source",
			source:  heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
			spec:    supplying(12, map[string]string{"state": "disabled"}),
			refused: "a disabled copy needs",
		},
		{
			name:    "a disabled source's copy",
			source:  heldSource(13, api.DataSourceStateDisabled, "sftp_password"),
			spec:    supplying(13, nil),
			refused: "a disabled copy needs every secret its source holds, and this one lacks sftp_password",
		},
		{
			name:    "secrets the server's connection check does not use",
			source:  heldSource(13, api.DataSourceStateActive, "file_password", "gpg_passphrase", "gpg_private_key", "sftp_password"),
			spec:    supplying(13, nil, "sftp_password"),
			refused: "this one lacks file_password, gpg_passphrase, gpg_private_key; add them to the entry",
		},
		{
			name:    "a Dropbox source without a code or a token",
			source:  heldSource(16, api.DataSourceStateActive, "dropbox_authorization_code"),
			spec:    supplying(16, nil),
			refused: "lacks dropbox_authorization_code",
		},
		{
			name:   "a Dropbox source with a token",
			source: heldSource(16, api.DataSourceStateActive, "dropbox_authorization_code"),
			spec:   supplying(16, nil, "dropbox_access_token"),
		},
		{
			name:   "a Dropbox source with a code",
			source: heldSource(16, api.DataSourceStateActive, "dropbox_authorization_code"),
			spec:   supplying(16, nil, "dropbox_authorization_code"),
		},
		{
			name:   "state: draft on an active source",
			source: heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
			spec:   supplying(12, map[string]string{"state": "draft"}),
		},
		{
			name:   "a draft source's copy",
			source: heldSource(13, api.DataSourceStateDraft, "gpg_passphrase", "sftp_password"),
			spec:   supplying(13, nil),
		},
		{
			name:   "every secret supplied",
			source: heldSource(13, api.DataSourceStateActive, "gpg_passphrase", "sftp_password"),
			spec:   supplying(13, nil, "gpg_passphrase", "sftp_password"),
		},
		{
			name:   "a source that holds none",
			source: heldSource(14, api.DataSourceStateActive),
			spec:   supplying(14, nil),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := refuseMissingSecrets([]api.DatasetCopyDataSourceInfo{tt.source}, []dataSourceSpec{tt.spec})
			if tt.refused == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			assertBadUsage(t, err, dataSourceEntryLabel(1, tt.source.ID)+": ", tt.refused,
				"(a value of exactly ${VAR} is read from the environment)", "or set state: draft to save the copy without")
		})
	}
}

// One error names every refused entry, one line each, and no other entry.
func TestRefuseMissingSecretsNamesEveryEntry(t *testing.T) {
	offered := []api.DatasetCopyDataSourceInfo{
		heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
		heldSource(13, api.DataSourceStateDisabled, "gpg_passphrase", "sftp_password"),
	}
	specs := []dataSourceSpec{
		supplying(12, nil),
		supplying(13, map[string]string{"state": "draft"}),
		supplying(13, nil, "sftp_password"),
	}
	for i := range specs {
		specs[i].Entry = i + 1
	}
	err := refuseMissingSecrets(offered, specs)
	assertBadUsage(t, err)
	want := "--data-sources-file entry 1 (source_id 12): an active copy needs every secret its source holds, " +
		"and this one lacks s3_secret_key; add it to the entry (a value of exactly ${VAR} is read from the environment), " +
		"or set state: draft to save the copy without it\n" +
		"--data-sources-file entry 3 (source_id 13): a disabled copy needs every secret its source holds, " +
		"and this one lacks gpg_passphrase; add it to the entry (a value of exactly ${VAR} is read from the environment), " +
		"or set state: draft to save the copy without it"
	if err.Error() != want {
		t.Errorf("error:\n%s\nwant:\n%s", err.Error(), want)
	}
}

func TestCopySecretsLine(t *testing.T) {
	tests := []struct {
		name   string
		source api.DatasetCopyDataSourceInfo
		spec   dataSourceSpec
		want   string
	}{
		{
			name:   "supplied in full",
			source: heldSource(13, api.DataSourceStateActive, "sftp_password"),
			spec:   supplying(13, nil, "sftp_password", "gpg_passphrase"),
			want:   "supplied gpg_passphrase, sftp_password",
		},
		{
			name:   "a draft missing two",
			source: heldSource(13, api.DataSourceStateActive, "gpg_passphrase", "gpg_private_key", "sftp_password"),
			spec:   supplying(13, map[string]string{"state": "draft"}, "sftp_password"),
			want:   "supplied sftp_password; missing gpg_passphrase, gpg_private_key; a draft copy saves without them",
		},
		{
			name:   "a disabled copy missing one",
			source: heldSource(13, api.DataSourceStateDisabled, "file_password"),
			spec:   supplying(13, nil),
			want:   "missing file_password; a disabled copy is refused without it",
		},
		{
			name:   "an active S3 copy without its secret key",
			source: heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
			spec:   supplying(12, nil),
			want: "missing s3_secret_key; an active copy is refused without it; " +
				"the copy keeps s3_access_key, so its connection check signs with an empty secret",
		},
		{
			name:   "a draft S3 copy without its secret key",
			source: heldSource(12, api.DataSourceStateActive, "s3_secret_key"),
			spec:   supplying(12, map[string]string{"state": "draft"}),
			want: "missing s3_secret_key; a draft copy saves without it; " +
				"the copy keeps s3_access_key, so its connection check signs with an empty secret",
		},
		{
			name:   "a Dropbox token for the code",
			source: heldSource(16, api.DataSourceStateActive, "dropbox_authorization_code"),
			spec:   supplying(16, nil, "dropbox_access_token"),
			want:   "supplied dropbox_access_token",
		},
		{
			name:   "a name the backend sends with a control character",
			source: heldSource(13, api.DataSourceStateDraft, "odd\tname\x1b"),
			spec:   supplying(13, nil),
			want:   `missing odd name\x1b; a draft copy saves without it`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := copySecretsLine(tt.source, tt.spec); got != tt.want {
				t.Errorf("copySecretsLine =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// copyTestSecretOptions is a GET body whose data sources hold secrets: an
// active S3 source, a disabled SFTP source, an active Dropbox source and an
// active simple_url source, whose location is withheld.
const copyTestSecretOptions = `{
  "source_dataset_id": 42,
  "name": "Orders (2)",
  "table_name": "orders_2",
  "destination_id": 3,
  "database_name": "analytics",
  "schema_name": "public",
  "options": [{"option": "settings", "count": 1, "default": true, "always": true}],
  "data_sources": [
    {"id": 12, "name": "orders-s3", "data_source_type": "s3", "state": "active", "schedule": null,
     "location_field": "s3_bucket", "location": "acme-drop",
     "distinguishing_fields": {"pattern": "*.csv", "s3_prefix": "2026-09/", "archive_pattern": null, "excel_sheet_name": null},
     "secrets_to_resupply": ["s3_secret_key"]},
    {"id": 13, "name": "orders-sftp", "data_source_type": "sftp", "state": "disabled", "schedule": null,
     "location_field": "sftp_host", "location": "sftp.example.com",
     "distinguishing_fields": {"pattern": "*.csv", "sftp_folder": "/in", "archive_pattern": null, "excel_sheet_name": null},
     "secrets_to_resupply": ["gpg_passphrase", "gpg_private_key", "sftp_password"]},
    {"id": 16, "name": "orders-dropbox", "data_source_type": "dropbox", "state": "active", "schedule": null,
     "location_field": "dropbox_folder", "location": "/orders",
     "distinguishing_fields": {"pattern": "*.csv", "archive_pattern": null, "excel_sheet_name": null},
     "secrets_to_resupply": ["dropbox_authorization_code"]},
    {"id": 17, "name": "orders-url", "data_source_type": "simple_url", "state": "active", "schedule": null,
     "location_field": "simple_url", "location": null,
     "distinguishing_fields": {"archive_pattern": null, "excel_sheet_name": null},
     "secrets_to_resupply": ["simple_url"]}
  ]
}`

// newSecretCopyEnv is newCopyEnv offering copyTestSecretOptions.
func newSecretCopyEnv(t *testing.T) *copyEnv {
	t.Helper()
	e := newCopyEnv(t)
	e.mock.RegisterHandler("GET", copyTestPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(copyTestSecretOptions))
	})
	return e
}

// Secret values a test supplies in copyTestSecretFile, none of which may reach
// any output.
const (
	testSFTPPassword  = "sftp-password-from-env-value"
	testDropboxToken  = "literal-dropbox-token-value"
	testLiteralSFTP   = "literal-sftp-password-value"
	copyTestSecretEnv = "QCTL_COPY_TEST_SFTP_PASSWORD"
	copyTestURLEnv    = "QCTL_COPY_TEST_URL"
)

// copyTestSecretFile copies every source of copyTestSecretOptions: entry 1 an
// active S3 copy without its secret key, entry 2 a draft SFTP copy without
// its GPG secrets, entries 3 and 4 copies that supply every secret their
// sources hold, and entry 5 a disabled SFTP copy without its GPG secrets.
const copyTestSecretFile = `
- source_id: 12
  s3_prefix: eu/
- source_id: 13
  state: draft
  sftp_folder: /eu
  sftp_password: ${` + copyTestSecretEnv + `}
- source_id: 16
  pattern: "*.xlsx"
  dropbox_access_token: ` + testDropboxToken + `
- source_id: 17
  simple_url: ${` + copyTestURLEnv + `}
- source_id: 13
  name: orders-sftp-gpg
  sftp_folder: /gpg
  sftp_password: ` + testLiteralSFTP + `
`

// assertNoSecretShown fails if any secret value copyTestSecretFile supplies
// is in stdout, stderr or err.
func assertNoSecretShown(t *testing.T, stdout, stderr string, err error) {
	t.Helper()
	shown := stdout + stderr
	if err != nil {
		shown += err.Error()
	}
	for _, secret := range []string{testSFTPPassword, testDropboxToken, testLiteralSFTP, testURL, "files.example.com"} {
		if strings.Contains(shown, secret) {
			t.Error("a supplied secret value reached the output")
		}
	}
}

// A --dry-run prints the whole plan, with each copy's secrets by name, then
// refuses every active or disabled copy that lacks one with exit 2.
func TestCopyTable_DryRunPrintsTheMissingSecretsThenExitsTwo(t *testing.T) {
	t.Setenv(copyTestSecretEnv, testSFTPPassword)
	t.Setenv(copyTestURLEnv, testURL)
	e := newSecretCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--dry-run",
		"--data-sources-file", writeDataSourcesFile(t, copyTestSecretFile))
	assertNoSecretShown(t, stdout, stderr, err)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	e.assertRequests(t, copyTestGET)
	want := `Copy table 42 into a new table:
  Name:            "Orders (2)"  (suggested)
  Table name:      orders_2      (suggested)
  Destination id:  3             (source's)
  Database:        analytics     (source's)
  Schema:          public        (source's)

Options:
  OPTION    INCLUDED  DEFAULT  ALWAYS  ITEMS
  settings  yes       yes      yes     1

Data sources:
  ID  NAME            TYPE        STATE     LOCATION                    DISTINGUISHING FIELDS                                                                 SECRETS                                         COPY
  12  orders-s3       s3          active    s3_bucket=acme-drop         archive_pattern=(unset), excel_sheet_name=(unset), pattern=*.csv, s3_prefix=2026-09/  s3_secret_key                                   yes: s3_prefix=eu/
  13  orders-sftp     sftp        disabled  sftp_host=sftp.example.com  archive_pattern=(unset), excel_sheet_name=(unset), pattern=*.csv, sftp_folder=/in     gpg_passphrase, gpg_private_key, sftp_password  yes: sftp_folder=/eu, sftp_password=(supplied), state=draft; yes: name=orders-sftp-gpg, sftp_folder=/gpg, sftp_password=(supplied)
  16  orders-dropbox  dropbox     active    dropbox_folder=/orders      archive_pattern=(unset), excel_sheet_name=(unset), pattern=*.csv                      dropbox_authorization_code                      yes: dropbox_access_token=(supplied), pattern=*.xlsx
  17  orders-url      simple_url  active    simple_url=(withheld)       archive_pattern=(unset), excel_sheet_name=(unset)                                     simple_url                                      yes: simple_url=(supplied)

Secrets of the copies:
  --data-sources-file entry 1 (source_id 12): missing s3_secret_key; an active copy is refused without it; the copy keeps s3_access_key, so its connection check signs with an empty secret
  --data-sources-file entry 2 (source_id 13): supplied sftp_password; missing gpg_passphrase, gpg_private_key; a draft copy saves without them
  --data-sources-file entry 3 (source_id 16): supplied dropbox_access_token
  --data-sources-file entry 4 (source_id 17): supplied simple_url
  --data-sources-file entry 5 (source_id 13): supplied sftp_password; missing gpg_passphrase, gpg_private_key; a disabled copy is refused without them
`
	if stdout != want {
		t.Errorf("stdout:\n%s\nwant:\n%s", stdout, want)
	}
	lines := strings.Split(err.Error(), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "--data-sources-file entry 1 (source_id 12): an active copy ") ||
		!strings.HasPrefix(lines[1], "--data-sources-file entry 5 (source_id 13): a disabled copy ") {
		t.Errorf("error names other entries than 1 and 5:\n%v", err)
	}
}

// Without a file, the plan still names the secrets each source holds, and a
// --dry-run succeeds.
func TestCopyTable_DryRunWithoutAFileNamesTheSecretsEachSourceHolds(t *testing.T) {
	newSecretCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--dry-run")
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	for _, want := range []string{
		"  13  orders-sftp     sftp        disabled  sftp_host=sftp.example.com  ",
		"  gpg_passphrase, gpg_private_key, sftp_password  no\n",
		"  simple_url=(withheld)  ",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "Secrets of the copies:") {
		t.Errorf("the plan has a secrets section with no copy:\n%s", stdout)
	}
}

// The refusal comes before the prompt and the POST, under every format, and
// shows no secret.
func TestCopyTable_MissingSecretsPostNothing(t *testing.T) {
	t.Setenv(copyTestSecretEnv, testSFTPPassword)
	t.Setenv(copyTestURLEnv, testURL)
	for _, args := range [][]string{
		{"--yes"},
		{"--yes", "-o", "json"},
		{"--yes", "-o", "name"},
		{"--dry-run", "-o", "yaml"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newSecretCopyEnv(t)
			stdout, stderr, err := runCopy(t, append([]string{"--table-id", "42",
				"--data-sources-file", writeDataSourcesFile(t, copyTestSecretFile)}, args...)...)
			assertNoSecretShown(t, stdout, stderr, err)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			e.assertRequests(t, copyTestGET)
			if len(args) > 1 && stdout != "" {
				t.Errorf("stdout = %q, want nothing: the plan goes to stderr", stdout)
			}
		})
	}
}

// A draft copy and copies that supply every secret their sources hold reach
// the POST.
func TestCopyTable_CopiesWithTheirSecretsOrAsDraftsArePosted(t *testing.T) {
	t.Setenv(copyTestSecretEnv, testSFTPPassword)
	t.Setenv(copyTestURLEnv, testURL)
	e := newSecretCopyEnv(t)
	stdout, stderr, err := runCopy(t, "--table-id", "42", "--yes", "--data-sources-file", writeDataSourcesFile(t, `
- {source_id: 12, s3_prefix: eu/, state: draft}
- {source_id: 13, sftp_folder: /eu, sftp_password: "${`+copyTestSecretEnv+`}", gpg_passphrase: p, gpg_private_key: k}
- {source_id: 16, pattern: "*.xlsx", dropbox_authorization_code: code}
- {source_id: 17, simple_url: "${`+copyTestURLEnv+`}"}
`))
	if err != nil {
		t.Fatalf("copy table: %v\n%s", err, stderr)
	}
	e.assertRequests(t, copyTestGET, copyTestPOST)
	if strings.Contains(stdout+stderr, testSFTPPassword) || strings.Contains(stdout+stderr, testURL) {
		t.Error("a supplied secret value reached the output")
	}
	var body struct {
		DataSources []map[string]any `json:"data_sources"`
	}
	if err := json.Unmarshal([]byte(e.postBodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DataSources) != 4 || body.DataSources[1]["sftp_password"] != testSFTPPassword ||
		body.DataSources[3]["simple_url"] != testURL {
		t.Error("the POST does not carry the four copies with their secrets")
	}
}
