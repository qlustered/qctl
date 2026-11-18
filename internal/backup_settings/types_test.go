package backup_settings //nolint:revive // Match the established package under test.

import (
	"encoding/json"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
)

func metadata(name string) pkgmanifest.Metadata { return pkgmanifest.Metadata{Name: name} }

func TestManifestValidationIsTypeSpecific(t *testing.T) {
	tests := []struct {
		name string
		spec Spec
		want string
	}{
		{"s3 bucket", Spec{BackupType: ptr(api.BackupTypeS3)}, "spec.backup_s3_bucket"},
		{"gs bucket", Spec{BackupType: ptr(api.BackupTypeGs)}, "spec.backup_gs_bucket"},
		{"s3_compatible endpoint", Spec{BackupType: ptr(api.BackupTypeS3Compatible), BackupS3Bucket: ptr("bucket")}, "spec.backup_s3_endpoint_url"},
		{"retired rustfs", Spec{BackupType: ptr(api.BackupType("rustfs")), BackupS3Bucket: ptr("bucket"), BackupS3EndpointURL: ptr("http://store")}, "spec.backup_type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := &Manifest{APIVersion: pkgmanifest.APIVersionV1, Kind: "BackupSettings", Metadata: metadata("daily"), Spec: tc.spec}
			errs := m.Validate()
			found := false
			for _, err := range errs {
				found = found || err.Field == tc.want
			}
			if !found {
				t.Fatalf("errors = %#v, want %s", errs, tc.want)
			}
		})
	}
}

func TestAPIResponseToManifestOmitsCredentials(t *testing.T) {
	masked := "**********"
	full := fullFixture("daily")
	full.BackupS3AccessKey = "visible-but-sensitive"
	full.BackupS3SecretKey = &masked
	full.BackupGpgPrivateKey = &masked
	full.BackupGpgPassphrase = &masked
	full.BackupGsServiceAccountKey = &masked

	m := APIResponseToManifest(&full)
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{
		"backup_s3_access_key", "backup_s3_secret_key", "backup_gpg_private_key",
		"backup_gpg_passphrase", "backup_gs_service_account_key",
	} {
		if json.Valid(raw) && containsJSONKey(raw, secret) {
			t.Errorf("manifest includes credential %q: %s", secret, raw)
		}
	}
	if m.Status == nil || m.Status.VersionID != 3 {
		t.Fatalf("status = %#v", m.Status)
	}
}

func containsJSONKey(raw []byte, key string) bool {
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return false
	}
	spec, _ := object["spec"].(map[string]any)
	_, ok := spec[key]
	return ok
}
