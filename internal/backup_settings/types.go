package backup_settings //nolint:revive // Public package name matches the established resource domain.

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

type (
	BackupSettingsTiny = api.BackupSettingsTinySchema
	BackupSettingsFull = api.BackupSettingsSchemaFull
	ListPage           = api.BackupSettingsListSchema
)

// Spec is the declarative backup-storage configuration. Pointer fields retain
// the distinction between an omitted credential (keep the stored value) and an
// explicitly empty credential (clear it).
type Spec struct {
	BackupType                *api.BackupType `yaml:"backup_type" json:"backup_type"`
	BackupGPGPublicKey        *string         `yaml:"backup_gpg_public_key,omitempty" json:"backup_gpg_public_key,omitempty"`
	BackupGPGRecipient        *string         `yaml:"backup_gpg_recipient,omitempty" json:"backup_gpg_recipient,omitempty"`
	BackupGPGPrivateKey       *string         `yaml:"backup_gpg_private_key,omitempty" json:"backup_gpg_private_key,omitempty"`
	BackupGPGPassphrase       *string         `yaml:"backup_gpg_passphrase,omitempty" json:"backup_gpg_passphrase,omitempty"`
	BackupS3AccessKey         *string         `yaml:"backup_s3_access_key,omitempty" json:"backup_s3_access_key,omitempty"`
	BackupS3SecretKey         *string         `yaml:"backup_s3_secret_key,omitempty" json:"backup_s3_secret_key,omitempty"`
	BackupS3EndpointURL       *string         `yaml:"backup_s3_endpoint_url,omitempty" json:"backup_s3_endpoint_url,omitempty"`
	BackupS3RegionName        *string         `yaml:"backup_s3_region_name,omitempty" json:"backup_s3_region_name,omitempty"`
	BackupS3DefaultBufferSize *int            `yaml:"backup_s3_default_buffer_size,omitempty" json:"backup_s3_default_buffer_size,omitempty"`
	BackupS3Bucket            *string         `yaml:"backup_s3_bucket,omitempty" json:"backup_s3_bucket,omitempty"`
	BackupGSServiceAccountKey *string         `yaml:"backup_gs_service_account_key,omitempty" json:"backup_gs_service_account_key,omitempty"`
	BackupGSBucket            *string         `yaml:"backup_gs_bucket,omitempty" json:"backup_gs_bucket,omitempty"`
}

type Status struct {
	ID        int       `yaml:"id" json:"id"`
	VersionID int       `yaml:"version_id" json:"version_id"`
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
}

type Manifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec                 `yaml:"spec" json:"spec"`
	Status     *Status              `yaml:"status,omitempty" json:"status,omitempty"`
}

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

func (m *Manifest) Validate() []ValidationError {
	var errs []ValidationError
	if m.APIVersion == "" {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: "required field is missing"})
	} else if m.APIVersion != pkgmanifest.APIVersionV1 {
		errs = append(errs, ValidationError{Field: "apiVersion", Message: fmt.Sprintf("must be %q", pkgmanifest.APIVersionV1)})
	}
	if m.Kind == "" {
		errs = append(errs, ValidationError{Field: "kind", Message: "required field is missing"})
	} else if m.Kind != "BackupSettings" {
		errs = append(errs, ValidationError{Field: "kind", Message: "must be 'BackupSettings'"})
	}
	if err := m.validateIdentity(); err != nil {
		errs = append(errs, *err)
	}
	if m.Spec.BackupType == nil {
		return append(errs, ValidationError{Field: "spec.backup_type", Message: "required field is missing"})
	}
	switch *m.Spec.BackupType {
	case api.BackupTypeS3:
		if empty(m.Spec.BackupS3Bucket) {
			errs = append(errs, ValidationError{Field: "spec.backup_s3_bucket", Message: "required for s3 backup settings"})
		}
	case api.BackupTypeS3Compatible:
		if empty(m.Spec.BackupS3Bucket) {
			errs = append(errs, ValidationError{Field: "spec.backup_s3_bucket", Message: "required for s3_compatible backup settings"})
		}
		if empty(m.Spec.BackupS3EndpointURL) {
			errs = append(errs, ValidationError{Field: "spec.backup_s3_endpoint_url", Message: "required for s3_compatible backup settings"})
		}
	case api.BackupTypeGs:
		if empty(m.Spec.BackupGSBucket) {
			errs = append(errs, ValidationError{Field: "spec.backup_gs_bucket", Message: "required for gs backup settings"})
		}
	default:
		errs = append(errs, ValidationError{Field: "spec.backup_type", Message: "must be one of: s3, s3_compatible, gs"})
	}
	return errs
}

func empty(s *string) bool { return s == nil || *s == "" }

func (m *Manifest) validateIdentity() *ValidationError {
	if m.Metadata.ID != "" {
		if _, err := m.targetID(); err != nil {
			return &ValidationError{Field: "metadata.id", Message: "must be a positive integer"}
		}
	} else if m.Metadata.Name == "" {
		return &ValidationError{Field: "metadata.name", Message: "required field is missing (or provide metadata.id)"}
	}
	return nil
}

func (m *Manifest) targetID() (int, error) {
	id, err := strconv.Atoi(m.Metadata.ID)
	if err != nil || id <= 0 {
		return 0, qerrors.NewBadUsage("metadata.id must be a positive integer")
	}
	return id, nil
}

func (s Spec) validateCreateCredentials() error {
	if s.BackupType == nil || (*s.BackupType != api.BackupTypeS3 && *s.BackupType != api.BackupTypeS3Compatible) {
		return nil
	}
	var missing []string
	if empty(s.BackupS3AccessKey) {
		missing = append(missing, "spec.backup_s3_access_key")
	}
	if empty(s.BackupS3SecretKey) {
		missing = append(missing, "spec.backup_s3_secret_key")
	}
	if len(missing) > 0 {
		return qerrors.NewBadUsagef("missing credentials for %s backup settings creation: %s", *s.BackupType, strings.Join(missing, ", "))
	}
	return nil
}

// APIResponseToManifest converts a detail response to the round-trippable
// manifest shape. Credential material is deliberately omitted even when the
// server returned a masked value.
func APIResponseToManifest(resp *BackupSettingsFull) *Manifest {
	return &Manifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       "BackupSettings",
		Metadata:   pkgmanifest.Metadata{ID: strconv.Itoa(resp.ID), Name: deref(resp.Name)},
		Spec: Spec{
			BackupType:                ptr(resp.BackupType),
			BackupGPGPublicKey:        nonEmptyPtr(resp.BackupGpgPublicKey),
			BackupGPGRecipient:        nonEmptyPtr(resp.BackupGpgRecipient),
			BackupS3EndpointURL:       nonEmptyPtr(resp.BackupS3EndpointURL),
			BackupS3RegionName:        nonEmptyPtr(resp.BackupS3RegionName),
			BackupS3DefaultBufferSize: intPtr(resp.BackupS3DefaultBufferSize),
			BackupS3Bucket:            nonEmptyPtr(resp.BackupS3Bucket),
			BackupGSBucket:            nonEmptyPtr(resp.BackupGsBucket),
		},
		Status: &Status{ID: resp.ID, VersionID: resp.VersionID, CreatedAt: resp.CreatedAt},
	}
}

type ApplyResult struct {
	Name   string `json:"name"`
	Action string `json:"action"`
	ID     int    `json:"id"`
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

func intPtr(v int) *int {
	if v == 0 {
		return nil
	}
	return &v
}
