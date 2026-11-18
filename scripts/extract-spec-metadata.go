//go:build ignore

// extract-spec-metadata.go lifts the backend's x- extensions out of the raw
// OpenAPI spec and emits them as a committed Go file.
//
// oapi-codegen cannot do this: it runs with skip-prune: false, so a schema no
// operation references is dropped entirely, and it never emits x- values as Go
// data even for the schemas it keeps. CLAUDE.md forbids hand-editing
// client.gen.go and sanctions separate files in the same package for exactly
// this.
//
// Usage: go run scripts/extract-spec-metadata.go < build/openapi.raw.json > internal/api/metadata.gen.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
)

type kind int

const (
	kindActions     kind = iota // map[string][]string
	kindFields                  // map[string]ActionFields
	kindStrings                 // []string
	kindStringEnums             // map[string][]string
	kindOperations              // map[string]string
	kindCatalog                 // ActionCatalog
)

type extraction struct {
	// Ext is the component or operation metadata key.
	Ext    string
	Schema string // the component that carries it
	GoName string // the exported variable to emit
	Kind   kind
	Doc    string
	// Offered is, for kindCatalog, the extension on the same component that
	// lists each issue type's offered actions; the catalog must describe
	// exactly those.
	Offered string
}

// extractions declares the metadata emitted from the spec.
var extractions = []extraction{
	{
		Ext: "x-required-permission", GoName: "OperationPermissions", Kind: kindOperations,
		Doc: "OperationPermissions maps an OpenAPI operation ID to its required permission.",
	},
	{
		Ext: "x-role-permissions", Schema: "RolePermissions",
		GoName: "RolePermissions", Kind: kindActions,
		Doc: "RolePermissions maps each role to its granted permissions.",
	},
	{
		Ext: "enum", GoName: "StringEnumValues", Kind: kindStringEnums,
		Doc: "StringEnumValues maps every named string enum schema to its allowed wire\n// values. Consumers key it by the generated Go type name, which matches the\n// component name; internal/paramflags uses it to validate reflection-derived\n// enum flags without copying either the types or their members by hand.",
	},
	{
		Ext: "x-issue-type-alert-actions", Schema: "IssueTypeAlertActions",
		GoName: "IssueTypeAlertActions", Kind: kindActions,
		Doc: "IssueTypeAlertActions maps an alert issue_type to every action the backend\n// can offer for it. An upper bound: a fetched alert's own spec.actions is\n// narrower, because the server filters by role and by runtime checks.",
	},
	{
		Ext: "x-issue-type-warning-actions", Schema: "IssueTypeWarningActions",
		GoName: "IssueTypeWarningActions", Kind: kindActions,
		Doc: "IssueTypeWarningActions is IssueTypeAlertActions for warnings.",
	},
	{
		Ext: "x-alert-action-catalog", Schema: "IssueTypeAlertActions", Offered: "x-issue-type-alert-actions",
		GoName: "AlertActionCatalog", Kind: kindCatalog,
		Doc: "AlertActionCatalog describes what resolving an alert with each action does,\n// per issue type. See ActionCatalog in metadata.go.",
	},
	{
		Ext: "x-warning-action-catalog", Schema: "IssueTypeWarningActions", Offered: "x-issue-type-warning-actions",
		GoName: "WarningActionCatalog", Kind: kindCatalog,
		Doc: "WarningActionCatalog is AlertActionCatalog for warnings.",
	},
	{
		Ext: "x-alert-action-fields", Schema: "AlertResolveSchema",
		GoName: "AlertActionFields", Kind: kindFields,
		Doc: "AlertActionFields maps an alert resolve action to the request fields the\n// backend expects alongside it. See ActionFields in metadata.go.",
	},
	{
		Ext: "x-warning-action-fields", Schema: "WarningsResolveSchema",
		GoName: "WarningActionFields", Kind: kindFields,
		Doc: "WarningActionFields is AlertActionFields for warnings. It is a genuinely\n// smaller map, not a filtered view of the alert one.",
	},
	{
		Ext: "x-error-codes", Schema: "ErrorCode",
		GoName: "ErrorCodes", Kind: kindStrings,
		Doc: "ErrorCodes is the backend's full catalog of APIError.error_code values,\n// lifted from components.schemas.ErrorCode. Nothing $refs that schema and\n// oapi-codegen runs with skip-prune: false, so it never reaches client.gen.go\n// and APIError.ErrorCode stays a bare *string. internal/apierror records an\n// outcome for every one of these.",
	},
	{
		Ext: "enum", Schema: "FieldType",
		GoName: "FieldTypes", Kind: kindStrings,
		Doc: "FieldTypes is the backend's full list of column types, lifted from\n// components.schemas.FieldType.enum. oapi-codegen runs with skip-prune: false\n// and no operation $refs the enum's constants, so client.gen.go declares a bare\n// `type FieldType string` and nothing there can tell a valid column type from a\n// typo. `apply -f <TableSchema>` validates spec.fields.<name>.type against this\n// list client-side, which is what turns a 422 with an object-shaped body into a\n// plain exit 2 naming the valid values.",
	},
}

