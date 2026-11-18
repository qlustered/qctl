package cloud_sources

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/codegen"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// TestCloudSourceSpecMirrorsThePatchSchema is the tripwire that lets
// spec.gen.go be trusted without regenerating it.
//
// scripts/generate-cloud-source-spec.go derives CloudSourceFields and
// SettingsOverrides from api.DataSourceModelPatchRequestSchema and needs no
// downloaded spec, so the derivation is reproducible here: for every field of
// the two structs, this asserts it is exactly what the generator would have
// emitted -- the wire field's Go name (without Override, for an override), the
// wire type as internal/pkg/codegen renders it with one pointer added unless it
// already is one, both tags spelled with the manifest key and omitempty, and
// the fields in key order.
//
// The case it exists for is a regen that retypes a Patch field while
// spec.gen.go keeps the old type. Apply and the export move fields through
// JSON, so for a field no Go code names everything still compiles, and the only
// symptom is a key that stops decoding.
//
// TestCloudSourceSpecCoversEveryPatchKey is the other half: that one checks the
// KEY SET, this one each field's SHAPE.
func TestCloudSourceSpecMirrorsThePatchSchema(t *testing.T) {
	wire := patchSchemaFields(t)

	fields := reflect.TypeOf(CloudSourceFields{})
	var keys []string
	for i := range fields.NumField() {
		f := fields.Field(i)
		key := manifestFieldKey(f)
		keys = append(keys, key)
		if strings.HasSuffix(key, overrideSuffix) {
			t.Errorf("CloudSourceFields.%s carries %s, but the generator puts every *%s key in SettingsOverrides without the suffix; spec.gen.go was edited by hand -- run make generate-client", f.Name, key, overrideSuffix)
			continue
		}
		src, ok := wire[key]
		if !ok {
			t.Errorf("CloudSourceFields.%s carries %s, which api.DataSourceModelPatchRequestSchema does not; the generator emits only the schema's keys -- run make generate-client", f.Name, key)
			continue
		}
		checkGeneratedField(t, "CloudSourceFields", f, key, src, src.Name)
	}
	checkSortedByKey(t, "CloudSourceFields", keys)

	overrides := reflect.TypeOf(SettingsOverrides{})
	keys = nil
	for i := range overrides.NumField() {
		f := overrides.Field(i)
		name := manifestFieldKey(f)
		keys = append(keys, name)
		src, ok := wire[name+overrideSuffix]
		if !ok {
			t.Errorf("SettingsOverrides.%s carries %s, but api.DataSourceModelPatchRequestSchema has no %s%s; the generator emits only the schema's keys -- run make generate-client", f.Name, name, name, overrideSuffix)
			continue
		}
		goName, ok := strings.CutSuffix(src.Name, "Override")
		if !ok || goName == "" {
			t.Errorf("api.DataSourceModelPatchRequestSchema.%s carries %s%s but its Go name does not end in Override, so the generator refuses to run; spec.gen.go cannot be current", src.Name, name, overrideSuffix)
			continue
		}
		checkGeneratedField(t, "SettingsOverrides", f, name, src, goName)
	}
	checkSortedByKey(t, "SettingsOverrides", keys)
}

