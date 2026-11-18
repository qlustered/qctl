package rule_merges //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/testutil"
)

// Source rules of the fixture merge, one per item action, and the target
// rules they match.
const (
	resolutionTestSrcConflict  = "10000000-0000-4000-8000-000000000001"
	resolutionTestSrcAmbiguous = "10000000-0000-4000-8000-000000000002"
	resolutionTestSrcCreated   = "10000000-0000-4000-8000-000000000003"
	resolutionTestSrcKept      = "10000000-0000-4000-8000-000000000004"
	resolutionTestSrcReplaced  = "10000000-0000-4000-8000-000000000005"
	resolutionTestSrcSkipped   = "10000000-0000-4000-8000-000000000006"
	resolutionTestSrcFailed    = "10000000-0000-4000-8000-000000000007"
	resolutionTestSrcOrphan    = "10000000-0000-4000-8000-000000000008" // conflict, target deleted
	resolutionTestSrcElsewhere = "10000000-0000-4000-8000-000000000009" // on the source table, no item

	resolutionTestTgtMatched = "20000000-0000-4000-8000-000000000001"
	resolutionTestTgtCandA   = "20000000-0000-4000-8000-000000000002"
	resolutionTestTgtCandB   = "20000000-0000-4000-8000-000000000003"
)

var resolutionTestSourceNames = map[string]string{
	resolutionTestSrcConflict:  "price_range",
	resolutionTestSrcAmbiguous: `Price: "range" # 2`,
	resolutionTestSrcCreated:   "not_null_sku",
	resolutionTestSrcKept:      "unique_id",
	resolutionTestSrcReplaced:  "date_format",
	resolutionTestSrcSkipped:   "waiting_rule",
	resolutionTestSrcFailed:    "broken_rule",
	resolutionTestSrcOrphan:    "orphan_rule",
	resolutionTestSrcElsewhere: "unmerged_rule",
}

var resolutionTestTargetNames = map[string]string{
	resolutionTestTgtMatched: "price_range_t",
	resolutionTestTgtCandA:   "range_a",
	resolutionTestTgtCandB:   "range b: x",
}

func resolutionTestStr(s string) *string { return &s }

func resolutionTestItem(id int, action api.DatasetCopyItemAction, source string) api.RuleMergeItemSchema {
	return api.RuleMergeItemSchema{ID: id, Action: action, SourceObjectID: resolutionTestStr(source)}
}

// resolutionTestItems is a merge with one item per action, an ambiguous
// conflict with two candidates, and a conflict whose target rule is gone.
func resolutionTestItems() []api.RuleMergeItemSchema {
	conflict := resolutionTestItem(101, api.DatasetCopyItemActionConflict, resolutionTestSrcConflict)
	conflict.TargetObjectID = resolutionTestStr(resolutionTestTgtMatched)
	conflict.Note = resolutionTestStr("Both rules changed since the copy.")
	ambiguous := resolutionTestItem(102, api.DatasetCopyItemActionConflict, resolutionTestSrcAmbiguous)
	ambiguous.CandidateObjectIds = []string{resolutionTestTgtCandA, resolutionTestTgtCandB}
	ambiguous.Note = resolutionTestStr("Its match by rule family and columns is ambiguous.")
	orphan := resolutionTestItem(108, api.DatasetCopyItemActionConflict, resolutionTestSrcOrphan)
	return []api.RuleMergeItemSchema{
		conflict,
		ambiguous,
		resolutionTestItem(103, api.DatasetCopyItemActionCreated, resolutionTestSrcCreated),
		resolutionTestItem(104, api.DatasetCopyItemActionKept, resolutionTestSrcKept),
		resolutionTestItem(105, api.DatasetCopyItemActionReplaced, resolutionTestSrcReplaced),
		resolutionTestItem(106, api.DatasetCopyItemActionSkipped, resolutionTestSrcSkipped),
		resolutionTestItem(107, api.DatasetCopyItemActionFailed, resolutionTestSrcFailed),
		orphan,
	}
}

// resolutionTestResolver resolves a rule by name or by its own UUID, and
// answers anything else with a not-found error (exit 3).
func resolutionTestResolver(names map[string]string) RuleResolver {
	return func(ref string) (string, error) {
		for id, name := range names {
			if ref == name || ref == id {
				return id, nil
			}
		}
		return "", qerrors.NewNotFound("no table rule " + ref)
	}
}

func resolutionTestToRequest(spec RuleMergeResolutionSpec) ([]Resolution, error) {
	return spec.ToRequest(resolutionTestItems(),
		resolutionTestResolver(resolutionTestSourceNames), resolutionTestResolver(resolutionTestTargetNames))
}

