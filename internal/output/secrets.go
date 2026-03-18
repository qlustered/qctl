package output

import (
	"reflect"
	"strings"
	"time"
)

// SecretMask handles masking of secret fields
type SecretMask struct {
	secretFields map[string]bool
}

// NewSecretMask creates a new SecretMask
func NewSecretMask() *SecretMask {
	return &SecretMask{
		secretFields: buildSecretFieldMap(),
	}
}

// IsSecret checks if a field name is a secret field
func (sm *SecretMask) IsSecret(fieldName string) bool {
	normalizedName := strings.ToLower(fieldName)
	return sm.secretFields[normalizedName]
}

// Mask applies masking to secret fields in data
func (sm *SecretMask) Mask(data interface{}) interface{} {
	return sm.maskValue(reflect.ValueOf(data)).Interface()
}

// maskValue recursively masks secret fields in a reflect.Value
func (sm *SecretMask) maskValue(v reflect.Value) reflect.Value {
	if !v.IsValid() {
		return v
	}

	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			// Return nil as interface{} for nil pointers
			return reflect.ValueOf(nil)
		}
		// Special handling for *time.Time - return formatted string
		timeType := reflect.TypeOf((*time.Time)(nil))
		if v.Type() == timeType || v.Type().String() == "*time.Time" {
			t := v.Interface().(*time.Time)
			return reflect.ValueOf(t.Format(time.RFC3339))
		}
		// For other pointers, dereference and recursively process
		return sm.maskValue(v.Elem())

	case reflect.Struct:
		// Special handling for time.Time - return formatted string
		timeType := reflect.TypeOf(time.Time{})
		if v.Type() == timeType {
			t := v.Interface().(time.Time)
			return reflect.ValueOf(t.Format(time.RFC3339))
		}
		// Also check if it's a named type based on time.Time
		if v.Type().String() == "time.Time" {
			t := v.Interface().(time.Time)
			return reflect.ValueOf(t.Format(time.RFC3339))
		}
		return sm.maskStruct(v)

	case reflect.Slice, reflect.Array:
		return sm.maskSlice(v)

	case reflect.Map:
		return sm.maskMap(v)

	default:
		return v
	}
}

// maskStruct masks secret fields in a struct by converting to a map
// This preserves JSON tag names in YAML output
func (sm *SecretMask) maskStruct(v reflect.Value) reflect.Value {
	t := v.Type()

	// Convert struct to map to preserve JSON tag names
	resultMap := make(map[string]interface{})

	for i := 0; i < v.NumField(); i++ {
		field := t.Field(i)
		value := v.Field(i)

		// Skip unexported fields
		if !field.IsExported() {
			continue
		}

		// Get JSON tag
		jsonTag := field.Tag.Get("json")
		if jsonTag == "-" {
			continue
		}

		// Parse JSON tag
		var fieldName string
		if jsonTag != "" {
			parts := strings.Split(jsonTag, ",")
			fieldName = parts[0]

			// Check for omitempty and skip if value is zero
			if len(parts) > 1 {
				for _, opt := range parts[1:] {
					if opt == "omitempty" && isZeroValue(value) {
						continue
					}
				}
			}
		} else {
			fieldName = field.Name
		}

		// Check if this is a secret field
		if sm.IsSecret(fieldName) {
			resultMap[fieldName] = "***"
		} else {
			// Recursively mask nested structures
			maskedValue := sm.maskValue(value)
			if maskedValue.IsValid() && maskedValue.CanInterface() {
				resultMap[fieldName] = maskedValue.Interface()
			}
		}
	}

	return reflect.ValueOf(resultMap)
}

// isZeroValue checks if a value is the zero value for its type
func isZeroValue(v reflect.Value) bool {
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

// maskSlice masks secret fields in a slice
func (sm *SecretMask) maskSlice(v reflect.Value) reflect.Value {
	// Create a slice of interface{} to handle heterogeneous types
	// (e.g., when structs are converted to maps)
	result := make([]interface{}, v.Len())
	for i := 0; i < v.Len(); i++ {
		maskedValue := sm.maskValue(v.Index(i))
		if maskedValue.IsValid() && maskedValue.CanInterface() {
			result[i] = maskedValue.Interface()
		}
	}
	return reflect.ValueOf(result)
}

// maskMap masks secret fields in a map
func (sm *SecretMask) maskMap(v reflect.Value) reflect.Value {
	result := reflect.MakeMap(v.Type())
	for _, key := range v.MapKeys() {
		value := v.MapIndex(key)

		// Check if the key is a secret field (for string keys)
		if key.Kind() == reflect.String && sm.IsSecret(key.String()) {
			result.SetMapIndex(key, reflect.ValueOf("***"))
		} else {
			result.SetMapIndex(key, sm.maskValue(value))
		}
	}
	return result
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
