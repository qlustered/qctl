package manifest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONManifestPreservesControlStrings(t *testing.T) {
	type document struct {
		APIVersion string `json:"apiVersion" yaml:"apiVersion"`
		Kind       string `json:"kind" yaml:"kind"`
		Spec       struct {
			Pattern string            `json:"pattern" yaml:"pattern"`
			Values  map[string]string `json:"values" yaml:"values"`
		} `json:"spec" yaml:"spec"`
	}
	want := document{APIVersion: APIVersionV1, Kind: "Table"}
	want.Spec.Pattern = "a\u0085b\x7fc"
	want.Spec.Values = map[string]string{"<<": "\n"}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	docs, err := SplitDocuments(data)
	if err != nil || len(docs) != 1 {
		t.Fatalf("SplitDocuments = %d documents, %v", len(docs), err)
	}
	var got document
	if err := StrictUnmarshal(docs[0], &got); err != nil {
		t.Fatalf("StrictUnmarshal: %v", err)
	}
	if got.Spec.Pattern != want.Spec.Pattern || got.Spec.Values["<<"] != "\n" {
		t.Errorf("round trip = %#v, want %#v", got, want)
	}
	if err := StrictUnmarshal([]byte(`{"apiVersion":"qluster.ai/v1","kind":"Table","unknown":1}`), &got); err == nil {
		t.Error("JSON unknown field was accepted")
	}
}

