package errorincidents

// ErrorIncidentSpec defines the specification for an error incident (read-only)
type ErrorIncidentSpec struct {
	// Error is the error class name
	Error string `yaml:"error" json:"error"`

	// Msg is the error message
	Msg string `yaml:"msg" json:"msg"`

	// Module is the module that raised the error
	Module string `yaml:"module" json:"module"`

	// Count is the count of this error
	Count int `yaml:"count" json:"count"`

	// JobName is the name of the job that encountered this error
	JobName *string `yaml:"job_name,omitempty" json:"job_name,omitempty"`

	// JobType is the type of job
	JobType *string `yaml:"job_type,omitempty" json:"job_type,omitempty"`

	// StackTrace is the stack trace of the exception if any
	StackTrace *string `yaml:"stack_trace,omitempty" json:"stack_trace,omitempty"`

	// DatasetID is the associated dataset ID
	DatasetID *int `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`

	// StoredItemID is the associated stored item ID
	StoredItemID *int `yaml:"stored_item_id,omitempty" json:"stored_item_id,omitempty"`

	// AlertItemID is the associated alert item ID
	AlertItemID *int `yaml:"alert_item_id,omitempty" json:"alert_item_id,omitempty"`

	// DataSourceModelID is the associated data source model ID
	DataSourceModelID *int `yaml:"data_source_model_id,omitempty" json:"data_source_model_id,omitempty"`

	// SettingsModelID is the associated settings model ID
	SettingsModelID *int `yaml:"settings_model_id,omitempty" json:"settings_model_id,omitempty"`

	// MetaData holds additional metadata about the error
	MetaData map[string]interface{} `yaml:"meta_data,omitempty" json:"meta_data,omitempty"`
}

// ErrorIncidentMetadata holds metadata for an error incident resource
type ErrorIncidentMetadata struct {
	// Annotations are optional key-value pairs for additional metadata
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`

	// Labels are optional key-value pairs for organization
	Labels map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
}

// ErrorIncidentStatus holds runtime status information
type ErrorIncidentStatus struct {
	// ID is the error incident ID
	ID int `yaml:"id" json:"id"`

	// Deleted indicates if the incident has been deleted
	Deleted bool `yaml:"deleted" json:"deleted"`

	// CreatedAt is when the incident was created
	CreatedAt *string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
}

// ErrorIncidentManifest is the declarative representation of an error incident
// This follows the Kubernetes-style manifest format
type ErrorIncidentManifest struct {
	// APIVersion is the API version (e.g., "qluster.ai/v1")
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`

	// Kind is the resource type (should be "ErrorIncident")
	Kind string `yaml:"kind" json:"kind"`

	// Metadata contains resource metadata
	Metadata ErrorIncidentMetadata `yaml:"metadata" json:"metadata"`

	// Spec contains the error incident specification
	Spec ErrorIncidentSpec `yaml:"spec" json:"spec"`

	// Status contains runtime status information
	Status *ErrorIncidentStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// APIVersionV1 is the expected API version for error incident manifests
const APIVersionV1 = "qluster.ai/v1"