func resolutionTestWantBadUsage(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
}

func TestParseResolutionReadsYAML(t *testing.T) {
	doc := []byte(`
apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: price_range
      choice: take_source
    - rule: 'Price: "range" # 2'
      choice: edit_source
      target_rule: range_a
      column_mapping:
        value: amount
      params:
        min: 1.5
        max: 10
      severity: blocker
`)
	got, err := ParseResolution(doc)
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	want := []RuleMergeResolutionEntry{
		{Rule: "price_range", Choice: "take_source"},
		{
			Rule: `Price: "range" # 2`, Choice: "edit_source", TargetRule: "range_a",
			ColumnMapping: map[string]string{"value": "amount"},
			Params:        map[string]interface{}{"min": 1.5, "max": 10},
			Severity:      "blocker",
		},
	}
	if !reflect.DeepEqual(got.Spec.Resolutions, want) {
		t.Errorf("resolutions = %#v\nwant %#v", got.Spec.Resolutions, want)
	}
}

// A JSON document is decoded with UseNumber, so params keep the literal the
// user wrote all the way to the wire.
func TestParseResolutionReadsJSON(t *testing.T) {
	doc := []byte(`{"apiVersion":"qluster.ai/v1","kind":"RuleMergeResolution","spec":{"resolutions":[` +
		`{"rule":"price_range","choice":"edit_source","params":{"min":1.5,"max":10}}]}}`)
	got, err := ParseResolution(doc)
	if err != nil {
		t.Fatalf("ParseResolution: %v", err)
	}
	req, err := resolutionTestToRequest(got.Spec)
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	raw, err := json.Marshal(req[0].Params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if string(raw) != `{"max":10,"min":1.5}` {
		t.Errorf("params on the wire = %s", raw)
	}
}

func TestParseResolutionPreservesYAMLDateParam(t *testing.T) {
	doc := []byte("apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n    - rule: price_range\n      choice: edit_source\n      params: {cutoff: 2024-01-01}\n")
	got, err := ParseResolution(doc)
	if err != nil {
		t.Fatal(err)
	}
	req, err := resolutionTestToRequest(got.Spec)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(req[0].Params)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"cutoff":"2024-01-01"}` {
		t.Errorf("params on the wire = %s", raw)
	}
}

func TestParseResolutionRejectsJSONCaseAndDuplicates(t *testing.T) {
	for name, doc := range map[string]string{
		"case":      `{"APIVERSION":"qluster.ai/v1","kind":"RuleMergeResolution","spec":{"resolutions":[{"rule":"a","choice":"take_source"}]}}`,
		"duplicate": `{"apiVersion":"qluster.ai/v1","kind":"RuleMergeResolution","spec":{"resolutions":[{"rule":"a","choice":"take_source","choice":"keep_target"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseResolution([]byte(doc))
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestParseResolutionRefusesAnUnknownKey(t *testing.T) {
	for name, doc := range map[string]string{
		"spec entry": "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n    - rule: a\n      choise: keep_both\n",
		"spec":       "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolution:\n    - rule: a\n",
		"top level":  "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nstatus: {}\nspec:\n  resolutions:\n    - {rule: a, choice: keep_both}\n",
		"json":       `{"apiVersion":"qluster.ai/v1","kind":"RuleMergeResolution","spec":{"resolutions":[{"rule":"a","choice":"keep_both","extra":1}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseResolution([]byte(doc))
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestParseResolutionRefusesTheWrongEnvelope(t *testing.T) {
	const spec = "spec:\n  resolutions:\n    - {rule: a, choice: keep_both}\n"
	for name, doc := range map[string]string{
		"wrong kind":         "apiVersion: qluster.ai/v1\nkind: AlertResolution\n" + spec,
		"missing kind":       "apiVersion: qluster.ai/v1\n" + spec,
		"wrong apiVersion":   "apiVersion: qluster.ai/v2\nkind: RuleMergeResolution\n" + spec,
		"missing apiVersion": "kind: RuleMergeResolution\n" + spec,
		"two documents":      "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\n" + spec + "---\napiVersion: qluster.ai/v1\nkind: RuleMergeResolution\n" + spec,
		"no document":        "# nothing here\n",
		"not YAML":           "spec: [\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseResolution([]byte(doc))
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestValidateRefuses(t *testing.T) {
	entry := func(rule, choice string) RuleMergeResolutionEntry {
		return RuleMergeResolutionEntry{Rule: rule, Choice: choice}
	}
	withMapping := entry("a", "take_source")
	withMapping.ColumnMapping = map[string]string{"value": "amount"}
	withParams := entry("a", "keep_target")
	withParams.Params = map[string]interface{}{"min": 1}
	withSeverity := entry("a", "keep_both")
	withSeverity.Severity = "blocker"
	badSeverity := entry("a", "edit_source")
	badSeverity.Severity = "fatal"

	for name, entries := range map[string][]RuleMergeResolutionEntry{
		"no entries":                  nil,
		"empty rule":                  {entry("", "keep_both")},
		"missing choice":              {entry("a", "")},
		"choice outside the enum":     {entry("a", "take_target")},
		"choice in the wrong case":    {entry("a", "Keep_Both")},
		"column_mapping, take_source": {withMapping},
		"params, keep_target":         {withParams},
		"severity, keep_both":         {withSeverity},
		"severity outside the enum":   {badSeverity},
		"the same rule twice":         {entry("a", "keep_both"), entry("b", "keep_both"), entry("a", "take_source")},
	} {
		t.Run(name, func(t *testing.T) {
			resolutionTestWantBadUsage(t, RuleMergeResolutionSpec{Resolutions: entries}.Validate())
		})
	}
}

// params YAML decodes but JSON cannot encode is refused before the prompt,
// not by the apply request after it.
func TestValidateRefusesParamsJSONCannotEncode(t *testing.T) {
	for name, params := range map[string]string{
		"infinity":            "{min: .inf}",
		"non-string map keys": "{levels: {1: one}}",
	} {
		t.Run(name, func(t *testing.T) {
			doc := "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n" +
				"    - {rule: a, choice: edit_source, params: " + params + "}\n"
			parsed, err := ParseResolution([]byte(doc))
			if err == nil {
				err = parsed.Spec.Validate()
			}
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestValidateAcceptsEveryChoiceAndSeverity(t *testing.T) {
	for _, choice := range []api.DatasetCopyConflictChoice{api.DatasetCopyConflictChoiceTakeSource, api.DatasetCopyConflictChoiceKeepTarget, api.DatasetCopyConflictChoiceEditSource, api.DatasetCopyConflictChoiceKeepBoth} {
		spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{{Rule: "a", Choice: string(choice)}}}
		if err := spec.Validate(); err != nil {
			t.Errorf("choice %s: %v", choice, err)
		}
	}
	for _, severity := range []api.RuleSeverity{api.RuleSeverityWarning, api.RuleSeverityBlocker, api.RuleSeverityAsDeclared} {
		spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{{Rule: "a", Choice: "edit_source", Severity: string(severity)}}}
		if err := spec.Validate(); err != nil {
			t.Errorf("severity %s: %v", severity, err)
		}
	}
}

// An empty map sets nothing, so it is not an edit field a non-edit choice
// refuses, and it never reaches the wire.
func TestEmptyEditMapsCountAsAbsent(t *testing.T) {
	spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{{
		Rule: "price_range", Choice: "take_source",
		ColumnMapping: map[string]string{}, Params: map[string]interface{}{},
	}}}
	req, err := resolutionTestToRequest(spec)
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	if req[0].ColumnMapping != nil || req[0].Params != nil {
		t.Errorf("empty maps reached the wire: %+v", req[0])
	}
}

// TestEnumValuesAreTheGeneratedConstants pins the api.EnumValues lookups
// Validate and Skeleton use, so a generator that stops keying
// api.StringEnumValues by the Go type name fails here instead of refusing
// every choice.
func TestEnumValuesAreTheGeneratedConstants(t *testing.T) {
	choices := []string{string(api.DatasetCopyConflictChoiceTakeSource), string(api.DatasetCopyConflictChoiceKeepTarget), string(api.DatasetCopyConflictChoiceEditSource), string(api.DatasetCopyConflictChoiceKeepBoth)}
	if got := api.EnumValues[api.DatasetCopyConflictChoice](); !slices.Equal(got, choices) {
		t.Errorf("DatasetCopyConflictChoice values = %v, want %v", got, choices)
	}
	severities := []string{string(api.RuleSeverityWarning), string(api.RuleSeverityBlocker), string(api.RuleSeverityAsDeclared)}
	if got := api.EnumValues[api.RuleSeverity](); !slices.Equal(got, severities) {
		t.Errorf("RuleSeverity values = %v, want %v", got, severities)
	}
}

// TestEveryResolutionFieldIsSupplied fails when a regenerated
// RuleMergeResolutionSchema gains a field no entry key supplies or explicitly defers.
func TestEveryResolutionFieldIsSupplied(t *testing.T) {
	supplied := map[string]string{
		"item_id":        "rule",
		"choice":         "choice",
		"target_rule_id": "target_rule",
		"column_mapping": "column_mapping",
		"params":         "params",
		"severity":       "severity",
		"row_filter":     "row_filter",
	}
	keys := map[string]bool{}
	entry := reflect.TypeOf(RuleMergeResolutionEntry{})
	for i := 0; i < entry.NumField(); i++ {
		keys[strings.Split(entry.Field(i).Tag.Get("yaml"), ",")[0]] = true
	}
	wire := reflect.TypeOf(api.RuleMergeResolutionSchema{})
	for i := 0; i < wire.NumField(); i++ {
		name := strings.Split(wire.Field(i).Tag.Get("json"), ",")[0]
		key, ok := supplied[name]
		if !ok {
			t.Errorf("RuleMergeResolutionSchema.%s (%s) is supplied by no RuleMergeResolutionEntry key", wire.Field(i).Name, name)
			continue
		}
		if !keys[key] {
			t.Errorf("wire field %s is supplied by key %q, which RuleMergeResolutionEntry no longer has", name, key)
		}
	}
}

// resolutionTestRowFilterDoc is a one-entry document whose entry ends with
// extra, YAML lines indented to the entry's keys.
func resolutionTestRowFilterDoc(choice, extra string) string {
	return "apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n" +
		"    - rule: price_range\n      choice: " + choice + "\n" + extra
}

// A row_filter key is carried to the wire as written: an object as the saved
// filter, null as null, and no key as no key, from YAML and JSON alike.
func TestRowFilterReachesTheWireAsWritten(t *testing.T) {
	const filterYAML = "      row_filter:\n        description: Completed orders\n" +
		"        conditions:\n          - {column_name: status, operator: in, value_list: [done, shipped], case_sensitive: true}\n" +
		"        predicate: {source: \"def applies(row):\\n    return True\\n\", columns: [status]}\n"
	const filterJSON = `{"description":"Completed orders",` +
		`"conditions":[{"column_name":"status","operator":"in","value_list":["done","shipped"],"case_sensitive":true}],` +
		`"predicate":{"source":"def applies(row):\n    return True\n","columns":["status"]}}`
	const filterWire = `{"description":"Completed orders",` +
		`"conditions":[{"column_name":"status","operator":"in","value":null,"value_high":null,"value_list":["done","shipped"],"case_sensitive":true}],` +
		`"predicate":{"source":"def applies(row):\n    return True\n","columns":["status"]}}`
	jsonDoc := func(extra string) string {
		return `{"apiVersion":"qluster.ai/v1","kind":"RuleMergeResolution","spec":{"resolutions":[` +
			`{"rule":"price_range","choice":"edit_source"` + extra + `}]}}`
	}
	for name, tc := range map[string]struct {
		doc  string
		want string // the wire row_filter; "" for no key
	}{
		"YAML filter":  {resolutionTestRowFilterDoc("edit_source", filterYAML), filterWire},
		"YAML null":    {resolutionTestRowFilterDoc("edit_source", "      row_filter: null\n"), "null"},
		"YAML tilde":   {resolutionTestRowFilterDoc("edit_source", "      row_filter: ~\n"), "null"},
		"YAML empty":   {resolutionTestRowFilterDoc("edit_source", "      row_filter:\n"), "null"},
		"YAML absent":  {resolutionTestRowFilterDoc("edit_source", "      severity: blocker\n"), ""},
		"JSON filter":  {jsonDoc(`,"row_filter":` + filterJSON), filterWire},
		"JSON null":    {jsonDoc(`,"row_filter":null`), "null"},
		"JSON absent":  {jsonDoc(""), ""},
		"YAML merge":   {resolutionTestRowFilterDoc("edit_source", "      <<: {row_filter: null}\n"), "null"},
		"flow mapping": {"apiVersion: qluster.ai/v1\nkind: RuleMergeResolution\nspec:\n  resolutions:\n    - {rule: price_range, choice: edit_source, row_filter: null}\n", "null"},
	} {
		t.Run(name, func(t *testing.T) {
			doc, err := ParseResolution([]byte(tc.doc))
			if err != nil {
				t.Fatalf("ParseResolution: %v", err)
			}
			req, err := resolutionTestToRequest(doc.Spec)
			if err != nil {
				t.Fatalf("ToRequest: %v", err)
			}
			data, err := json.Marshal(req[0])
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var wire map[string]json.RawMessage
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatalf("unmarshal %s: %v", data, err)
			}
			got, present := wire["row_filter"]
			switch {
			case tc.want == "" && present:
				t.Errorf("row_filter = %s, want no key", got)
			case tc.want == "null" && string(got) != "null":
				t.Errorf("row_filter = %s (present %t), want null", got, present)
			case tc.want != "" && tc.want != "null":
				testutil.RequireSameJSONTree(t, got, tc.want)
			}
		})
	}
}

// Any row_filter key, null included, goes with edit_source only: the server
// answers 400 for one on any other choice.
func TestRowFilterGoesWithEditSourceOnly(t *testing.T) {
	const filter = "      row_filter: {description: Done, conditions: [{column_name: status, operator: eq, value: done}]}\n"
	for _, choice := range []string{"take_source", "keep_target", "keep_both"} {
		for name, extra := range map[string]string{"filter": filter, "null": "      row_filter: null\n"} {
			t.Run(choice+" "+name, func(t *testing.T) {
				_, err := ParseResolution([]byte(resolutionTestRowFilterDoc(choice, extra)))
				resolutionTestWantBadUsage(t, err)
				if err != nil && !strings.Contains(err.Error(), "row_filter") {
					t.Errorf("error does not name row_filter: %v", err)
				}
			})
		}
	}
}

// A row filter is checked as a saved one before the prompt, and decoded
// strictly.
func TestRowFilterIsValidatedAndStrict(t *testing.T) {
	for name, extra := range map[string]string{
		"no description":     "      row_filter: {conditions: [{column_name: status, operator: eq, value: done}]}\n",
		"blank description":  "      row_filter: {description: '  ', conditions: [{column_name: status, operator: eq, value: done}]}\n",
		"long description":   "      row_filter: {description: " + strings.Repeat("x", 201) + ", conditions: [{column_name: status, operator: eq, value: done}]}\n",
		"empty filter":       "      row_filter: {}\n",
		"description only":   "      row_filter: {description: Done}\n",
		"unknown filter key": "      row_filter: {description: Done, column: status}\n",
		"unknown condition":  "      row_filter: {description: Done, conditions: [{column: status, operator: eq, value: done}]}\n",
		"not a mapping":      "      row_filter: status = done\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseResolution([]byte(resolutionTestRowFilterDoc("edit_source", extra)))
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestToRequestMapsEntriesOntoItemsInOrder(t *testing.T) {
	spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{
		{Rule: resolutionTestSrcReplaced, Choice: "keep_target"},
		{
			Rule: `Price: "range" # 2`, Choice: "edit_source", TargetRule: "range b: x",
			ColumnMapping: map[string]string{"value": "amount"},
			Params:        map[string]interface{}{"min": 1},
			Severity:      "warning",
		},
		{Rule: "price_range", Choice: "take_source"},
		{Rule: "not_null_sku", Choice: "keep_target"},
		{Rule: "orphan_rule", Choice: "keep_both"},
	}}
	got, err := resolutionTestToRequest(spec)
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d resolutions, want 5", len(got))
	}
	for i, want := range []struct {
		item   int
		choice api.DatasetCopyConflictChoice
	}{{105, api.DatasetCopyConflictChoiceKeepTarget}, {102, api.DatasetCopyConflictChoiceEditSource}, {101, api.DatasetCopyConflictChoiceTakeSource}, {103, api.DatasetCopyConflictChoiceKeepTarget}, {108, api.DatasetCopyConflictChoiceKeepBoth}} {
		if got[i].ItemID != want.item || got[i].Choice != want.choice {
			t.Errorf("resolution %d = item %d %s, want item %d %s", i, got[i].ItemID, got[i].Choice, want.item, want.choice)
		}
		if i != 1 && (got[i].TargetRuleID != nil || got[i].ColumnMapping != nil || got[i].Params != nil || got[i].Severity != nil ||
			got[i].RowFilter != nil || got[i].RowFilterSet) {
			t.Errorf("resolution %d carries a field its entry did not set: %+v", i, got[i])
		}
	}
	edit := got[1]
	if edit.TargetRuleID == nil || edit.TargetRuleID.String() != resolutionTestTgtCandB {
		t.Errorf("target_rule_id = %v, want %s", edit.TargetRuleID, resolutionTestTgtCandB)
	}
	if edit.ColumnMapping == nil || !reflect.DeepEqual(*edit.ColumnMapping, map[string]string{"value": "amount"}) {
		t.Errorf("column_mapping = %v", edit.ColumnMapping)
	}
	if edit.Params == nil || !reflect.DeepEqual(*edit.Params, map[string]interface{}{"min": 1}) {
		t.Errorf("params = %v", edit.Params)
	}
	if edit.Severity == nil || *edit.Severity != api.RuleSeverityWarning {
		t.Errorf("severity = %v", edit.Severity)
	}

	// The request must not alias the parsed manifest.
	(*edit.ColumnMapping)["value"] = "changed"
	(*edit.Params)["min"] = 99
	if spec.Resolutions[1].ColumnMapping["value"] != "amount" || spec.Resolutions[1].Params["min"] != 1 {
		t.Error("editing the request changed the spec")
	}
}

// keep_both on an ambiguous conflict pairs it with no candidate, so it needs
// no target_rule.
func TestToRequestTakesKeepBothOnAnAmbiguousConflictWithoutTargetRule(t *testing.T) {
	got, err := resolutionTestToRequest(RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{
		{Rule: resolutionTestSrcAmbiguous, Choice: "keep_both"},
	}})
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	if got[0].ItemID != 102 || got[0].TargetRuleID != nil {
		t.Errorf("resolution = %+v", got[0])
	}
}

// Two source rules sharing one target rule make each an ambiguous conflict
// with a single candidate, and it still takes target_rule.
func TestToRequestTreatsASingleCandidateConflictAsAmbiguous(t *testing.T) {
	items := resolutionTestItems()
	items[1].CandidateObjectIds = []string{resolutionTestTgtCandA}
	toRequest := func(entry RuleMergeResolutionEntry) ([]Resolution, error) {
		return RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{entry}}.ToRequest(items,
			resolutionTestResolver(resolutionTestSourceNames), resolutionTestResolver(resolutionTestTargetNames))
	}

	_, err := toRequest(RuleMergeResolutionEntry{Rule: resolutionTestSrcAmbiguous, Choice: "take_source"})
	resolutionTestWantBadUsage(t, err)

	got, err := toRequest(RuleMergeResolutionEntry{Rule: resolutionTestSrcAmbiguous, Choice: "take_source", TargetRule: "range_a"})
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	if got[0].TargetRuleID == nil || got[0].TargetRuleID.String() != resolutionTestTgtCandA {
		t.Errorf("resolution = %+v, want target_rule_id %s", got[0], resolutionTestTgtCandA)
	}
}