// checkGeneratedField reports every way f differs from the field the generator
// emits into owner for the Patch field src under key.
func checkGeneratedField(t *testing.T, owner string, f reflect.StructField, key string, src reflect.StructField, wantName string) {
	t.Helper()
	if f.Name != wantName {
		t.Errorf("%s is %s.%s, want %s: the generator takes the Go name from api.DataSourceModelPatchRequestSchema.%s, so a different one means the file was edited by hand", key, owner, f.Name, wantName, src.Name)
	}

	var imports codegen.Imports
	want, err := imports.GoType(src.Type)
	if err != nil {
		t.Errorf("api.DataSourceModelPatchRequestSchema.%s (%s) does not render: %v; the generator refuses to run, so spec.gen.go cannot be current", src.Name, key, err)
	} else {
		want = codegen.PointerTo(want)
		got, err := imports.GoType(f.Type)
		if err != nil || got != want {
			t.Errorf("%s is %s on %s, want %s (api.DataSourceModelPatchRequestSchema.%s is %s): the manifest and the wire disagree on the value's type -- run make generate-client", key, f.Type, owner, want, src.Name, src.Type)
		}
	}

	if f.Type.Kind() != reflect.Pointer {
		t.Errorf("%s is %s, which is not a pointer: the manifest could not tell a field left out from one written as a zero value, and apply reads the two differently", key, f.Type)
	}

	if got, want := f.Tag.Get("yaml"), key+",omitempty"; got != want {
		t.Errorf("%s.%s has yaml:%q, want %q -- the yaml key is what a manifest spells, and without omitempty get cloud-source -o yaml prints every unset field as null", owner, f.Name, got, want)
	}
	if got, want := f.Tag.Get("json"), key+",omitempty"; got != want {
		t.Errorf("%s.%s has json:%q, want %q -- the json key is what apply sends, and without omitempty the spec's JSON encoding, which apply builds its request from, carries every unset field as null", owner, f.Name, got, want)
	}
}

// checkSortedByKey reports keys out of strictly ascending order.
func checkSortedByKey(t *testing.T, owner string, keys []string) {
	t.Helper()
	for i := 1; i < len(keys); i++ {
		if keys[i-1] >= keys[i] {
			t.Errorf("%s is out of order or repeated at %s / %s; the generator emits each key once, sorted", owner, keys[i-1], keys[i])
		}
	}
}

