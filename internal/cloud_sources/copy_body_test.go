package cloud_sources

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"strconv"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

const copyBodyTemplate = `{
	"name":"ordersabcd", "state":"active", "data_source_type":"s3",
	"settings_model_id":802, "dataset_id":42,
	"s3_bucket":"orders", "s3_prefix":"incoming/", "pattern":"*.csv",
	"schedule":"", "delete_source_file_after_backup":false, "sftp_port":0,
	"chunk_size_to_read_for_header":9007199254740993, "raw_headers":[],
	"csv_delimiter":null, "s3_region_name":null,
	"infer_datetime_override":false, "null_values_override":["NULL"],
	"value_mapping_override":{}, "xls_date_mode_override":null,
	"s3_access_key":"unmasked-access-key", "s3_secret_key":"•••••••",
	"sftp_password":"•••••••", "sftp_ssh_key":"•••••••",
	"sftp_ssh_key_passphrase":"•••••••", "gpg_private_key":"•••••••",
	"gpg_passphrase":"•••••••", "file_password":"•••••••",
	"gs_service_account_key":"•••••••", "dropbox_access_token":"•••••••",
	"dropbox_authorization_code":"unmasked-code", "simple_url":"https://user:secret@example.com",
	"id":7, "version_id":3, "created_at":"2026-10-06T00:00:00Z",
	"last_sync":"2026-10-06T01:00:00Z", "bad_rows_count":19,
	"internal_id":"server-id", "delta_applied_cursor":88,
	"period_delta_cursors":[{"dataset_id":42,"settings_model_id":802,"period_key":"2026-06","delta_applied_cursor":0}],
	"dataset_name":"target", "destination_name":"destination",
	"redirect_post_submit_to":"/datasets/42",
	"excluded_row_signatures_override":["signature"],
	"composite_spec_group_id":"group", "future_response_key":"response"
}`

const copyBodyExpected = `{
	"name":"ordersabcd", "state":"disabled", "data_source_type":"s3",
	"settings_model_id":802, "dataset_id":42,
	"s3_bucket":"orders", "s3_prefix":"incoming/", "pattern":"*.csv",
	"schedule":"", "delete_source_file_after_backup":false, "sftp_port":0,
	"chunk_size_to_read_for_header":9007199254740993, "raw_headers":[],
	"infer_datetime_override":false, "null_values_override":["NULL"],
	"value_mapping_override":{}, "s3_access_key":"unmasked-access-key"
}`

func TestBuildCopyBody(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides CopyOverrides
		changed   map[string]json.RawMessage
		absent    []string
	}{
		{
			name:      "template suggestion and caller state",
			overrides: CopyOverrides{State: "disabled"},
		},
		{
			name:      "name and state overrides",
			overrides: CopyOverrides{Name: cloudSourcePtr("new-name"), State: "draft"},
			changed:   map[string]json.RawMessage{"name": json.RawMessage(`"new-name"`), "state": json.RawMessage(`"draft"`)},
		},
		{
			name: "field overrides replace template values",
			overrides: CopyOverrides{State: "active", Fields: map[string]json.RawMessage{
				"s3_prefix": json.RawMessage(`"archive/"`),
				"pattern":   json.RawMessage(`"*.parquet"`),
				"sftp_port": json.RawMessage(`2222`),
			}},
			changed: map[string]json.RawMessage{
				"state": json.RawMessage(`"active"`), "s3_prefix": json.RawMessage(`"archive/"`),
				"pattern": json.RawMessage(`"*.parquet"`), "sftp_port": json.RawMessage(`2222`),
			},
		},
		{
			name: "field override supplies a null template field",
			overrides: CopyOverrides{State: "disabled", Fields: map[string]json.RawMessage{
				"csv_delimiter": json.RawMessage(`","`),
			}},
			changed: map[string]json.RawMessage{"csv_delimiter": json.RawMessage(`","`)},
		},
		{
			name: "null members stay absent",
			overrides: CopyOverrides{State: "disabled", Fields: map[string]json.RawMessage{
				"s3_prefix": json.RawMessage(` null `), "pattern": nil,
			}},
			absent: []string{"s3_prefix", "pattern"},
		},
		{
			name: "field overrides cannot inject credentials or other key classes",
			overrides: CopyOverrides{State: "disabled", Fields: map[string]json.RawMessage{
				"s3_access_key": json.RawMessage(`"new-key"`), "s3_secret_key": json.RawMessage(`"new-secret"`),
				"dropbox_authorization_code": json.RawMessage(`"new-code"`),
				"name":                       json.RawMessage(`"wrong-name"`), "state": json.RawMessage(`"deleted"`),
				"dataset_id": json.RawMessage(`999`), "settings_model_id": json.RawMessage(`999`),
				"data_source_type": json.RawMessage(`"dropbox"`), "id": json.RawMessage(`999`),
				"delta_applied_cursor": json.RawMessage(`999`), "future_field": json.RawMessage(`true`),
				"period_delta_cursors":             json.RawMessage(`[{}]`),
				"infer_datetime_override":          json.RawMessage(`true`),
				"excluded_row_signatures_override": json.RawMessage(`["new-signature"]`),
			}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			template := []byte(copyBodyTemplate)
			fieldsBefore, err := json.Marshal(tc.overrides.Fields)
			if err != nil {
				t.Fatal(err)
			}
			body, err := BuildCopyBody(template, tc.overrides)
			if err != nil {
				t.Fatalf("BuildCopyBody: %v", err)
			}
			got := bodyMembers(t, "copy", body)
			want := bodyMembers(t, "expected copy", []byte(copyBodyExpected))
			for key, value := range tc.changed {
				want[key] = value
			}
			for _, key := range tc.absent {
				delete(want, key)
			}
			assertCopyBodyMembers(t, got, want)
			if !bytes.Equal(template, []byte(copyBodyTemplate)) {
				t.Error("BuildCopyBody changed its template input")
			}
			fieldsAfter, err := json.Marshal(tc.overrides.Fields)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(fieldsBefore, fieldsAfter) {
				t.Error("BuildCopyBody changed its field overrides")
			}
		})
	}
}