func TestToRequestRefuses(t *testing.T) {
	entry := func(rule, choice, target string) RuleMergeResolutionEntry {
		return RuleMergeResolutionEntry{Rule: rule, Choice: choice, TargetRule: target}
	}
	for name, entries := range map[string][]RuleMergeResolutionEntry{
		"a rule with no item": {entry("unmerged_rule", "keep_both", "")},
		"two entries on one item": {
			entry("price_range", "take_source", ""), entry(resolutionTestSrcConflict, "keep_target", ""),
		},
		"a kept item":                          {entry("unique_id", "keep_target", "")},
		"a skipped item":                       {entry("waiting_rule", "keep_target", "")},
		"a failed item":                        {entry("broken_rule", "keep_target", "")},
		"a created item, take_source":          {entry("not_null_sku", "take_source", "")},
		"a replaced item, keep_both":           {entry("date_format", "keep_both", "")},
		"a created item, edit_source":          {entry("not_null_sku", "edit_source", "")},
		"a created item with target_rule":      {entry("not_null_sku", "keep_target", "range_a")},
		"ambiguous take_source, no target":     {entry(resolutionTestSrcAmbiguous, "take_source", "")},
		"ambiguous keep_target, no target":     {entry(resolutionTestSrcAmbiguous, "keep_target", "")},
		"ambiguous edit_source, no target":     {entry(resolutionTestSrcAmbiguous, "edit_source", "")},
		"ambiguous keep_both with target":      {entry(resolutionTestSrcAmbiguous, "keep_both", "range_a")},
		"ambiguous target not a candidate":     {entry(resolutionTestSrcAmbiguous, "take_source", "price_range_t")},
		"single-match conflict, target_rule":   {entry("price_range", "take_source", "price_range_t")},
		"target-deleted conflict, target_rule": {entry("orphan_rule", "keep_target", "range_a")},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := resolutionTestToRequest(RuleMergeResolutionSpec{Resolutions: entries})
			resolutionTestWantBadUsage(t, err)
		})
	}
}

