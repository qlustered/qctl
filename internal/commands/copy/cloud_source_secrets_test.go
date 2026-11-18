package copycmd

import (
	"encoding/base64"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// assertSecrets fails unless got holds exactly want. A failure names the keys
// that differ without their values.
func assertSecrets(t *testing.T, got, want map[string]string) {
	t.Helper()
	if got == nil {
		t.Fatal("secrets is nil, want a map")
	}
	gotKeys, wantKeys := slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("secret keys %v, want %v", gotKeys, wantKeys)
	}
	for _, key := range wantKeys {
		if got[key] != want[key] {
			t.Errorf("secret %s is not the value wanted", key)
		}
	}
}

func TestParseSecretsFile(t *testing.T) {
	t.Setenv(testSecretEnv, "from-the-environment")
	tests := []struct {
		name string
		file string
		want map[string]string
	}{
		{name: "one key", file: "s3_secret_key: literal-secret\n", want: map[string]string{"s3_secret_key": "literal-secret"}},
		{
			name: "an access key replaces the source's",
			file: "s3_access_key: other-id\ns3_secret_key: other-secret\n",
			want: map[string]string{"s3_access_key": "other-id", "s3_secret_key": "other-secret"},
		},
		{
			name: "exactly ${VAR} is read from the environment",
			file: "sftp_password: '${" + testSecretEnv + "}'\n",
			want: map[string]string{"sftp_password": "from-the-environment"},
		},
		{
			name: "more than ${VAR} is taken as written",
			file: "sftp_password: 'x${" + testSecretEnv + "}'\n",
			want: map[string]string{"sftp_password": "x${" + testSecretEnv + "}"},
		},
		{
			name: "null is a key left out",
			file: "s3_secret_key: null\nsftp_password: ~\ngpg_passphrase:\nfile_password: kept\n",
			want: map[string]string{"file_password": "kept"},
		},
		{name: "every key null", file: "s3_secret_key: null\n", want: map[string]string{}},
		{name: "a JSON object", file: `{"simple_url": "https://example.com/a,b.csv"}`, want: map[string]string{"simple_url": "https://example.com/a,b.csv"}},
		{
			name: "an alias stands for its anchor",
			file: "gpg_passphrase: &p shared\nfile_password: *p\n",
			want: map[string]string{"gpg_passphrase": "shared", "file_password": "shared"},
		},
		{name: "an empty document after the mapping", file: "s3_secret_key: v\n---\n", want: map[string]string{"s3_secret_key": "v"}},
		{name: "a scalar of another type is its text as written", file: "sftp_password: 007\n", want: map[string]string{"sftp_password": "007"}},
		{name: "a quoted key starting with *", file: "s3_secret_key: '*starts-with-star'\n", want: map[string]string{"s3_secret_key": "*starts-with-star"}},
		{name: "a !!str tag", file: "sftp_password: !!str 007\n", want: map[string]string{"sftp_password": "007"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSecretsFile([]byte(tt.file))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertSecrets(t, got, tt.want)
		})
	}
}