// TestCloudSourceSpecCoversEveryPatchKey checks that the manifest can express
// every key api.DataSourceModelPatchRequestSchema carries, except the ones
// excluded on purpose.
//
// The Patch schema's keys are exactly CloudSourceFields' keys, the
// SettingsOverrides keys with _override appended, and the exclusions, with no
// key in two of those. The exclusions are checked for existence too, so a regen
// that renames or drops one leaves a stale entry that fails here rather than a
// hole that hides a real key.
//
// The exclusion map below is a copy of excluded in
// scripts/generate-cloud-source-spec.go. Their key sets cannot drift apart
// silently: a key excluded in one map and not the other is either unexpressed
// or in two places, and both fail here.
func TestCloudSourceSpecCoversEveryPatchKey(t *testing.T) {
	excluded := map[string]string{
		"id":                   "status",
		"version_id":           "status",
		"state":                "status",
		"internal_id":          "status",
		"delta_applied_cursor": "status",

		"name": "metadata.name",

		"dataset_id":        "declared on CloudSourceSpec",
		"data_source_type":  "declared on CloudSourceSpec",
		"settings_model_id": "declared on CloudSourceSpec",

		"redirect_post_submit_to":          "not source configuration",
		"excluded_row_signatures_override": "not source configuration",
	}

	wire := patchSchemaFields(t)
	for key, where := range excluded {
		if _, ok := wire[key]; !ok {
			t.Errorf("%s is on the exclusion list (%s) but no longer on api.DataSourceModelPatchRequestSchema; delete it from excluded in scripts/generate-cloud-source-spec.go and from this test, rather than leaving it to swallow a key a later regen gives that name", key, where)
		}
	}

	places := map[string][]string{}
	for _, key := range manifestKeysOf(CloudSourceFields{}) {
		places[key] = append(places[key], "spec."+key)
	}
	for _, name := range manifestKeysOf(SettingsOverrides{}) {
		places[name+overrideSuffix] = append(places[name+overrideSuffix], "spec.settings_overrides."+name)
	}

	for _, key := range slices.Sorted(maps.Keys(wire)) {
		where, isExcluded := excluded[key]
		switch {
		case len(places[key]) == 0 && !isExcluded:
			t.Errorf("api.DataSourceModelPatchRequestSchema carries %s, but no manifest field expresses it and it is not excluded: a source that has it exports without it, and apply can never set it -- run make generate-client, or, to keep it off the manifest, add it to excluded in scripts/generate-cloud-source-spec.go and in this test", key)
		case len(places[key]) > 0 && isExcluded:
			t.Errorf("%s is on the manifest as %s, but the exclusion list keeps it off (%s) -- spec.gen.go was edited by hand, or the two exclusion maps disagree: make excluded in scripts/generate-cloud-source-spec.go and in this test match, then run make generate-client", key, strings.Join(places[key], " and "), where)
		case len(places[key]) > 1:
			t.Errorf("%s is on the manifest twice, as %s: two manifest paths write one wire key -- spec.gen.go was edited by hand; run make generate-client", key, strings.Join(places[key], " and "))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(places)) {
		if _, ok := wire[key]; !ok {
			t.Errorf("%s maps to %s, which api.DataSourceModelPatchRequestSchema does not carry: a manifest can set it and apply sends a key the PATCH does not declare -- run make generate-client", strings.Join(places[key], " and "), key)
		}
	}
}

// TestEveryManifestKeyIsReadBack checks that the read the export decodes
// carries every key the manifest can write.
//
// APIResponseToManifest re-encodes a decoded CloudSourceFull
// (api.DataSourceModelAllOptionalSchema, the GET detail's type), so a key that
// type does not declare never reaches an exported manifest, even when the
// server sends it. PrepareApply reads a left-out nullable field or override as
// null, so re-applying that unedited export would clear the stored value.
//
// writeOnly names the keys the read never carries by design. Each must stay a
// secret, which the export leaves out anyway, and must not be cleared when
// omitted, so an export that lacks it keeps the stored value.
func TestEveryManifestKeyIsReadBack(t *testing.T) {
	writeOnly := map[string]string{
		"dropbox_authorization_code": "an OAuth code the server exchanges for the dropbox_access_token the read carries",
	}

	read := map[string]bool{}
	rt := reflect.TypeOf(CloudSourceFull{})
	for i := range rt.NumField() {
		read[codegen.JSONKey(rt.Field(i))] = true
	}

	for key, why := range writeOnly {
		switch {
		case read[key]:
			t.Errorf("%s is on writeOnly (%s) but api.DataSourceModelAllOptionalSchema now carries it; delete it from writeOnly", key, why)
		case !slices.Contains(manifestKeysOf(CloudSourceFields{}), key):
			t.Errorf("%s is on writeOnly (%s) but is no longer a manifest key; delete it from writeOnly", key, why)
		case !isSecret(key):
			t.Errorf("%s is on writeOnly (%s) but is not a secret, so the export would drop a value it could show", key, why)
		case clearedWhenOmitted(key):
			t.Errorf("%s is on writeOnly (%s) but is cleared when omitted, so applying an export would clear it", key, why)
		}
	}

	for _, key := range manifestKeysOf(CloudSourceFields{}) {
		if _, ok := writeOnly[key]; ok {
			continue
		}
		if !read[key] {
			t.Errorf("spec.%s is not a key of api.DataSourceModelAllOptionalSchema: get cloud-source can never export it, and apply of that export treats it as left out", key)
		}
	}
	for _, name := range manifestKeysOf(SettingsOverrides{}) {
		if !read[name+overrideSuffix] {
			t.Errorf("spec.settings_overrides.%s is %s%s on the wire, which is not a key of api.DataSourceModelAllOptionalSchema: get cloud-source can never export it, and apply of that export clears it", name, name, overrideSuffix)
		}
	}
}

// TestCloudSourceSpecHandKeysDoNotCollide checks that the generated fields and
// the ones CloudSourceSpec declares by hand share one mapping, and that a
// manifest setting every generated field survives the trip from get
// cloud-source -o yaml|json through apply -f's StrictUnmarshal.
//
// CloudSourceFields is inlined into CloudSourceSpec, so a Patch key a regen
// adds under the name of a hand-declared one (dataset_name, say) makes yaml.v3
// panic on every manifest, while encoding/json silently lets the hand field
// hide the generated one.
func TestCloudSourceSpecHandKeysDoNotCollide(t *testing.T) {
	spec := reflect.TypeOf(CloudSourceSpec{})
	handYAML, handJSON := map[string]string{}, map[string]string{}
	var embedded bool
	for i := range spec.NumField() {
		f := spec.Field(i)
		if f.Anonymous {
			if f.Type == reflect.TypeOf(CloudSourceFields{}) {
				embedded = true
				if got := f.Tag.Get("yaml"); got != ",inline" {
					t.Errorf("CloudSourceSpec embeds CloudSourceFields with yaml:%q, want \",inline\": YAML would nest every generated key while JSON keeps it flat", got)
				}
			}
			continue
		}
		handYAML[tagName(f, "yaml")] = f.Name
		handJSON[tagName(f, "json")] = f.Name
	}
	if !embedded {
		t.Fatalf("CloudSourceSpec does not embed CloudSourceFields; the generated keys are not on the manifest at all")
	}

	var collided bool
	fields := reflect.TypeOf(CloudSourceFields{})
	for i := range fields.NumField() {
		f := fields.Field(i)
		if hand, ok := handYAML[tagName(f, "yaml")]; ok {
			collided = true
			t.Errorf("CloudSourceFields.%s and CloudSourceSpec.%s are both spec.%s in YAML: yaml.v3 panics on a struct with a duplicated key, so get cloud-source -o yaml and apply -f both crash", f.Name, hand, tagName(f, "yaml"))
		}
		if hand, ok := handJSON[tagName(f, "json")]; ok {
			collided = true
			t.Errorf("CloudSourceFields.%s and CloudSourceSpec.%s are both spec.%s in JSON: encoding/json drops the generated one, so get cloud-source -o json never prints it", f.Name, hand, tagName(f, "json"))
		}
	}
	if collided {
		t.FailNow() // the round trip below would panic in yaml.v3
	}

	want := CloudSourceManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "CloudSource",
		Metadata:   pkgmanifest.Metadata{Name: "orders"},
		Spec: CloudSourceSpec{
			DatasetID:         42,
			DataSourceType:    cloudSourcePtr(DataSourceTypeS3),
			SettingsModelID:   cloudSourcePtr(2),
			SettingsOverrides: &SettingsOverrides{},
		},
	}
	fillPointers(t, reflect.ValueOf(&want.Spec.CloudSourceFields).Elem())
	fillPointers(t, reflect.ValueOf(want.Spec.SettingsOverrides).Elem())

	for _, format := range []string{"yaml", "json"} {
		raw := encodeManifest(t, format, want)
		var got CloudSourceManifest
		if err := pkgmanifest.StrictUnmarshal(raw, &got); err != nil {
			t.Errorf("a manifest with every field set, printed as %s, does not parse back strictly: %v", format, err)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("a manifest with every field set, printed as %s, parses back different.\nprinted:\n%s\nparsed, printed again:\n%s", format, raw, encodeManifest(t, format, got))
		}
	}
}