func TestToRequestValidatesTheSpec(t *testing.T) {
	_, err := resolutionTestToRequest(RuleMergeResolutionSpec{})
	resolutionTestWantBadUsage(t, err)
}

// A resolver's failure keeps its own exit code: a rule that does not exist is
// exit 3, not a manifest error.
func TestToRequestKeepsTheResolverExitCode(t *testing.T) {
	sentinel := qerrors.NewNotFound("no such rule")
	failing := func(string) (string, error) { return "", sentinel }
	source := resolutionTestResolver(resolutionTestSourceNames)
	target := resolutionTestResolver(resolutionTestTargetNames)
	cases := map[string]struct {
		entry          RuleMergeResolutionEntry
		source, target RuleResolver
	}{
		"source rule": {RuleMergeResolutionEntry{Rule: "price_range", Choice: "keep_target"}, failing, target},
		"target rule": {RuleMergeResolutionEntry{Rule: resolutionTestSrcAmbiguous, Choice: "take_source", TargetRule: "range_a"}, source, failing},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{tc.entry}}
			_, err := spec.ToRequest(resolutionTestItems(), tc.source, tc.target)
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want it to wrap the resolver's error", err)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
			}
		})
	}
}

func TestUnresolvedConflicts(t *testing.T) {
	items := resolutionTestItems()
	got := UnresolvedConflicts(items, []Resolution{
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 102, Choice: api.DatasetCopyConflictChoiceKeepBoth}},
		{RuleMergeResolutionSchema: api.RuleMergeResolutionSchema{ItemID: 103, Choice: api.DatasetCopyConflictChoiceKeepTarget}},
	})
	var ids []int
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, []int{101, 108}) {
		t.Errorf("unresolved = %v, want [101 108]", ids)
	}
	if got := UnresolvedConflicts(items, nil); len(got) != 3 {
		t.Errorf("with no resolutions, %d unresolved, want the 3 conflicts", len(got))
	}
}

