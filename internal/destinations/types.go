package destinations

// DestinationType represents the type of destination
type DestinationType string

const (
	// DestinationTypePostgresql represents a PostgreSQL destination
	DestinationTypePostgresql DestinationType = "postgresql"
)

// ValidDestinationTypes returns a list of valid destination types
func ValidDestinationTypes() []DestinationType {
	return []DestinationType{
		DestinationTypePostgresql,
	}
}

// IsValidDestinationType checks if the given type is valid
func IsValidDestinationType(t string) bool {
	for _, valid := range ValidDestinationTypes() {
		if string(valid) == t {
			return true
		}
	}
	return false
}

// DestinationSpec defines the specification for a destination
// This is a clean spec that does NOT use oapi-codegen types
type DestinationSpec struct {
	// Type is the destination type (e.g., "postgresql")
	Type *DestinationType `yaml:"type,omitempty" json:"type,omitempty"`

	// DatabaseName is the name of the database
	DatabaseName *string `yaml:"database_name,omitempty" json:"database_name,omitempty"`

	// Host is the database host
	Host *string `yaml:"host,omitempty" json:"host,omitempty"`

	// User is the database user
	User *string `yaml:"user,omitempty" json:"user,omitempty"`

	// Password is the database password (optional, can be injected via env vars)
	// Use pointer to distinguish between nil (not set) and empty string
	Password *string `yaml:"password,omitempty" json:"password,omitempty"`

	// ConnectTimeout is the connection timeout in seconds (optional)
	ConnectTimeout *int `yaml:"connect_timeout,omitempty" json:"connect_timeout,omitempty"`

	// Port is the database port
	Port int32 `yaml:"port" json:"port"`
}

// DestinationMetadata holds metadata for a destination resource
type DestinationMetadata struct {
	// Annotations are optional key-value pairs for additional metadata
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`

	// Labels are optional key-value pairs for organization
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`

	// Name is the unique name of the destination
	Name string `yaml:"name" json:"name"`
}

// DestinationManifest is the declarative representation of a destination
// This follows the Kubernetes-style manifest format
type DestinationManifest struct {
	// Spec contains the destination specification
	Spec DestinationSpec `yaml:"spec" json:"spec"`

	// Kind is the resource type (should be "Destination")
	Kind string `yaml:"kind" json:"kind"`

	// APIVersion is the API version (e.g., "qluster.ai/v1")
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`

	// Metadata contains resource metadata
	Metadata DestinationMetadata `yaml:"metadata" json:"metadata"`
}

// APIVersionV1 is the expected API version for destination manifests
const APIVersionV1 = "qluster.ai/v1"

// isEmptyStringPtr returns true if the pointer is nil or points to an empty string
func isEmptyStringPtr(s *string) bool {
	return s == nil || *s == ""
}

// isEmptyDestinationType returns true if the pointer is nil or points to an empty type
func isEmptyDestinationType(t *DestinationType) bool {
	return t == nil || *t == ""
}

// Validate validates the destination manifest
func (m *DestinationManifest) Validate() []ValidationError {
	var errors []ValidationError

	// Validate required fields
	errors = m.validateAPIVersion(errors)
	errors = m.validateKind(errors)
	errors = m.validateMetadata(errors)
	errors = m.validateSpec(errors)

	return errors
}

func (m *DestinationManifest) validateAPIVersion(errors []ValidationError) []ValidationError {
	if m.APIVersion == "" {
		return append(errors, ValidationError{Field: "apiVersion", Message: "required field is missing"})
	}
	if m.APIVersion != APIVersionV1 {
		return append(errors, ValidationError{Field: "apiVersion", Message: "must be 'qluster.ai/v1'"})
	}
	return errors
}

func (m *DestinationManifest) validateKind(errors []ValidationError) []ValidationError {
	if m.Kind == "" {
		return append(errors, ValidationError{Field: "kind", Message: "required field is missing"})
	}
	if m.Kind != "Destination" {
		return append(errors, ValidationError{Field: "kind", Message: "must be 'Destination'"})
	}
	return errors
}

func (m *DestinationManifest) validateMetadata(errors []ValidationError) []ValidationError {
	if m.Metadata.Name == "" {
		return append(errors, ValidationError{Field: "metadata.name", Message: "required field is missing"})
	}
	return errors
}

func (m *DestinationManifest) validateSpec(errors []ValidationError) []ValidationError {
	// Validate type
	if isEmptyDestinationType(m.Spec.Type) {
		errors = append(errors, ValidationError{Field: "spec.type", Message: "required field is missing"})
	} else if !IsValidDestinationType(string(*m.Spec.Type)) {
		errors = append(errors, ValidationError{Field: "spec.type", Message: "invalid destination type, must be one of: postgresql"})
	}

	if isEmptyStringPtr(m.Spec.Host) {
		errors = append(errors, ValidationError{Field: "spec.host", Message: "required field is missing"})
	}

	if m.Spec.Port <= 0 {
		errors = append(errors, ValidationError{Field: "spec.port", Message: "must be a positive integer"})
	}

	if isEmptyStringPtr(m.Spec.DatabaseName) {
		errors = append(errors, ValidationError{Field: "spec.database_name", Message: "required field is missing"})
	}

	if isEmptyStringPtr(m.Spec.User) {
		errors = append(errors, ValidationError{Field: "spec.user", Message: "required field is missing"})
	}

	return errors
}

// ValidationError represents a manifest validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// ApplyResult represents the result of an apply operation
type ApplyResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Action  string `json:"action,omitempty"` // "created" or "updated"
	ID      int    `json:"id,omitempty"`
}