// encodeManifest encodes m as get cloud-source -o yaml|json does.
func encodeManifest(t *testing.T, format string, m CloudSourceManifest) []byte {
	t.Helper()
	var buf bytes.Buffer
	var err error
	switch format {
	case "yaml":
		enc := jsonnum.NewYAMLEncoder(&buf)
		enc.SetIndent(2)
		if err = enc.Encode(m); err == nil {
			err = enc.Close()
		}
	case "json":
		enc := json.NewEncoder(&buf)
		enc.SetIndent("", "  ")
		err = enc.Encode(m)
	}
	if err != nil {
		t.Fatalf("encode as %s: %v", format, err)
	}
	return buf.Bytes()
}

// TestCloudSourceSpecFileIsGenerated keeps spec.gen.go's DO NOT EDIT banner
// honest. .golangci.yml skips generated files only by that banner, and a field
// added by hand is reverted by the next make generate-client.
func TestCloudSourceSpecFileIsGenerated(t *testing.T) {
	const path = "spec.gen.go"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	first, rest, _ := strings.Cut(string(raw), "\n")

	const banner = "// Code generated by scripts/generate-cloud-source-spec.go. DO NOT EDIT."
	if first != banner {
		t.Errorf("%s starts with %q, want %q; without it nothing tells a reader or a linter the file is machine-written", path, first, banner)
	}
	const regen = "// Regenerate with: make generate-client"
	if !strings.Contains(rest, regen) {
		t.Errorf("%s is missing %q; without it a reader does not know how the file is rebuilt", path, regen)
	}
}