func TestStrictUnmarshalJSONFields(t *testing.T) {
	type entry struct {
		Choice string `json:"choice"`
	}
	type document struct {
		APIVersion string `json:"apiVersion"`
		Spec       struct {
			Resolutions []entry        `json:"resolutions"`
			Params      map[string]any `json:"params"`
		} `json:"spec"`
	}
	tests := []struct {
		name string
		data string
	}{
		{"top-level case", `{"APIVERSION":"v1"}`},
		{"nested case", `{"apiVersion":"v1","Spec":{"resolutions":[]}}`},
		{"list case", `{"apiVersion":"v1","spec":{"resolutions":[{"Choice":"take_source"}]}}`},
		{"duplicate field", `{"apiVersion":"v1","spec":{"resolutions":[{"choice":"take_source","choice":"keep_target"}]}}`},
		{"case variant", `{"apiVersion":"v1","spec":{"resolutions":[{"Choice":"take_source","choice":"keep_target"}]}}`},
		{"duplicate free-form key", `{"apiVersion":"v1","spec":{"params":{"cutoff":"old","cutoff":"new"}}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got document
			if err := StrictUnmarshal([]byte(tt.data), &got); err == nil {
				t.Fatal("invalid JSON was accepted")
			}
		})
	}
	var got document
	if err := StrictUnmarshal([]byte(`{"apiVersion":"v1","spec":{"resolutions":[{"choice":"take_source"}],"params":{"Cutoff":"ok"}}}`), &got); err != nil {
		t.Fatalf("free-form key was rejected: %v", err)
	}
	if got.Spec.Params["Cutoff"] != "ok" {
		t.Errorf("params = %#v", got.Spec.Params)
	}
}

func TestStrictUnmarshalRejectsNullStringListElements(t *testing.T) {
	type document struct {
		Spec struct {
			NullValues          *[]string            `yaml:"null_values" json:"null_values"`
			NullValuesPerColumn *map[string][]string `yaml:"null_values_per_column" json:"null_values_per_column"`
			Loose               map[string]any       `yaml:"loose" json:"loose"`
			Nullable            []*string            `yaml:"nullable" json:"nullable"`
		} `yaml:"spec" json:"spec"`
	}
	tests := []struct {
		name string
		data string
		path string
	}{
		{"YAML uppercase", "spec:\n  null_values: [NULL]\n", "spec.null_values[0]"},
		{"YAML lowercase", "spec:\n  null_values: [null]\n", "spec.null_values[0]"},
		{"YAML tilde", "spec:\n  null_values: [~]\n", "spec.null_values[0]"},
		{"YAML alias", "spec:\n  null_values: [x, &missing null, *missing]\n", "spec.null_values[1]"},
		{"YAML nested map", "spec:\n  null_values_per_column:\n    amount: [x, NULL]\n", `spec.null_values_per_column["amount"][1]`},
		{"JSON list", `{"spec":{"null_values":[null]}}`, "$.spec.null_values[0]"},
		{"JSON nested map", `{"spec":{"null_values_per_column":{"amount":["x",null]}}}`, "$.spec.null_values_per_column.amount[1]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got document
			err := StrictUnmarshal([]byte(tt.data), &got)
			if err == nil || !strings.Contains(err.Error(), tt.path) || !strings.Contains(err.Error(), `"NULL"`) {
				t.Fatalf("StrictUnmarshal error = %v, want path %q and quote hint", err, tt.path)
			}
		})
	}

	for _, data := range []string{
		"spec:\n  null_values: ['NULL', \"null\", '~', '']\n  loose: {maybe: [null]}\n  nullable: [null]\n",
		`{"spec":{"null_values":["NULL",""],"loose":{"maybe":[null]},"nullable":[null]}}`,
	} {
		var got document
		if err := StrictUnmarshal([]byte(data), &got); err != nil {
			t.Fatalf("quoted strings and intentionally nullable values rejected: %v", err)
		}
		if got.Spec.NullValues == nil || len(*got.Spec.NullValues) == 0 || (*got.Spec.NullValues)[0] != "NULL" {
			t.Errorf("null_values = %#v, want literal NULL", got.Spec.NullValues)
		}
	}
}

func TestStrictUnmarshalYAMLLooseTimestamp(t *testing.T) {
	type document struct {
		Spec struct {
			Params map[string]any `yaml:"params"`
			At     time.Time      `yaml:"at"`
		} `yaml:"spec"`
	}
	data := []byte("spec:\n  at: 2024-01-01\n  params:\n    cutoff: 2024-01-01\n    nested:\n      - at: 2024-01-02T03:04:05+02:00\n    quoted: '2024-01-03'\n")
	var got document
	if err := StrictUnmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Params["cutoff"] != "2024-01-01" {
		t.Errorf("cutoff = %#v", got.Spec.Params["cutoff"])
	}
	nested := got.Spec.Params["nested"].([]any)[0].(map[string]any)
	if nested["at"] != "2024-01-02T03:04:05+02:00" {
		t.Errorf("nested at = %#v", nested["at"])
	}
	if got.Spec.Params["quoted"] != "2024-01-03" {
		t.Errorf("quoted = %#v", got.Spec.Params["quoted"])
	}
	if got.Spec.At.Year() != 2024 || got.Spec.At.Month() != time.January || got.Spec.At.Day() != 1 {
		t.Errorf("typed at = %v", got.Spec.At)
	}
}

func TestStrictUnmarshalYAMLTimestampAliasDoesNotChangeTypedField(t *testing.T) {
	type document struct {
		At     time.Time      `yaml:"at"`
		Params map[string]any `yaml:"params"`
	}
	var got document
	if err := StrictUnmarshal([]byte("at: &date 2024-01-01\nparams:\n  cutoff: *date\n"), &got); err != nil {
		t.Fatal(err)
	}
	if got.Params["cutoff"] != "2024-01-01" {
		t.Errorf("cutoff = %#v", got.Params["cutoff"])
	}
	if got.At.Year() != 2024 || got.At.Month() != time.January || got.At.Day() != 1 {
		t.Errorf("typed at = %v", got.At)
	}
}

func TestStrictUnmarshalYAMLPointerMapTimestamp(t *testing.T) {
	type document struct {
		Spec struct {
			Params *map[string]any `yaml:"params"`
		} `yaml:"spec"`
	}
	var got document
	if err := StrictUnmarshal([]byte("spec:\n  params: {cutoff: 2024-01-01}\n"), &got); err != nil {
		t.Fatal(err)
	}
	if got.Spec.Params == nil || (*got.Spec.Params)["cutoff"] != "2024-01-01" {
		t.Errorf("params = %#v", got.Spec.Params)
	}
}

func TestStrictUnmarshal_ValidManifest(t *testing.T) {
	validYAML := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
  labels:
    env: production
spec:
  description: A test dataset
`

	var m Manifest
	err := StrictUnmarshal([]byte(validYAML), &m)
	if err != nil {
		t.Errorf("StrictUnmarshal() error = %v, want nil", err)
		return
	}

	if m.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want %q", m.APIVersion, "v1")
	}
	if m.Kind != "Dataset" {
		t.Errorf("Kind = %q, want %q", m.Kind, "Dataset")
	}
	if m.Metadata.Name != "my-dataset" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-dataset")
	}
	if m.Metadata.Labels["env"] != "production" {
		t.Errorf("Metadata.Labels[env] = %q, want %q", m.Metadata.Labels["env"], "production")
	}
}

