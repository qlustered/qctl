package cloud_sources

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// CloudSourceSpec is a CloudSource manifest's spec.
type CloudSourceSpec struct {
	DatasetID int `yaml:"dataset_id" json:"dataset_id"`
	// DatasetName is the table's name as the read reported it. apply does not
	// read it.
	DatasetName     *string         `yaml:"dataset_name,omitempty" json:"dataset_name,omitempty"`
	DataSourceType  *DataSourceType `yaml:"data_source_type" json:"data_source_type"`
	SettingsModelID *int            `yaml:"settings_model_id,omitempty" json:"settings_model_id,omitempty"`
	// State is the state apply creates the source in: draft, or active when
	// absent. Only draft is valid, and only on a create.
	State *DataSourceState `yaml:"state,omitempty" json:"state,omitempty"`

	CloudSourceFields `yaml:",inline"`

	SettingsOverrides *SettingsOverrides `yaml:"settings_overrides,omitempty" json:"settings_overrides,omitempty"`
}

// CloudSourceStatus contains runtime fields for a cloud source.
type CloudSourceStatus struct {
	ID           int             `yaml:"id" json:"id"`
	State        DataSourceState `yaml:"state,omitempty" json:"state,omitempty"`
	VersionID    int             `yaml:"version_id,omitempty" json:"version_id,omitempty"`
	DatasetID    int             `yaml:"dataset_id,omitempty" json:"dataset_id,omitempty"`
	DatasetName  string          `yaml:"dataset_name,omitempty" json:"dataset_name,omitempty"`
	InternalID   string          `yaml:"internal_id,omitempty" json:"internal_id,omitempty"`
	Schedule     *string         `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	BadRowsCount *int            `yaml:"bad_rows_count,omitempty" json:"bad_rows_count,omitempty"`
	CreatedAt    string          `yaml:"created_at,omitempty" json:"created_at,omitempty"`

	DeltaAppliedCursor *int                `yaml:"delta_applied_cursor,omitempty" json:"delta_applied_cursor,omitempty"`
	PeriodDeltaCursors []PeriodDeltaCursor `yaml:"period_delta_cursors" json:"period_delta_cursors"`
}

// CloudSourceManifest is the declarative representation `get cloud-source -o
// yaml|json` prints and `apply` reads.
type CloudSourceManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       CloudSourceSpec      `yaml:"spec" json:"spec"`
	Status     *CloudSourceStatus   `yaml:"status,omitempty" json:"status,omitempty"`
}

const (
	// Canonical data source types for validation.
	DataSourceTypeS3           DataSourceType = api.DataSourceTypeS3
	DataSourceTypeS3Compatible DataSourceType = api.DataSourceTypeS3Compatible
	DataSourceTypeSftp         DataSourceType = api.DataSourceTypeSftp
	DataSourceTypeGs           DataSourceType = api.DataSourceTypeGs
	DataSourceTypeDropbox      DataSourceType = api.DataSourceTypeDropbox
	DataSourceTypeSimpleURL    DataSourceType = api.DataSourceTypeSimpleURL
	DataSourceTypeQlusterUI    DataSourceType = api.DataSourceTypeQlusterUI
)

// ValidationError represents a manifest validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// ValidDataSourceTypes returns the list of supported types for manifest validation.
func ValidDataSourceTypes() []DataSourceType {
	return []DataSourceType{
		DataSourceTypeS3,
		DataSourceTypeS3Compatible,
		DataSourceTypeSftp,
		DataSourceTypeGs,
		DataSourceTypeDropbox,
		DataSourceTypeSimpleURL,
		DataSourceTypeQlusterUI,
	}
}

// IsValidDataSourceType checks if the value is allowed.
func IsValidDataSourceType(t string) bool {
	for _, allowed := range ValidDataSourceTypes() {
		if string(allowed) == t {
			return true
		}
	}
	return false
}

// Validate validates the manifest contents.
func (m *CloudSourceManifest) Validate() []ValidationError {
	var errs []ValidationError
	errs = append(errs, validateAPIVersion(m.APIVersion)...)
	errs = append(errs, validateKind(m.Kind)...)
	errs = append(errs, validateRequiredString("metadata.name", m.Metadata.Name)...)
	errs = append(errs, validatePositiveInteger("spec.dataset_id", m.Spec.DatasetID)...)
	errs = append(errs, validateOptionalPositiveInteger("spec.settings_model_id", m.Spec.SettingsModelID)...)
	errs = append(errs, validateDataSourceType(m.Spec.DataSourceType)...)
	errs = append(errs, validateSourceSettings(m.Spec, m.DataSourceTypeValue())...)
	errs = append(errs, validateState(m.Spec.State)...)
	return errs
}

