package cloud_sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/secretmask"
)

// keptWhenOmitted holds the nullableOnWire keys a manifest that leaves them
// out keeps rather than clears: the columns of schedule and source_time_zone
// are NOT NULL, a series source refuses a null table_series_id or
// routing_mode, and only ops may change preload_row_drop_share, which a new
// source stores as 0.2.
var keptWhenOmitted = map[string]bool{
	"schedule":               true,
	"source_time_zone":       true,
	"table_series_id":        true,
	"routing_mode":           true,
	"preload_row_drop_share": true,
}

// secretMask is a value a cloud source read carries in place of a credential.
const secretMask = "**********"

var (
	// createIdentityKeys are the identity wire keys of a create request.
	createIdentityKeys = []string{"name", "state", "data_source_type", "settings_model_id", "dataset_id"}
	// fieldKeys is the wire key of every CloudSourceFields field, in
	// declaration order.
	fieldKeys = jsonKeys(reflect.TypeOf(CloudSourceFields{}))
	// overrideNames is the key of every SettingsOverrides field, in declaration
	// order. The wire key is the name followed by overrideSuffix.
	overrideNames = jsonKeys(reflect.TypeOf(SettingsOverrides{}))
	// nullableOnWire holds every key api.DataSourceModelFullSchema declares
	// as a pointer without omitempty.
	nullableOnWire = nullableKeys(reflect.TypeOf(api.DataSourceModelFullSchema{}))

	secrets = secretmask.New()
)

// clearedWhenOmitted reports whether a manifest that leaves out the
// CloudSourceFields field key asks for null.
func clearedWhenOmitted(key string) bool {
	return !keptWhenOmitted[key] && nullableOnWire[key]
}

// ClearedWhenOmittedKeys returns, sorted, the spec keys a CloudSource
// manifest's update sets to null when it leaves them out.
func ClearedWhenOmittedKeys() []string {
	var keys []string
	for _, key := range fieldKeys {
		if clearedWhenOmitted(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// isSecret reports whether key names a credential.
func isSecret(key string) bool {
	return secrets.IsSecret(key)
}

// CredentialFields returns a pointer to each *string field of f whose key
// names a credential or URL.
func CredentialFields(f *CloudSourceFields) []**string {
	v := reflect.ValueOf(f).Elem()
	var fields []**string
	for i, key := range fieldKeys {
		if p, ok := v.Field(i).Addr().Interface().(**string); ok && isCopyCredential(key) {
			fields = append(fields, p)
		}
	}
	return fields
}

// obfuscated reports whether value is a JSON string that equals secretMask or
// contains '•'.
func obfuscated(value json.RawMessage) bool {
	var s string
	return json.Unmarshal(value, &s) == nil && (s == secretMask || strings.ContainsRune(s, '•'))
}

// exportedFields returns the members of a cloud source read that a manifest's
// CloudSourceFields carries: not null, not a secret, and not an empty string
// in a field whose omission keeps the stored value.
func exportedFields(members map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, key := range fieldKeys {
		value, ok := members[key]
		if !ok || isNull(value) || isSecret(key) {
			continue
		}
		if !clearedWhenOmitted(key) && bytes.Equal(value, []byte(`""`)) {
			continue
		}
		out[key] = value
	}
	return out
}

// exportedOverrides returns the non-null overrides of a cloud source read,
// keyed by override name.
func exportedOverrides(members map[string]json.RawMessage) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for _, name := range overrideNames {
		value, ok := members[name+overrideSuffix]
		if !ok || isNull(value) {
			continue
		}
		out[name] = value
	}
	return out
}

// decodeMembers decodes members as one JSON object into dst.
func decodeMembers(members map[string]json.RawMessage, dst any) error {
	raw, err := json.Marshal(members)
	if err != nil {
		return err
	}
	return jsonnum.Unmarshal(raw, dst)
}

// encodeMembers encodes v as a JSON object and returns its members. A v that
// encodes as null has none.
func encodeMembers(v any) (map[string]json.RawMessage, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	members := map[string]json.RawMessage{}
	if isNull(raw) {
		return members, nil
	}
	if err := json.Unmarshal(raw, &members); err != nil {
		return nil, err
	}
	return members, nil
}

// createBodyFromMembers encodes writable, non-null fields, overrides and identity.
func createBodyFromMembers(fields, overrides, identity map[string]json.RawMessage) ([]byte, error) {
	members := map[string]json.RawMessage{}
	add := func(key string, value json.RawMessage) {
		if len(value) > 0 && !isNull(value) {
			members[key] = value
		}
	}
	for _, key := range fieldKeys {
		add(key, fields[key])
	}
	for _, name := range overrideNames {
		add(name+overrideSuffix, overrides[name])
	}
	for _, key := range createIdentityKeys {
		add(key, identity[key])
	}
	body, err := json.Marshal(members)
	if err != nil {
		return nil, fmt.Errorf("failed to encode cloud source: %w", err)
	}
	return body, nil
}

func isNull(value json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func jsonKeys(t reflect.Type) []string {
	keys := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		key, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		keys = append(keys, key)
	}
	return keys
}

func nullableKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := range t.NumField() {
		f := t.Field(i)
		key, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if f.Type.Kind() == reflect.Pointer && !strings.Contains(opts, "omitempty") {
			keys[key] = true
		}
	}
	return keys
}