func resolutionTestNamer(names map[string]string) func(string) string {
	return func(id string) string { return names[id] }
}

func resolutionTestSkeleton(items []api.RuleMergeItemSchema) string {
	return Skeleton(items, resolutionTestNamer(resolutionTestSourceNames), resolutionTestNamer(resolutionTestTargetNames))
}

// resolutionTestFill replaces the skeleton's empty lines for key, in order,
// with one value each.
func resolutionTestFill(t *testing.T, skeleton, key string, values ...string) string {
	t.Helper()
	blank := "      " + key + ":\n"
	if n := strings.Count(skeleton, blank); n != len(values) {
		t.Fatalf("skeleton has %d empty %s lines, want %d:\n%s", n, key, len(values), skeleton)
	}
	for _, v := range values {
		skeleton = strings.Replace(skeleton, blank, "      "+key+": "+v+"\n", 1)
	}
	return skeleton
}

func TestSkeletonUneditedFailsToParse(t *testing.T) {
	skeleton := resolutionTestSkeleton(resolutionTestItems())
	_, err := ParseResolution([]byte(skeleton))
	resolutionTestWantBadUsage(t, err)

	// It fails for the intended reason: the document decodes, and only the
	// empty choices are wrong.
	var doc RuleMergeResolution
	if err := pkgmanifest.StrictUnmarshal([]byte(skeleton), &doc); err != nil {
		t.Fatalf("the unedited skeleton does not even decode: %v\n%s", err, skeleton)
	}
	for i, entry := range doc.Spec.Resolutions {
		if entry.Choice != "" {
			t.Errorf("entry %d arrives with choice %q; the skeleton must leave it empty", i, entry.Choice)
		}
	}
}

