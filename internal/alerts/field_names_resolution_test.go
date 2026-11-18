package alerts

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const fieldNamesTestSpec = `spec:
  field_names_resolution:
    - incoming_field_name: Customer Name
      field_name: customer_name
      operation: new
      scope: dataset
      expand: true
    - incoming_field_name: Legacy ID
      field_name: id
      operation: map
      scope: data_source_model
      expand: false
    - incoming_field_name: Unused
      field_name: ""
      operation: delete
      scope: stored_item
`

func TestFieldNamesResolutionReachesTheWireWithoutAction(t *testing.T) {
	res, err := ParseResolution([]byte(fieldNamesTestSpec))
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Fatal(err)
	}
	req := res.Spec.ToRequest()
	if req.Action != nil || req.FieldNamesResolution == nil {
		t.Fatalf("request = %+v", req)
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if _, present := body["action"]; present {
		t.Fatalf("action must be omitted: %s", data)
	}
	want := []api.FieldNameResolutionParams{
		{IncomingFieldName: "Customer Name", FieldName: "customer_name", Operation: api.FieldNameResolutionOperatorNew, Scope: api.ScopeDataset, Expand: boolPtrForFieldNamesTest(true)},
		{IncomingFieldName: "Legacy ID", FieldName: "id", Operation: api.FieldNameResolutionOperatorMap, Scope: api.ScopeDataSourceModel, Expand: boolPtrForFieldNamesTest(false)},
		{IncomingFieldName: "Unused", FieldName: "", Operation: api.FieldNameResolutionOperatorDelete, Scope: api.ScopeStoredItem},
	}
	if !reflect.DeepEqual(*req.FieldNamesResolution, want) {
		t.Errorf("mappings = %+v, want %+v", *req.FieldNamesResolution, want)
	}
	res.Spec.FieldNamesResolution[0].Operation = "delete"
	*res.Spec.FieldNamesResolution[0].Expand = false
	if (*req.FieldNamesResolution)[0].Operation != api.FieldNameResolutionOperatorNew || !*(*req.FieldNamesResolution)[0].Expand {
		t.Error("request aliases the parsed manifest")
	}
}

func TestFieldNamesResolutionRejectsMalformedSpecs(t *testing.T) {
	for _, tc := range []struct {
		name string
		doc  string
	}{
		{"missing resolution", "spec: {}"},
		{"empty mappings", "spec: {field_names_resolution: []}"},
		{"null mappings", "spec: {field_names_resolution: null}"},
		{"action and mappings", strings.Replace(fieldNamesTestSpec, "spec:\n", "spec:\n  action: define_file_header\n", 1)},
		{"unread field", strings.Replace(fieldNamesTestSpec, "spec:\n", "spec:\n  new_value: x\n", 1)},
		{"missing incoming", strings.Replace(fieldNamesTestSpec, "incoming_field_name: Customer Name", "incoming_field_name:", 1)},
		{"missing target", strings.Replace(fieldNamesTestSpec, "field_name: customer_name", "field_name:", 1)},
		{"bad operation", strings.Replace(fieldNamesTestSpec, "operation: new", "operation: add", 1)},
		{"bad scope", strings.Replace(fieldNamesTestSpec, "scope: dataset", "scope: table", 1)},
		{"nested typo", strings.Replace(fieldNamesTestSpec, "incoming_field_name: Customer Name", "incoming_field_nam: Customer Name", 1)},
		{"null mapping", "spec: {field_names_resolution: [null]}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseResolution([]byte(tc.doc))
			if err == nil {
				err = res.Spec.Validate()
			}
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
		})
	}
}

func TestFieldNamesResolutionAcceptsExplicitEmptyNames(t *testing.T) {
	res, err := ParseResolution([]byte(strings.ReplaceAll(fieldNamesTestSpec, "field_name: customer_name", "field_name: \"\"")))
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Spec.Validate(); err != nil {
		t.Fatal(err)
	}
	if (*res.Spec.ToRequest().FieldNamesResolution)[0].FieldName != "" {
		t.Fatal("explicit empty field_name was lost")
	}
}

