package destinations

import (
	"testing"
)

func TestAPIVersionV1_Constant(t *testing.T) {
	if APIVersionV1 != "qluster.ai/v1" {
		t.Errorf("APIVersionV1 = %q, want %q", APIVersionV1, "qluster.ai/v1")
	}
}

func TestDestinationType_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		destType string
		want     bool
	}{
		{
			name:     "valid postgresql type",
			destType: "postgresql",
			want:     true,
		},
		{
			name:     "invalid type",
			destType: "mysql",
			want:     false,
		},
		{
			name:     "empty type",
			destType: "",
			want:     false,
		},
		{
			name:     "case sensitive - uppercase",
			destType: "POSTGRESQL",
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsValidDestinationType(tt.destType)
			if got != tt.want {
				t.Errorf("IsValidDestinationType(%q) = %v, want %v", tt.destType, got, tt.want)
			}
		})
	}
}

func TestValidDestinationTypes(t *testing.T) {
	types := ValidDestinationTypes()
	if len(types) == 0 {
		t.Error("ValidDestinationTypes() returned empty slice")
	}

	// Ensure postgresql is in the list
	found := false
	for _, dt := range types {
		if dt == DestinationTypePostgresql {
			found = true
			break
		}
	}
	if !found {
		t.Error("DestinationTypePostgresql not found in ValidDestinationTypes()")
	}
}

func TestDestinationManifest_Validate(t *testing.T) {
	validPassword := "secret"
	validTimeout := 30

	tests := []struct {
		name       string
		manifest   DestinationManifest
		wantErrors int
		wantFields []string // Expected error fields
	}{
		{
			name: "valid manifest with all fields",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:           destTypePtr(DestinationTypePostgresql),
					Host:           strPtr("localhost"),
					Port:           5432,
					DatabaseName:   strPtr("mydb"),
					User:           strPtr("admin"),
					Password:       &validPassword,
					ConnectTimeout: &validTimeout,
				},
			},
			wantErrors: 0,
		},
		{
			name: "valid manifest without optional fields",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 0,
		},
		{
			name: "missing apiVersion",
			manifest: DestinationManifest{
				Kind: "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"apiVersion"},
		},
		{
			name: "wrong apiVersion - wrong domain",
			manifest: DestinationManifest{
				APIVersion: "qluster.io/v1", // wrong domain (.io instead of .ai)
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"apiVersion"},
		},
		{
			name: "wrong apiVersion - wrong version",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v2", // wrong version (v2 instead of v1)
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"apiVersion"},
		},
		{
			name: "wrong apiVersion - arbitrary string",
			manifest: DestinationManifest{
				APIVersion: "v1", // missing domain prefix
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"apiVersion"},
		},
		{
			name: "missing kind",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"kind"},
		},
		{
			name: "wrong kind",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "WrongKind",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"kind"},
		},
		{
			name: "missing metadata.name",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata:   DestinationMetadata{},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"metadata.name"},
		},
		{
			name: "missing spec.type",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.type"},
		},
		{
			name: "invalid spec.type",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr("invalid"),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.type"},
		},
		{
			name: "missing spec.host",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.host"},
		},
		{
			name: "invalid spec.port (zero)",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         0,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.port"},
		},
		{
			name: "invalid spec.port (negative)",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         -1,
					DatabaseName: strPtr("mydb"),
					User:         strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.port"},
		},
		{
			name: "missing spec.database_name",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type: destTypePtr(DestinationTypePostgresql),
					Host: strPtr("localhost"),
					Port: 5432,
					User: strPtr("admin"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.database_name"},
		},
		{
			name: "missing spec.user",
			manifest: DestinationManifest{
				APIVersion: "qluster.ai/v1",
				Kind:       "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type:         destTypePtr(DestinationTypePostgresql),
					Host:         strPtr("localhost"),
					Port:         5432,
					DatabaseName: strPtr("mydb"),
				},
			},
			wantErrors: 1,
			wantFields: []string{"spec.user"},
		},
		{
			name: "multiple errors",
			manifest: DestinationManifest{
				Kind: "Destination",
				Metadata: DestinationMetadata{
					Name: "my-destination",
				},
				Spec: DestinationSpec{
					Type: destTypePtr(DestinationTypePostgresql),
					Port: 5432,
				},
			},
			wantErrors: 4, // apiVersion, host, database_name, user
			wantFields: []string{"apiVersion", "spec.host", "spec.database_name", "spec.user"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := tt.manifest.Validate()

			if len(errors) != tt.wantErrors {
				t.Errorf("Validate() returned %d errors, want %d", len(errors), tt.wantErrors)
				for _, e := range errors {
					t.Logf("  Error: %s", e.Error())
				}
				return
			}

			// Check that expected fields are in errors
			for _, wantField := range tt.wantFields {
				found := false
				for _, err := range errors {
					if err.Field == wantField {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected error for field %q, but not found", wantField)
				}
			}
		})
	}
}

func TestValidationError_Error(t *testing.T) {
	err := &ValidationError{
		Field:   "spec.host",
		Message: "required field is missing",
	}

	got := err.Error()
	want := "spec.host: required field is missing"

	if got != want {
		t.Errorf("ValidationError.Error() = %q, want %q", got, want)
	}
}

func TestApplyResult_Fields(t *testing.T) {
	result := ApplyResult{
		Status:  "applied",
		Name:    "my-destination",
		ID:      123,
		Action:  "created",
		Message: "destination created successfully",
	}

	if result.Status != "applied" {
		t.Errorf("Status = %q, want %q", result.Status, "applied")
	}
	if result.Name != "my-destination" {
		t.Errorf("Name = %q, want %q", result.Name, "my-destination")
	}
	if result.ID != 123 {
		t.Errorf("ID = %d, want %d", result.ID, 123)
	}
	if result.Action != "created" {
		t.Errorf("Action = %q, want %q", result.Action, "created")
	}
	if result.Message != "destination created successfully" {
		t.Errorf("Message = %q, want %q", result.Message, "destination created successfully")
	}
}
