package cloud_sources

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// diffNil stands for a nil FieldChange value in a diffWant.
const diffNil = "<nil>"

// diffWant is one FieldChange as a test expects it, its values as JSON text or
// diffNil.
type diffWant struct {
	path, key, before, after, verb string
	secret                         bool
}

// diffSpec decodes a CloudSource manifest whose spec is an s3 source in table
// 42 plus specYAML, whose lines are written unindented.
func diffSpec(t *testing.T, specYAML string) *CloudSourceSpec {
	t.Helper()
	var doc strings.Builder
	doc.WriteString("apiVersion: " + pkgmanifest.APIVersionV1 + "\nkind: CloudSource\n")
	doc.WriteString("metadata:\n  name: orders\nspec:\n  dataset_id: 42\n  data_source_type: s3\n")
	for line := range strings.Lines(specYAML) {
		doc.WriteString("  " + line)
	}
	var m CloudSourceManifest
	if err := pkgmanifest.StrictUnmarshal([]byte(doc.String()), &m); err != nil {
		t.Fatalf("manifest does not decode: %v\n%s", err, doc.String())
	}
	return &m.Spec
}

// diffLive is the non-null members of the cloud source read liveJSON.
func diffLive(t *testing.T, liveJSON string) map[string]json.RawMessage {
	t.Helper()
	members, err := nonNullMembers([]byte(liveJSON))
	if err != nil {
		t.Fatalf("live read does not decode: %v\n%s", err, liveJSON)
	}
	return members
}

// diffChanges is updateChanges of spec against live.
func diffChanges(t *testing.T, spec *CloudSourceSpec, live map[string]json.RawMessage) []FieldChange {
	t.Helper()
	changes, err := updateChanges(spec, live)
	if err != nil {
		t.Fatalf("updateChanges: %v", err)
	}
	return changes
}

// diffGot is changes as diffWants.
func diffGot(changes []FieldChange) []diffWant {
	var got []diffWant
	for _, c := range changes {
		got = append(got, diffWant{
			path: c.Path, key: c.Key, before: diffRaw(c.Old), after: diffRaw(c.New),
			verb: c.Verb(), secret: c.Secret,
		})
	}
	return got
}

func diffRaw(raw json.RawMessage) string {
	if raw == nil {
		return diffNil
	}
	return string(raw)
}

// diffCase is one update diff: a manifest spec against a live read.
type diffCase struct {
	name string
	spec string
	live string
	want []diffWant
}