func assertCopyBodyMembers(t *testing.T, got, want map[string]json.RawMessage) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("copy has %d keys, want %d", len(got), len(want))
	}
	for key, value := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("copy carries unexpected key %s", key)
		}
		if isNull(value) {
			t.Errorf("copy carries null member %s", key)
		}
	}
	for key, value := range want {
		actual, ok := got[key]
		same, err := sameJSON(actual, value)
		if !ok || err != nil || !same {
			t.Errorf("copy %s = %s (present %v), want %s: %v", key, actual, ok, value, err)
		}
	}
}

// templateAccessKey is copyBodyTemplate's s3_access_key value.
const templateAccessKey = "unmasked-access-key"

// copyCredentialMembers returns the credential and URL members of members.
func copyCredentialMembers(members map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for key, value := range members {
		if isCopyCredential(key) {
			out[key] = value
		}
	}
	return out
}

func jsonString(t *testing.T, value string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func expectedCopyBody(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	return bodyMembers(t, "expected copy", []byte(copyBodyExpected))
}

func TestBuildCopyBodyForwardsNoTemplateCredentialButTheAccessKey(t *testing.T) {
	unmasked := bodyMembers(t, "template", []byte(copyBodyTemplate))
	for key := range unmasked {
		if isCopyCredential(key) && key != s3AccessKey {
			unmasked[key] = json.RawMessage(`"unmasked-credential"`)
		}
	}
	for _, tc := range []struct {
		name    string
		members map[string]json.RawMessage
	}{
		{name: "as the template sends them", members: bodyMembers(t, "template", []byte(copyBodyTemplate))},
		{name: "every credential unmasked", members: unmasked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credentials := copyCredentialMembers(tc.members)
			for _, key := range append(CopyRequestSecretKeys(), legacyDropboxKey) {
				if _, ok := credentials[key]; !ok {
					t.Fatalf("the template has no %s, so its omission proves nothing", key)
				}
			}
			template, err := json.Marshal(tc.members)
			if err != nil {
				t.Fatal(err)
			}
			body, err := BuildCopyBody(template, CopyOverrides{State: "disabled"})
			if err != nil {
				t.Fatalf("BuildCopyBody: %v", err)
			}
			assertCopyBodyMembers(t, bodyMembers(t, "copy", body), expectedCopyBody(t))
			if bytes.Contains(body, []byte("unmasked-credential")) || bytes.Contains(body, []byte("•")) {
				t.Errorf("copy carries a template credential: %s", body)
			}
		})
	}
}

