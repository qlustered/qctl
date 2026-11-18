package cloud_sources

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// ParseCopyFields parses --set values into the field members of a copy request.
// String fields take literal text; other fields take YAML.
func ParseCopyFields(values []string) (map[string]json.RawMessage, error) {
	if len(values) == 0 {
		return nil, nil
	}
	fields := make(map[string]json.RawMessage, len(values))
	typ := reflect.TypeOf(CloudSourceFields{})
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, qerrors.NewBadUsage("invalid --set: expected key=value")
		}
		key = strings.ReplaceAll(strings.TrimSpace(key), "-", "_")
		if err := validateCopyFieldKey(key); err != nil {
			return nil, err
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, qerrors.NewBadUsagef("invalid --set: %s is given more than once", key)
		}
		fieldType := typ.Field(slices.Index(fieldKeys, key)).Type
		member, err := decodeCopyField(value, fieldType)
		if err != nil {
			return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "invalid --set "+key)
		}
		fields[key] = member
	}
	return fields, nil
}

func validateCopyFieldKey(key string) error {
	if isCopyCredential(key) {
		return copyCredentialRefusal(key)
	}
	if slices.Contains(createIdentityKeys, key) {
		remedy := "the copy keeps the source's type"
		switch key {
		case "dataset_id", "settings_model_id":
			remedy = "use --to-table or --to-table-id"
		case "name":
			remedy = "use --name"
		case "state":
			remedy = "use --state"
		}
		return qerrors.NewBadUsagef("invalid --set: %s is an identity field; %s", key, remedy)
	}
	if !slices.Contains(fieldKeys, key) {
		return qerrors.NewBadUsagef("invalid --set: unknown field %q; run qctl explain cloud-source for field names", key)
	}
	return nil
}

// copyCredentialRefusal refuses --set of the credential or URL key, naming
// where a copy takes it, if anywhere.
func copyCredentialRefusal(key string) error {
	switch {
	case slices.Contains(copyRequestSecretKeys, key):
		return qerrors.NewBadUsagef("invalid --set: %s is a credential or URL; supply it in --secrets-file", key)
	case key == legacyDropboxKey:
		return qerrors.NewBadUsagef("invalid --set: a copy cannot take %s; supply %s in --secrets-file",
			key, dropboxAuthorizationCode)
	default:
		return qerrors.NewBadUsagef("invalid --set: %s is a credential; a copy cannot take it", key)
	}
}

func decodeCopyField(text string, typ reflect.Type) (json.RawMessage, error) {
	value := reflect.New(typ)
	if typ.Elem().Kind() == reflect.String {
		literal := reflect.New(typ.Elem())
		literal.Elem().SetString(text)
		value.Elem().Set(literal)
	} else {
		if text == "" {
			return nil, fmt.Errorf("an empty value is only allowed for a string field")
		}
		if err := singleCopyFieldDocument(text); err != nil {
			return nil, err
		}
		if err := pkgmanifest.StrictUnmarshal([]byte(text), value.Interface()); err != nil {
			return nil, err
		}
	}
	member, err := json.Marshal(value.Interface())
	if err != nil {
		return nil, fmt.Errorf("failed to encode field: %w", err)
	}
	if isNull(member) {
		return nil, fmt.Errorf("value must not be null")
	}
	return member, nil
}

func singleCopyFieldDocument(text string) error {
	decoder := yaml.NewDecoder(strings.NewReader(text))
	var node yaml.Node
	if err := decoder.Decode(&node); err != nil {
		return fmt.Errorf("failed to decode field: %w", err)
	}
	if err := decoder.Decode(&node); err != io.EOF {
		if err != nil {
			return fmt.Errorf("failed to decode field: %w", err)
		}
		return fmt.Errorf("value must be one YAML document")
	}
	return nil
}
