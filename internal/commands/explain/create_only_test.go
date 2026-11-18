package explain_test

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// createOnlySpec serves cloud-source's read and write components, both with
// state and table_series_id, the fields cloud-source.md lists in createOnly:.
var createOnlySpec = cloudSourceSpec(
	`"DataSourceState": {"type": "string", "enum": ["active", "deleted", "disabled", "draft"]}`,
	`"DataSourceModelAllOptionalSchema": {
      "type": "object",
      "properties": {
        "dropbox_folder": {"type": "string"},
        "state": {"anyOf": [{"$ref": "#/components/schemas/DataSourceState"}, {"type": "null"}]},
        "table_series_id": {"anyOf": [{"type": "string", "format": "uuid"}, {"type": "null"}]}
      }
    }`,
	`"DataSourceModelPatchRequestSchema": {
      "type": "object",
      "properties": {
        "dropbox_folder": {"type": "string"},
        "state": {"anyOf": [{"$ref": "#/components/schemas/DataSourceState"}, {"type": "null"}]},
        "table_series_id": {"anyOf": [{"type": "string", "format": "uuid"}, {"type": "null"}]}
      }
    }`,
)

// explainRawJSON runs `qctl explain <arg> -o json` and decodes stdout into a
// generic map, so the assertions see the keys as written.
func explainRawJSON(t *testing.T, arg string) map[string]any {
	t.Helper()
	var doc map[string]any
	explainJSON(t, arg, &doc)
	return doc
}

// propertyNamed returns the entry of doc's properties whose name is name.
func propertyNamed(t *testing.T, doc map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := doc["properties"].([]any)
	for _, p := range props {
		if m, ok := p.(map[string]any); ok && m["name"] == name {
			return m
		}
	}
	t.Fatalf("no property %q in %v", name, doc)
	return nil
}

// explain cloud-source marks the fields its page lists in createOnly:, in the
// field list, in the field-path form, and in -o json and -o yaml; a field the
// page does not list carries no mark.
func TestExplainMarksCreateOnlyFields(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	serveSpec(t, env, "explain-create-only-1", createOnlySpec)

	out, errOut, err := runExplain(t, "explain", "cloud-source", "-o", "table")
	if err != nil {
		t.Fatalf("qctl explain cloud-source: %v\nstderr:\n%s", err, errOut)
	}
	for _, line := range []string{
		"  state <DataSourceState, nullable> <create-only: draft>\n",
		"    Allowed: active, deleted, disabled, draft\n",
		"  dropbox_folder <string>\n",
		"  table_series_id <string(uuid), nullable> <create-only>\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("qctl explain cloud-source: want the line %q in:\n%s", line, out)
		}
	}

	out, errOut, err = runExplain(t, "explain", "cloud-source.state", "-o", "table")
	if err != nil {
		t.Fatalf("qctl explain cloud-source.state: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(out, "CREATEONLY: true\n") {
		t.Errorf("qctl explain cloud-source.state: want CREATEONLY: true in:\n%s", out)
	}
	if !strings.Contains(out, "CREATEONLY VALUES: draft\n") {
		t.Errorf("qctl explain cloud-source.state: want accepted create values in:\n%s", out)
	}
	if !strings.Contains(out, "ALLOWED VALUES:\n  - active\n  - deleted\n  - disabled\n  - draft\n") {
		t.Errorf("qctl explain cloud-source.state: want all read enum values in:\n%s", out)
	}

	out, errOut, err = runExplain(t, "explain", "cloud-source.dropbox_folder", "-o", "table")
	if err != nil {
		t.Fatalf("qctl explain cloud-source.dropbox_folder: %v\nstderr:\n%s", err, errOut)
	}
	if strings.Contains(out, "CREATEONLY") {
		t.Errorf("qctl explain cloud-source.dropbox_folder: an unlisted field is marked create-only:\n%s", out)
	}

	doc := explainRawJSON(t, "cloud-source")
	if got := propertyNamed(t, doc, "state")["createOnly"]; got != true {
		t.Errorf("explain cloud-source -o json: state.createOnly = %v, want true", got)
	}
	assertCreateOnlyValues(t, propertyNamed(t, doc, "state"))
	series := propertyNamed(t, doc, "table_series_id")
	if got := series["createOnly"]; got != true {
		t.Errorf("explain cloud-source -o json: table_series_id.createOnly = %v, want true", got)
	}
	if got, ok := series["createOnlyValues"]; ok {
		t.Errorf("explain cloud-source -o json: table_series_id.createOnlyValues = %v, want no key", got)
	}
	if got, ok := propertyNamed(t, doc, "dropbox_folder")["createOnly"]; ok {
		t.Errorf("explain cloud-source -o json: dropbox_folder.createOnly = %v, want no key", got)
	}
	if got := explainRawJSON(t, "cloud-source.state")["createOnly"]; got != true {
		t.Errorf("explain cloud-source.state -o json: createOnly = %v, want true", got)
	}
	assertCreateOnlyValues(t, explainRawJSON(t, "cloud-source.state"))
	assertCreateOnlyValues(t, explainRawJSON(t, "data-source.state"))
	if got, ok := explainRawJSON(t, "cloud-source.dropbox_folder")["createOnly"]; ok {
		t.Errorf("explain cloud-source.dropbox_folder -o json: createOnly = %v, want no key", got)
	}
	if got, ok := explainRawJSON(t, "cloud-source.dropbox_folder")["createOnlyValues"]; ok {
		t.Errorf("explain cloud-source.dropbox_folder -o json: createOnlyValues = %v, want no key", got)
	}

	out, errOut, err = runExplain(t, "explain", "cloud-source.state", "-o", "yaml")
	if err != nil {
		t.Fatalf("qctl explain cloud-source.state -o yaml: %v\nstderr:\n%s", err, errOut)
	}
	if !strings.Contains(out, "\ncreateOnly: true\n") {
		t.Errorf("qctl explain cloud-source.state -o yaml: want createOnly: true in:\n%s", out)
	}
	var field map[string]any
	if err := yaml.Unmarshal([]byte(out), &field); err != nil {
		t.Fatalf("decode explain YAML: %v", err)
	}
	assertCreateOnlyValues(t, field)
}

func assertCreateOnlyValues(t *testing.T, field map[string]any) {
	t.Helper()
	values, _ := field["createOnlyValues"].([]any)
	if len(values) != 1 || values[0] != "draft" {
		t.Errorf("createOnlyValues = %v, want [draft]", field["createOnlyValues"])
	}
	enum, _ := field["enum"].([]any)
	if len(enum) != 4 || enum[0] != "active" || enum[1] != "deleted" || enum[2] != "disabled" || enum[3] != "draft" {
		t.Errorf("enum = %v, want all four read values", field["enum"])
	}
}