func TestBuildCopyBodyOmitsAnAccessKeyTheTemplateLacks(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]json.RawMessage)
	}{
		{name: "absent", mutate: func(m map[string]json.RawMessage) { delete(m, s3AccessKey) }},
		{name: "null", mutate: func(m map[string]json.RawMessage) { m[s3AccessKey] = json.RawMessage(`null`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			members := bodyMembers(t, "template", []byte(copyBodyTemplate))
			if _, ok := members[s3AccessKey]; !ok {
				t.Fatal("the template has no s3_access_key to remove")
			}
			tc.mutate(members)
			template, err := json.Marshal(members)
			if err != nil {
				t.Fatal(err)
			}
			body, err := BuildCopyBody(template, CopyOverrides{State: "disabled"})
			if err != nil {
				t.Fatalf("BuildCopyBody: %v", err)
			}
			want := expectedCopyBody(t)
			delete(want, s3AccessKey)
			assertCopyBodyMembers(t, bodyMembers(t, "copy", body), want)
		})
	}
}

func TestBuildCopyBodyCarriesEachSuppliedSecretExactlyOnce(t *testing.T) {
	keys := CopyRequestSecretKeys()
	if len(keys) == 0 {
		t.Fatal("CopyRequestSecretKeys is empty; this check cannot fail as written")
	}
	templateMembers := bodyMembers(t, "template", []byte(copyBodyTemplate))
	all := map[string]string{}
	for _, key := range keys {
		all[key] = "supplied-" + key + "-value"
	}
	cases := map[string]map[string]string{"every key at once": all}
	for _, key := range keys {
		cases[key] = map[string]string{key: all[key]}
	}
	for name, secrets := range cases {
		t.Run(name, func(t *testing.T) {
			before := maps.Clone(secrets)
			body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{State: "disabled", Secrets: secrets})
			if err != nil {
				t.Fatalf("BuildCopyBody: %v", err)
			}
			want := expectedCopyBody(t)
			for key, value := range secrets {
				supplied := jsonString(t, value)
				if same, _ := sameJSON(templateMembers[key], supplied); same {
					t.Fatalf("the template already holds the supplied %s", key)
				}
				want[key] = supplied
				if n := bytes.Count(body, []byte(value)); n != 1 {
					t.Errorf("copy carries the supplied %s %d times, want once", key, n)
				}
			}
			assertCopyBodyMembers(t, bodyMembers(t, "copy", body), want)
			if !maps.Equal(secrets, before) {
				t.Error("BuildCopyBody changed its secrets")
			}
		})
	}
}

func TestBuildCopyBodySuppliedAccessKeyReplacesTheTemplates(t *testing.T) {
	if !bytes.Contains([]byte(copyBodyTemplate), []byte(templateAccessKey)) {
		t.Fatal("the template has no access key to replace")
	}
	body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{
		State: "disabled", Secrets: map[string]string{s3AccessKey: "replacement-access-key"},
	})
	if err != nil {
		t.Fatalf("BuildCopyBody: %v", err)
	}
	want := expectedCopyBody(t)
	want[s3AccessKey] = jsonString(t, "replacement-access-key")
	assertCopyBodyMembers(t, bodyMembers(t, "copy", body), want)
	if bytes.Contains(body, []byte(templateAccessKey)) {
		t.Errorf("copy still carries the template's access key: %s", body)
	}
}

func TestBuildCopyBodyForwardsOnlyASuppliedURL(t *testing.T) {
	const templateURL = "https://user:secret@example.com"
	if !bytes.Contains([]byte(copyBodyTemplate), []byte(templateURL)) {
		t.Fatal("the template has no URL, so its omission proves nothing")
	}
	for _, tc := range []struct {
		name    string
		secrets map[string]string
	}{
		{name: "not supplied"},
		{name: "supplied", secrets: map[string]string{simpleURLKey: "https://example.org/orders.csv"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{State: "disabled", Secrets: tc.secrets})
			if err != nil {
				t.Fatalf("BuildCopyBody: %v", err)
			}
			want := expectedCopyBody(t)
			if url, ok := tc.secrets[simpleURLKey]; ok {
				want[simpleURLKey] = jsonString(t, url)
			}
			assertCopyBodyMembers(t, bodyMembers(t, "copy", body), want)
			if bytes.Contains(body, []byte("example.com")) {
				t.Errorf("copy carries the template's URL: %s", body)
			}
		})
	}
}

