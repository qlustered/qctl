package explain

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func setupExplainRoot() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "Output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow insecure http")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Allow plaintext secrets")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity")
	rootCmd.AddCommand(NewCommand())
	return rootCmd
}

// execExplain runs `qctl explain <args...>` against a fresh test env and
// returns (stdout, error).
func execExplain(t *testing.T, mock *testutil.MockAPIServer, args ...string) (string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "token")

	cmd := setupExplainRoot()
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"explain"}, args...))
	err := cmd.Execute()
	return stdout.String(), err
}

func TestExplainTableRule(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// No schema endpoints registered: the table-rule schema must be static.

	out, err := execExplain(t, mock, "table-rule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"TableRule", "dataset_id", "rule_revision_id", "instance_name", "column_mapping", "metadata"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain table-rule output missing %q; got:\n%s", want, out)
		}
	}
}

func TestExplainTableRuleFieldPath(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	out, err := execExplain(t, mock, "table-rule.spec.column_mapping")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "column_mapping") || !strings.Contains(out, "the server binds each rule field left out") {
		t.Errorf("expected column_mapping field docs describing the server's binding; got:\n%s", out)
	}
	for _, want := range []string{"placeholder", "optional_columns", "standard field", "always wins", "original header", "normalizer", "spec.upgradedFrom", "describe rule <rule> --table", "candidate columns"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected column_mapping field docs to mention %q; got:\n%s", want, out)
		}
	}
}

// TestExplainTableRuleColumnField: explain is the offline schema for what
// `apply -f` parses. A field apply accepts and explain omits is a field nobody
// finds.
func TestExplainTableRuleColumnField(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	out, err := execExplain(t, mock, "table-rule.spec.column")
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	for _, want := range []string{"column_mapping", "Instantiate-only"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected spec.column docs to mention %q; got:\n%s", want, out)
		}
	}
}

func TestExplainTableRuleDocumentsSpecRule(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	out, err := execExplain(t, mock, "table-rule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"rule_revision_id", "SHORT-ID", "slug", "release"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain table-rule output missing %q; got:\n%s", want, out)
		}
	}
}

func TestExplainTableRuleSpecRuleField(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	out, err := execExplain(t, mock, "table-rule.spec.rule")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Mutually exclusive") {
		t.Errorf("expected spec.rule docs to state the exclusivity with rule_revision_id; got:\n%s", out)
	}
}

func TestExplainTableRuleUnknownField(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	_, err := execExplain(t, mock, "table-rule.spec.bogus_field")
	if err == nil {
		t.Fatal("expected error for unknown field path")
	}
	if !strings.Contains(err.Error(), "bogus_field") {
		t.Errorf("expected error to name the unknown field, got: %v", err)
	}
}

// minimalCompositeSpec is a tiny OpenAPI 3.0 document with just the
// composite-dataset apply schemas, served by the mock transport.
const minimalCompositeSpec = `{
  "openapi": "3.0.3",
  "info": {"title": "qluster", "version": "0.0.1"},
  "paths": {},
  "components": {
    "schemas": {
      "CompositeDatasetApplyManifest": {
        "type": "object",
        "description": "Body of POST /orgs/{org}/composite-datasets/apply.",
        "required": ["apiVersion", "kind", "metadata", "spec"],
        "properties": {
          "apiVersion": {"type": "string"},
          "kind": {"type": "string"},
          "metadata": {"type": "object", "properties": {"name": {"type": "string"}}},
          "spec": {"$ref": "#/components/schemas/CompositeDatasetApplySpec"}
        }
      },
      "CompositeDatasetApplySpec": {
        "type": "object",
        "required": ["destination"],
        "properties": {
          "destination": {"type": "string", "description": "Destination name"},
          "desired_state": {"type": "string", "enum": ["draft", "enabled"]}
        }
      }
    }
  }
}`