type rawFields struct {
	Required []string   `json:"required"`
	OneOf    [][]string `json:"one_of"`
	Optional []string   `json:"optional"`
	Note     string     `json:"note"`
}

func main() {
	var spec struct {
		Paths      map[string]map[string]json.RawMessage `json:"paths"`
		Components struct {
			Schemas map[string]map[string]json.RawMessage `json:"schemas"`
		} `json:"components"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&spec); err != nil {
		fail("decode spec: %v", err)
	}

	var b strings.Builder
	b.WriteString("// Code generated by scripts/extract-spec-metadata.go. DO NOT EDIT.\n")
	b.WriteString("// Regenerate with: make generate-client\n\n")
	b.WriteString("package api\n")

	for _, e := range extractions {
		if e.Kind == kindOperations {
			writeOperations(&b, e, spec.Paths)
			continue
		}
		if e.Kind == kindStringEnums {
			writeStringEnums(&b, e, spec.Components.Schemas)
			continue
		}
		schema, ok := spec.Components.Schemas[e.Schema]
		if !ok {
			fail("spec has no component %q, which carries %s", e.Schema, e.Ext)
		}
		raw, ok := schema[e.Ext]
		if !ok {
			fail("component %q no longer carries %s", e.Schema, e.Ext)
		}
		switch e.Kind {
		case kindActions:
			writeActions(&b, e, raw)
		case kindFields:
			writeFields(&b, e, raw)
		case kindStrings:
			writeStrings(&b, e, raw)
		case kindCatalog:
			writeCatalog(&b, e, raw, schema[e.Offered])
		}
	}
	fmt.Print(b.String())
}

func writeOperations(b *strings.Builder, e extraction, paths map[string]map[string]json.RawMessage) {
	permissions := make(map[string]string)
	for _, path := range sortedKeys(paths) {
		for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
			raw, ok := paths[path][method]
			if !ok {
				continue
			}
			var operation map[string]json.RawMessage
			if err := json.Unmarshal(raw, &operation); err != nil {
				fail("%s %s is not an operation: %v", method, path, err)
			}
			permissionJSON, ok := operation[e.Ext]
			if !ok {
				continue
			}
			var id, permission string
			if err := json.Unmarshal(operation["operationId"], &id); err != nil || strings.TrimSpace(id) == "" {
				fail("%s %s carrying %s has no non-empty operationId", method, path, e.Ext)
			}
			if err := json.Unmarshal(permissionJSON, &permission); err != nil || strings.TrimSpace(permission) == "" {
				fail("operation %q %s is not a non-empty string", id, e.Ext)
			}
			if _, exists := permissions[id]; exists {
				fail("duplicate permission-bearing operationId %q", id)
			}
			permissions[id] = permission
		}
	}
	if len(permissions) == 0 {
		fail("no operation carries %s", e.Ext)
	}
	fmt.Fprintf(b, "\n// %s\nvar %s = map[string]string{\n", e.Doc, e.GoName)
	for _, id := range sortedKeys(permissions) {
		fmt.Fprintf(b, "\t%q: %q,\n", id, permissions[id])
	}
	b.WriteString("}\n")
}

func writeActions(b *strings.Builder, e extraction, raw json.RawMessage) {
	var m map[string][]string
	if err := json.Unmarshal(raw, &m); err != nil {
		fail("%s is not map[string][]string: %v", e.Ext, err)
	}
	// An empty map here would compile, ship, and fail open at runtime.
	if len(m) == 0 {
		fail("%s extracted to an empty map", e.Ext)
	}
	fmt.Fprintf(b, "\n// %s\nvar %s = map[string][]string{\n", e.Doc, e.GoName)
	for _, k := range sortedKeys(m) {
		fmt.Fprintf(b, "\t%q: {", k)
		for i, v := range m[k] {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(b, "%q", v)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
}

func writeFields(b *strings.Builder, e extraction, raw json.RawMessage) {
	var m map[string]rawFields
	if err := json.Unmarshal(raw, &m); err != nil {
		fail("%s is not map[string]ActionFields: %v", e.Ext, err)
	}
	if len(m) == 0 {
		fail("%s extracted to an empty map", e.Ext)
	}
	fmt.Fprintf(b, "\n// %s\nvar %s = map[string]ActionFields{\n", e.Doc, e.GoName)
	for _, k := range sortedKeys(m) {
		f := m[k]
		fmt.Fprintf(b, "\t%q: {", k)
		if len(f.Required) > 0 {
			// Explicit []string: this literal sits directly in a struct field
			// position (not as an element of an outer slice/map literal), so
			// Go's composite-literal type elision does not reach it.
			fmt.Fprintf(b, "Required: []string%s, ", goStrings(f.Required))
		}
		if len(f.OneOf) > 0 {
			b.WriteString("OneOf: [][]string{")
			for i, g := range f.OneOf {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(goStrings(g))
			}
			b.WriteString("}, ")
		}
		if len(f.Optional) > 0 {
			fmt.Fprintf(b, "Optional: []string%s, ", goStrings(f.Optional))
		}
		if f.Note != "" {
			fmt.Fprintf(b, "Note: %q, ", f.Note)
		}
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
}

type rawCatalogEntry struct {
	Description  string   `json:"description"`
	Scope        []string `json:"scope"`
	ModifiesData *bool    `json:"modifies_data"`
	FollowOn     []string `json:"follow_on"`
	Reversible   *bool    `json:"reversible"`
	Proposable   string   `json:"proposable"`
	SameEffectAs []struct {
		Operation string   `json:"operation"`
		Key       []string `json:"key"`
	} `json:"same_effect_as"`
}

type rawIssueTypeCatalog struct {
	BusinessLabel         *string `json:"business_label"`
	BusinessSummary       *string `json:"business_summary"`
	BusinessNamesByReason map[string]struct {
		Label   string `json:"label"`
		Summary string `json:"summary"`
	} `json:"business_names_by_reason"`
	FixOwner             *string                     `json:"fix_owner"`
	FixOwnerByReason     map[string]string           `json:"fix_owner_by_reason"`
	UserResolvable       *bool                       `json:"user_resolvable"`
	Actions              map[string]*rawCatalogEntry `json:"actions"`
	NoAction             *rawCatalogEntry            `json:"no_action"`
	FieldNamesResolution *rawCatalogEntry            `json:"field_names_resolution"`
}

type rawCatalog struct {
	Actions    map[string]*rawCatalogEntry     `json:"actions"`
	IssueTypes map[string]*rawIssueTypeCatalog `json:"issue_types"`
}

// writeCatalog emits an ActionCatalog. An issue type's action entry is kept
// only where it differs from the action's own entry, which is what
// ActionCatalog.Entry falls back to; every other pair is identical, and
// repeating each would multiply the file's size.
func writeCatalog(b *strings.Builder, e extraction, raw, offeredRaw json.RawMessage) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// A new key in the catalog fails the generator instead of being dropped:
	// add it to ActionCatalog and to the raw types here.
	dec.DisallowUnknownFields()
	var c rawCatalog
	if err := dec.Decode(&c); err != nil {
		fail("%s is not an action catalog: %v", e.Ext, err)
	}
	if len(c.Actions) == 0 {
		fail("%s extracted to an empty catalog", e.Ext)
	}
	var offered map[string][]string
	if err := json.Unmarshal(offeredRaw, &offered); err != nil {
		fail("%s is not map[string][]string: %v", e.Offered, err)
	}
	for _, name := range sortedKeys(c.Actions) {
		checkEntry(e, "action "+name, c.Actions[name])
	}
	for _, issueType := range sortedKeys(offered) {
		if _, ok := c.IssueTypes[issueType]; !ok {
			fail("%s has no entry for issue type %q, which %s lists", e.Ext, issueType, e.Offered)
		}
	}
	for _, issueType := range sortedKeys(c.IssueTypes) {
		it := c.IssueTypes[issueType]
		want, ok := offered[issueType]
		if !ok {
			fail("%s describes issue type %q, which %s does not list", e.Ext, issueType, e.Offered)
		}
		if it.UserResolvable == nil {
			fail("%s issue type %q has no user_resolvable", e.Ext, issueType)
		}
		got := sortedKeys(it.Actions)
		sortedWant := append([]string(nil), want...)
		sort.Strings(sortedWant)
		if !reflect.DeepEqual(got, sortedWant) && (len(got) > 0 || len(sortedWant) > 0) {
			fail("%s issue type %q describes actions %v, but %s offers %v", e.Ext, issueType, got, e.Offered, sortedWant)
		}
		for _, action := range got {
			checkEntry(e, fmt.Sprintf("issue type %q action %q", issueType, action), it.Actions[action])
		}
		if it.NoAction != nil {
			checkEntry(e, fmt.Sprintf("issue type %q no_action", issueType), it.NoAction)
		}
		if it.FieldNamesResolution != nil {
			checkEntry(e, fmt.Sprintf("issue type %q field_names_resolution", issueType), it.FieldNamesResolution)
		}
	}

	fmt.Fprintf(b, "\n// %s\nvar %s = ActionCatalog{\n\tActions: map[string]ActionCatalogEntry{\n", e.Doc, e.GoName)
	for _, name := range sortedKeys(c.Actions) {
		fmt.Fprintf(b, "%q: %s,\n", name, goEntry(c.Actions[name]))
	}
	b.WriteString("},\nIssueTypes: map[string]IssueTypeCatalog{\n")
	for _, issueType := range sortedKeys(c.IssueTypes) {
		it := c.IssueTypes[issueType]
		fmt.Fprintf(b, "%q: {\n", issueType)
		if it.BusinessLabel != nil {
			fmt.Fprintf(b, "BusinessLabel: %q,\n", *it.BusinessLabel)
		}
		if it.BusinessSummary != nil {
			fmt.Fprintf(b, "BusinessSummary: %q,\n", *it.BusinessSummary)
		}
		if len(it.BusinessNamesByReason) > 0 {
			b.WriteString("BusinessNamesByReason: map[string]BusinessNames{\n")
			for _, reason := range sortedKeys(it.BusinessNamesByReason) {
				n := it.BusinessNamesByReason[reason]
				fmt.Fprintf(b, "%q: {Label: %q, Summary: %q},\n", reason, n.Label, n.Summary)
			}
			b.WriteString("},\n")
		}
		if it.FixOwner != nil {
			fmt.Fprintf(b, "FixOwner: %q,\n", *it.FixOwner)
		}
		if len(it.FixOwnerByReason) > 0 {
			b.WriteString("FixOwnerByReason: map[string]string{\n")
			for _, reason := range sortedKeys(it.FixOwnerByReason) {
				fmt.Fprintf(b, "%q: %q,\n", reason, it.FixOwnerByReason[reason])
			}
			b.WriteString("},\n")
		}
		fmt.Fprintf(b, "UserResolvable: %t,\n", *it.UserResolvable)
		var overrides []string
		for _, action := range sortedKeys(it.Actions) {
			if own := c.Actions[action]; own == nil || !reflect.DeepEqual(own, it.Actions[action]) {
				overrides = append(overrides, action)
			}
		}
		if len(overrides) > 0 {
			b.WriteString("Actions: map[string]ActionCatalogEntry{\n")
			for _, action := range overrides {
				fmt.Fprintf(b, "%q: %s,\n", action, goEntry(it.Actions[action]))
			}
			b.WriteString("},\n")
		}
		if it.NoAction != nil {
			fmt.Fprintf(b, "NoAction: &ActionCatalogEntry%s,\n", goEntry(it.NoAction))
		}
		if it.FieldNamesResolution != nil {
			fmt.Fprintf(b, "FieldNamesResolution: &ActionCatalogEntry%s,\n", goEntry(it.FieldNamesResolution))
		}
		b.WriteString("},\n")
	}
	b.WriteString("},\n}\n")
}

func checkEntry(e extraction, what string, entry *rawCatalogEntry) {
	switch {
	case entry == nil:
		fail("%s %s is null", e.Ext, what)
	case strings.TrimSpace(entry.Description) == "":
		fail("%s %s has no description", e.Ext, what)
	case len(entry.Scope) == 0:
		fail("%s %s has no scope", e.Ext, what)
	case entry.ModifiesData == nil || entry.Reversible == nil:
		fail("%s %s lacks modifies_data or reversible", e.Ext, what)
	case entry.Proposable == "":
		fail("%s %s has no proposable", e.Ext, what)
	}
}

// goEntry renders entry as an ActionCatalogEntry composite literal without
// its type name.
func goEntry(entry *rawCatalogEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{\nDescription: %q,\nScope: []string%s,\n", entry.Description, goStrings(entry.Scope))
	if *entry.ModifiesData {
		b.WriteString("ModifiesData: true,\n")
	}
	if len(entry.FollowOn) > 0 {
		fmt.Fprintf(&b, "FollowOn: []string%s,\n", goStrings(entry.FollowOn))
	}
	if *entry.Reversible {
		b.WriteString("Reversible: true,\n")
	}
	fmt.Fprintf(&b, "Proposable: %q,\n", entry.Proposable)
	if len(entry.SameEffectAs) > 0 {
		b.WriteString("SameEffectAs: []ActionEffect{\n")
		for _, effect := range entry.SameEffectAs {
			fmt.Fprintf(&b, "{Operation: %q, Key: []string%s},\n", effect.Operation, goStrings(effect.Key))
		}
		b.WriteString("},\n")
	}
	b.WriteString("}")
	return b.String()
}

func writeStrings(b *strings.Builder, e extraction, raw json.RawMessage) {
	var vals []string
	if err := json.Unmarshal(raw, &vals); err != nil {
		fail("%s is not []string: %v", e.Ext, err)
	}
	// An empty list here would compile, ship, and make every code
	// unclassified at runtime.
	if len(vals) == 0 {
		fail("%s extracted to an empty list", e.Ext)
	}
	fmt.Fprintf(b, "\n// %s\nvar %s = []string{\n", e.Doc, e.GoName)
	for _, v := range vals {
		fmt.Fprintf(b, "\t%q,\n", v)
	}
	b.WriteString("}\n")
}

func writeStringEnums(b *strings.Builder, e extraction, schemas map[string]map[string]json.RawMessage) {
	values := make(map[string][]string)
	for name, schema := range schemas {
		raw, ok := schema[e.Ext]
		if !ok || !schemaIncludesType(schema, "string") {
			continue
		}
		var enum []string
		if err := json.Unmarshal(raw, &enum); err != nil {
			fail("component %q %s is not []string: %v", name, e.Ext, err)
		}
		if len(enum) == 0 {
			fail("component %q %s extracted to an empty list", name, e.Ext)
		}
		values[name] = enum
	}
	if len(values) == 0 {
		fail("no string-valued component carries %s", e.Ext)
	}

	fmt.Fprintf(b, "\n// %s\nvar %s = map[string][]string{\n", e.Doc, e.GoName)
	for _, name := range sortedKeys(values) {
		fmt.Fprintf(b, "\t%q: %s,\n", name, goStrings(values[name]))
	}
	b.WriteString("}\n")
}

func schemaIncludesType(schema map[string]json.RawMessage, want string) bool {
	raw, ok := schema["type"]
	if !ok {
		return false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return single == want
	}
	var multiple []string
	if err := json.Unmarshal(raw, &multiple); err != nil {
		fail("schema type is neither string nor []string: %v", err)
	}
	for _, typ := range multiple {
		if typ == want {
			return true
		}
	}
	return false
}

func goStrings(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return "{" + strings.Join(quoted, ", ") + "}"
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "extract-spec-metadata: "+format+"\n", args...)
	os.Exit(1)
}