// validateState accepts an absent state and draft.
func validateState(state *DataSourceState) []ValidationError {
	if state == nil || *state == api.DataSourceStateDraft {
		return nil
	}
	return []ValidationError{{
		Field: "spec.state",
		Message: fmt.Sprintf("%q is not accepted: the only value is draft, which creates the source as a draft; "+
			"leave spec.state out to create an active source, and change an existing source's state with "+
			"'qctl enable cloud-source <id>' or 'qctl disable cloud-source <id>'", string(*state)),
	}}
}

// CreatesDraft reports whether the spec asks for a draft.
func (s *CloudSourceSpec) CreatesDraft() bool {
	return s.State != nil && *s.State == api.DataSourceStateDraft
}

func validateAPIVersion(version string) []ValidationError {
	if version == "" {
		return []ValidationError{{Field: "apiVersion", Message: "required field is missing"}}
	}
	if version != pkgmanifest.APIVersionV1 {
		return []ValidationError{{Field: "apiVersion", Message: "must be 'qluster.ai/v1'"}}
	}
	return nil
}

func validateKind(kind string) []ValidationError {
	if kind == "" {
		return []ValidationError{{Field: "kind", Message: "required field is missing"}}
	}
	if kind != "CloudSource" {
		return []ValidationError{{Field: "kind", Message: "must be 'CloudSource'"}}
	}
	return nil
}

func validateRequiredString(field, value string) []ValidationError {
	if value == "" {
		return []ValidationError{{Field: field, Message: "required field is missing"}}
	}
	return nil
}

func validatePositiveInteger(field string, value int) []ValidationError {
	if value <= 0 {
		return []ValidationError{{Field: field, Message: "must be a positive integer"}}
	}
	return nil
}

// validateOptionalPositiveInteger accepts an absent value and applies the
// positive-integer rule to a present one. Optional is not unchecked: a
// supplied 0 would take the backend's derive-the-table-from-the-settings-model
// branch and create the source somewhere the manifest does not name.
func validateOptionalPositiveInteger(field string, value *int) []ValidationError {
	if value == nil {
		return nil
	}
	return validatePositiveInteger(field, *value)
}

func validateDataSourceType(sourceType *DataSourceType) []ValidationError {
	if sourceType == nil || *sourceType == "" {
		return []ValidationError{{Field: "spec.data_source_type", Message: "required field is missing"}}
	}
	if !IsValidDataSourceType(string(*sourceType)) {
		return []ValidationError{{Field: "spec.data_source_type", Message: "invalid type"}}
	}
	return nil
}

func validateSourceSettings(spec CloudSourceSpec, sourceType DataSourceType) []ValidationError {
	var errs []ValidationError
	switch sourceType {
	case DataSourceTypeS3, DataSourceTypeS3Compatible:
		if isEmpty(spec.S3Bucket) {
			errs = append(errs, ValidationError{Field: "spec.s3_bucket", Message: "required for s3/s3_compatible sources"})
		}
	case DataSourceTypeSftp:
		if isEmpty(spec.SftpHost) {
			errs = append(errs, ValidationError{Field: "spec.sftp_host", Message: "required for sftp sources"})
		}
		if isEmpty(spec.SftpUser) {
			errs = append(errs, ValidationError{Field: "spec.sftp_user", Message: "required for sftp sources"})
		}
	case DataSourceTypeGs:
		if isEmpty(spec.GsBucket) {
			errs = append(errs, ValidationError{Field: "spec.gs_bucket", Message: "required for gs sources"})
		}
	case DataSourceTypeSimpleURL:
		if isEmpty(spec.SimpleURL) && !spec.CreatesDraft() {
			errs = append(errs, ValidationError{Field: "spec.simple_url", Message: "required for simple_url sources"})
		}
	}

	return errs
}

// validateCreateCredentials checks credentials needed to create a source. A
// draft needs none.
func validateCreateCredentials(spec CloudSourceSpec, sourceType DataSourceType) []ValidationError {
	if spec.CreatesDraft() {
		return nil
	}
	switch sourceType {
	case DataSourceTypeSftp:
		if !credentialProvided(spec.SftpPassword) && !credentialProvided(spec.SftpSSHKey) {
			return []ValidationError{{Field: "spec.sftp_password", Message: "password or ssh key is required for sftp sources"}}
		}
	case DataSourceTypeDropbox:
		if !credentialProvided(spec.DropboxAuthorizationCode) {
			return []ValidationError{{Field: "spec.dropbox_authorization_code", Message: "required for dropbox sources"}}
		}
	}
	return nil
}

