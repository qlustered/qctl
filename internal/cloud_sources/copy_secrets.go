package cloud_sources

import "slices"

const (
	// s3AccessKey is the wire key of the S3 access-key ID.
	s3AccessKey = "s3_access_key"
	// simpleURLKey is the wire key of a URL source's URL.
	simpleURLKey = "simple_url"
	// dropboxAuthorizationCode is the wire key of a Dropbox authorization code.
	dropboxAuthorizationCode = "dropbox_authorization_code"
)

// copySecretKeys are the wire keys CopySecretKeys returns.
var copySecretKeys = []string{
	"s3_secret_key", "sftp_password", "sftp_ssh_key", "sftp_ssh_key_passphrase",
	"gpg_private_key", "gpg_passphrase", "file_password", "gs_service_account_key",
	"dropbox_access_token", dropboxAuthorizationCode, simpleURLKey,
}

// CopySecretKeys returns the wire keys of api.DatasetCopyDataSourceRequest
// that carry a credential or a URL.
func CopySecretKeys() []string {
	return slices.Clone(copySecretKeys)
}

// copyRequestSecretKeys are the wire keys CopyRequestSecretKeys returns.
var copyRequestSecretKeys = []string{
	s3AccessKey, "s3_secret_key", "sftp_password", "sftp_ssh_key", "sftp_ssh_key_passphrase",
	"gpg_private_key", "gpg_passphrase", "file_password", "gs_service_account_key",
	dropboxAuthorizationCode, simpleURLKey,
}

// CopyRequestSecretKeys returns the wire keys of
// api.DataSourceModelCopyRequestSchema that carry a credential or a URL.
func CopyRequestSecretKeys() []string {
	return slices.Clone(copyRequestSecretKeys)
}
