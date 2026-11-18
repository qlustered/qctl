package copycmd

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// testSecretEnv is the environment variable the reader tests set.
const testSecretEnv = "QCTL_COPY_TEST_SECRET"

// assertSpecs fails unless got equals want. A failure names the secrets that
// differ without their values.
func assertSpecs(t *testing.T, got, want []dataSourceSpec) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d specs, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.Entry != w.Entry || g.ID != w.ID || !reflect.DeepEqual(g.Values, w.Values) {
			t.Errorf("spec %d = {Entry %d, ID %d, Values %v}, want {Entry %d, ID %d, Values %v}",
				i, g.Entry, g.ID, g.Values, w.Entry, w.ID, w.Values)
		}
		if g.Secrets == nil {
			t.Errorf("spec %d: Secrets is nil, want a map", i)
		}
		gotKeys, wantKeys := slices.Sorted(maps.Keys(g.Secrets)), slices.Sorted(maps.Keys(w.Secrets))
		if !slices.Equal(gotKeys, wantKeys) {
			t.Errorf("spec %d: secrets %v, want %v", i, gotKeys, wantKeys)
			continue
		}
		for _, key := range wantKeys {
			if g.Secrets[key] != w.Secrets[key] {
				t.Errorf("spec %d: secret %s is not the value wanted", i, key)
			}
		}
	}
}

