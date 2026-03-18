package schema

import (
	"os"
	"testing"
)

// skipIfNoAPI skips the test if QCTL_TEST_API_URL is not set.
// These tests require a live API to fetch the OpenAPI spec.
func skipIfNoAPI(t *testing.T) {
	t.Helper()
	apiURL := os.Getenv("QCTL_TEST_API_URL")
	if apiURL == "" {
		t.Skip("Skipping test: QCTL_TEST_API_URL not set (requires live API)")
	}
	SetAPIEndpoint(apiURL)
}

func TestGetSchema(t *testing.T) {
	skipIfNoAPI(t)

	tests := []struct {
		name       string
		schemaName string
		wantErr    bool
		wantFields []string // Some expected fields to verify
	}{
		{
			name:       "DataSetSchemaFull",
			schemaName: "DataSetSchemaFull",
			wantErr:    false,
			wantFields: []string{"id", "name", "destination_id", "database_name", "migration_policy"},
		},
		{
			name:       "DataSetPostRequestSchema",
			schemaName: "DataSetPostRequestSchema",
			wantErr:    false,
			wantFields: []string{"name", "destination_id", "database_name"},
		},
		{
			name:       "DataSetCreateQuickPostRequest",
			schemaName: "DataSetCreateQuickPostRequest",
			wantErr:    false,
			wantFields: []string{"name", "destination_id", "database_name", "table_name"},
		},
		{
			name:       "unknown schema",
			schemaName: "NonExistentSchema",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := GetSchema(tt.schemaName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetSchema() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if schema == nil {
				t.Fatal("GetSchema() returned nil schema")
			}

			if schema.Name != tt.schemaName {
				t.Errorf("GetSchema() name = %v, want %v", schema.Name, tt.schemaName)
			}

			// Check that expected fields exist
			fieldMap := make(map[string]bool)
			for _, f := range schema.Fields {
				fieldMap[f.Name] = true
			}

			for _, expectedField := range tt.wantFields {
				if !fieldMap[expectedField] {
					t.Errorf("GetSchema() missing expected field %q", expectedField)
				}
			}
		})
	}
}

func TestGetFieldProps(t *testing.T) {
	skipIfNoAPI(t)

	tests := []struct {
		name       string
		schemaName string
		fieldPath  string
		wantType   string
		wantEnum   []string
		wantErr    bool
	}{
		{
			name:       "simple field",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "name",
			wantType:   "string",
			wantErr:    false,
		},
		{
			name:       "integer field",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "id",
			wantType:   "integer",
			wantErr:    false,
		},
		{
			name:       "enum field via ref",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "migration_policy",
			wantType:   "string",
			wantEnum:   []string{"ask_user", "apply_asap", "migration_window", "locked"},
			wantErr:    false,
		},
		{
			name:       "non-existent field",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "non_existent_field",
			wantErr:    true,
		},
		{
			name:       "non-existent schema",
			schemaName: "NonExistentSchema",
			fieldPath:  "name",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			field, err := GetFieldProps(tt.schemaName, tt.fieldPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetFieldProps() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if field == nil {
				t.Fatal("GetFieldProps() returned nil field")
			}

			if field.Type != tt.wantType {
				t.Errorf("GetFieldProps() type = %v, want %v", field.Type, tt.wantType)
			}

			if len(tt.wantEnum) > 0 {
				if len(field.Enum) != len(tt.wantEnum) {
					t.Errorf("GetFieldProps() enum length = %v, want %v", len(field.Enum), len(tt.wantEnum))
				}
				for i, v := range tt.wantEnum {
					if i < len(field.Enum) && field.Enum[i] != v {
						t.Errorf("GetFieldProps() enum[%d] = %v, want %v", i, field.Enum[i], v)
					}
				}
			}
		})
	}
}