// diffRun runs each case as a subtest.
func diffRun(t *testing.T, cases []diffCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := diffGot(diffChanges(t, diffSpec(t, tc.spec), diffLive(t, tc.live)))
			if !slices.Equal(got, tc.want) {
				t.Errorf("changes =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

// An omitted override or nullable field is cleared; an omitted NOT NULL
// field, and schedule, table_series_id, routing_mode or preload_row_drop_share
// left out or null, keep the stored value.
func TestDiffOmissionClearsOnlyNullableFields(t *testing.T) {
	diffRun(t, []diffCase{
		{
			name: "omitted overrides with live values are cleared, false and [] included",
			live: `{"boolean_true_override":[],"infer_datetime_override":false}`,
			want: []diffWant{
				{path: "spec.settings_overrides.boolean_true", key: "boolean_true_override", before: `[]`, after: diffNil, verb: "CLEAR"},
				{path: "spec.settings_overrides.infer_datetime", key: "infer_datetime_override", before: `false`, after: diffNil, verb: "CLEAR"},
			},
		},
		{
			name: "an override left out of a present settings_overrides is cleared",
			spec: "settings_overrides:\n  infer_datetime: true\n",
			live: `{"boolean_true_override":["yes"],"infer_datetime_override":true}`,
			want: []diffWant{
				{path: "spec.settings_overrides.boolean_true", key: "boolean_true_override", before: `["yes"]`, after: diffNil, verb: "CLEAR"},
			},
		},
		{
			name: "omitted override with a null live value",
			live: `{"infer_datetime_override":null,"boolean_true_override":null}`,
		},
		{
			name: "omitted csv_delimiter with a live value is cleared",
			live: `{"csv_delimiter":";"}`,
			want: []diffWant{
				{path: "spec.csv_delimiter", key: "csv_delimiter", before: `";"`, after: diffNil, verb: "CLEAR"},
			},
		},
		{
			name: "omitted csv_delimiter with a null live value",
			live: `{"csv_delimiter":null}`,
		},
		{
			name: "omitted NOT NULL fields and credentials are kept",
			live: `{"pattern":"*.csv","sftp_port":22,"s3_bucket":"raw-orders","connection_timeout":30,"s3_secret_key":"**********","s3_access_key":"AKIA1"}`,
		},
		{
			name: "omitted schedule is kept",
			live: `{"schedule":"@hourly"}`,
		},
		{
			name: "schedule: null is kept",
			spec: "schedule: null\n",
			live: `{"schedule":"@hourly"}`,
		},
		{
			name: `schedule: "" replaces the live schedule`,
			spec: "schedule: \"\"\n",
			live: `{"schedule":"@hourly"}`,
			want: []diffWant{
				{path: "spec.schedule", key: "schedule", before: `"@hourly"`, after: `""`, verb: "REPLACE"},
			},
		},
		{
			name: "omitted table_series_id and routing_mode of a series source are kept",
			live: `{"table_series_id":"5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90","routing_mode":"uploader_chooses"}`,
		},
		{
			name: "table_series_id: null and routing_mode: null are kept",
			spec: "table_series_id: null\nrouting_mode: null\n",
			live: `{"table_series_id":"5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90","routing_mode":"uploader_chooses"}`,
		},
		{
			name: "omitted preload_row_drop_share is kept",
			live: `{"preload_row_drop_share":0.2}`,
		},
		{
			name: "preload_row_drop_share: null is kept",
			spec: "preload_row_drop_share: null\n",
			live: `{"preload_row_drop_share":0.2}`,
		},
		{
			name: "preload_row_drop_share replaces the live share",
			spec: "preload_row_drop_share: 0.35\n",
			live: `{"preload_row_drop_share":0.2}`,
			want: []diffWant{
				{path: "spec.preload_row_drop_share", key: "preload_row_drop_share", before: `0.2`, after: `0.35`, verb: "REPLACE"},
			},
		},
		{
			name: "switching routing mode clears the omitted routing_pattern",
			spec: "routing_mode: column\nrouting_column: period\n",
			live: `{"table_series_id":"5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90","routing_mode":"file_name","routing_pattern":"{period}.csv"}`,
			want: []diffWant{
				{path: "spec.routing_column", key: "routing_column", before: diffNil, after: `"period"`, verb: "SET"},
				{path: "spec.routing_mode", key: "routing_mode", before: `"file_name"`, after: `"column"`, verb: "REPLACE"},
				{path: "spec.routing_pattern", key: "routing_pattern", before: `"{period}.csv"`, after: diffNil, verb: "CLEAR"},
			},
		},
		{
			name: "switching routing mode clears the omitted routing_column",
			spec: "routing_mode: file_name\nrouting_pattern: \"{period}.csv\"\n",
			live: `{"table_series_id":"5f0c7d1e-9a43-4b8e-8d2a-3c6f1e2b7a90","routing_mode":"column","routing_column":"period"}`,
			want: []diffWant{
				{path: "spec.routing_column", key: "routing_column", before: `"period"`, after: diffNil, verb: "CLEAR"},
				{path: "spec.routing_mode", key: "routing_mode", before: `"column"`, after: `"file_name"`, verb: "REPLACE"},
				{path: "spec.routing_pattern", key: "routing_pattern", before: diffNil, after: `"{period}.csv"`, verb: "SET"},
			},
		},
	})
}

// Verb is SET over a null or "" live value, CLEAR for a null new value, and
// REPLACE otherwise.
func TestDiffVerb(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after json.RawMessage
		want          string
	}{
		{"null to x", nil, json.RawMessage(`"x"`), "SET"},
		{`"" to x`, json.RawMessage(`""`), json.RawMessage(`"x"`), "SET"},
		{"x to null", json.RawMessage(`"x"`), nil, "CLEAR"},
		{`"" to null`, json.RawMessage(`""`), nil, "CLEAR"},
		{"x to y", json.RawMessage(`"x"`), json.RawMessage(`"y"`), "REPLACE"},
		{"false to true", json.RawMessage(`false`), json.RawMessage(`true`), "REPLACE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (FieldChange{Old: tc.before, New: tc.after}).Verb(); got != tc.want {
				t.Errorf("Verb() = %s, want %s", got, tc.want)
			}
		})
	}
}