func TestFieldNamesSkeletonKeepsIncomingNamesAsData(t *testing.T) {
	name := "Customer\nName\"\nkind: Injected" + string(rune(0x85)) + string(rune(0x2028)) + string(rune(0x2029))
	a := &AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders\nkind: Injected", FieldNamesSuggestionsInfo: &api.FieldNamesSuggestionsInfo{
		Suggestions: []api.FieldNameSuggestion{{IncomingFieldName: name, RecommendedFieldNameToMapTo: "customer\nkind: Injected"}},
	}}
	out := FieldNamesSkeleton(a)
	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("ParseResolution: %v\n%s", err, out)
	}
	if res.Kind != AlertResolutionKind || res.Metadata.ID != "9" || res.Spec.Action != "" {
		t.Fatalf("envelope = %+v", res)
	}
	fields := res.Spec.FieldNamesResolution
	if len(fields) != 1 || fields[0].IncomingFieldName == nil || *fields[0].IncomingFieldName != name {
		t.Fatalf("incoming field name did not round trip: %+v", fields)
	}
	if err := res.Spec.Validate(); err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("unedited skeleton must fail with exit 2: %v", err)
	}
}

func TestFieldNamesSkeletonWithoutSuggestionsNeedsInput(t *testing.T) {
	res, err := ParseResolution([]byte(FieldNamesSkeleton(&AlertFull{ID: 9})))
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Spec.Validate(); err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("unedited skeleton must fail with exit 2: %v", err)
	}
}

func boolPtrForFieldNamesTest(b bool) *bool { return &b }

func TestEveryFieldNameResolutionFieldIsMapped(t *testing.T) {
	mapped := map[string]bool{}
	spec := reflect.TypeFor[FieldNameResolutionSpec]()
	for i := range spec.NumField() {
		mapped[jsonNameForTest(spec.Field(i).Tag.Get("yaml"))] = true
	}
	wire := reflect.TypeFor[api.FieldNameResolutionParams]()
	for i := range wire.NumField() {
		name := jsonNameForTest(wire.Field(i).Tag.Get("json"))
		if !mapped[name] {
			t.Errorf("FieldNameResolutionParams.%s (%s) has no manifest field", wire.Field(i).Name, name)
		}
	}
}

func scopesForFieldNamesTest(scopes ...api.Scope) *[]api.Scope { return &scopes }

func strPtrForFieldNamesTest(s string) *string { return &s }

func editorFieldNamesAlert() *AlertFull {
	mapOp := api.FieldNameResolutionOperatorMap
	newOp := api.FieldNameResolutionOperatorNew
	return &AlertFull{ID: 9, DatasetID: 42, DatasetName: "orders", IssueType: api.AlertTypeUnexpectedFieldName,
		FieldMappingScopes: scopesForFieldNamesTest(api.ScopeDataSourceModel, api.ScopeStoredItem),
		FieldNamesSuggestionsInfo: &api.FieldNamesSuggestionsInfo{
			DatasetFieldNamesNotInFile: []string{"customer_name", "amount", "notes"},
			Suggestions: []api.FieldNameSuggestion{
				{IncomingFieldName: "Customer Name", RecommendedFieldNameToMapTo: "customer_name", RecommendedOperation: &mapOp},
				{IncomingFieldName: "Amt", RecommendedFieldNameToMapTo: "amount", RecommendedOperation: &mapOp, BlankInFile: boolPtrForFieldNamesTest(true)},
				{IncomingFieldName: "Region", RecommendedOperation: &newOp},
				{IncomingFieldName: "Legacy", RecommendedFieldNameToMapTo: "notes"},
				{IncomingFieldName: "Junk"},
			},
			DatasetFields: &[]api.DatasetFieldInfo{
				{FieldName: "id", Label: strPtrForFieldNamesTest("id"), Required: boolPtrForFieldNamesTest(true)},
				{FieldName: "customer_name", Label: strPtrForFieldNamesTest("Customer"), Required: boolPtrForFieldNamesTest(true)},
				{FieldName: "amount", Required: boolPtrForFieldNamesTest(true)},
				{FieldName: "notes"},
			},
		}}
}