func TestParseDataSourcesFile(t *testing.T) {
	t.Setenv(testSecretEnv, "from-the-environment")
	tests := []struct {
		name string
		file string
		want []dataSourceSpec
	}{
		{
			name: "source_id only",
			file: "- source_id: 12\n",
			want: []dataSourceSpec{{Entry: 1, ID: 12, Values: map[string]string{}}},
		},
		{
			name: "every non-secret key",
			file: `
- source_id: 3
  name: nightly
  state: disabled
  schedule: "0 2 * * *"
  pattern: "*.csv"
  archive_pattern: "*.zip"
  s3_prefix: in/
  sftp_folder: /x
  gs_prefix: g/
  excel_sheet_name: Sheet 1
`,
			want: []dataSourceSpec{{Entry: 1, ID: 3, Values: map[string]string{
				"name": "nightly", "state": "disabled", "schedule": "0 2 * * *", "pattern": "*.csv",
				"archive_pattern": "*.zip", "s3_prefix": "in/", "sftp_folder": "/x", "gs_prefix": "g/",
				"excel_sheet_name": "Sheet 1",
			}}},
		},
		{
			name: "entries keep their order and number",
			file: "- source_id: 13\n- {source_id: 12, s3_prefix: eu/}\n",
			want: []dataSourceSpec{
				{Entry: 1, ID: 13, Values: map[string]string{}},
				{Entry: 2, ID: 12, Values: map[string]string{"s3_prefix": "eu/"}},
			},
		},
		{
			name: "null is a key left out",
			file: "- {source_id: 3, name: null, s3_prefix: ~, schedule: , s3_secret_key: null}",
			want: []dataSourceSpec{{Entry: 1, ID: 3, Values: map[string]string{}}},
		},
		{
			name: "an empty string clears a non-secret field",
			file: `- {source_id: 3, s3_prefix: "", archive_pattern: '', schedule: ""}`,
			want: []dataSourceSpec{{Entry: 1, ID: 3, Values: map[string]string{
				"s3_prefix": "", "archive_pattern": "", "schedule": "",
			}}},
		},
		{
			// yaml.v3 decodes any scalar into a string as the text it was
			// written as, so a sheet named 2024 needs no quotes.
			name: "a scalar of another type is its text as written",
			file: `
- source_id: 3
  excel_sheet_name: 2024
  pattern: 007
  gs_prefix: 1.50
  sftp_folder: 2024-01-01
  archive_pattern: true
  schedule: 0x1F
`,
			want: []dataSourceSpec{{Entry: 1, ID: 3, Values: map[string]string{
				"excel_sheet_name": "2024", "pattern": "007", "gs_prefix": "1.50",
				"sftp_folder": "2024-01-01", "archive_pattern": "true", "schedule": "0x1F",
			}}},
		},
		{
			name: "a JSON list",
			file: `[{"source_id": 12, "pattern": "a,b*.csv"}]`,
			want: []dataSourceSpec{{Entry: 1, ID: 12, Values: map[string]string{"pattern": "a,b*.csv"}}},
		},
		{
			name: "an alias stands for its anchor",
			file: "- {source_id: 12, pattern: &p \"*.csv\"}\n- {source_id: 13, pattern: *p}\n",
			want: []dataSourceSpec{
				{Entry: 1, ID: 12, Values: map[string]string{"pattern": "*.csv"}},
				{Entry: 2, ID: 13, Values: map[string]string{"pattern": "*.csv"}},
			},
		},
		{
			name: "an aliased anchor on a secret",
			file: "- {source_id: 12, s3_secret_key: &k shared}\n- {source_id: 13, s3_secret_key: *k}\n",
			want: []dataSourceSpec{
				{Entry: 1, ID: 12, Values: map[string]string{}, Secrets: map[string]string{"s3_secret_key": "shared"}},
				{Entry: 2, ID: 13, Values: map[string]string{}, Secrets: map[string]string{"s3_secret_key": "shared"}},
			},
		},
		{
			name: "an empty document after the list",
			file: "- source_id: 12\n---\n",
			want: []dataSourceSpec{{Entry: 1, ID: 12, Values: map[string]string{}}},
		},
		{
			name: "secrets are kept apart from the other keys",
			file: `
- source_id: 12
  s3_prefix: eu/
  s3_secret_key: literal-secret
  simple_url: https://example.com/orders.csv
`,
			want: []dataSourceSpec{{
				Entry: 1, ID: 12, Values: map[string]string{"s3_prefix": "eu/"},
				Secrets: map[string]string{"s3_secret_key": "literal-secret", "simple_url": "https://example.com/orders.csv"},
			}},
		},
		{
			name: "a secret that is exactly ${VAR} is read from the environment",
			file: "- {source_id: 12, sftp_password: '${" + testSecretEnv + "}'}",
			want: []dataSourceSpec{{
				Entry: 1, ID: 12, Values: map[string]string{},
				Secrets: map[string]string{"sftp_password": "from-the-environment"},
			}},
		},
		{
			name: "a secret holding more than ${VAR} is taken as written",
			file: "- {source_id: 12, sftp_password: 'x${" + testSecretEnv + "}'}",
			want: []dataSourceSpec{{
				Entry: 1, ID: 12, Values: map[string]string{},
				Secrets: map[string]string{"sftp_password": "x${" + testSecretEnv + "}"},
			}},
		},
		{
			name: "a non-secret ${VAR} is taken as written",
			file: "- {source_id: 12, s3_prefix: '${" + testSecretEnv + "}'}",
			want: []dataSourceSpec{{
				Entry: 1, ID: 12, Values: map[string]string{"s3_prefix": "${" + testSecretEnv + "}"},
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDataSourcesFile([]byte(tt.file))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertSpecs(t, got, tt.want)
		})
	}
}

func TestParseDataSourcesFileReadsEverySecretKey(t *testing.T) {
	var file strings.Builder
	file.WriteString("- source_id: 12\n")
	want := map[string]string{}
	for _, key := range dataSourceSecretKeys {
		file.WriteString("  " + key + ": value-of-" + key + "\n")
		want[key] = "value-of-" + key
	}
	got, err := parseDataSourcesFile([]byte(file.String()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSpecs(t, got, []dataSourceSpec{{Entry: 1, ID: 12, Values: map[string]string{}, Secrets: want}})
}

// Every refusal exits 2, and none quotes a secret.
func TestParseDataSourcesFileRefusals(t *testing.T) {
	const secret = "s3cr3t-value"
	unsetEnv := testSecretEnv + "_UNSET"
	emptyEnv := testSecretEnv + "_EMPTY"
	maskEnv := testSecretEnv + "_MASK"
	t.Setenv(unsetEnv, "")
	if err := os.Unsetenv(unsetEnv); err != nil {
		t.Fatal(err)
	}
	t.Setenv(emptyEnv, "")
	t.Setenv(maskEnv, "ab•cd")
	label := "--data-sources-file entry 1 (source_id 1): "
	tests := []struct {
		name  string
		file  string
		wants []string
	}{
		{name: "empty file", file: "", wants: []string{"--data-sources-file lists no data source; leave the flag out to copy none"}},
		{name: "comments only", file: "# none yet\n", wants: []string{"lists no data source"}},
		{name: "null document", file: "~\n", wants: []string{"lists no data source"}},
		{name: "empty list", file: "[]\n", wants: []string{"lists no data source"}},
		{
			name:  "two documents",
			file:  "- source_id: 1\n---\n- source_id: 2\n",
			wants: []string{"--data-sources-file holds 2 YAML documents; list every entry in one"},
		},
		{
			name:  "a mapping, not a list",
			file:  "source_id: 1\ns3_secret_key: " + secret + "\n",
			wants: []string{"--data-sources-file must be a YAML list with one entry per copy, got a mapping"},
		},
		{name: "a scalar, not a list", file: secret + "\n", wants: []string{"got a scalar"}},
		{name: "invalid YAML", file: "- {source_id: 1\n", wants: []string{"--data-sources-file is not valid YAML: yaml: "}},
		{
			name:  "invalid YAML after a secret",
			file:  "- source_id: 1\n  s3_secret_key: \"" + secret + "\n",
			wants: []string{"is not valid YAML"},
		},
		{
			name:  "unquoted secret starting with *",
			file:  "- source_id: 1\n  s3_secret_key: *" + secret + "\n",
			wants: []string{"is not valid YAML", "quote the value"},
		},
		{
			name:  "entry that is not a mapping",
			file:  "- id=1,s3_secret_key=" + secret + "\n",
			wants: []string{"--data-sources-file entry 1: expected a mapping of keys, got a scalar"},
		},
		{name: "entry that is a list", file: "- [1]\n", wants: []string{"entry 1: expected a mapping of keys, got a list"}},
		{name: "null entry", file: "-\n", wants: []string{"entry 1: expected a mapping of keys"}},
		{
			name:  "a later entry",
			file:  "- source_id: 1\n- {source_id: 2, bucket: x}\n",
			wants: []string{"--data-sources-file entry 2 (source_id 2): unknown key on line 2"},
		},
		{name: "missing source_id", file: "- {pattern: x}", wants: []string{"--data-sources-file entry 1: source_id is required"}},
		{name: "null source_id", file: "- {source_id: null}", wants: []string{"entry 1: source_id is required"}},
		{name: "zero source_id", file: "- {source_id: 0}", wants: []string{`entry 1: source_id must be a positive integer, got "0"`}},
		{name: "negative source_id", file: "- {source_id: -3}", wants: []string{`got "-3"`}},
		{name: "quoted source_id", file: `- {source_id: "12"}`, wants: []string{`got "12"`}},
		{name: "fractional source_id", file: "- {source_id: 12.5}", wants: []string{`got "12.5"`}},
		{name: "word source_id", file: "- {source_id: orders}", wants: []string{`got "orders"`}},
		{name: "list source_id", file: "- {source_id: [12]}", wants: []string{"got a list"}},
		{
			name: "unknown key",
			file: "- {source_id: 1, bucket: x}",
			wants: []string{label + "unknown key on line 1; valid keys are source_id, name, state, schedule, pattern, " +
				"archive_pattern, s3_prefix, sftp_folder, gs_prefix, excel_sheet_name, s3_secret_key, sftp_password, " +
				"sftp_ssh_key, sftp_ssh_key_passphrase, gpg_private_key, gpg_passphrase, file_password, " +
				"gs_service_account_key, dropbox_access_token, dropbox_authorization_code, simple_url"},
		},
		{name: "hyphen spelling", file: "- {source_id: 1, s3-prefix: x}", wants: []string{`unknown key "s3-prefix"`}},
		{name: "merge key", file: "- {source_id: 1, <<: {pattern: x}}", wants: []string{"unknown key on line 1"}},
		{name: "a secret in key position", file: "- {source_id: 1, s3_secret_key, " + secret + "}", wants: []string{label + "unknown key on line 1"}},
		{
			name:  "unquoted secret starting with &",
			file:  "- {source_id: 1, s3_secret_key: &abc " + secret + "}",
			wants: []string{label + "s3_secret_key: a value starting with & is read as a YAML anchor; quote the value"},
		},
		{
			name:  "unquoted secret starting with !",
			file:  "- {source_id: 1, s3_secret_key: !abc " + secret + "}",
			wants: []string{label + "s3_secret_key: a value starting with ! is read as a YAML tag; quote the value"},
		},
		{name: "duplicate key", file: "- {source_id: 1, pattern: a, pattern: b}", wants: []string{label + "pattern is given more than once"}},
		{name: "duplicate key, one null", file: "- {source_id: 1, pattern: a, pattern: null}", wants: []string{"pattern is given more than once"}},
		{name: "duplicate source_id", file: "- {source_id: 1, source_id: 2}", wants: []string{"source_id is given more than once"}},
		{
			name:  "duplicate secret",
			file:  "- {source_id: 1, s3_secret_key: " + secret + ", s3_secret_key: " + secret + "}",
			wants: []string{"s3_secret_key is given more than once"},
		},
		{name: "list for a string key", file: "- {source_id: 1, pattern: [a, b]}", wants: []string{label + "pattern must be a string, got a list"}},
		{
			name:  "mapping for a secret",
			file:  "- {source_id: 1, s3_secret_key: {value: " + secret + "}}",
			wants: []string{label + "s3_secret_key must be a string, got a mapping"},
		},
		{name: "empty name", file: `- {source_id: 1, name: ""}`, wants: []string{label + "name cannot be empty; leave it out to keep the source's name"}},
		{name: "empty state", file: `- {source_id: 1, state: ""}`, wants: []string{label + `state must be one of active, draft, disabled, got ""`}},
		{name: "deleted state", file: "- {source_id: 1, state: deleted}", wants: []string{`state must be one of active, draft, disabled, got "deleted"`}},
		{name: "wrong-case state", file: "- {source_id: 1, state: Active}", wants: []string{`got "Active"`}},
		{
			name:  "empty secret",
			file:  `- {source_id: 1, sftp_password: ""}`,
			wants: []string{label + "sftp_password cannot be empty: the server reads an empty secret as not supplied, so leave the key out"},
		},
		{
			name: "secret holding the mask character",
			file: "- {source_id: 1, gpg_passphrase: " + secret + "•}",
			wants: []string{label + "gpg_passphrase contains the mask character •, which makes the server ignore it; " +
				"give the secret itself"},
		},
		{
			name:  "unset ${VAR}",
			file:  "- {source_id: 1, s3_secret_key: '${" + unsetEnv + "}'}",
			wants: []string{label + "s3_secret_key: environment variable " + unsetEnv + " is not set"},
		},
		{
			name:  "empty ${VAR}",
			file:  "- {source_id: 1, s3_secret_key: '${" + emptyEnv + "}'}",
			wants: []string{label + "s3_secret_key: environment variable " + emptyEnv + " is empty"},
		},
		{
			name:  "${VAR} holding the mask character",
			file:  "- {source_id: 1, file_password: '${" + maskEnv + "}'}",
			wants: []string{label + "file_password contains the mask character •"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDataSourcesFile([]byte(tt.file))
			for _, value := range []string{secret, "ab•cd"} {
				if err != nil && strings.Contains(err.Error(), value) {
					t.Fatal("the error quotes a secret value")
				}
			}
			assertBadUsage(t, err, tt.wants...)
			if got != nil {
				t.Errorf("got %d specs alongside the error, want nil", len(got))
			}
		})
	}
}