func TestSkeletonFilledInParsesAndMaps(t *testing.T) {
	skeleton := resolutionTestSkeleton(resolutionTestItems())
	filled := resolutionTestFill(t, skeleton, "choice", "take_source", "edit_source", "keep_target")
	filled = resolutionTestFill(t, filled, "target_rule", "range_a")

	doc, err := ParseResolution([]byte(filled))
	if err != nil {
		t.Fatalf("ParseResolution: %v\n%s", err, filled)
	}
	var rules []string
	for _, entry := range doc.Spec.Resolutions {
		rules = append(rules, entry.Rule)
	}
	if want := []string{"price_range", `Price: "range" # 2`, "orphan_rule"}; !slices.Equal(rules, want) {
		t.Errorf("rules = %q, want one per conflict item: %q", rules, want)
	}
	req, err := resolutionTestToRequest(doc.Spec)
	if err != nil {
		t.Fatalf("ToRequest: %v", err)
	}
	if len(req) != 3 || req[0].ItemID != 101 || req[1].ItemID != 102 || req[2].ItemID != 108 {
		t.Fatalf("request = %+v", req)
	}
	if req[1].TargetRuleID == nil || req[1].TargetRuleID.String() != resolutionTestTgtCandA {
		t.Errorf("target_rule_id = %v, want %s", req[1].TargetRuleID, resolutionTestTgtCandA)
	}
	if left := UnresolvedConflicts(resolutionTestItems(), req); len(left) != 0 {
		t.Errorf("the filled skeleton leaves %d conflicts unresolved", len(left))
	}
}

