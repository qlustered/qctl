package secretmask

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

const maskedValue = "***"

// SecretMask handles masking of secret fields
type SecretMask struct {
	secretFields map[string]bool
}

// New creates a mask for the built-in secret fields and any additional names.
func New(additionalFields ...string) *SecretMask {
	fields := buildSecretFieldMap()
	for _, name := range additionalFields {
		fields[strings.ToLower(name)] = true
	}
	return &SecretMask{secretFields: fields}
}

// IsSecret checks if a field name is a secret field
func (sm *SecretMask) IsSecret(fieldName string) bool {
	normalizedName := strings.ToLower(fieldName)
	return sm.secretFields[normalizedName]
}

// Mask returns data as the plain values Plain does, with the value of every
// secret field or map key replaced by "***".
func (sm *SecretMask) Mask(data interface{}) interface{} {
	return sm.plainValue(reflect.ValueOf(data), true)
}

// Plain returns data as the maps, slices and scalars encoding/json would
// write for it: embedded fields are promoted, JSON tags and omitempty
// are honored, and a value with a MarshalJSON or MarshalText method takes the
// shape that method gives it. A value whose method fails becomes one that
// encoding/json and yaml.v3 both refuse to encode, with that failure.
// Timestamps are RFC3339 strings.
func (sm *SecretMask) Plain(data interface{}) interface{} {
	return sm.plainValue(reflect.ValueOf(data), false)
}

var (
	timeType          = reflect.TypeOf(time.Time{})
	jsonMarshalerType = reflect.TypeOf((*json.Marshaler)(nil)).Elem()
	textMarshalerType = reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem()
)

func (sm *SecretMask) plainValue(v reflect.Value, mask bool) interface{} {
	if !v.IsValid() {
		return nil
	}

	// Ahead of the marshaler check: time.Time's own MarshalJSON is RFC3339Nano.
	if v.Type() == timeType {
		return v.Interface().(time.Time).Format(time.RFC3339)
	}
	if v.Kind() == reflect.Ptr || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return sm.plainValue(v.Elem(), mask)
	}

	if marshaler, ok := customMarshaler(v); ok {
		decoded, ok := jsonnum.Remarshal(marshaler.Interface())
		if !ok {
			return decoded
		}
		return sm.plainValue(reflect.ValueOf(decoded), mask)
	}

	switch v.Kind() {
	case reflect.Struct:
		return sm.plainStruct(v, mask)
	case reflect.Slice, reflect.Array:
		result := make([]interface{}, v.Len())
		for i := 0; i < v.Len(); i++ {
			result[i] = sm.plainValue(v.Index(i), mask)
		}
		return result
	case reflect.Map:
		result := make(map[string]interface{}, v.Len())
		for _, key := range v.MapKeys() {
			name := fmt.Sprintf("%v", key.Interface())
			if mask && sm.IsSecret(name) {
				result[name] = maskedValue
			} else {
				result[name] = sm.plainValue(v.MapIndex(key), mask)
			}
		}
		return result
	default:
		return v.Interface()
	}
}

// customMarshaler returns v when its type has a MarshalJSON or MarshalText
// method, else v's address when that has one.
func customMarshaler(v reflect.Value) (reflect.Value, bool) {
	if hasCustomMarshaler(v) {
		return v, true
	}
	if v.CanAddr() && hasCustomMarshaler(v.Addr()) {
		return v.Addr(), true
	}
	return reflect.Value{}, false
}

func hasCustomMarshaler(v reflect.Value) bool {
	return v.CanInterface() &&
		(v.Type().Implements(jsonMarshalerType) || v.Type().Implements(textMarshalerType))
}

func (sm *SecretMask) plainStruct(v reflect.Value, mask bool) map[string]interface{} {
	result := make(map[string]interface{})
	for _, field := range cachedStructFields(v.Type()) {
		value := fieldByIndex(v, field.index)
		if !value.IsValid() || (field.omitEmpty && isEmptyValue(value)) {
			continue
		}
		if mask && sm.IsSecret(field.name) {
			result[field.name] = maskedValue
		} else {
			result[field.name] = sm.plainValue(value, mask)
		}
	}

	return result
}

// isEmptyValue reports whether omitempty drops v, by encoding/json's rule.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

// buildSecretFieldMap builds the map of secret field names
func buildSecretFieldMap() map[string]bool {
	// Hardcoded list of secret field names based on OpenAPI spec and common patterns
	secretFields := []string{
		// Common password/token fields
		"password",
		"access_token",
		"refresh_token",
		"token",
		"secret",
		"api_key",
		"apikey",
		"private_key",
		"privatekey",

		// Database credentials
		"db_password",
		"database_password",
		"postgres_password",
		"mysql_password",

		// S3/Cloud credentials
		"aws_secret_access_key",
		"aws_access_key_id",
		"s3_secret_key",
		"s3_access_key",
		"azure_storage_key",
		"gcp_credentials",
		"service_account_key",
		"gs_service_account_key",

		// Cloud source credentials
		"sftp_password",
		"sftp_ssh_key",
		"sftp_ssh_key_passphrase",
		"dropbox_access_token",
		"dropbox_authorization_code",
		"gpg_private_key",
		"gpg_passphrase",
		"file_password",

		// API/Service credentials
		"client_secret",
		"oauth_secret",
		"webhook_secret",
		"signing_secret",

		// Encryption keys
		"encryption_key",
		"cipher_key",
		"master_key",

		// Connection strings (may contain passwords)
		"connection_string",
		"conn_string",
		"dsn",
	}

	m := make(map[string]bool, len(secretFields))
	for _, field := range secretFields {
		m[strings.ToLower(field)] = true
	}

	return m
}