// patchSchemaFields returns the fields of api.DataSourceModelPatchRequestSchema
// by json key.
func patchSchemaFields(t *testing.T) map[string]reflect.StructField {
	t.Helper()
	fields := map[string]reflect.StructField{}
	rt := reflect.TypeOf(api.DataSourceModelPatchRequestSchema{})
	for i := range rt.NumField() {
		f := rt.Field(i)
		key := codegen.JSONKey(f)
		if key == "" || key == "-" {
			t.Fatalf("api.DataSourceModelPatchRequestSchema.%s has no usable json tag, so the generator refuses to run", f.Name)
		}
		fields[key] = f
	}
	return fields
}

// manifestFieldKey returns the name in f's yaml tag, or f's Go name when the
// tag names none.
func manifestFieldKey(f reflect.StructField) string {
	key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
	if key == "" {
		return f.Name
	}
	return key
}

// manifestKeysOf returns manifestFieldKey of every field of v's struct type.
func manifestKeysOf(v any) []string {
	rt := reflect.TypeOf(v)
	keys := make([]string, 0, rt.NumField())
	for i := range rt.NumField() {
		keys = append(keys, manifestFieldKey(rt.Field(i)))
	}
	return keys
}

// tagName returns the key encoder ("yaml" or "json") writes f under.
func tagName(f reflect.StructField, encoder string) string {
	name, _, _ := strings.Cut(f.Tag.Get(encoder), ",")
	switch {
	case name != "":
		return name
	case encoder == "yaml":
		return strings.ToLower(f.Name)
	default:
		return f.Name
	}
}

// fillPointers sets every field of the struct v, each of which must be a
// pointer, to a non-zero value seeded by the field's name.
func fillPointers(t *testing.T, v reflect.Value) {
	t.Helper()
	for i := range v.NumField() {
		f := v.Type().Field(i)
		if f.Type.Kind() != reflect.Pointer {
			t.Fatalf("%s.%s is %s, not a pointer", v.Type(), f.Name, f.Type)
		}
		v.Field(i).Set(sampleValue(t, f.Type, f.Name))
	}
}

// sampleValue returns a non-zero value of type rt built from seed.
func sampleValue(t *testing.T, rt reflect.Type, seed string) reflect.Value {
	t.Helper()
	v := reflect.New(rt).Elem()
	switch rt.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(rt.Elem()))
		v.Elem().Set(sampleValue(t, rt.Elem(), seed))
	case reflect.String:
		v.SetString(seed)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(int64(len(seed)))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(uint64(len(seed)))
	case reflect.Float32, reflect.Float64:
		v.SetFloat(float64(len(seed)))
	case reflect.Interface:
		v.Set(reflect.ValueOf(seed))
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Array:
		for i := range rt.Len() {
			v.Index(i).Set(sampleValue(t, rt.Elem(), seed))
		}
	case reflect.Slice:
		v.Set(reflect.Append(v, sampleValue(t, rt.Elem(), seed)))
	case reflect.Map:
		v.Set(reflect.MakeMap(rt))
		v.SetMapIndex(sampleValue(t, rt.Key(), "key_"+seed), sampleValue(t, rt.Elem(), seed))
	default:
		t.Fatalf("sampleValue has no value for %s; add its kind", rt)
	}
	return v
}
