package cloud_sources

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestParseCopyFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   map[string]json.RawMessage
	}{
		{name: "omitted"},
		{
			name: "typed fields and hyphen aliases",
			values: []string{
				"sftp-port=2222", "schedule=@hourly", "pattern=*.csv",
				"s3_prefix=", "delete-source-file-after-backup=false",
				`raw_headers=[name, "NULL", count]`, "connection_timeout=0",
				"chunk_size_to_read_for_header=9007199254740993",
			},
			want: map[string]json.RawMessage{
				"sftp_port": json.RawMessage(`2222`), "schedule": json.RawMessage(`"@hourly"`),
				"pattern": json.RawMessage(`"*.csv"`), "s3_prefix": json.RawMessage(`""`),
				"delete_source_file_after_backup": json.RawMessage(`false`),
				"raw_headers":                     json.RawMessage(`["name","NULL","count"]`),
				"connection_timeout":              json.RawMessage(`0`),
				"chunk_size_to_read_for_header":   json.RawMessage(`9007199254740993`),
			},
		},
		{
			name: "strings preserve flag text",
			values: []string{
				" pattern =a=b,c", "sftp_host=  example.com  ",
				"s3_bucket=null", "s3_prefix=[folder]", "csv_quotechar=\"",
				"schedule=0 0 * * *",
			},
			want: map[string]json.RawMessage{
				"pattern": json.RawMessage(`"a=b,c"`), "sftp_host": json.RawMessage(`"  example.com  "`),
				"s3_bucket": json.RawMessage(`"null"`), "s3_prefix": json.RawMessage(`"[folder]"`),
				"csv_quotechar": json.RawMessage(`"\""`), "schedule": json.RawMessage(`"0 0 * * *"`),
			},
		},
		{
			name:   "true and empty list remain supplied",
			values: []string{"delete_source_file_after_backup=true", "raw_headers=[]"},
			want: map[string]json.RawMessage{
				"delete_source_file_after_backup": json.RawMessage(`true`), "raw_headers": json.RawMessage(`[]`),
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.values)
			got, err := ParseCopyFields(tc.values)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != len(tc.want) {
				t.Errorf("ParseCopyFields returned %d fields, want %d", len(got), len(tc.want))
			}
			for key, want := range tc.want {
				same, err := sameJSON(got[key], want)
				if err != nil || !same {
					t.Errorf("ParseCopyFields %s = %s, want %s: %v", key, got[key], want, err)
				}
			}
			if !slices.Equal(tc.values, before) {
				t.Error("ParseCopyFields changed its input")
			}
		})
	}
}

