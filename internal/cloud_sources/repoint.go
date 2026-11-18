package cloud_sources

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
)

// repointKeys mirrors atlas data_source_patch_policy._REPOINT_FIELDS.
var repointKeys = map[string]bool{
	"archive_pattern":  true,
	"data_source_type": true,
	"dropbox_folder":   true,
	"excel_sheet_name": true,
	"gs_bucket":        true,
	"gs_prefix":        true,
	"pattern":          true,
	s3AccessKey:        true,
	"s3_bucket":        true,
	"s3_endpoint_url":  true,
	"s3_prefix":        true,
	"s3_region_name":   true,
	"sftp_folder":      true,
	"sftp_host":        true,
	"sftp_port":        true,
	"sftp_user":        true,
	simpleURLKey:       true,
}

func prepareRepointSecrets(manifest *CloudSourceManifest, existing *CloudSourceFull,
	live map[string]json.RawMessage, changes []FieldChange, patch map[string]json.RawMessage,
) ([]string, error) {
	if !slices.ContainsFunc(changes, func(c FieldChange) bool { return repointKeys[c.Key] }) {
		return nil, nil
	}
	supplied, err := setFields(&manifest.Spec)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, key := range copySecretKeys {
		if key == dropboxAuthorizationCode {
			continue
		}
		stored := live[key]
		if key == legacyDropboxKey {
			key = dropboxAuthorizationCode
		}
		value := supplied[key]
		if !blankJSON(value) && !obfuscated(value) {
			// The backend clears all secrets before storing supplied values.
			patch[key] = value
		} else if !blankJSON(stored) {
			missing = append(missing, key)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	slices.Sort(missing)
	paths := make([]string, len(missing))
	for i, key := range missing {
		paths[i] = "spec." + key
	}
	name := terminal.Sanitize(manifest.Metadata.Name)
	if derefDataSourceState(existing.State) == api.DataSourceStateDraft {
		return []string{fmt.Sprintf("repointing draft cloud source %q clears omitted secrets: %s; supply them in the manifest to keep them", name, strings.Join(paths, ", "))}, nil
	}
	return nil, qerrors.NewBadUsagef("repointing cloud source %q requires every stored secret to be supplied again; missing: %s. "+
		"Set each in the manifest using ${VAR}, for example %s: ${%s}, then re-run apply",
		name, strings.Join(paths, ", "), paths[0], strings.ToUpper(missing[0]))
}
