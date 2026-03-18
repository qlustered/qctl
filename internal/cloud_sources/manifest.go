package cloud_sources

import (
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

// CloudSourceSpec defines the declarative configuration for a cloud source (data source model).
type CloudSourceSpec struct {
	DatasetID                   int             `yaml:"dataset_id" json:"dataset_id"`
	DatasetName                 *string         `yaml:"dataset_name,omitempty" json:"dataset_name,omitempty"`
	DataSourceType              *DataSourceType `yaml:"data_source_type" json:"data_source_type"`
	SettingsModelID             int             `yaml:"settings_model_id" json:"settings_model_id"`
	Schedule                    *string         `yaml:"schedule,omitempty" json:"schedule,omitempty"`
	Pattern                     *string         `yaml:"pattern,omitempty" json:"pattern,omitempty"`
	ConnectionTimeout           *int            `yaml:"connection_timeout,omitempty" json:"connection_timeout,omitempty"`
	DeleteSourceFileAfterBackup *bool           `yaml:"delete_source_file_after_backup,omitempty" json:"delete_source_file_after_backup,omitempty"`

	// Generic URL/source configs
	SimpleURL      *string `yaml:"simple_url,omitempty" json:"simple_url,omitempty"`
	ArchivePattern *string `yaml:"archive_pattern,omitempty" json:"archive_pattern,omitempty"`

	// S3 / MinIO
	S3Bucket      *string `yaml:"s3_bucket,omitempty" json:"s3_bucket,omitempty"`
	S3RegionName  *string `yaml:"s3_region_name,omitempty" json:"s3_region_name,omitempty"`
	S3Prefix      *string `yaml:"s3_prefix,omitempty" json:"s3_prefix,omitempty"`
	S3EndpointURL *string `yaml:"s3_endpoint_url,omitempty" json:"s3_endpoint_url,omitempty"`
	S3AccessKey   *string `yaml:"s3_access_key,omitempty" json:"s3_access_key,omitempty"`
	S3SecretKey   *string `yaml:"s3_secret_key,omitempty" json:"s3_secret_key,omitempty"`

	// Google Cloud Storage
	GsBucket            *string `yaml:"gs_bucket,omitempty" json:"gs_bucket,omitempty"`
	GsPrefix            *string `yaml:"gs_prefix,omitempty" json:"gs_prefix,omitempty"`
	GsServiceAccountKey *string `yaml:"gs_service_account_key,omitempty" json:"gs_service_account_key,omitempty"`

	// Dropbox
	DropboxAccessToken *string `yaml:"dropbox_access_token,omitempty" json:"dropbox_access_token,omitempty"`
	DropboxFolder      *string `yaml:"dropbox_folder,omitempty" json:"dropbox_folder,omitempty"`

	// SFTP
	SftpHost             *string `yaml:"sftp_host,omitempty" json:"sftp_host,omitempty"`
	SftpPort             *int    `yaml:"sftp_port,omitempty" json:"sftp_port,omitempty"`
	SftpUser             *string `yaml:"sftp_user,omitempty" json:"sftp_user,omitempty"`
	SftpPassword         *string `yaml:"sftp_password,omitempty" json:"sftp_password,omitempty"`
	SftpFolder           *string `yaml:"sftp_folder,omitempty" json:"sftp_folder,omitempty"`
	SftpSSHKey           *string `yaml:"sftp_ssh_key,omitempty" json:"sftp_ssh_key,omitempty"`
	SftpSSHKeyPassphrase *string `yaml:"sftp_ssh_key_passphrase,omitempty" json:"sftp_ssh_key_passphrase,omitempty"`

	// File-level security
	FilePassword  *string `yaml:"file_password,omitempty" json:"file_password,omitempty"`
	GpgPrivateKey *string `yaml:"gpg_private_key,omitempty" json:"gpg_private_key,omitempty"`
	GpgPassphrase *string `yaml:"gpg_passphrase,omitempty" json:"gpg_passphrase,omitempty"`
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
}

// CloudSourceManifest is the declarative representation used by describe/apply.
type CloudSourceManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       CloudSourceSpec      `yaml:"spec" json:"spec"`
	Status     *CloudSourceStatus   `yaml:"status,omitempty" json:"status,omitempty"`
}