func TestBuildCopyBodyTakesCredentialsOnlyFromSecrets(t *testing.T) {
	fields := map[string]json.RawMessage{
		"s3_secret_key": json.RawMessage(`"field-secret"`), s3AccessKey: json.RawMessage(`"field-access-key"`),
		simpleURLKey: json.RawMessage(`"https://field.example.org"`), "s3_prefix": json.RawMessage(`"archive/"`),
	}
	body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{
		State: "disabled", Fields: fields, Secrets: map[string]string{"s3_secret_key": "supplied-secret"},
	})
	if err != nil {
		t.Fatalf("BuildCopyBody: %v", err)
	}
	want := expectedCopyBody(t)
	want["s3_prefix"] = json.RawMessage(`"archive/"`)
	want["s3_secret_key"] = jsonString(t, "supplied-secret")
	assertCopyBodyMembers(t, bodyMembers(t, "copy", body), want)
	if bytes.Contains(body, []byte("field-")) {
		t.Errorf("copy carries a credential from its field overrides: %s", body)
	}
}

func TestBuildCopyBodyRefusesAnUnknownSecretKeyWithoutItsValue(t *testing.T) {
	const value = "secret-value-must-not-appear"
	for _, key := range []string{legacyDropboxKey, "s3_bucket", "not_a_field", "S3_SECRET_KEY", ""} {
		t.Run(key, func(t *testing.T) {
			body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{
				State: "disabled", Secrets: map[string]string{key: value, "s3_secret_key": value},
			})
			if err == nil || body != nil {
				t.Fatalf("BuildCopyBody = %s, %v; want no body and an error", body, err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d: %v", code, qerrors.ExitGenericError, err)
			}
			if !strings.Contains(err.Error(), strconv.Quote(key)) {
				t.Errorf("error = %v, want it to name %q", err, key)
			}
			if strings.Contains(err.Error(), value) {
				t.Error("secret value appears in the error")
			}
		})
	}
}

func TestRedactCopyTemplateRemovesEveryCredentialIncludingTheAccessKey(t *testing.T) {
	template := bodyMembers(t, "template", []byte(copyBodyTemplate))
	credentials := copyCredentialMembers(template)
	if _, ok := credentials[s3AccessKey]; !ok || len(credentials) < 2 {
		t.Fatalf("the template carries credentials %v; this check cannot fail as written", credentials)
	}
	redacted, err := RedactCopyTemplate([]byte(copyBodyTemplate))
	if err != nil {
		t.Fatalf("RedactCopyTemplate: %v", err)
	}
	got := bodyMembers(t, "redacted template", redacted)
	for key, value := range template {
		_, kept := got[key]
		if isCopyCredential(key) == kept {
			t.Errorf("redacted template keeps %s = %v, want %v", key, kept, !isCopyCredential(key))
		}
		if kept {
			if same, err := sameJSON(got[key], value); err != nil || !same {
				t.Errorf("redacted template %s = %s, want %s: %v", key, got[key], value, err)
			}
		}
	}
	if len(got) != len(template)-len(credentials) {
		t.Errorf("redacted template has %d keys, want %d", len(got), len(template)-len(credentials))
	}
}

func TestBuildCopyBodyRejectsMalformedJSON(t *testing.T) {
	for _, template := range []string{`{`, `[]`, `null`, `"template"`, `42`, ``} {
		t.Run(template, func(t *testing.T) {
			body, err := BuildCopyBody([]byte(template), CopyOverrides{State: "draft"})
			if err == nil || body != nil {
				t.Fatalf("BuildCopyBody(%q) = %s, %v; want no body and an error", template, body, err)
			}
		})
	}
	fields := map[string]json.RawMessage{"s3_prefix": json.RawMessage(`{`)}
	before := append(json.RawMessage(nil), fields["s3_prefix"]...)
	body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{State: "draft", Fields: fields})
	if err == nil || body != nil {
		t.Fatalf("invalid raw override produced %s, %v; want no body and an error", body, err)
	}
	if !reflect.DeepEqual(fields["s3_prefix"], before) {
		t.Error("BuildCopyBody changed its invalid field override")
	}
}

func TestRedactCopyTemplateRejectsMalformedJSON(t *testing.T) {
	for _, template := range []string{`{`, `[]`, `null`, `"template"`, `42`, ``} {
		if body, err := RedactCopyTemplate([]byte(template)); err == nil || body != nil {
			t.Fatalf("RedactCopyTemplate(%q) = %s, %v; want no body and an error", template, body, err)
		}
	}
}
