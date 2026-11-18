//go:build ignore

// generate-settings-model.go emits internal/datasets/settings_model.gen.go: the
// manifest form of the backend's settings model, derived from
// api.SettingsSchema instead of hand-copied out of it.
//
// The manifest cannot simply BE api.SettingsSchema, for three reasons that have
// nothing to do with the key list:
//
//  1. internal/pkg/manifest.StrictUnmarshal decodes with yaml.v3 KnownFields(true),
//     which honours yaml tags; client.gen.go carries only json tags, so a user
//     would have to spell array_delimiters as "arraydelimiters".
//  2. api.SettingsSchema's required fields are non-pointer, so an unset one is
//     indistinguishable from a zero and `get table -o yaml` could not tell "the
//     server sent 0" from "nobody said". Every manifest field is a pointer.
//  3. Four keys must not be on the manifest at all: id, version_id and
//     whodunit_id are server-owned, and schema_overrides is a second, unguarded
//     way to rewrite a schema (see the doc comment this emits).
//
// Those need different tags, different pointer-ness and one exclusion list. They
// do not need 43 hand-written field declarations -- which is what this replaces,
// after a hand copy drifted three keys behind the wire (qctl-o22) with make test
// green.
//
// WHY api.SettingsSchema AND NOT THE RAW SPEC: the client is one step further
// from the backend than components.schemas.SettingsSchema is, and it is what the
// converters actually marshal through, so deriving from it makes the manifest's
// Go field names and JSON keys match the wire types BY CONSTRUCTION rather than
// by reimplementing oapi-codegen's name mangling. It also means this generator
// needs no live backend and no downloaded spec: it runs anywhere, which is what
// lets TestSettingsModelMirrorsTheWireSchema assert the committed file is
// current on every `make test` instead of only after a regen.
//
// Usage: go run scripts/generate-settings-model.go > internal/datasets/settings_model.gen.go
package main

import (
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/codegen"
)

// excluded is the ONE place the manifest's exclusions are spelled. Every other
// key on api.SettingsSchema reaches the manifest.
//
// The reason text is emitted into the generated doc comment, so the file a
// reader opens explains itself. A key here that is no longer on the schema is a
// hard error rather than a silent no-op: left in place it would go on swallowing
// whatever key a later regen gives that name.
var excluded = map[string]string{
	"id":               "server-owned",
	"version_id":       "server-owned",
	"whodunit_id":      "server-owned",
	"schema_overrides": "deliberately absent; see the doc comment",
}

const docComment = `// SettingsModel is the manifest form of the table's settings model: every key
// api.SettingsSchema carries except the four below, each one a pointer so that
// "absent from the manifest" and "written as a zero value" stay distinguishable
// all the way to the wire.
//
// That distinction is the whole contract of a settings edit. apply seeds the
// PATCH from the table's current settings model and overlays the manifest on
// top, so a nil field keeps the stored value while a field written as ` + "`[]`" + ` or
// ` + "`{}`" + ` is an edit that clears it. A non-pointer field could not express the
// difference.
//
// EXCLUDED, and the exclusions are the only hand-maintained part of this file:
//
//   - id, version_id, whodunit_id -- server-owned. apply carries the two version
//     ids from the read for the nested optimistic-concurrency check; a manifest
//     that could set them would only ever set them wrong.
//   - schema_overrides -- DELIBERATELY ABSENT and must stay absent, even though
//     api.SettingsSchema declares it. PATCH /orgs/{org}/datasets accepts it --
//     get_dataset_patch_schema (atlas capi/capi/schemas/settings_schema.py)
//     clones SettingsSchema all-optional and schema_overrides is in no ignore
//     list -- and writes the column under DATASET_CONFIGS_EDIT with no migration
//     version check, no draft, no needs_review_migration alert and no
//     training-job kill. That is a second, unguarded way to change a schema, open
//     to roles that do not hold MIGRATION_EXECUTE. ` + "`kind: TableSchema`" + ` is the
//     guarded one. TestSettingsModelHasNoSchemaOverrides is the tripwire.`

// field is one emitted struct field: the wire's JSON key, the Go name taken from
// the wire type, and the manifest's pointer-wrapped rendering of its type.
type field struct {
	key    string
	name   string
	goType string
}

func main() {
	rt := reflect.TypeOf(api.SettingsSchema{})

	var imports codegen.Imports
	var fields []field
	seen := map[string]bool{}
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		key := codegen.JSONKey(f)
		if key == "" || key == "-" {
			fail("api.SettingsSchema.%s has no usable json tag; the manifest key would be guessed rather than derived", f.Name)
		}
		seen[key] = true
		if _, skip := excluded[key]; skip {
			continue
		}
		gt, err := imports.GoType(f.Type)
		if err != nil {
			fail("api.SettingsSchema.%s (%s): %v", f.Name, key, err)
		}
		fields = append(fields, field{key: key, name: f.Name, goType: codegen.PointerTo(gt)})
	}

	for key, why := range excluded {
		if !seen[key] {
			fail("%q is on the exclusion list (%s) but no longer on api.SettingsSchema; delete the entry rather than leaving it to swallow whatever key a later regen gives that name", key, why)
		}
	}
	if len(fields) == 0 {
		fail("api.SettingsSchema produced no manifest fields at all")
	}

	sort.Slice(fields, func(i, j int) bool { return fields[i].key < fields[j].key })

	var b strings.Builder
	b.WriteString("// Code generated by scripts/generate-settings-model.go. DO NOT EDIT.\n")
	b.WriteString("// Regenerate with: make generate-client\n\n")
	b.WriteString("package datasets\n\n")
	// Only the packages an emitted field names, because gofmt does not prune an
	// unused import the way goimports would: an unused one lets the recipe
	// succeed and `go build ./...` fail a step later, several Makefile lines
	// away from the cause.
	b.WriteString(imports.Decl())
	b.WriteString(docComment + "\n")
	b.WriteString("type SettingsModel struct {\n")
	for _, f := range fields {
		fmt.Fprintf(&b, "\t%s %s `yaml:%q json:%q`\n", f.name, f.goType, f.key+",omitempty", f.key+",omitempty")
	}
	b.WriteString("}\n")

	fmt.Print(b.String())
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "generate-settings-model: "+format+"\n", args...)
	os.Exit(1)
}