func TestExplainLinkedTable(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	mock.RegisterHandler("GET", "/api/version", func(w http.ResponseWriter, r *http.Request) {
		// Unique version string: avoids hitting another test's in-memory/disk spec cache.
		testutil.RespondJSON(w, http.StatusOK, map[string]string{"version": "explain-linked-table-test-1"})
	})
	mock.RegisterHandler("GET", "/api/docs/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondText(w, http.StatusOK, minimalCompositeSpec)
	})

	out, err := execExplain(t, mock, "linked-table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"destination", "desired_state", "draft"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain linked-table output missing %q; got:\n%s", want, out)
		}
	}
}

func TestExplainDataReviewPolicyStaticSchema(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// No schema endpoints registered: the data-review-policy schema must be static.

	out, err := execExplain(t, mock, "data-review-policy")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	for _, anchor := range []string{"requires_review", "quarantine", "kind: DataReviewPolicy"} {
		if !strings.Contains(out, anchor) {
			t.Errorf("explain output missing %q; got:\n%s", anchor, out)
		}
	}
}

func TestPrintPageRendersEverySection(t *testing.T) {
	var buf bytes.Buffer
	printPage(&buf, catalog.Resource{
		Page: resources.Page{
			Name:     "data",
			Summary:  "rows of a table",
			Aliases:  []string{"rows"},
			Usage:    []string{"qctl get data <table-id-or-name>"},
			Examples: []resources.Example{{Description: "first page", Command: "qctl get data orders"}},
			Gotchas:  []resources.Gotcha{{Text: "one backend page per call"}},
			SeeAlso:  []string{"`qctl guide inspect-data`"},
		},
	}, true)
	out := buf.String()
	for _, want := range []string{
		"RESOURCE: data", "SUMMARY:  rows of a table", "ALIASES:  rows",
		"USAGE:", "qctl get data <table-id-or-name>",
		"EXAMPLES:", "# first page", "qctl get data orders",
		"GOTCHAS:", "one backend page per call",
		"SEE ALSO:", "qctl guide inspect-data",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("page output missing %q; got:\n%s", want, out)
		}
	}
}

// TestPrintPageOmitsTheBackendMarker: [backend] is an internal maintenance tag
// for tools/gotchassweep. It leaked into `qctl explain <resource>` output for
// 61 bullets across 22 pages before the Gotcha struct existed.
func TestPrintPageOmitsTheBackendMarker(t *testing.T) {
	var buf bytes.Buffer
	printPage(&buf, catalog.Resource{
		Page: resources.Page{
			Name:    "org",
			Gotchas: []resources.Gotcha{{Text: "the server decides", Backend: true}},
		},
	}, true)
	out := buf.String()
	if strings.Contains(out, "[backend]") {
		t.Errorf("explain leaked the [backend] marker:\n%s", out)
	}
	if !strings.Contains(out, "the server decides") {
		t.Errorf("explain dropped the gotcha text:\n%s", out)
	}
}

