package copycmd_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cloud_sources"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// cloudCopyDryRunMask is the mask the --dry-run document shows for each
// credential member of the request.
const cloudCopyDryRunMask = "••••••••"

// The --dry-run document's request is the POST the copy sends, with each
// credential member it carries masked, and no credential value reaches the
// output.
func TestCopyCloudSourceDryRunDocumentMatchesPOST(t *testing.T) {
	const sftpPassword = "supplied-sftp-password"
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.template["future"] = json.RawMessage(`{"integer":9007199254740993,"nullable":null,"<<":"literal"}`)
			e.template["chunk_size_to_read_for_header"] = json.Number("9007199254740993")
			e.template["raw_headers"] = []string{"\n", "NULL"}
			e.template["secrets_to_resupply"] = []string{"s3_secret_key", "sftp_password"}
			credentials := []string{
				"s3_access_key", "s3_secret_key", "sftp_password", "sftp_ssh_key", "sftp_ssh_key_passphrase",
				"gpg_private_key", "gpg_passphrase", "file_password", "gs_service_account_key",
				"dropbox_access_token", "dropbox_authorization_code", "simple_url",
			}
			for _, key := range credentials {
				e.template[key] = "sensitive-" + key
			}
			secretsFile := writeCloudSecretsFile(t, cloudCopySecretsFile+"sftp_password: "+sftpPassword+"\n")
			args := []string{"12", "--name", "orders-copy", "--set", "s3_prefix=eu/", "--set", "sftp_port=2222", "--secrets-file", secretsFile}
			out, stderr, err := runCloudCopy(t, append(args, "--dry-run", "-o", format)...)
			if err != nil {
				t.Fatalf("dry run: %v; stderr: %s", err, stderr)
			}
			assertNoCloudCopySecret(t, out, stderr, nil, "sensitive-", sftpPassword)
			assertCloudCopyRequests(t, e, "GET "+cloudCopyTiny, "GET "+cloudCopyTemplate)
			if len(e.postBodies) != 0 {
				t.Fatal("dry run sent a POST")
			}
			if format == "table" {
				if !strings.HasPrefix(out, "Copy cloud source orders-s3 (12)") || !strings.HasSuffix(out, "Dry run: nothing was copied.\n") || stderr != "" {
					t.Fatalf("stdout = %q, stderr = %q", out, stderr)
				}
				checkTemplateControl(t, e, cloudCopyTemplate, append(args, "--dry-run", "-o", format)...)
				return
			}
			if !strings.HasPrefix(stderr, "Copy cloud source orders-s3 (12)") {
				t.Errorf("plan missing from stderr: %q", stderr)
			}
			if _, stderr, err := runCloudCopy(t, append(args, "--yes", "-o", "name")...); err != nil {
				t.Fatalf("real copy: %v; stderr: %s", err, stderr)
			}
			// Harness-alive: the POST carries each masked member's value in
			// clear, and those are the only credential members it carries.
			body := cloudCopyPOSTBody(t, e)
			masked := map[string]string{"s3_access_key": "sensitive-s3_access_key", "s3_secret_key": cloudCopySecret, "sftp_password": sftpPassword}
			for _, key := range cloud_sources.CopyRequestSecretKeys() {
				value, wantMasked := masked[key]
				got, sent := body[key]
				switch {
				case sent != wantMasked:
					t.Errorf("POST has %s: %v, want %v", key, sent, wantMasked)
				case sent && string(got) != strconv.Quote(value):
					t.Errorf("POST %s is not the value the copy was given", key)
				}
				if sent {
					body[key] = json.RawMessage(strconv.Quote(cloudCopyDryRunMask))
				}
			}
			request, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			safeTemplate := make(map[string]any, len(e.template))
			for key, value := range e.template {
				safeTemplate[key] = value
			}
			for _, key := range credentials {
				delete(safeTemplate, key)
			}
			want, err := json.Marshal(map[string]any{"template": safeTemplate, "request": json.RawMessage(request)})
			if err != nil {
				t.Fatal(err)
			}
			if format == "yaml" {
				testutil.AssertYAMLReadsAsJSON(t, out, string(want))
			} else if !reflect.DeepEqual(decodeJSONNumbers(t, out), decodeJSONNumbers(t, string(want))) {
				t.Errorf("dry run document = %s, want %s", out, want)
			}
			checkTemplateControl(t, e, cloudCopyTemplate, append(args, "--dry-run", "-o", format)...)
		})
	}
}