func TestParseCopyFieldsRefusals(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		values     []string
	}{
		{name: "unknown field", values: []string{"not_a_field=value"}, want: "unknown field"},
		{name: "empty key", values: []string{"=value"}, want: "unknown field"},
		{name: "settings overrides", values: []string{"settings_overrides={}"}, want: "unknown field"},
		{name: "wire settings override", values: []string{"null_values_override=[]"}, want: "unknown field"},
		{name: "status field", values: []string{"delta_applied_cursor=1"}, want: "unknown field"},
		{name: "period cursor status", values: []string{"period_delta_cursors=[]"}, want: "unknown field"},
		{name: "duplicate key", values: []string{"schedule=@hourly", "schedule=@daily"}, want: "more than once"},
		{name: "duplicate alias", values: []string{"sftp-port=22", " sftp_port =2222"}, want: "more than once"},
		{name: "missing equals", values: []string{"schedule"}, want: "expected key=value"},
		{name: "empty flag", values: []string{""}, want: "expected key=value"},
		{name: "empty integer", values: []string{"sftp_port="}, want: "empty value"},
		{name: "empty boolean", values: []string{"delete_source_file_after_backup="}, want: "empty value"},
		{name: "empty list", values: []string{"raw_headers="}, want: "empty value"},
		{name: "invalid integer", values: []string{"sftp_port=not-an-int"}, want: "cannot unmarshal"},
		{name: "integer overflow", values: []string{"sftp_port=99999999999999999999999999999999999"}, want: "cannot unmarshal"},
		{name: "invalid boolean", values: []string{"delete_source_file_after_backup=not-a-bool"}, want: "cannot unmarshal"},
		{name: "invalid list", values: []string{"raw_headers=header"}, want: "cannot unmarshal"},
		{name: "invalid YAML", values: []string{"raw_headers=["}, want: "decode field"},
		{name: "null integer", values: []string{"sftp_port=null"}, want: "must not be null"},
		{name: "null boolean", values: []string{"delete_source_file_after_backup=~"}, want: "must not be null"},
		{name: "null list", values: []string{"raw_headers=null"}, want: "must not be null"},
		{name: "null list element", values: []string{"raw_headers=[NULL]"}, want: "null"},
		{name: "blank non-string", values: []string{"sftp_port= "}, want: "decode field"},
		{name: "comment only", values: []string{"sftp_port=# ignored"}, want: "decode field"},
		{name: "extra YAML document", values: []string{"sftp_port=22\n---\n2222"}, want: "one YAML document"},
		{name: "malformed extra document", values: []string{"raw_headers=[]\n---\n["}, want: "decode field"},
		{name: "table identity", values: []string{"dataset-id=42"}, want: "--to-table"},
		{name: "settings identity", values: []string{"settings_model_id=42"}, want: "--to-table"},
		{name: "name identity", values: []string{"name=copy"}, want: "--name"},
		{name: "state identity", values: []string{"state=active"}, want: "--state"},
		{name: "type identity", values: []string{"data_source_type=sftp"}, want: "keeps the source's type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseCopyFields(tc.values)
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want 2: %v", qerrors.GetExitCode(err), err)
			}
			if got != nil {
				t.Error("refused overrides returned field members")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestParseCopyFieldsRefusesCredentialsWithoutTheirValues(t *testing.T) {
	remedies := map[string][]string{legacyDropboxKey: {dropboxAuthorizationCode, "--secrets-file"}, "password": {"a copy cannot take it"}}
	for _, key := range CopyRequestSecretKeys() {
		remedies[key] = []string{"--secrets-file"}
	}
	for _, key := range fieldKeys {
		if _, ok := remedies[key]; isCopyCredential(key) && !ok {
			remedies[key] = []string{"a copy cannot take it"}
		}
	}
	if len(remedies) < 3 {
		t.Fatalf("only %d credential keys to refuse; this check cannot fail as written", len(remedies))
	}
	for key, want := range remedies {
		t.Run(key, func(t *testing.T) {
			if !isCopyCredential(key) {
				t.Fatalf("%s is not a credential, so --set refuses it for another reason", key)
			}
			const credential = "credential-value-must-not-appear"
			_, err := ParseCopyFields([]string{strings.ReplaceAll(key, "_", "-") + "=" + credential})
			if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want 2: %v", qerrors.GetExitCode(err), err)
			}
			for _, remedy := range want {
				if !strings.Contains(err.Error(), remedy) {
					t.Errorf("error = %v, want it to name %s", err, remedy)
				}
			}
			if strings.Contains(err.Error(), credential) {
				t.Error("credential value appears in the error")
			}
		})
	}
}

func TestParseCopyFieldsAcceptsEveryEligibleGeneratedField(t *testing.T) {
	typ := reflect.TypeOf(CloudSourceFields{})
	for i, key := range fieldKeys {
		if isCopyCredential(key) || slices.Contains(createIdentityKeys, key) {
			continue
		}
		t.Run(key, func(t *testing.T) {
			value := sampleValue(t, typ.Field(i).Type, key)
			want, err := json.Marshal(value.Interface())
			if err != nil {
				t.Fatal(err)
			}
			text := string(want)
			if value.Elem().Kind() == reflect.String {
				text = value.Elem().String()
			}
			for _, spelling := range []string{key, strings.ReplaceAll(key, "_", "-")} {
				got, err := ParseCopyFields([]string{spelling + "=" + text})
				if err != nil {
					t.Fatalf("ParseCopyFields(%s): %v", spelling, err)
				}
				if len(got) != 1 || !bytes.Equal(got[key], want) {
					t.Errorf("ParseCopyFields(%s) = %v, want %s=%s", spelling, got, key, want)
				}
			}
		})
	}
}

func TestParsedCopyFieldsReachCopyBody(t *testing.T) {
	fields, err := ParseCopyFields([]string{"sftp_port=2222", "schedule=@hourly", "s3_prefix=", "raw_headers=[]"})
	if err != nil {
		t.Fatal(err)
	}
	body, err := BuildCopyBody([]byte(copyBodyTemplate), CopyOverrides{State: "draft", Fields: fields})
	if err != nil {
		t.Fatal(err)
	}
	got := bodyMembers(t, "copy", body)
	for key, want := range fields {
		if !bytes.Equal(got[key], want) {
			t.Errorf("copy %s = %s, want %s", key, got[key], want)
		}
	}
}