// TestTableSchemaColumnFieldsMatchTheManifest keeps the explain drill-down in
// step with the struct `qctl apply -f` parses: display_decimals was parsed,
// emitted and applied before `qctl explain table-schema` named it at all.
// original_name is the one key left out on purpose -- FieldOverride declares it
// only so the parser can refuse it with a reason.
func TestTableSchemaColumnFieldsMatchTheManifest(t *testing.T) {
	var want []string
	rt := reflect.TypeOf(datasets.FieldOverride{})
	for i := 0; i < rt.NumField(); i++ {
		key, _, _ := strings.Cut(rt.Field(i).Tag.Get("yaml"), ",")
		if key != "" && key != "-" && key != "original_name" {
			want = append(want, key)
		}
	}
	got := make([]string, 0, len(tableSchemaColumnFields))
	for _, f := range tableSchemaColumnFields {
		got = append(got, f.Name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tableSchemaColumnFields documents %v, but datasets.FieldOverride parses %v", got, want)
	}
}

// TestDeliverableSpecFieldsMatchTheManifest keeps `qctl explain deliverable`
// in step with the spec keys `qctl apply -f` decodes, down to a sheet's
// columns and row order.
func TestDeliverableSpecFieldsMatchTheManifest(t *testing.T) {
	for _, tc := range []struct {
		path   string
		fields []schema.FieldInfo
		decode any
	}{
		{"spec", deliverableSpecFields, deliverables.ManifestSpec{}},
		{"spec.sheets.<sheet>", manifestField(t, "spec.sheets.<sheet>").Properties, deliverables.ManifestSheet{}},
		{"spec.sheets.<sheet>.row_order", manifestField(t, "spec.sheets.<sheet>.row_order").Properties, deliverables.ManifestSortKey{}},
		{"spec.sheets.<sheet>.columns.<column>", manifestField(t, "spec.sheets.<sheet>.columns.<column>").Properties, deliverables.ManifestColumn{}},
		{"spec.cells.<cell>", manifestField(t, "spec.cells.<cell>").Properties, deliverables.ManifestCell{}},
		{"spec.cells.<cell>.total", manifestField(t, "spec.cells.<cell>.total").Properties, deliverables.ManifestTotal{}},
	} {
		var want []string
		rt := reflect.TypeOf(tc.decode)
		for i := 0; i < rt.NumField(); i++ {
			if key, _, _ := strings.Cut(rt.Field(i).Tag.Get("yaml"), ","); key != "" && key != "-" {
				want = append(want, key)
			}
		}
		got := make([]string, 0, len(tc.fields))
		for _, f := range tc.fields {
			got = append(got, f.Name)
		}
		sort.Strings(want)
		sort.Strings(got)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("explain deliverable.%s documents %v, but %s decodes %v", tc.path, got, rt, want)
		}
	}
	rowOrder := manifestField(t, "spec.sheets.<sheet>.row_order")
	if rowOrder.Items == nil || !reflect.DeepEqual(rowOrder.Items.Properties, rowOrder.Properties) {
		t.Errorf("row_order's item properties %+v differ from the properties its field path drills into %+v", rowOrder.Items, rowOrder.Properties)
	}
}

// manifestField resolves path in the kind: Deliverable schema.
func manifestField(t *testing.T, path string) *schema.FieldInfo {
	t.Helper()
	field, err := findManifestField(deliverableManifestSchema, path)
	if err != nil {
		t.Fatalf("deliverable.%s: %v", path, err)
	}
	return field
}

// TestExplainDeliverableSheets pins `qctl explain deliverable.spec.sheets`:
// the field, its sheet entry's keys, and the field paths into a sheet entry
// and a column entry.
func TestExplainDeliverableSheets(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	// No schema endpoints registered: the deliverable schema must be static.

	out, err := execExplain(t, mock, "deliverable.spec.sheets", "-o", "json")
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	var sheets schema.FieldInfo
	if err := json.Unmarshal([]byte(out), &sheets); err != nil {
		t.Fatalf("-o json does not parse: %v\n%s", err, out)
	}
	if sheets.Name != "sheets" || sheets.Type != "object" || len(sheets.Properties) != 1 || sheets.Properties[0].Name != "<sheet>" {
		t.Fatalf("deliverable.spec.sheets = %+v, want an object with one <sheet> entry", sheets)
	}
	var keys []string
	for _, f := range sheets.Properties[0].Properties {
		keys = append(keys, f.Name)
	}
	if want := []string{"table", "series", "header_rows", "data_start_row", "instruction_row_count", "row_order", "row_filter", "columns"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("<sheet> keys = %v, want %v", keys, want)
	}

	for path, want := range map[string]string{
		"deliverable.spec.sheets":                                    "FIELD:    sheets\nTYPE:     object\n",
		"deliverable.spec.sheets.<sheet>.table":                      "FIELD:    table\nTYPE:     string\nREQUIRED: true\n",
		"deliverable.spec.sheets.<sheet>.header_rows":                "FIELD:    header_rows\nTYPE:     []integer\nREQUIRED: true\n",
		"deliverable.spec.sheets.<sheet>.series":                     "FIELD:    series\nTYPE:     string, nullable\n",
		"deliverable.spec.sheets.<sheet>.row_order":                  "FIELD:    row_order\nTYPE:     []object\n",
		"deliverable.spec.sheets.<sheet>.row_order.descending":       "FIELD:    descending\nTYPE:     boolean\n",
		"deliverable.spec.sheets.<sheet>.columns.<column>.format":    "FIELD:    format\nTYPE:     string\n",
		"deliverable.spec.sheets.<sheet>.columns.<column>.run_param": "FIELD:    run_param\nTYPE:     string\n",
	} {
		out, err := execExplain(t, mock, path)
		if err != nil {
			t.Errorf("explain %s: %v", path, err)
			continue
		}
		if !strings.HasPrefix(out, want) {
			t.Errorf("explain %s =\n%s\nwant it to start\n%s", path, out, want)
		}
	}
}