func TestSkeletonCommentsNameTheNoteChoicesAndCandidates(t *testing.T) {
	skeleton := resolutionTestSkeleton(resolutionTestItems())
	for _, want := range []string{
		"    # note: Both rules changed since the copy.\n",
		"    # matched target rule: price_range_t\n",
		"    # choice: one of take_source, keep_target, edit_source, keep_both\n",
		"      # target_rule: one of range_a, 'range b: x' (not with keep_both)\n",
	} {
		if !strings.Contains(skeleton, want) {
			t.Errorf("skeleton lacks %q:\n%s", want, skeleton)
		}
	}
	for _, absent := range []string{"not_null_sku", "unique_id", "date_format", "waiting_rule", "broken_rule"} {
		if strings.Contains(skeleton, absent) {
			t.Errorf("skeleton names non-conflict rule %s:\n%s", absent, skeleton)
		}
	}
}

// Any rule name round-trips through the skeleton's rule: line, and a name
// the namer does not know falls back to the UUID.
func TestSkeletonRuleNamesRoundTrip(t *testing.T) {
	for _, name := range []string{
		`Price: "range" # 2`, "  leading spaces", "trailing ", "null", "~", "123", "yes", "- dash", "#hash",
		"'quoted'", "line\nbreak", "ends in a newline\n", "x\u2028y", "tab\there", "\x00nul", "{flow}", "[seq]",
		"*alias", "&anchor", "!tag", "%directive", "@at", "`tick`", "a: b: c", strings.Repeat("long name ", 30),
		"rtl\u200fmark", "\u202eoverride", "c1\u009bcsi", "esc\x1b[31m",
	} {
		t.Run(name, func(t *testing.T) {
			item := resolutionTestItem(1, api.DatasetCopyItemActionConflict, resolutionTestSrcConflict)
			// The command writes the skeleton through terminal.Sanitize.
			skeleton := terminal.Sanitize(Skeleton([]api.RuleMergeItemSchema{item},
				func(string) string { return name }, nil))
			doc, err := ParseResolution([]byte(resolutionTestFill(t, skeleton, "choice", "keep_both")))
			if err != nil {
				t.Fatalf("ParseResolution: %v\n%s", err, skeleton)
			}
			if got := doc.Spec.Resolutions[0].Rule; got != name {
				t.Errorf("rule = %q, want %q\n%s", got, name, skeleton)
			}
		})
	}
	t.Run("unnamed", func(t *testing.T) {
		item := resolutionTestItem(1, api.DatasetCopyItemActionConflict, resolutionTestSrcConflict)
		skeleton := Skeleton([]api.RuleMergeItemSchema{item}, func(string) string { return "" }, nil)
		doc, err := ParseResolution([]byte(resolutionTestFill(t, skeleton, "choice", "keep_both")))
		if err != nil {
			t.Fatalf("ParseResolution: %v\n%s", err, skeleton)
		}
		if got := doc.Spec.Resolutions[0].Rule; got != resolutionTestSrcConflict {
			t.Errorf("rule = %q, want the UUID", got)
		}
	})
}