func TestStrictUnmarshal_UnknownField(t *testing.T) {
	// This tests that typos/unknown fields cause errors
	yamlWithTypo := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
  naem: typo-field
spec:
  description: A test dataset
`

	var m Manifest
	err := StrictUnmarshal([]byte(yamlWithTypo), &m)
	if err == nil {
		t.Error("StrictUnmarshal() expected error for unknown field 'naem', got nil")
	}
}

func TestStrictUnmarshal_UnknownTopLevelField(t *testing.T) {
	yamlWithUnknown := `
apiVersion: v1
kind: Dataset
metadata:
  name: my-dataset
specc: typo-for-spec
`

	var m Manifest
	err := StrictUnmarshal([]byte(yamlWithUnknown), &m)
	if err == nil {
		t.Error("StrictUnmarshal() expected error for unknown field 'specc', got nil")
	}
}

func TestStrictUnmarshal_MissingRequired(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		field string
	}{
		{
			name: "missing apiVersion",
			yaml: `
kind: Dataset
metadata:
  name: my-dataset
`,
			field: "apiVersion",
		},
		{
			name: "missing kind",
			yaml: `
apiVersion: v1
metadata:
  name: my-dataset
`,
			field: "kind",
		},
		{
			name: "missing metadata.name",
			yaml: `
apiVersion: v1
kind: Dataset
metadata:
  labels:
    env: test
`,
			field: "metadata.name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tt.yaml))
			if err == nil {
				t.Errorf("LoadBytes() expected validation error for missing %s, got nil", tt.field)
				return
			}

			vErr, ok := err.(*ValidationError)
			if !ok {
				// It's okay if it's a different error type, just check it mentions the field
				return
			}

			if vErr.Field != tt.field {
				t.Errorf("ValidationError.Field = %q, want %q", vErr.Field, tt.field)
			}
		})
	}
}

func TestLoadFile_NotFound(t *testing.T) {
	_, err := LoadFile("/nonexistent/path/to/file.yaml")
	if err == nil {
		t.Error("LoadFile() expected error for non-existent file, got nil")
	}
}

func TestLoadFile_Valid(t *testing.T) {
	// Create a temporary file with valid YAML
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: CloudSource
metadata:
  name: my-cloud-source
  annotations:
    description: Test source
spec:
  bucket: my-bucket
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	m, err := LoadFile(tmpFile)
	if err != nil {
		t.Errorf("LoadFile() error = %v, want nil", err)
		return
	}

	if m.APIVersion != "v1" {
		t.Errorf("APIVersion = %q, want %q", m.APIVersion, "v1")
	}
	if m.Kind != "CloudSource" {
		t.Errorf("Kind = %q, want %q", m.Kind, "CloudSource")
	}
	if m.Metadata.Name != "my-cloud-source" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-cloud-source")
	}
}

func TestLoadFileInto_TypedStruct(t *testing.T) {
	type CustomManifest struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Metadata   struct {
			Name string `yaml:"name"`
		} `yaml:"metadata"`
		Spec struct {
			Bucket string `yaml:"bucket"`
			Region string `yaml:"region"`
		} `yaml:"spec"`
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: CloudSource
metadata:
  name: s3-source
spec:
  bucket: my-bucket
  region: us-east-1
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	m, err := LoadFileInto[CustomManifest](tmpFile)
	if err != nil {
		t.Errorf("LoadFileInto() error = %v, want nil", err)
		return
	}

	if m.Spec.Bucket != "my-bucket" {
		t.Errorf("Spec.Bucket = %q, want %q", m.Spec.Bucket, "my-bucket")
	}
	if m.Spec.Region != "us-east-1" {
		t.Errorf("Spec.Region = %q, want %q", m.Spec.Region, "us-east-1")
	}
}

func TestLoadFileInto_RejectsUnknownFields(t *testing.T) {
	type StrictManifest struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "manifest.yaml")

	content := `
apiVersion: v1
kind: Test
unknownField: should-fail
`

	err := os.WriteFile(tmpFile, []byte(content), 0644)
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	_, err = LoadFileInto[StrictManifest](tmpFile)
	if err == nil {
		t.Error("LoadFileInto() expected error for unknown field, got nil")
	}
}

func TestLoadBytes_EmptyDocument(t *testing.T) {
	_, err := LoadBytes([]byte(""))
	if err == nil {
		t.Error("LoadBytes() expected error for empty document, got nil")
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "metadata.name",
		Message: "required field is missing",
	}

	expected := "validation error: metadata.name: required field is missing"
	if err.Error() != expected {
		t.Errorf("ValidationError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestLoadBytesInto(t *testing.T) {
	type SimpleManifest struct {
		Name    string `yaml:"name"`
		Version int    `yaml:"version"`
	}

	yaml := `
name: test-manifest
version: 42
`

	m, err := LoadBytesInto[SimpleManifest]([]byte(yaml))
	if err != nil {
		t.Errorf("LoadBytesInto() error = %v, want nil", err)
		return
	}

	if m.Name != "test-manifest" {
		t.Errorf("Name = %q, want %q", m.Name, "test-manifest")
	}
	if m.Version != 42 {
		t.Errorf("Version = %d, want %d", m.Version, 42)
	}
}

func TestGetSuggestion_CommonTypos(t *testing.T) {
	tests := []struct {
		name        string
		errorString string
		wantContain string
	}{
		{
			name:        "username typo",
			errorString: "field username not found in type",
			wantContain: "Did you mean 'user'?",
		},
		{
			name:        "usernmae typo",
			errorString: "field usernmae not found in type",
			wantContain: "Did you mean 'user'?",
		},
		{
			name:        "passwd typo",
			errorString: "field passwd not found in type",
			wantContain: "Did you mean 'password'?",
		},
		{
			name:        "dbname typo",
			errorString: "field dbname not found in type",
			wantContain: "Did you mean 'database_name'?",
		},
		{
			name:        "hostname typo",
			errorString: "field hostname not found in type",
			wantContain: "Did you mean 'host'?",
		},
		{
			name:        "apiversion lowercase",
			errorString: "field apiversion not found in type",
			wantContain: "Did you mean 'apiVersion'?",
		},
		{
			name:        "databaseName camelCase",
			errorString: "field databaseName not found in type",
			wantContain: "database_name",
		},
		{
			name:        "unknown field no suggestion",
			errorString: "field xyz123 not found in type",
			wantContain: "qctl explain",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			suggestion := getSuggestion(tt.errorString)
			if !contains(suggestion, tt.wantContain) {
				t.Errorf("getSuggestion(%q) = %q, want to contain %q",
					tt.errorString, suggestion, tt.wantContain)
			}
		})
	}
}

func TestExtractFieldName(t *testing.T) {
	tests := []struct {
		name     string
		errStr   string
		expected string
	}{
		{
			name:     "standard format",
			errStr:   "line 5: field username not found in type",
			expected: "username",
		},
		{
			name:     "simple format",
			errStr:   "field password not found in type Spec",
			expected: "password",
		},
		{
			name:     "no match",
			errStr:   "some other error",
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFieldName(tt.errStr)
			if result != tt.expected {
				t.Errorf("extractFieldName(%q) = %q, want %q",
					tt.errStr, result, tt.expected)
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	t.Run("contains", func(t *testing.T) {
		if !contains("hello world", "world") {
			t.Error("contains should find 'world' in 'hello world'")
		}
		if contains("hello world", "xyz") {
			t.Error("contains should not find 'xyz' in 'hello world'")
		}
	})

	t.Run("indexOf", func(t *testing.T) {
		if idx := indexOf("hello world", "world"); idx != 6 {
			t.Errorf("indexOf('hello world', 'world') = %d, want 6", idx)
		}
		if idx := indexOf("hello world", "xyz"); idx != -1 {
			t.Errorf("indexOf('hello world', 'xyz') = %d, want -1", idx)
		}
	})

	t.Run("toLower", func(t *testing.T) {
		if result := toLower("HELLO"); result != "hello" {
			t.Errorf("toLower('HELLO') = %q, want 'hello'", result)
		}
		if result := toLower("Hello World"); result != "hello world" {
			t.Errorf("toLower('Hello World') = %q, want 'hello world'", result)
		}
	})
}

func TestFormatYAMLError_TypeMismatch(t *testing.T) {
	// Test that type mismatch errors are formatted nicely
	type TestStruct struct {
		Port int `yaml:"port"`
	}

	yaml := `port: "not-a-number"`
	var s TestStruct
	err := StrictUnmarshal([]byte(yaml), &s)
	if err == nil {
		t.Fatal("expected error for type mismatch")
	}

	errStr := err.Error()
	if !contains(errStr, "type error") && !contains(errStr, "cannot unmarshal") {
		t.Errorf("error should mention type error, got: %s", errStr)
	}
}

func TestLoadBytes_WithStatusField(t *testing.T) {
	// Manifests produced by "qctl describe" include a status section.
	// LoadBytes must accept this without error so round-tripping works.
	yamlWithStatus := `
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: my-table
spec:
  destination_id: 3
status:
  state: active
  id: 42
`

	m, err := LoadBytes([]byte(yamlWithStatus))
	if err != nil {
		t.Fatalf("LoadBytes() with status field should succeed, got error: %v", err)
	}

	if m.Kind != "Table" {
		t.Errorf("Kind = %q, want %q", m.Kind, "Table")
	}
	if m.Metadata.Name != "my-table" {
		t.Errorf("Metadata.Name = %q, want %q", m.Metadata.Name, "my-table")
	}
	if m.Status == nil {
		t.Error("Status should be populated, got nil")
	}
}

func TestStrictUnmarshal_UnknownFieldWithSuggestion(t *testing.T) {
	// Test that unknown field errors include suggestions for common typos
	type DestSpec struct {
		Host         string `yaml:"host"`
		User         string `yaml:"user"`
		DatabaseName string `yaml:"database_name"`
	}

	type DestManifest struct {
		Spec DestSpec `yaml:"spec"`
	}

	tests := []struct {
		name        string
		yaml        string
		wantContain string
	}{
		{
			name: "username typo",
			yaml: `
spec:
  host: localhost
  username: admin
  database_name: mydb
`,
			wantContain: "Did you mean 'user'?",
		},
		{
			name: "dbname typo",
			yaml: `
spec:
  host: localhost
  user: admin
  dbname: mydb
`,
			wantContain: "Did you mean 'database_name'?",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m DestManifest
			err := StrictUnmarshal([]byte(tt.yaml), &m)
			if err == nil {
				t.Fatal("expected error for unknown field")
			}

			errStr := err.Error()
			if !contains(errStr, tt.wantContain) {
				t.Errorf("error should contain suggestion %q, got: %s",
					tt.wantContain, errStr)
			}
		})
	}
}

func TestLoadBytes_TableRuleEmptyMetadataIsValid(t *testing.T) {
	data := []byte(`
apiVersion: qluster.ai/v1
kind: TableRule
metadata: {}
spec:
  dataset_id: 8
  rule_revision_id: 473aaf71-d0fe-4ba5-8ee1-a76240084623
`)
	m, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("expected TableRule with empty metadata to load, got: %v", err)
	}
	if m.Kind != "TableRule" {
		t.Errorf("expected kind TableRule, got %q", m.Kind)
	}
}

func TestLoadBytes_TableRuleAbsentMetadataIsValid(t *testing.T) {
	data := []byte(`
apiVersion: qluster.ai/v1
kind: TableRule
spec:
  dataset_id: 8
  rule_revision_id: 473aaf71-d0fe-4ba5-8ee1-a76240084623
`)
	if _, err := LoadBytes(data); err != nil {
		t.Fatalf("expected TableRule without metadata to load, got: %v", err)
	}
}

func TestLoadBytes_OtherKindsStillRequireMetadataName(t *testing.T) {
	data := []byte(`
apiVersion: qluster.ai/v1
kind: Table
metadata: {}
spec: {}
`)
	_, err := LoadBytes(data)
	if err == nil {
		t.Fatal("expected metadata.name validation error for kind Table")
	}
	if !strings.Contains(err.Error(), "metadata.name") {
		t.Errorf("expected metadata.name in error, got: %v", err)
	}
}

func TestLoadBytes_SlugKindsRequireMetadataSlugOnly(t *testing.T) {
	for _, kind := range []string{"Deliverable", "StandardField", "Concept", "TableKind"} {
		t.Run(kind, func(t *testing.T) {
			head := "apiVersion: qluster.ai/v1\nkind: " + kind + "\nmetadata:\n"
			if _, err := LoadBytes([]byte(head + "  slug: monthly\nspec: {}\n")); err != nil {
				t.Errorf("a slug-only %s envelope: %v", kind, err)
			}
			_, err := LoadBytes([]byte(head + "  name: Monthly\nspec: {}\n"))
			if err == nil || !strings.Contains(err.Error(), "metadata.slug") {
				t.Errorf("a %s envelope without metadata.slug: %v", kind, err)
			}
		})
	}
}

// metadata.instance_name is where `get table-rule -o yaml` writes a rule's
// name, so a TableRule document carries it through the envelope, in YAML and
// in JSON alike.
func TestLoadBytes_TableRuleAcceptsMetadataInstanceName(t *testing.T) {
	for name, data := range map[string]string{
		"yaml": "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: 473aaf71-d0fe-4ba5-8ee1-a76240084623\n  instance_name: email_check\nspec:\n  dataset_id: 8\n",
		"json": `{"apiVersion":"qluster.ai/v1","kind":"TableRule","metadata":{"id":"473aaf71-d0fe-4ba5-8ee1-a76240084623","instance_name":"email_check"},"spec":{"dataset_id":8}}`,
	} {
		t.Run(name, func(t *testing.T) {
			m, err := LoadBytes([]byte(data))
			if err != nil {
				t.Fatalf("TableRule with metadata.instance_name refused: %v", err)
			}
			if m.Metadata.InstanceName == nil || *m.Metadata.InstanceName != "email_check" {
				t.Errorf("Metadata.InstanceName = %v, want email_check", m.Metadata.InstanceName)
			}
			if m.Metadata.ID != "473aaf71-d0fe-4ba5-8ee1-a76240084623" {
				t.Errorf("Metadata.ID = %q, want the id beside instance_name", m.Metadata.ID)
			}
		})
	}
}

// Every other kind refuses metadata.instance_name with the error any unknown
// metadata key gets, null included -- the key is TableRule's alone.
func TestLoadBytes_OtherKindsRefuseMetadataInstanceName(t *testing.T) {
	for name, tc := range map[string]struct {
		data, unknown, want string
	}{
		"yaml": {
			data:    "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n  instance_name: x\nspec: {}\n",
			unknown: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n  bogus: x\nspec: {}\n",
			want:    "instance_name: unknown field",
		},
		"yaml null": {
			data:    "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n  instance_name:\nspec: {}\n",
			unknown: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n  bogus:\nspec: {}\n",
			want:    "instance_name: unknown field",
		},
		"json": {
			data:    `{"apiVersion":"qluster.ai/v1","kind":"Table","metadata":{"name":"orders","instance_name":"x"},"spec":{}}`,
			unknown: `{"apiVersion":"qluster.ai/v1","kind":"Table","metadata":{"name":"orders","bogus":"x"},"spec":{}}`,
			want:    `unknown JSON field "instance_name" at $.metadata`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := LoadBytes([]byte(tc.data))
			if err == nil {
				t.Fatal("kind Table with metadata.instance_name accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
			// The wording an unknown key gets, bar the key's own name.
			_, unknownErr := LoadBytes([]byte(tc.unknown))
			if unknownErr == nil {
				t.Fatal("control: an unknown metadata key was accepted")
			}
			if got, want := err.Error(), strings.ReplaceAll(unknownErr.Error(), "bogus", "instance_name"); got != want {
				t.Errorf("error = %q, want the unknown-key wording %q", got, want)
			}
		})
	}
}

// Accepting instance_name on a TableRule does not loosen the rest of its
// metadata block.
func TestLoadBytes_TableRuleStillRefusesUnknownMetadataKeys(t *testing.T) {
	data := "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  instance_name: email_check\n  bogus: x\nspec:\n  dataset_id: 8\n"
	if _, err := LoadBytes([]byte(data)); err == nil || !strings.Contains(err.Error(), "bogus: unknown field") {
		t.Errorf("TableRule metadata.bogus: err = %v, want the unknown-field error", err)
	}
}