const (
	// Canonical data source types for validation.
	DataSourceTypeS3        DataSourceType = api.DataSourceTypeS3
	DataSourceTypeMinio     DataSourceType = api.DataSourceTypeMinio
	DataSourceTypeSftp      DataSourceType = api.DataSourceTypeSftp
	DataSourceTypeGs        DataSourceType = api.DataSourceTypeGs
	DataSourceTypeDropbox   DataSourceType = api.DataSourceTypeDropbox
	DataSourceTypeSimpleURL DataSourceType = api.DataSourceTypeSimpleURL
	DataSourceTypeQlusterUI DataSourceType = api.DataSourceTypeQlusterUI
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
		DataSourceTypeMinio,
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

	if m.APIVersion == "" {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: "required field is missing"})
	} else if m.APIVersion != pkgmanifest.APIVersionV1 {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: "must be 'qluster.ai/v1'"})
	}

	if m.Kind == "" {
		errs = append(errs, ValidationError{Field: "kind", Message: "required field is missing"})
	} else if m.Kind != "CloudSource" {
		errs = append(errs, ValidationError{Field: "kind", Message: "must be 'CloudSource'"})
	}

	if m.Metadata.Name == "" {
		errs = append(errs, ValidationError{Field: "metadata.name", Message: "required field is missing"})
	}

	if m.Spec.DatasetID <= 0 {
		errs = append(errs, ValidationError{Field: "spec.dataset_id", Message: "must be a positive integer"})
	}

	if m.Spec.SettingsModelID <= 0 {
		errs = append(errs, ValidationError{Field: "spec.settings_model_id", Message: "must be a positive integer"})
	}

	if m.Spec.DataSourceType == nil || *m.Spec.DataSourceType == "" {
		errs = append(errs, ValidationError{Field: "spec.data_source_type", Message: "required field is missing"})
	} else if !IsValidDataSourceType(string(*m.Spec.DataSourceType)) {
		errs = append(errs, ValidationError{Field: "spec.data_source_type", Message: "invalid type"})
	}

	switch m.DataSourceTypeValue() {
	case DataSourceTypeS3, DataSourceTypeMinio:
		if isEmpty(m.Spec.S3Bucket) {
			errs = append(errs, ValidationError{Field: "spec.s3_bucket", Message: "required for s3/minio sources"})
		}
	case DataSourceTypeSftp:
		if isEmpty(m.Spec.SftpHost) {
			errs = append(errs, ValidationError{Field: "spec.sftp_host", Message: "required for sftp sources"})
		}
		if isEmpty(m.Spec.SftpUser) {
			errs = append(errs, ValidationError{Field: "spec.sftp_user", Message: "required for sftp sources"})
		}
		if isEmpty(m.Spec.SftpPassword) && isEmpty(m.Spec.SftpSSHKey) {
			errs = append(errs, ValidationError{Field: "spec.sftp_password", Message: "password or ssh key is required for sftp sources"})
		}
	case DataSourceTypeGs:
		if isEmpty(m.Spec.GsBucket) {
			errs = append(errs, ValidationError{Field: "spec.gs_bucket", Message: "required for gs sources"})
		}
	case DataSourceTypeDropbox:
		if isEmpty(m.Spec.DropboxAccessToken) {
			errs = append(errs, ValidationError{Field: "spec.dropbox_access_token", Message: "required for dropbox sources"})
		}
	case DataSourceTypeSimpleURL:
		if isEmpty(m.Spec.SimpleURL) {
			errs = append(errs, ValidationError{Field: "spec.simple_url", Message: "required for simple_url sources"})
		}
	}

	return errs
}

// DataSourceTypeValue returns the dereferenced type (or empty if nil).
func (m *CloudSourceManifest) DataSourceTypeValue() DataSourceType {
	if m.Spec.DataSourceType == nil {
		return ""
	}
	return *m.Spec.DataSourceType
}

// APIResponseToManifest converts an API response into a manifest structure.
func APIResponseToManifest(resp *CloudSourceFull) *CloudSourceManifest {
	name := derefString(resp.Name)
	labels := map[string]string{}
	if resp.DatasetID != nil {
		labels["dataset_id"] = intToString(*resp.DatasetID)
	}

	spec := CloudSourceSpec{
		DatasetID:                   derefInt(resp.DatasetID),
		DatasetName:                 copyString(resp.DatasetName),
		DataSourceType:              copyDataSourceType(resp.DataSourceType),
		SettingsModelID:             derefInt(resp.SettingsModelID),
		Schedule:                    copyString(resp.Schedule),
		Pattern:                     copyString(resp.Pattern),
		ConnectionTimeout:           resp.ConnectionTimeout,
		DeleteSourceFileAfterBackup: resp.DeleteSourceFileAfterBackup,
		SimpleURL:                   copyString(resp.SimpleURL),
		ArchivePattern:              copyString(resp.ArchivePattern),
		S3Bucket:                    copyString(resp.S3Bucket),
		S3RegionName:                copyString(resp.S3RegionName),
		S3Prefix:                    copyString(resp.S3Prefix),
		S3EndpointURL:               copyString(resp.S3EndpointURL),
		GsBucket:                    copyString(resp.GsBucket),
		GsPrefix:                    copyString(resp.GsPrefix),
		DropboxFolder:               copyString(resp.DropboxFolder),
		SftpHost:                    copyString(resp.SftpHost),
		SftpPort:                    resp.SftpPort,
		SftpUser:                    copyString(resp.SftpUser),
		SftpFolder:                  copyString(resp.SftpFolder),
	}

	// Keep secret values out of output
	spec.S3AccessKey = nil
	spec.S3SecretKey = nil
	spec.DropboxAccessToken = nil
	spec.GsServiceAccountKey = nil
	spec.SftpPassword = nil
	spec.SftpSSHKey = nil
	spec.SftpSSHKeyPassphrase = nil
	spec.FilePassword = nil
	spec.GpgPrivateKey = nil
	spec.GpgPassphrase = nil

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
	}

	return &CloudSourceManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "CloudSource",
		Metadata: pkgmanifest.Metadata{
			Name:   name,
			Labels: labels,
		},
		Spec:   spec,
		Status: status,
	}
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