func TestExplainObjectFieldListsChildren(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	for _, tc := range []struct {
		path string
		keys []string
		next string
	}{
		{"deliverable.spec", []string{"name <string> <required>", "filename_pattern <string> <required>", "sheets <object>"}, "deliverable.spec.name"},
		{"deliverable.spec.sheets", []string{"<sheet> <object>"}, "'deliverable.spec.sheets.<sheet>'"},
		{"deliverable.spec.sheets.<sheet>", []string{"table <string> <required>", "row_order <[]object>", "columns <object>"}, "'deliverable.spec.sheets.<sheet>.table'"},
		{"deliverable.spec.sheets.<sheet>.columns", []string{"<column> <object>"}, "'deliverable.spec.sheets.<sheet>.columns.<column>'"},
		{"deliverable.spec.sheets.<sheet>.columns.<column>", []string{"column <string>", "run_param <string>", "format <string>"}, "'deliverable.spec.sheets.<sheet>.columns.<column>.column'"},
		{"table-schema.spec.fields.<column>", []string{"type <string> <required>", "is_deleted <boolean>"}, "'table-schema.spec.fields.<column>.type'"},
		{"table-rule.spec.row_filter", []string{"description <string> <required>", "conditions <[]object>", "predicate <object>"}, "table-rule.spec.row_filter.description"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			out, err := execExplain(t, mock, tc.path)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			_, fields, ok := strings.Cut(out, "FIELDS:\n")
			if !ok {
				t.Fatalf("missing child fields:\n%s", out)
			}
			for _, key := range tc.keys {
				if !strings.Contains(fields, "  "+key+"\n") {
					t.Errorf("missing child %q:\n%s", key, out)
				}
			}
			if !strings.Contains(out, "To explain a child field: qctl explain "+tc.next+"\n") {
				t.Errorf("missing drill command %s:\n%s", tc.next, out)
			}
		})
	}
}

func TestExplainDeliverableRecursiveListsColumnAndRowOrderKeys(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	for _, path := range []string{"deliverable", "deliverable.spec.sheets"} {
		t.Run(path, func(t *testing.T) {
			out, err := execExplain(t, mock, path, "--recursive")
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			for _, key := range []string{"run_param <string>", "format <string>", "descending <boolean>"} {
				want := 1
				if path == "deliverable" && key != "descending <boolean>" {
					want = 2 // Both column and cell bindings declare these keys.
				}
				if got := strings.Count(out, key+"\n"); got != want {
					t.Errorf("%q appears %d times, want %d:\n%s", key, got, want, out)
				}
			}
			if !strings.Contains(out, "[array item properties]\n") {
				t.Errorf("row_order item keys are not labelled:\n%s", out)
			}
			plain, err := execExplain(t, mock, path)
			if err != nil {
				t.Fatalf("explain without --recursive: %v", err)
			}
			if strings.Contains(plain, "run_param <string>\n") {
				t.Errorf("default summary expanded deep column keys:\n%s", plain)
			}
		})
	}
}

func TestExplainDeliverableCellSourcesAndTotalFields(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	out, err := execExplain(t, mock, "deliverable.spec.cells.<cell>", "--recursive")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"run_param <string>", "literal <string>", "total <object>", "label_cell <string>",
		"format <string>", "table <string>", "series <string>", "same_rows_as <string>", "func <string> <required>", "column <string>"} {
		if got := strings.Count(out, key+"\n"); got != 1 {
			t.Errorf("%q appears %d times, want once:\n%s", key, got, out)
		}
	}
}