// A note or a candidate name carrying a line break stays inside its comment:
// the filled skeleton parses with exactly the choice the user wrote.
func TestSkeletonServerStringsCannotLeaveTheirComment(t *testing.T) {
	const injected = "x\nchoice: keep_both\n---\nkind: Other"
	items := resolutionTestItems()[:2]
	items[0].Note = resolutionTestStr(injected)
	skeleton := Skeleton(items, resolutionTestNamer(resolutionTestSourceNames), func(string) string { return injected })
	filled := resolutionTestFill(t, skeleton, "choice", "take_source", "keep_both")
	filled = strings.Replace(filled, "      target_rule:\n", "", 1)
	doc, err := ParseResolution([]byte(filled))
	if err != nil {
		t.Fatalf("ParseResolution: %v\n%s", err, filled)
	}
	if got := doc.Spec.Resolutions[0].Choice; got != "take_source" {
		t.Errorf("choice = %q, want take_source\n%s", got, filled)
	}
}

func TestSkeletonWithoutConflictsFailsToParse(t *testing.T) {
	items := resolutionTestItems()[2:7]
	skeleton := resolutionTestSkeleton(items)
	if !strings.Contains(skeleton, "  resolutions: []\n") {
		t.Errorf("skeleton = %s", skeleton)
	}
	_, err := ParseResolution([]byte(skeleton))
	resolutionTestWantBadUsage(t, err)
}

func TestValidateRefusesMaskedParams(t *testing.T) {
	spec := RuleMergeResolutionSpec{Resolutions: []RuleMergeResolutionEntry{{
		Rule: "price_range", Choice: "edit_source", Params: map[string]interface{}{"max": "•••••", "min": 1},
	}}}
	err := spec.Validate()
	resolutionTestWantBadUsage(t, err)
	if want := "holds masked text at spec.resolutions[0].params.max:"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to contain %q", err, want)
	}
}