func credentialProvided(value *string) bool {
	return !isEmpty(value) && *value != secretMask && !strings.ContainsRune(*value, '•')
}

// legacyDropboxKey is the spec key dropbox_authorization_code replaced.
const legacyDropboxKey = "dropbox_access_token"

// CheckRenamedKeys refuses a spec carrying dropbox_access_token, naming the key
// that replaced it. spec is the spec as a generic manifest decodes it; a value
// that is not a map passes.
func CheckRenamedKeys(spec any) error {
	m, ok := spec.(map[string]any)
	if !ok {
		return nil
	}
	if _, found := m[legacyDropboxKey]; !found {
		return nil
	}
	return &ValidationError{
		Field: "spec." + legacyDropboxKey,
		Message: "the key is now spec.dropbox_authorization_code, and it takes a Dropbox OAuth authorization code, " +
			"which the server exchanges with Dropbox for the access token it stores; an access token is refused",
	}
}

// DataSourceTypeValue returns the dereferenced type (or empty if nil).
func (m *CloudSourceManifest) DataSourceTypeValue() DataSourceType {
	if m.Spec.DataSourceType == nil {
		return ""
	}
	return *m.Spec.DataSourceType
}

// APIResponseToManifest converts a cloud source read into the manifest apply
// reads. It leaves out secrets, nulls, and an empty string in a field whose
// omission keeps the stored value. The source's state goes to status.state,
// never spec.state.
func APIResponseToManifest(resp *CloudSourceFull) (*CloudSourceManifest, error) {
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the cloud source: %w", err)
	}
	members, err := nonNullMembers(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to decode the cloud source: %w", err)
	}

	spec := CloudSourceSpec{
		DatasetID:       derefInt(resp.DatasetID),
		DatasetName:     copyString(resp.DatasetName),
		DataSourceType:  copyDataSourceType(resp.DataSourceType),
		SettingsModelID: copyInt(resp.SettingsModelID),
	}
	if err := decodeMembers(exportedFields(members), &spec.CloudSourceFields); err != nil {
		return nil, fmt.Errorf("failed to read the cloud source's fields: %w", err)
	}
	if overrides := exportedOverrides(members); len(overrides) > 0 {
		spec.SettingsOverrides = &SettingsOverrides{}
		if err := decodeMembers(overrides, spec.SettingsOverrides); err != nil {
			return nil, fmt.Errorf("failed to read the cloud source's overrides: %w", err)
		}
	}

	labels := map[string]string{}
	if resp.DatasetID != nil {
		labels["dataset_id"] = intToString(*resp.DatasetID)
	}
	status := &CloudSourceStatus{
		ID:           derefInt(resp.ID),
		VersionID:    derefInt(resp.VersionID),
		DatasetID:    derefInt(resp.DatasetID),
		DatasetName:  derefString(resp.DatasetName),
		InternalID:   derefString(resp.InternalID),
		State:        derefDataSourceState(resp.State),
		Schedule:     copyString(resp.Schedule),
		BadRowsCount: resp.BadRowsCount,
		CreatedAt:    formatTime(resp.CreatedAt),

		DeltaAppliedCursor: resp.DeltaAppliedCursor,
		PeriodDeltaCursors: periodDeltaCursors(resp.PeriodDeltaCursors),
	}

	return &CloudSourceManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "CloudSource",
		Metadata: pkgmanifest.Metadata{
			Name:   derefString(resp.Name),
			Labels: labels,
		},
		Spec:   spec,
		Status: status,
	}, nil
}

func isEmpty(v *string) bool {
	return v == nil || *v == ""
}

func copyString(v *string) *string {
	if v == nil || *v == "" {
		return nil
	}
	val := *v
	return &val
}

// copyInt is copyString for integers, with one deliberate difference: it does
// NOT treat a zero as absent. copyString drops "" because an empty string in a
// manifest is indistinguishable from an omitted one; a settings model id of 0
// is a value the server should never have sent, and swallowing it would hide
// that.
func copyInt(v *int) *int {
	if v == nil {
		return nil
	}
	val := *v
	return &val
}

func copyDataSourceType(v *DataSourceType) *DataSourceType {
	if v == nil {
		return nil
	}
	val := *v
	return &val
}

func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func derefDataSourceState(v *DataSourceState) DataSourceState {
	if v == nil {
		return ""
	}
	return *v
}

func intToString(v int) string {
	return strconv.Itoa(v)
}

func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