func TestExplainArrayFieldListsItemKeysOnce(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	for _, tc := range []struct {
		path string
		keys []string
	}{
		{"deliverable.spec.sheets.<sheet>.row_order", []string{"column <string> <required>", "descending <boolean>"}},
		{"table-rule.spec.produced_fields", []string{"rule_field_name <string> <required>", "dataset_field_name <string> <required>", "field_schema <object>"}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			out, err := execExplain(t, mock, tc.path)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			for _, key := range tc.keys {
				if got := strings.Count(out, key+"\n"); got != 1 {
					t.Errorf("%q appears %d times, want once:\n%s", key, got, out)
				}
			}
			if !strings.Contains(out, "FIELDS:\n  [array item properties]\n") {
				t.Errorf("missing item fields section:\n%s", out)
			}
		})
	}
}

func TestExplainObjectMachineOutputDoesNotDependOnRecursive(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	path := "deliverable.spec.sheets"
	var outputs []string
	for _, format := range []string{"json", "yaml"} {
		plain, err := execExplain(t, mock, path, "-o", format)
		if err != nil {
			t.Fatalf("explain -o %s: %v", format, err)
		}
		recursive, err := execExplain(t, mock, path, "-o", format, "--recursive")
		if err != nil {
			t.Fatalf("explain -o %s --recursive: %v", format, err)
		}
		if recursive != plain {
			t.Errorf("-o %s changed with --recursive", format)
		}
		outputs = append(outputs, plain)
	}
	testutil.AssertYAMLReadsAsJSON(t, outputs[1], outputs[0])
}

// TestOutputFieldInfoKeepsAFalsyDefault: every schema key is omitempty, and a
// default of false or 0 is still printed by both machine formats.
func TestOutputFieldInfoKeepsAFalsyDefault(t *testing.T) {
	for _, info := range []schema.FieldInfo{
		{Name: "enabled", Type: "boolean", Default: false},
		{Name: "retries", Type: "integer", Default: float64(0)},
	} {
		var jsonOut, yamlOut bytes.Buffer
		if err := outputFieldInfo(&jsonOut, &info, explainJSONOutputFormat, false, info.Name, fieldDecoration{}); err != nil {
			t.Fatalf("-o json: %v", err)
		}
		if err := outputFieldInfo(&yamlOut, &info, "yaml", false, info.Name, fieldDecoration{}); err != nil {
			t.Fatalf("-o yaml: %v", err)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(jsonOut.Bytes(), &doc); err != nil {
			t.Fatalf("-o json does not parse: %v\n%s", err, jsonOut.String())
		}
		if got, ok := doc["default"]; !ok || got != info.Default {
			t.Errorf("%s: -o json default = %v (present %v), want %v\n%s", info.Name, got, ok, info.Default, jsonOut.String())
		}
		testutil.AssertYAMLReadsAsJSON(t, yamlOut.String(), jsonOut.String())
	}
}

func TestFormatTypeNamesUnions(t *testing.T) {
	union := []schema.FieldInfo{{Type: "string"}, {Type: "integer"}}
	for _, tc := range []struct {
		name string
		info schema.FieldInfo
		want string
	}{
		{"anyOf", schema.FieldInfo{AnyOf: union}, "string | integer"},
		{"oneOf", schema.FieldInfo{OneOf: union}, "string | integer"},
		{"nullable", schema.FieldInfo{AnyOf: union, Nullable: true}, "string | integer, nullable"},
		{"a named union is its name", schema.FieldInfo{Ref: "JSONValue", AnyOf: union}, "JSONValue"},
		{"array of a union", schema.FieldInfo{Type: "array", Items: &schema.FieldInfo{AnyOf: union}}, "[](string | integer)"},
		{"array of a named union", schema.FieldInfo{Type: "array", Items: &schema.FieldInfo{Ref: "JSONValue", AnyOf: union}}, "[]JSONValue"},
		{"array of arrays", schema.FieldInfo{Type: "array", Items: &schema.FieldInfo{Type: "array", Items: &schema.FieldInfo{Type: "string"}}}, "[][]string"},
	} {
		if got := formatType(&tc.info); got != tc.want {
			t.Errorf("%s: formatType = %q, want %q", tc.name, got, tc.want)
		}
	}
}
