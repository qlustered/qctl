package explain_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/qlustered/qctl/internal/testutil"
)

// cloudSourceRead is the read component cloud-source.md names in schema:.
const cloudSourceRead = `"DataSourceModelAllOptionalSchema": {
      "type": "object",
      "properties": {
        "id": {"type": "integer"},
        "created_at": {"type": "string", "format": "date-time"},
        "dropbox_access_token": {"anyOf": [{"type": "string", "format": "password", "writeOnly": true}, {"type": "null"}]},
        "dropbox_folder": {"type": "string", "description": "The folder on Dropbox to look for files."}
      }
    }`

// cloudSourceWrite is the write component cloud-source.md names in
// writeSchema:.
const cloudSourceWrite = `"DataSourceModelPatchRequestSchema": {
      "type": "object",
      "required": ["id"],
      "properties": {
        "id": {"type": "integer"},
        "dropbox_authorization_code": {"anyOf": [{"type": "string", "minLength": 1, "format": "password", "writeOnly": true}, {"type": "null"}],
          "description": "Dropbox OAuth authorization code to exchange for an access token"},
        "dropbox_folder": {"type": "string", "description": "The folder on Dropbox to look for files."}
      }
    }`

func cloudSourceSpec(components ...string) string {
	return `{"openapi": "3.1.0", "info": {"title": "t", "version": "1"}, "paths": {},
  "components": {"schemas": {` + strings.Join(components, ",") + `}}}`
}

var (
	idField           = schema.FieldInfo{Name: "id", Type: "integer"}
	dropboxFolder     = schema.FieldInfo{Name: "dropbox_folder", Type: "string", Description: "The folder on Dropbox to look for files."}
	createdAtReadOnly = schema.FieldInfo{Name: "created_at", Type: "string", Format: "date-time", ReadOnly: true}
	accessToken       = schema.FieldInfo{Name: "dropbox_access_token", Type: "string", Format: "password", Nullable: true}
	authorizationCode = schema.FieldInfo{
		Name: "dropbox_authorization_code", Type: "string", Format: "password", Nullable: true, WriteOnly: true,
		Description: "Dropbox OAuth authorization code to exchange for an access token",
	}
)

// explainJSON runs `qctl explain <arg> -o json` and decodes stdout into v.
func explainJSON(t *testing.T, arg string, v any) {
	t.Helper()
	out, errOut, err := runExplain(t, "explain", arg, "-o", "json")
	if err != nil {
		t.Fatalf("qctl explain %s -o json: %v\nstderr:\n%s", arg, err, errOut)
	}
	if err := json.Unmarshal([]byte(out), v); err != nil {
		t.Fatalf("-o json does not parse: %v\n%s", err, out)
	}
}

// explain cloud-source serves the read schema with the write schema merged in:
// a field only writes take is listed write-only, and a read field writes do not
// take is read-only.
func TestExplainMergesTheWriteSchema(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-write-schema-1", cloudSourceSpec(cloudSourceRead, cloudSourceWrite))

	var info schema.SchemaInfo
	explainJSON(t, "cloud-source", &info)
	tokenReadOnly := accessToken
	tokenReadOnly.ReadOnly = true
	want := []schema.FieldInfo{createdAtReadOnly, tokenReadOnly, authorizationCode, dropboxFolder, idField}
	if !reflect.DeepEqual(info.Fields, want) {
		t.Errorf("explain cloud-source -o json fields\n got: %+v\nwant: %+v", info.Fields, want)
	}

	out, errOut, err := runExplain(t, "explain", "cloud-source", "-o", "table")
	if err != nil {
		t.Fatalf("qctl explain cloud-source: %v\nstderr:\n%s", err, errOut)
	}
	for _, line := range []string{
		"  dropbox_authorization_code <string(password), nullable> <write-only>\n",
		"  dropbox_access_token <string(password), nullable> <read-only>\n",
		"  dropbox_folder <string>\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("qctl explain cloud-source: want the line %q in:\n%s", line, out)
		}
	}

	for _, tc := range []struct {
		arg  string
		want schema.FieldInfo
		line string
	}{
		{"cloud-source.dropbox_authorization_code", authorizationCode, "WRITEONLY: true\n"},
		{"cloud-source.dropbox_access_token", tokenReadOnly, "READONLY: true\n"},
	} {
		var got schema.FieldInfo
		explainJSON(t, tc.arg, &got)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("explain %s -o json\n got: %+v\nwant: %+v", tc.arg, got, tc.want)
		}
		out, errOut, err := runExplain(t, "explain", tc.arg, "-o", "table")
		if err != nil {
			t.Fatalf("qctl explain %s: %v\nstderr:\n%s", tc.arg, err, errOut)
		}
		if !strings.Contains(out, tc.line) {
			t.Errorf("qctl explain %s: want the line %q in:\n%s", tc.arg, tc.line, out)
		}
	}
}

// A live spec without the write component serves the read schema as it is:
// nothing is marked, and a field only writes take is not a field.
func TestExplainWithoutTheWriteSchemaServesTheRead(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-write-schema-absent-1", cloudSourceSpec(cloudSourceRead))

	var info schema.SchemaInfo
	explainJSON(t, "cloud-source", &info)
	createdAt := createdAtReadOnly
	createdAt.ReadOnly = false
	want := []schema.FieldInfo{createdAt, accessToken, dropboxFolder, idField}
	if !reflect.DeepEqual(info.Fields, want) {
		t.Errorf("explain cloud-source -o json fields\n got: %+v\nwant: %+v", info.Fields, want)
	}

	_, _, err := runExplain(t, "explain", "cloud-source.dropbox_authorization_code", "-o", "table")
	if err == nil {
		t.Fatal("explain cloud-source.dropbox_authorization_code resolved against a spec that lacks it")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
}