func TestFieldNamesSkeletonShowsScopesRecommendationsAndColumns(t *testing.T) {
	out := FieldNamesSkeleton(editorFieldNamesAlert())
	for _, want := range []string{
		"# operation: new | map | delete; scope open to you: data_source_model | stored_item\n",
		"      # recommended: map customer_name\n      field_name:\n      operation:\n      scope: data_source_model\n",
		"      # recommended: map amount\n      # blank in this file\n",
		"      # recommended: new\n",
		"      # recommended field_name: notes\n",
		"      # no recommendation\n",
		"# table columns: name (label) [required]\n#   id [required]\n#   customer_name (Customer) [required]\n" +
			"#   amount [required] not in this file, and no column with data is recommended for it: map one or the server refuses with 422\n" +
			"#   notes\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("skeleton missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "scope:\n") {
		t.Errorf("scope left blank although field_mapping_scopes was sent:\n%s", out)
	}
	res, err := ParseResolution([]byte(out))
	if err != nil {
		t.Fatalf("ParseResolution: %v\n%s", err, out)
	}
	if len(res.Spec.FieldNamesResolution) != 5 || res.Spec.FieldNamesResolution[0].Scope != "data_source_model" {
		t.Fatalf("mappings = %+v", res.Spec.FieldNamesResolution)
	}
	if err := res.Spec.Validate(); err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("unedited skeleton must fail with exit 2: %v", err)
	}
}

func TestFieldNamesSkeletonWithoutScopesListsAllAndPrefillsNone(t *testing.T) {
	a := editorFieldNamesAlert()
	for name, out := range map[string]string{
		"older server": FieldNamesSkeleton(&AlertFull{ID: 9, FieldNamesSuggestionsInfo: a.FieldNamesSuggestionsInfo}),
		"propose":      ProposeFieldNamesSkeleton(a),
	} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(out, "scope: dataset | data_source_model | stored_item\n") || strings.Contains(out, "scope: data_source_model\n") {
				t.Errorf("skeleton = %s", out)
			}
		})
	}
}

func TestValidateScopesMirrorsTheServersDatasetScopeCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mapping string
		refused bool
	}{
		{"delete at dataset", "{incoming_field_name: A, field_name: '', operation: delete, scope: dataset}", true},
		{"map to a column at dataset", "{incoming_field_name: A, field_name: a, operation: map, scope: dataset}", true},
		{"expand at dataset", "{incoming_field_name: A, field_name: '', operation: map, scope: dataset, expand: true}", true},
		{"new ignores scope", "{incoming_field_name: A, field_name: a, operation: new, scope: dataset}", false},
		{"map without a column", "{incoming_field_name: A, field_name: '', operation: map, scope: dataset}", false},
		{"open scope", "{incoming_field_name: A, field_name: '', operation: delete, scope: data_source_model}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := ParseResolution([]byte("spec: {field_names_resolution: [" + tc.mapping + "]}"))
			if err != nil {
				t.Fatal(err)
			}
			if err := res.Spec.Validate(); err != nil {
				t.Fatal(err)
			}
			err = res.Spec.ValidateScopes(editorFieldNamesAlert())
			if tc.refused != (err != nil) || (err != nil && qerrors.GetExitCode(err) != qerrors.ExitBadUsage) {
				t.Fatalf("ValidateScopes = %v, refused want %v", err, tc.refused)
			}
			if err := res.Spec.ValidateScopes(&AlertFull{ID: 9}); err != nil {
				t.Fatalf("no field_mapping_scopes must not refuse: %v", err)
			}
		})
	}
}

func TestDescribeAlertNamesTheOpenScopes(t *testing.T) {
	a := editorFieldNamesAlert()
	a.Resolved = boolPtrForFieldNamesTest(false)
	if footer := RenderNextActionsPlain(a, false); !strings.Contains(footer, "    scopes open to you: data_source_model, stored_item\n") {
		t.Errorf("footer = %s", footer)
	}
	spec := APIResponseToManifest(a, nil, 0).Spec
	if !reflect.DeepEqual(spec.FieldMappingScopes, []string{"data_source_model", "stored_item"}) {
		t.Errorf("spec.field_mapping_scopes = %v", spec.FieldMappingScopes)
	}
}