func TestParseSecretsFileReadsEveryCopyRequestSecretKey(t *testing.T) {
	var file strings.Builder
	want := map[string]string{}
	for _, key := range cloud_sources.CopyRequestSecretKeys() {
		file.WriteString(key + ": value-of-" + key + "\n")
		want[key] = "value-of-" + key
	}
	got, err := parseSecretsFile([]byte(file.String()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertSecrets(t, got, want)
}

// Every refusal exits 2 and none quotes a value. Each case's fix parses, so a
// refusal is the input's doing.
func TestParseSecretsFileRefusals(t *testing.T) {
	const secret = "s3cr3t-value"
	unsetEnv := testSecretEnv + "_UNSET"
	emptyEnv := testSecretEnv + "_EMPTY"
	maskEnv := testSecretEnv + "_MASK"
	t.Setenv(testSecretEnv, secret)
	t.Setenv(unsetEnv, "")
	if err := os.Unsetenv(unsetEnv); err != nil {
		t.Fatal(err)
	}
	t.Setenv(emptyEnv, "")
	t.Setenv(maskEnv, "ab•cd")
	notUTF8Env := testSecretEnv + "_NOT_UTF8"
	t.Setenv(notUTF8Env, "ab\xffcd")
	fixed := "s3_secret_key: " + secret + "\n"
	tests := []struct {
		name, file, fix string
		wants           []string
	}{
		{name: "empty file", file: "", wants: []string{"--secrets-file holds no secret; leave the flag out to supply none"}},
		{name: "comments only", file: "# none yet\n", wants: []string{"holds no secret"}},
		{name: "null document", file: "~\n", wants: []string{"holds no secret"}},
		{name: "empty mapping", file: "{}\n", wants: []string{"holds no secret"}},
		{
			name:  "two documents",
			file:  fixed + "---\nsftp_password: " + secret + "\n",
			fix:   fixed + "sftp_password: " + secret + "\n",
			wants: []string{"--secrets-file holds 2 YAML documents; put every secret in one mapping"},
		},
		{
			name:  "a list",
			file:  "- s3_secret_key: " + secret + "\n",
			wants: []string{"--secrets-file must be a YAML mapping of secret keys to values, got a list"},
		},
		{name: "a scalar", file: secret + "\n", wants: []string{"got a scalar"}},
		{name: "invalid YAML", file: "{s3_secret_key: " + secret + "\n", wants: []string{"--secrets-file is not valid YAML: yaml: "}},
		{
			name:  "unquoted value starting with *",
			file:  "s3_secret_key: *" + secret + "\n",
			fix:   "s3_secret_key: '*" + secret + "'\n",
			wants: []string{"--secrets-file is not valid YAML", "quote the value"},
		},
		{
			name: "a non-credential field",
			file: "s3_prefix: eu/\n",
			fix:  "s3_secret_key: eu/\n",
			wants: []string{"--secrets-file: unknown key on line 1; valid keys are s3_access_key, s3_secret_key, " +
				"sftp_password, sftp_ssh_key, sftp_ssh_key_passphrase, gpg_private_key, gpg_passphrase, file_password, " +
				"gs_service_account_key, dropbox_authorization_code, simple_url; set other fields with --set"},
		},
		{name: "hyphen spelling", file: "s3-secret-key: " + secret + "\n", wants: []string{`unknown key "s3-secret-key"`}},
		{name: "merge key", file: "<<: {s3_secret_key: " + secret + "}\n", wants: []string{"unknown key on line 1"}},
		{
			name:  "a value fused into its key",
			file:  "{s3_secret_key:" + secret + "}\n",
			fix:   "{s3_secret_key: " + secret + "}\n",
			wants: []string{"--secrets-file: unknown key on line 1"},
		},
		{
			name:  "a value in key position",
			file:  "{s3_secret_key, " + secret + "}\n",
			fix:   "{s3_secret_key: " + secret + "}\n",
			wants: []string{"--secrets-file: unknown key on line 1"},
		},
		{
			name:  "an explicit key holding a value",
			file:  fixed + "? " + secret + "\n",
			wants: []string{"--secrets-file: unknown key on line 2"},
		},
		{
			name:  "an alias as a key",
			file:  "&sftp_password s3_secret_key: " + secret + "\n*sftp_password : " + secret + "\n",
			fix:   fixed + "sftp_password: " + secret + "\n",
			wants: []string{"--secrets-file: unknown key on line 2"},
		},
		{
			name:  "unquoted value starting with &",
			file:  "s3_secret_key: &abc " + secret + "\n",
			fix:   "s3_secret_key: '&abc " + secret + "'\n",
			wants: []string{"--secrets-file: s3_secret_key: a value starting with & is read as a YAML anchor; quote the value"},
		},
		{
			name:  "unquoted value that is only an anchor",
			file:  "s3_secret_key: &" + secret + "\n",
			fix:   "s3_secret_key: '&" + secret + "'\n",
			wants: []string{"--secrets-file: s3_secret_key: a value starting with & is read as a YAML anchor"},
		},
		{
			name:  "unquoted value starting with !",
			file:  "s3_secret_key: !abc " + secret + "\n",
			fix:   "s3_secret_key: '!abc " + secret + "'\n",
			wants: []string{"--secrets-file: s3_secret_key: a value starting with ! is read as a YAML tag; quote the value"},
		},
		{
			name:  "a !!binary value",
			file:  "gpg_private_key: !!binary " + base64.StdEncoding.EncodeToString([]byte(secret)) + "\n",
			wants: []string{"--secrets-file: gpg_private_key: a value starting with ! is read as a YAML tag"},
		},
		{
			name:  "dropbox_access_token",
			file:  "dropbox_access_token: " + secret + "\n",
			fix:   "dropbox_authorization_code: " + secret + "\n",
			wants: []string{"--secrets-file: a copy cannot take dropbox_access_token; supply dropbox_authorization_code"},
		},
		{
			name:  "duplicate key",
			file:  fixed + fixed,
			wants: []string{"--secrets-file: s3_secret_key is given more than once"},
		},
		{name: "duplicate key, one null", file: fixed + "s3_secret_key: null\n", wants: []string{"s3_secret_key is given more than once"}},
		{
			name:  "a mapping value",
			file:  "s3_secret_key: {value: " + secret + "}\n",
			wants: []string{"--secrets-file: s3_secret_key must be a string, got a mapping"},
		},
		{name: "a list value", file: "s3_secret_key: [" + secret + "]\n", wants: []string{"s3_secret_key must be a string, got a list"}},
		{
			name:  "empty value",
			file:  `sftp_password: ""` + "\n",
			wants: []string{"--secrets-file: sftp_password cannot be empty: the server reads an empty secret as not supplied, so leave the key out"},
		},
		{
			name:  "empty access key",
			file:  `s3_access_key: ""` + "\n",
			fix:   "s3_access_key: " + secret + "\n",
			wants: []string{"--secrets-file: s3_access_key cannot be empty; leave the key out to keep the source's access key"},
		},
		{
			name:  "value holding the mask character",
			file:  "gpg_passphrase: " + secret + "•\n",
			wants: []string{"--secrets-file: gpg_passphrase contains the mask character •, which makes the server ignore it"},
		},
		{
			name:  "access key holding the mask character",
			file:  "s3_access_key: " + secret + "•\n",
			fix:   "s3_access_key: " + secret + "\n",
			wants: []string{"--secrets-file: s3_access_key contains the mask character •, so it is a masked value"},
		},
		{
			name:  "unset ${VAR}",
			file:  "s3_secret_key: '${" + unsetEnv + "}'\n",
			fix:   "s3_secret_key: '${" + testSecretEnv + "}'\n",
			wants: []string{"--secrets-file: s3_secret_key: environment variable " + unsetEnv + " is not set"},
		},
		{
			name:  "empty ${VAR}",
			file:  "s3_secret_key: '${" + emptyEnv + "}'\n",
			fix:   "s3_secret_key: '${" + testSecretEnv + "}'\n",
			wants: []string{"--secrets-file: s3_secret_key: environment variable " + emptyEnv + " is empty"},
		},
		{
			name:  "${VAR} holding the mask character",
			file:  "file_password: '${" + maskEnv + "}'\n",
			fix:   "file_password: '${" + testSecretEnv + "}'\n",
			wants: []string{"--secrets-file: file_password contains the mask character •"},
		},
		{
			name:  "${VAR} that is not UTF-8",
			file:  "file_password: '${" + notUTF8Env + "}'\n",
			fix:   "file_password: '${" + testSecretEnv + "}'\n",
			wants: []string{"--secrets-file: file_password: environment variable " + notUTF8Env + " is not valid UTF-8"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSecretsFile([]byte(tt.file))
			for _, value := range []string{secret, "ab•cd", "ab\xffcd"} {
				if err != nil && strings.Contains(err.Error(), value) {
					t.Fatal("the error quotes a secret value")
				}
			}
			assertBadUsage(t, err, tt.wants...)
			if got != nil {
				t.Errorf("got %d secrets alongside the error, want nil", len(got))
			}
			fix := tt.fix
			if fix == "" {
				fix = fixed
			}
			if _, err := parseSecretsFile([]byte(fix)); err != nil {
				t.Fatalf("control: the fixed file is refused: %v", err)
			}
		})
	}
}

func TestMissingCopySecrets(t *testing.T) {
	resupply := []string{"sftp_password", "gpg_private_key", "gpg_passphrase"}
	for _, tc := range []struct {
		name     string
		supplied map[string]string
		want     []string
	}{
		{"none supplied", nil, resupply},
		{"some supplied", map[string]string{"gpg_private_key": "k", "s3_access_key": "id"}, []string{"sftp_password", "gpg_passphrase"}},
		{"all supplied", map[string]string{"sftp_password": "p", "gpg_private_key": "k", "gpg_passphrase": "x"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := missingCopySecrets(resupply, tc.supplied); !slices.Equal(got, tc.want) {
				t.Errorf("missing = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRefuseMissingCopySecrets(t *testing.T) {
	source := &cloud_sources.CloudSourceTiny{ID: 12, Name: "orders\x1b-s3"}
	resupply := []string{"sftp_password", "gpg_passphrase"}
	for _, tc := range []struct {
		state    api.DataSourceState
		supplied map[string]string
		refused  bool
		wants    []string
	}{
		{api.DataSourceStateActive, nil, true, []string{
			`an active copy of cloud source 12 (orders\x1b-s3) needs every secret the source holds, ` +
				"and this one lacks sftp_password, gpg_passphrase; add them to --secrets-file " +
				"(a value of exactly ${VAR} is read from the environment), or pass --state draft to save the copy without them",
		}},
		{api.DataSourceStateDisabled, map[string]string{"sftp_password": "supplied-value"}, true, []string{
			"a disabled copy of cloud source 12", "lacks gpg_passphrase; add it to --secrets-file", "save the copy without it",
		}},
		{api.DataSourceStateDraft, nil, false, nil},
		{api.DataSourceStateActive, map[string]string{"sftp_password": "p", "gpg_passphrase": "x"}, false, nil},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			err := refuseMissingCopySecrets(source, resupply, cloud_sources.CopyOverrides{State: tc.state, Secrets: tc.supplied})
			if !tc.refused {
				if err != nil {
					t.Fatalf("unexpected refusal: %v", err)
				}
				return
			}
			assertBadUsage(t, err, tc.wants...)
			if strings.Contains(err.Error(), "supplied-value") || strings.ContainsRune(err.Error(), '\x1b') {
				t.Errorf("refusal quotes a value or an escape: %q", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d", code)
			}
		})
	}
}