// The diff compares JSON values, not their text: key order, whitespace and
// escaping do not count, list order and null versus empty do.
func TestDiffComparesJSONValues(t *testing.T) {
	diffRun(t, []diffCase{
		{
			name: "a map in another key order",
			spec: "settings_overrides:\n  field_name_full_conversion:\n    a: \"1\"\n    b: \"2\"\n",
			live: `{"field_name_full_conversion_override":{"b":"2","a":"1"}}`,
		},
		{
			name: "nested maps in another key order",
			spec: "settings_overrides:\n  value_mapping_per_column:\n    col:\n      x: \"1\"\n      y: \"2\"\n    other:\n      z: \"3\"\n",
			live: `{"value_mapping_per_column_override":{"other":{"z":"3"},"col":{"y":"2","x":"1"}}}`,
		},
		{
			name: "an equal list",
			spec: "settings_overrides:\n  boolean_true: [\"yes\", \"y\"]\n",
			live: `{"boolean_true_override":[ "yes", "y" ]}`,
		},
		{
			name: "an int equal to the live int",
			spec: "sftp_port: 22\n",
			live: `{"sftp_port":22}`,
		},
		{
			name: "a string Go escapes and the read does not",
			spec: "pattern: \"<a>&b\"\n",
			live: `{"pattern":"<a>&b"}`,
		},
		{
			name: "a list in another order",
			spec: "settings_overrides:\n  boolean_true: [\"y\", \"yes\"]\n",
			live: `{"boolean_true_override":["yes","y"]}`,
			want: []diffWant{
				{path: "spec.settings_overrides.boolean_true", key: "boolean_true_override", before: `["yes","y"]`, after: `["y","yes"]`, verb: "REPLACE"},
			},
		},
		{
			name: "[] over null",
			spec: "settings_overrides:\n  boolean_true: []\n",
			live: `{"boolean_true_override":null}`,
			want: []diffWant{
				{path: "spec.settings_overrides.boolean_true", key: "boolean_true_override", before: diffNil, after: `[]`, verb: "SET"},
			},
		},
		{
			name: "{} over null",
			spec: "settings_overrides:\n  value_mapping: {}\n",
			live: `{"value_mapping_override":null}`,
			want: []diffWant{
				{path: "spec.settings_overrides.value_mapping", key: "value_mapping_override", before: diffNil, after: `{}`, verb: "SET"},
			},
		},
		{
			name: "an int unlike the live int",
			spec: "sftp_port: 2222\n",
			live: `{"sftp_port":22}`,
			want: []diffWant{
				{path: "spec.sftp_port", key: "sftp_port", before: `22`, after: `2222`, verb: "REPLACE"},
			},
		},
	})
}

// A supplied credential that differs from the live value is a change marked
// Secret, and FormatChanges prints neither its value nor the live one.
func TestDiffSecretIsAChangeWithNoValueShown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spec   string
		live   string
		want   []diffWant
		hidden []string
	}{
		{
			name: "a secret over a masked live value",
			spec: "s3_secret_key: new-secret\n",
			live: `{"s3_secret_key":"**********"}`,
			want: []diffWant{
				{path: "spec.s3_secret_key", key: "s3_secret_key", before: `"**********"`, after: `"new-secret"`, verb: "REPLACE", secret: true},
			},
			hidden: []string{"new-secret", "**********"},
		},
		{
			name: "a secret the read never carries",
			spec: "dropbox_authorization_code: fresh-code\n",
			live: `{"dropbox_access_token":"**********"}`,
			want: []diffWant{
				{path: "spec.dropbox_authorization_code", key: "dropbox_authorization_code", before: diffNil, after: `"fresh-code"`, verb: "SET", secret: true},
			},
			hidden: []string{"fresh-code"},
		},
		{
			name: "s3_access_key equal to the live plaintext",
			spec: "s3_access_key: AKIA1\n",
			live: `{"s3_access_key":"AKIA1"}`,
		},
		{
			name: "s3_access_key unlike the live plaintext",
			spec: "s3_access_key: AKIA2\n",
			live: `{"s3_access_key":"AKIA1"}`,
			want: []diffWant{
				{path: "spec.s3_access_key", key: "s3_access_key", before: `"AKIA1"`, after: `"AKIA2"`, verb: "REPLACE", secret: true},
			},
			hidden: []string{"AKIA"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes := diffChanges(t, diffSpec(t, tc.spec), diffLive(t, tc.live))
			if got := diffGot(changes); !slices.Equal(got, tc.want) {
				t.Fatalf("changes =\n%+v\nwant\n%+v", got, tc.want)
			}
			out := FormatChanges(changes, "")
			for _, change := range changes {
				if want := fmt.Sprintf("%-7s  %s: (secret, not shown)\n", change.Verb(), change.Path); !strings.Contains(out, want) {
					t.Errorf("FormatChanges lacks %q:\n%s", want, out)
				}
			}
			for _, secret := range tc.hidden {
				if strings.Contains(out, secret) {
					t.Errorf("FormatChanges printed %q:\n%s", secret, out)
				}
			}
		})
	}
}