// decodeJSONNumbers decodes raw, one JSON document, keeping each number's
// literal.
func decodeJSONNumbers(t *testing.T, raw string) any {
	t.Helper()
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// An s3_access_key the template holds empty is sent empty and shown empty: the
// document masks only a credential that has a value.
func TestCopyCloudSourceDryRunShowsAnEmptyAccessKey(t *testing.T) {
	for _, tc := range []struct{ accessKey, want string }{
		{"", `""`},
		// Control: the same member with a value is masked.
		{"credential-id", strconv.Quote(cloudCopyDryRunMask)},
	} {
		t.Run(tc.want, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.template["s3_access_key"] = tc.accessKey
			out, stderr, err := runCloudCopy(t, "12", "--set", "s3_prefix=eu/", "--dry-run", "-o", "json")
			if err != nil {
				t.Fatalf("dry run: %v; stderr: %s", err, stderr)
			}
			assertNoCloudCopySecret(t, out, stderr, nil, "credential-id")
			var document struct {
				Request map[string]json.RawMessage `json:"request"`
			}
			if err := json.Unmarshal([]byte(out), &document); err != nil {
				t.Fatal(err)
			}
			if got := string(document.Request["s3_access_key"]); got != tc.want {
				t.Errorf("request s3_access_key = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestCopyCloudSourceUploadSideEffect(t *testing.T) {
	for _, state := range []string{"active", "draft", "disabled"} {
		for _, uploads := range []int{0, 1, 2} {
			t.Run(fmt.Sprintf("%s/%d", state, uploads), func(t *testing.T) {
				e := newCloudCopyEnv(t)
				e.tiny["state"] = state
				e.template["dataset_id"], e.template["dataset_name"] = 84, "target"
				path := cloudCopyBase + "/datasets/84/data-sources/12/copy"
				e.registerTemplate(path)
				e.mock.RegisterHandler("GET", cloudCopyList, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Query().Get("dataset_id") != "84" || r.URL.Query().Get("states") != "active" {
						t.Errorf("listing query = %v", r.URL.Query())
					}
					// Put the active uploads on separate pages, among ignored sources.
					results := []any{
						map[string]any{"id": 1, "dataset_id": 84, "state": "disabled", "data_source_type": "qluster_ui"},
						map[string]any{"id": 2, "dataset_id": 42, "state": "active", "data_source_type": "qluster_ui"},
						map[string]any{"id": 3, "dataset_id": 84, "state": "active", "data_source_type": "s3"},
					}
					if uploads > 0 {
						results = append(results, map[string]any{"id": 70, "name": "Upload\x1b", "dataset_id": 84, "state": "active", "data_source_type": "qluster_ui"})
					}
					total := len(results)
					if uploads == 2 {
						total++
						if r.URL.Query().Get("page") == "2" {
							results = []any{map[string]any{"id": 71, "dataset_id": 84, "state": "active", "data_source_type": "qluster_ui"}}
						}
					}
					testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": total, "results": results})
				})
				args := []string{"12", "--to-table-id", "84", "--set", "s3_prefix=eu/", "--dry-run",
					"--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile)}
				out, stderr, err := runCloudCopy(t, args...)
				if err != nil {
					t.Fatalf("dry run: %v; stderr: %s", err, stderr)
				}
				wantEffect := state == "active" && uploads == 1
				if strings.Contains(out, "disables upload source") != wantEffect {
					t.Errorf("disable effect (want %v): %s", wantEffect, out)
				}
				if wantEffect && !strings.Contains(out, `Upload\x1b (70)`) {
					t.Errorf("upload label missing or unsanitized: %q", out)
				}
				wantRequests := []string{"GET " + cloudCopyTiny, "GET " + path}
				if state == "active" {
					wantRequests = append(wantRequests, "GET "+cloudCopyList)
					if uploads == 2 {
						wantRequests = append(wantRequests, "GET "+cloudCopyList)
					}
				}
				assertCloudCopyRequests(t, e, wantRequests...)
				checkTemplateControl(t, e, path, args...)
			})
		}
	}
}

// An active copy that joins a table series disables no upload source, so its
// plan names none and skips the listing.
func TestCopyCloudSourceSeriesCopyKeepsUploadSource(t *testing.T) {
	for _, series := range []bool{false, true} {
		t.Run(fmt.Sprintf("series=%v", series), func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.tiny["state"] = "active"
			e.mock.RegisterHandler("GET", cloudCopyList, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, map[string]any{"total_rows": 1, "results": []any{
					map[string]any{"id": 70, "name": "Upload", "dataset_id": 42, "state": "active", "data_source_type": "qluster_ui"},
				}})
			})
			args := []string{"12", "--set", "s3_prefix=eu/", "--dry-run", "--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile)}
			if series {
				args = append(args, "--set", "table_series_id=6f1c2b4e-0d3a-4c5b-9e8f-7a6b5c4d3e2f")
			}
			out, stderr, err := runCloudCopy(t, args...)
			if err != nil {
				t.Fatalf("dry run: %v; stderr: %s", err, stderr)
			}
			if strings.Contains(out, "disables upload source Upload (70)") == series {
				t.Errorf("disable effect (series %v): %s", series, out)
			}
			wantRequests := []string{"GET " + cloudCopyTiny, "GET " + cloudCopyTemplate}
			if !series {
				wantRequests = append(wantRequests, "GET "+cloudCopyList)
			}
			assertCloudCopyRequests(t, e, wantRequests...)
		})
	}
}

func TestCopyCloudSourcePlanValuesAndEffects(t *testing.T) {
	for _, state := range []string{"active", "draft", "disabled"} {
		for _, schedule := range []string{"", "0 * * * *"} {
			for _, cursor := range []any{nil, 0} {
				t.Run(fmt.Sprintf("%s/%s/%v", state, schedule, cursor), func(t *testing.T) {
					e := newCloudCopyEnv(t)
					e.tiny["name"], e.tiny["delta_applied_cursor"] = "orders\x1b[31m", cursor
					e.template["dataset_name"], e.template["pattern"] = "target\nname", nil
					e.template["s3_prefix"] = "old\x1b"
					args := []string{"12", "--state", state, "--name", "copy\x1b", "--set", "s3_prefix=new\x1b", "--set", "pattern=", "--set", "schedule=" + schedule, "--dry-run",
						"--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile)}
					out, stderr, err := runCloudCopy(t, args...)
					if err != nil {
						t.Fatalf("dry run: %v; stderr: %s", err, stderr)
					}
					for _, want := range []string{`orders\x1b[31m (12)`, "target name (42)", `"copy\x1b"`, "(--name)", "(--state)", `"old\x1b"`, `"new\x1b"`, "(unset)", `""`, "→", "Credentials: secrets and URLs are not inherited", "supplied s3_secret_key; s3_access_key kept from the source\n"} {
						if !strings.Contains(out, want) {
							t.Errorf("plan missing %q: %s", want, out)
						}
					}
					if strings.ContainsRune(out, '\x1b') {
						t.Fatalf("plan has terminal escape: %q", out)
					}
					if strings.Contains(out, "starts polling") != (state == "active" && schedule != "") {
						t.Errorf("incorrect polling effect: %s", out)
					}
					if strings.Contains(out, "no applied delta cursor") != (cursor != nil) {
						t.Errorf("incorrect cursor effect: %s", out)
					}
					if strings.Contains(out, "missing") {
						t.Errorf("plan names a supplied secret as missing: %s", out)
					}
					checkTemplateControl(t, e, cloudCopyTemplate, args...)
				})
			}
		}
	}
}

func TestCopyCloudSourcePlanListingFailureStopsCopy(t *testing.T) {
	e := newCloudCopyEnv(t)
	delete(e.mock.Handlers, "GET "+cloudCopyList)
	out, _, err := runCloudCopy(t, "12", "--state", "active", "--yes")
	if err == nil || qerrors.GetExitCode(err) != qerrors.ExitNotFound {
		t.Fatalf("listing failure = %v, want exit 3", err)
	}
	if out != "" || len(e.postBodies) != 0 {
		t.Fatalf("listing failure produced a plan or sent POST: %q, %v", out, e.postBodies)
	}
}

func TestCopyCloudSourcePlanUsesDefaults(t *testing.T) {
	e := newCloudCopyEnv(t)
	e.tiny["state"] = "active"
	e.template["schedule"] = "0 * * * *"
	args := []string{"12", "--set", "pattern=*.json", "--dry-run", "--secrets-file", writeCloudSecretsFile(t, cloudCopySecretsFile)}
	out, stderr, err := runCloudCopy(t, args...)
	if err != nil {
		t.Fatalf("dry run: %v; stderr: %s", err, stderr)
	}
	for _, want := range []string{"orders-s3abcd", "(suggested)", "active", "(source's)", "starts polling on schedule"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan missing %q: %s", want, out)
		}
	}
	checkTemplateControl(t, e, cloudCopyTemplate, args...)
}

// planCredentialLines returns the plan's "Credentials:" line and the indented
// lines that follow it.
func planCredentialLines(t *testing.T, plan string) string {
	t.Helper()
	start := strings.Index(plan, "Credentials:")
	if start < 0 {
		t.Fatalf("plan has no Credentials: line: %q", plan)
	}
	lines := strings.SplitAfter(plan[start:], "\n")
	block := lines[0]
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "  ") {
			break
		}
		block += line
	}
	return block
}

// The plan names the credentials the copy is given and lacks, and where its
// s3_access_key comes from, without a value. Each copy that is not refused
// reaches the POST carrying exactly the credential values the plan withholds.
func TestCopyCloudSourcePlanCredentials(t *testing.T) {
	const (
		sftpPassword = "supplied-sftp-password"
		replacement  = "replacement-key-id"
		notInherited = "Credentials: secrets and URLs are not inherited; values are not shown.\n"
	)
	for _, tc := range []struct {
		name, state, accessKey, file, want string
		resupply                           []string
		// sent is the credential members the POST carries, nil when refused.
		sent map[string]string
	}{
		{
			name: "active, every secret supplied, access key kept", state: "active",
			accessKey: "credential-id", resupply: []string{"s3_secret_key"}, file: cloudCopySecretsFile,
			want: notInherited + "  supplied s3_secret_key; s3_access_key kept from the source\n",
			sent: map[string]string{"s3_access_key": "credential-id", "s3_secret_key": cloudCopySecret},
		},
		{
			name: "file replaces the access key", state: "active",
			accessKey: "credential-id", resupply: []string{"s3_secret_key"},
			file: cloudCopySecretsFile + "s3_access_key: " + replacement + "\n",
			want: notInherited + "  supplied s3_secret_key; s3_access_key replaced from --secrets-file\n",
			sent: map[string]string{"s3_access_key": replacement, "s3_secret_key": cloudCopySecret},
		},
		{
			name: "file supplies an access key the source lacks", state: "disabled",
			resupply: []string{"s3_secret_key"}, file: cloudCopySecretsFile + "s3_access_key: " + replacement + "\n",
			want: notInherited + "  supplied s3_access_key, s3_secret_key\n",
			sent: map[string]string{"s3_access_key": replacement, "s3_secret_key": cloudCopySecret},
		},
		{
			name: "draft missing secrets", state: "draft",
			accessKey: "credential-id", resupply: []string{"s3_secret_key", "sftp_password", "gpg_passphrase"},
			file: "sftp_password: " + sftpPassword + "\n",
			want: notInherited +
				"  supplied sftp_password; s3_access_key kept from the source; missing s3_secret_key, gpg_passphrase; a draft copy saves without them\n" +
				"  To activate it later: add them to the spec of 'qctl get cloud-source <id> -o yaml', apply that with 'qctl apply -f', " +
				"then run 'qctl enable cloud-source <id>'\n",
			sent: map[string]string{"s3_access_key": "credential-id", "sftp_password": sftpPassword},
		},
		{
			name: "draft missing one secret", state: "draft",
			resupply: []string{"s3_secret_key"},
			want: notInherited + "  missing s3_secret_key; a draft copy saves without it\n" +
				"  To activate it later: add it to the spec of 'qctl get cloud-source <id> -o yaml', apply that with 'qctl apply -f', " +
				"then run 'qctl enable cloud-source <id>'\n",
			sent: map[string]string{"s3_access_key": ""},
		},
		{
			name: "source with no secrets", state: "active", resupply: []string{},
			want: notInherited,
			sent: map[string]string{"s3_access_key": ""},
		},
		{
			name: "active copy missing a secret", state: "active",
			accessKey: "credential-id", resupply: []string{"s3_secret_key"},
			want: notInherited + "  s3_access_key kept from the source; missing s3_secret_key; an active copy is refused without it\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newCloudCopyEnv(t)
			e.template["s3_access_key"], e.template["secrets_to_resupply"] = tc.accessKey, tc.resupply
			args := []string{"12", "--state", tc.state, "--yes"}
			if tc.file != "" {
				args = append(args, "--secrets-file", writeCloudSecretsFile(t, tc.file))
			}
			out, stderr, err := runCloudCopy(t, args...)
			assertNoCloudCopySecret(t, out, stderr, err, "credential-id", replacement, sftpPassword)
			if got := planCredentialLines(t, out); got != tc.want {
				t.Errorf("credential lines:\n%s\nwant:\n%s", got, tc.want)
			}
			if tc.sent == nil {
				if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage || len(e.postBodies) != 0 {
					t.Fatalf("error = %v, POSTs = %d; want exit 2 and no POST", err, len(e.postBodies))
				}
				return
			}
			if err != nil {
				t.Fatalf("copy: %v; stderr: %s", err, stderr)
			}
			// Harness-alive: the values the plan withholds are in the POST.
			body := cloudCopyPOSTBody(t, e)
			for _, key := range cloud_sources.CopyRequestSecretKeys() {
				want, wantSent := tc.sent[key]
				got, sent := body[key]
				switch {
				case sent != wantSent:
					t.Errorf("POST has %s: %v, want %v", key, sent, wantSent)
				case sent && string(got) != strconv.Quote(want):
					t.Errorf("POST %s is not the value the copy was given", key)
				}
			}
		})
	}
}
