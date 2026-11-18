package cloud_sources

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
)

// CopyOverrides holds the name, state, field and credential members for a
// copy request.
type CopyOverrides struct {
	// Name replaces the template's name when non-nil.
	Name *string
	// State is the caller-selected state.
	State  DataSourceState
	Fields map[string]json.RawMessage
	// Secrets holds credential and URL values by wire key, each key one that
	// CopyRequestSecretKeys returns.
	Secrets map[string]string
}

// BuildCopyBody builds a copy request from the template and overrides. The
// only credential or URL members it carries are the template's s3_access_key
// and the overrides.Secrets entries, an entry replacing the template's value.
// A Secrets key that CopyRequestSecretKeys does not return is an error.
func BuildCopyBody(template []byte, overrides CopyOverrides) ([]byte, error) {
	supplied, err := copySecretMembers(overrides.Secrets)
	if err != nil {
		return nil, err
	}
	members, err := nonNullMembers(template)
	if err != nil {
		return nil, fmt.Errorf("failed to decode cloud source copy template: %w", err)
	}
	if members == nil {
		return nil, fmt.Errorf("cloud source copy template must be a JSON object")
	}
	settings := exportedOverrides(members)
	identity := map[string]json.RawMessage{}
	for _, key := range createIdentityKeys {
		identity[key] = members[key]
	}
	selected, err := encodeMembers(struct {
		Name  *string         `json:"name,omitempty"`
		State DataSourceState `json:"state"`
	}{Name: overrides.Name, State: overrides.State})
	if err != nil {
		return nil, fmt.Errorf("failed to encode cloud source copy identity: %w", err)
	}
	for key, value := range selected {
		identity[key] = value
	}
	accessKey, hasAccessKey := members[s3AccessKey]
	for key, value := range overrides.Fields {
		members[key] = value
	}
	for key := range members {
		if isCopyCredential(key) {
			delete(members, key)
		}
	}
	if hasAccessKey {
		members[s3AccessKey] = accessKey
	}
	maps.Copy(members, supplied)
	return createBodyFromMembers(members, settings, identity)
}

// copySecretMembers encodes each of secrets as a JSON string member, failing
// on a key that CopyRequestSecretKeys does not return.
func copySecretMembers(secrets map[string]string) (map[string]json.RawMessage, error) {
	members := make(map[string]json.RawMessage, len(secrets))
	for _, key := range slices.Sorted(maps.Keys(secrets)) {
		if !slices.Contains(copyRequestSecretKeys, key) {
			return nil, fmt.Errorf("cloud source copy cannot take secret %q", key)
		}
		value, err := json.Marshal(secrets[key])
		if err != nil {
			return nil, fmt.Errorf("failed to encode cloud source copy secret %s: %w", key, err)
		}
		members[key] = value
	}
	return members, nil
}

func isCopyCredential(key string) bool {
	return isSecret(key) || key == simpleURLKey
}

// RedactCopyTemplate removes credential and URL fields from a copy template.
func RedactCopyTemplate(template []byte) ([]byte, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(template, &members); err != nil {
		return nil, fmt.Errorf("failed to decode cloud source copy template: %w", err)
	}
	if members == nil {
		return nil, fmt.Errorf("cloud source copy template must be a JSON object")
	}
	for key := range members {
		if isCopyCredential(key) {
			delete(members, key)
		}
	}
	return json.Marshal(members)
}