// A manifest credential that is masked, the literal read mask or a value
// containing '•', is neither a change nor sent on create.
func TestDiffMaskedSecretIsNeverSent(t *testing.T) {
	for _, tc := range []struct {
		name, spec, live, key string
	}{
		{"the read mask over an empty secret", `s3_secret_key: "**********"`, `{"s3_secret_key":""}`, "s3_secret_key"},
		{"a bullet value over a masked secret", "s3_secret_key: ab••cd", `{"s3_secret_key":"**********"}`, "s3_secret_key"},
		{"a bullet value over a plaintext access key", "s3_access_key: AK••", `{"s3_access_key":"AKIA1"}`, "s3_access_key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := diffSpec(t, tc.spec+"\n")
			if got := diffGot(diffChanges(t, spec, diffLive(t, tc.live))); len(got) > 0 {
				t.Errorf("changes = %+v, want none", got)
			}
			set, err := setFields(spec)
			if err != nil {
				t.Fatalf("setFields: %v", err)
			}
			if value, ok := set[tc.key]; ok {
				t.Errorf("setFields carries %s = %s", tc.key, value)
			}
			m := validCloudSourceManifest(DataSourceTypeS3)
			m.Spec.CloudSourceFields = spec.CloudSourceFields
			post, err := createBody(&m)
			if err != nil {
				t.Fatalf("createBody: %v", err)
			}
			if value, ok := bodyMembers(t, "POST", post)[tc.key]; ok {
				t.Errorf("create body carries %s = %s", tc.key, value)
			}
		})
	}
}

// FormatChanges prints each value's JSON unescaped, after its verb and its
// spec.<key> or spec.settings_overrides.<name> path.
func TestDiffFormatChangesPrintsPathsAndLiteralValues(t *testing.T) {
	spec := diffSpec(t, "pattern: \"<a>&b\"\n")
	live := diffLive(t, `{"pattern":"\u003cold\u003e\u0026x","value_mapping_override":{"\u003ck\u003e":"\u0026v"}}`)
	got := FormatChanges(diffChanges(t, spec, live), "  ")
	want := `  REPLACE  spec.pattern: "<old>&x" → "<a>&b"
  CLEAR    spec.settings_overrides.value_mapping: {"<k>":"&v"} → null
`
	if got != want {
		t.Errorf("FormatChanges =\n%s\nwant\n%s", got, want)
	}
}

// StaleExport reports status.version_id and the live version only when
// status names this cloud source at an older, positive version.
func TestDiffStaleExport(t *testing.T) {
	existing := &CloudSourceFull{ID: cloudSourcePtr(7), VersionID: cloudSourcePtr(5)}
	for _, tc := range []struct {
		name                   string
		existing               *CloudSourceFull
		status                 *CloudSourceStatus
		wantExported, wantLive int
		wantOK                 bool
	}{
		{"an older version of this source", existing, &CloudSourceStatus{ID: 7, VersionID: 3}, 3, 5, true},
		{"the live version", existing, &CloudSourceStatus{ID: 7, VersionID: 5}, 0, 0, false},
		{"a newer version than the live one", existing, &CloudSourceStatus{ID: 7, VersionID: 6}, 0, 0, false},
		{"another source's id", existing, &CloudSourceStatus{ID: 8, VersionID: 3}, 0, 0, false},
		{"no status", existing, nil, 0, 0, false},
		{"version_id 0", existing, &CloudSourceStatus{ID: 7}, 0, 0, false},
		{"a create", nil, &CloudSourceStatus{ID: 7, VersionID: 3}, 0, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &ApplyPlan{manifest: &CloudSourceManifest{Status: tc.status}, existing: tc.existing}
			exported, live, ok := plan.StaleExport()
			if ok != tc.wantOK || (ok && (exported != tc.wantExported || live != tc.wantLive)) {
				t.Errorf("StaleExport() = (%d, %d, %v), want (%d, %d, %v)", exported, live, ok, tc.wantExported, tc.wantLive, tc.wantOK)
			}
		})
	}
}