func TestGetEnumValues(t *testing.T) {
	skipIfNoAPI(t)

	tests := []struct {
		name       string
		schemaName string
		fieldPath  string
		wantValues []string
		wantErr    bool
	}{
		{
			name:       "migration_policy enum",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "migration_policy",
			wantValues: []string{"ask_user", "apply_asap", "migration_window", "locked"},
			wantErr:    false,
		},
		{
			name:       "non-enum field",
			schemaName: "DataSetSchemaFull",
			fieldPath:  "name",
			wantValues: nil,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, err := GetEnumValues(tt.schemaName, tt.fieldPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetEnumValues() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if len(values) != len(tt.wantValues) {
				t.Errorf("GetEnumValues() length = %v, want %v", len(values), len(tt.wantValues))
				return
			}

			for i, v := range tt.wantValues {
				if i < len(values) && values[i] != v {
					t.Errorf("GetEnumValues() [%d] = %v, want %v", i, values[i], v)
				}
			}
		})
	}
}

func TestListSchemas(t *testing.T) {
	skipIfNoAPI(t)

	schemas, err := ListSchemas()
	if err != nil {
		t.Fatalf("ListSchemas() error = %v", err)
	}

	if len(schemas) == 0 {
		t.Error("ListSchemas() returned empty list")
	}

	// Check for some expected schemas
	schemaSet := make(map[string]bool)
	for _, s := range schemas {
		schemaSet[s] = true
	}

	expectedSchemas := []string{"DataSetSchemaFull", "DataSetPostRequestSchema", "DataSetCreateQuickPostRequest"}
	for _, expected := range expectedSchemas {
		if !schemaSet[expected] {
			t.Errorf("ListSchemas() missing expected schema %q", expected)
		}
	}
}

func TestGetSchemaForResource(t *testing.T) {
	skipIfNoAPI(t)

	tests := []struct {
		name     string
		resource string
		wantName string
		wantErr  bool
	}{
		{
			name:     "table resource",
			resource: "table",
			wantName: "DataSetSchemaFull",
			wantErr:  false,
		},
		{
			name:     "dataset alias",
			resource: "dataset",
			wantName: "DataSetSchemaFull",
			wantErr:  false,
		},
		{
			name:     "unknown resource",
			resource: "unknown",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := GetSchemaForResource(tt.resource)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetSchemaForResource() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if schema.Name != tt.wantName {
				t.Errorf("GetSchemaForResource() name = %v, want %v", schema.Name, tt.wantName)
			}
		})
	}
}

func TestCompareSchemas(t *testing.T) {
	skipIfNoAPI(t)

	// Compare DataSetSchemaFull with DataSetPostRequestSchema
	// Fields in Full but not in Post should be read-only
	readOnlyFields, err := CompareSchemas("DataSetSchemaFull", "DataSetPostRequestSchema")
	if err != nil {
		t.Fatalf("CompareSchemas() error = %v", err)
	}

	// Expected read-only fields (these should be in Full but not in Post)
	expectedReadOnly := map[string]bool{
		"id":                   true,
		"version_id":           true,
		"organization_id":      true,
		"state":                true,
		"destination_name":     true,
		"destinations_list":    true,
		"backup_settings_list": true,
		"settings_model":       true,
	}

	for _, field := range readOnlyFields {
		if expectedReadOnly[field] {
			delete(expectedReadOnly, field)
		}
	}

	// Check that we found at least some of the expected read-only fields
	if len(expectedReadOnly) > 4 { // Allow some flexibility as schemas may change
		t.Errorf("CompareSchemas() missing many expected read-only fields: %v", expectedReadOnly)
	}
}

func TestGetDatasetReadOnlyFields(t *testing.T) {
	skipIfNoAPI(t)

	fields, err := GetDatasetReadOnlyFields()
	if err != nil {
		t.Fatalf("GetDatasetReadOnlyFields() error = %v", err)
	}

	if len(fields) == 0 {
		t.Error("GetDatasetReadOnlyFields() returned empty list")
	}

	// Verify id is in the read-only list
	found := false
	for _, f := range fields {
		if f == "id" {
			found = true
			break
		}
	}
	if !found {
		t.Error("GetDatasetReadOnlyFields() should include 'id'")
	}
}