func TestDiffSideEffects(t *testing.T) {
	const learn, sniff = "learn its schema", "cant_sniff_header"
	override := FieldChange{Key: "infer_datetime_override"}
	ignoreIDs := FieldChange{Key: "ignore_column_ids"}
	nonBoundary := FieldChange{Key: "date_formats_override"}
	csv := FieldChange{Key: "csv_delimiter", New: json.RawMessage(`"|"`)}
	csv2 := FieldChange{Key: "csv_quotechar", Old: json.RawMessage(`"'"`)}
	other := FieldChange{Key: "pattern"}
	for _, tc := range []struct {
		name    string
		changes []FieldChange
		want    []string
	}{
		{"a boundary override", []FieldChange{override}, []string{learn}},
		{"ignore_column_ids", []FieldChange{ignoreIDs}, []string{learn}},
		{"two boundary keys", []FieldChange{override, ignoreIDs}, []string{learn}},
		{"a non-boundary override", []FieldChange{nonBoundary, {Key: "boolean_true_override"}}, nil},
		{"a csv field", []FieldChange{csv}, []string{sniff}},
		{"two csv fields", []FieldChange{csv, csv2, {Key: "csv_escapechar", Old: json.RawMessage(`"\\"`)}}, []string{sniff}},
		{"a csv field from \"\" to null", []FieldChange{{Key: "csv_delimiter", Old: json.RawMessage(`""`)}}, nil},
		{"a csv field from null to \"\"", []FieldChange{{Key: "csv_escapechar", New: json.RawMessage(`""`)}}, nil},
		{"a csv field from \"\" to a value", []FieldChange{{Key: "csv_quotechar", Old: json.RawMessage(`""`), New: json.RawMessage(`"'"`)}}, []string{sniff}},
		{"a boundary key and a csv field", []FieldChange{other, nonBoundary, csv, override}, []string{learn, sniff}},
		{"neither", []FieldChange{other, nonBoundary, {Key: "schedule"}, {Key: "s3_secret_key"}}, nil},
		{"no changes", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := (&ApplyPlan{changes: tc.changes}).SideEffects()
			if len(got) != len(tc.want) {
				t.Fatalf("SideEffects() = %q, want %d notes about %q", got, len(tc.want), tc.want)
			}
			for _, about := range tc.want {
				n := 0
				for _, note := range got {
					if strings.Contains(note, about) {
						n++
					}
				}
				if n != 1 {
					t.Errorf("SideEffects() = %q has %d notes about %q, want 1", got, n, about)
				}
			}
		})
	}

	// Each boundary key alone announces the re-learn, and is a key an update
	// can send.
	for _, key := range []string{
		"fields_to_expand_override", "infer_datetime_override", "infer_datetime_for_columns_override",
		"ignore_column_ids", "ignore_column_names_override",
	} {
		got := (&ApplyPlan{changes: []FieldChange{{Key: key}}}).SideEffects()
		if len(got) != 1 || !strings.Contains(got[0], learn) {
			t.Errorf("SideEffects() for %s = %q, want one note about %q", key, got, learn)
		}
		name, isOverride := strings.CutSuffix(key, overrideSuffix)
		if !slices.Contains(fieldKeys, key) && (!isOverride || !slices.Contains(overrideNames, name)) {
			t.Errorf("%s is neither a CloudSourceFields key nor a settings override", key)
		}
	}
}

// The top-level keys an update clears when the manifest leaves them out.
func TestDiffClearedWhenOmittedKeys(t *testing.T) {
	want := []string{
		"broker_due_days", "csv_delimiter", "csv_escapechar", "csv_quotechar", "end_period_key",
		"routing_column", "routing_pattern", "start_period_key",
	}
	if got := ClearedWhenOmittedKeys(); !slices.Equal(got, want) {
		t.Errorf("ClearedWhenOmittedKeys() = %v, want %v.\n"+
			"A client regen changed which fields api.DataSourceModelFullSchema declares nullable (a pointer without omitempty). "+
			"Before accepting a new key, check its column in atlas cettings/cettings/models/data_source_model.py: "+
			"the schema's nullability is not the column's (schedule is nullable in the schema and NOT NULL in the column; sftp_port is the reverse). "+
			"A key whose column is NOT NULL, or whose null the server refuses (table_series_id, routing_mode), is kept when omitted, not cleared, and needs an entry in keptWhenOmitted.",
			got, want)
	}
}
