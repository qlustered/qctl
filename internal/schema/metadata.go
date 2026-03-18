// Package schema provides runtime access to OpenAPI schema metadata.
// It fetches the OpenAPI spec from the API server and caches it locally
// to provide field descriptions, types, constraints, and enum values for CLI commands.
package schema

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/qlustered/qctl/internal/schema/cache"
)

// singleton instance of the OpenAPI spec and fetcher
var (
	specOnce    sync.Once
	spec        *openapi3.T
	specErr     error
	specVersion string
	fetcher     *cache.Fetcher
	fetcherLock sync.RWMutex
)

// SetAPIEndpoint configures the API endpoint for fetching the OpenAPI spec.
// This must be called before any schema operations that require the spec.
func SetAPIEndpoint(baseURL string) {
	fetcherLock.Lock()
	defer fetcherLock.Unlock()
	fetcher = cache.NewFetcher(baseURL)
	// Reset the spec so it will be re-fetched with the new endpoint
	specOnce = sync.Once{}
	spec = nil
	specErr = nil
	specVersion = ""
}

// getSpec returns the parsed OpenAPI spec, fetching and caching it on first access.
func getSpec() (*openapi3.T, error) {
	specOnce.Do(func() {
		fetcherLock.RLock()
		f := fetcher
		fetcherLock.RUnlock()

		if f == nil {
			specErr = fmt.Errorf("API endpoint not configured; call SetAPIEndpoint first")
			return
		}

		spec, specVersion, specErr = f.GetOrFetch(context.Background())
	})
	return spec, specErr
}

// GetSpecVersion returns the version of the currently loaded spec.
func GetSpecVersion() string {
	return specVersion
}

// FieldInfo contains metadata about a schema field.
type FieldInfo struct {
	Name        string      // Field name (JSON key)
	Type        string      // Type (string, integer, boolean, array, object, etc.)
	Format      string      // Format (uuid, uri, etc.)
	Description string      // Field description from OpenAPI spec
	Required    bool        // Whether the field is required
	ReadOnly    bool        // Whether the field is read-only
	Default     interface{} // Default value, if any
	Enum        []string    // Enum values, if applicable
	Nullable    bool        // Whether the field can be null
	Ref         string      // Reference to another schema, if applicable
	Items       *FieldInfo  // For arrays, the item type info
	Properties  []FieldInfo // For objects, the nested properties
}

// SchemaInfo contains metadata about a schema.
type SchemaInfo struct {
	Name        string      // Schema name
	Description string      // Schema description
	Type        string      // Schema type (object, etc.)
	Fields      []FieldInfo // All fields in the schema
	Required    []string    // Required field names
}

// GetSchema returns metadata for a named schema.
// Supports schema names like "DataSetSchemaFull", "DataSetPostRequestSchema", etc.
func GetSchema(name string) (*SchemaInfo, error) {
	swagger, err := getSpec()
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	schemaRef, ok := swagger.Components.Schemas[name]
	if !ok {
		return nil, fmt.Errorf("schema %q not found", name)
	}

	schema := schemaRef.Value
	if schema == nil {
		return nil, fmt.Errorf("schema %q has no value", name)
	}

	info := &SchemaInfo{
		Name:        name,
		Description: schema.Description,
		Type:        schema.Type.Slice()[0],
		Required:    schema.Required,
	}

	// Extract fields
	for propName, propRef := range schema.Properties {
		field := extractFieldInfo(propName, propRef, swagger, contains(schema.Required, propName))
		info.Fields = append(info.Fields, field)
	}

	// Sort fields alphabetically for consistent output
	sort.Slice(info.Fields, func(i, j int) bool {
		return info.Fields[i].Name < info.Fields[j].Name
	})

	return info, nil
}

// GetFieldProps returns metadata for a specific field within a schema.
// The path can be dot-separated for nested fields (e.g., "settings_model.field_name").
func GetFieldProps(schemaName, fieldPath string) (*FieldInfo, error) {
	swagger, err := getSpec()
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	schemaRef, ok := swagger.Components.Schemas[schemaName]
	if !ok {
		return nil, fmt.Errorf("schema %q not found", schemaName)
	}

	schema := schemaRef.Value
	if schema == nil {
		return nil, fmt.Errorf("schema %q has no value", schemaName)
	}

	parts := strings.Split(fieldPath, ".")
	currentSchema := schema
	var currentRequired []string = schema.Required

	for i, part := range parts {
		propRef, ok := currentSchema.Properties[part]
		if !ok {
			return nil, fmt.Errorf("field %q not found in schema (at %q)", part, strings.Join(parts[:i+1], "."))
		}

		isLast := i == len(parts)-1
		if isLast {
			field := extractFieldInfo(part, propRef, swagger, contains(currentRequired, part))
			return &field, nil
		}

		// Navigate into nested schema
		resolved := resolveRef(propRef, swagger)
		if resolved == nil || resolved.Type.Slice()[0] != "object" {
			return nil, fmt.Errorf("cannot navigate into non-object field %q", part)
		}
		currentSchema = resolved
		currentRequired = resolved.Required
	}

	return nil, fmt.Errorf("field path %q not found", fieldPath)
}

// GetEnumValues returns the allowed enum values for a field.
func GetEnumValues(schemaName, fieldPath string) ([]string, error) {
	field, err := GetFieldProps(schemaName, fieldPath)
	if err != nil {
		return nil, err
	}
	return field.Enum, nil
}

// ListSchemas returns all available schema names.
func ListSchemas() ([]string, error) {
	swagger, err := getSpec()
	if err != nil {
		return nil, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	var names []string
	for name := range swagger.Components.Schemas {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// SchemaMapping maps CLI resource names to OpenAPI schema names.
var SchemaMapping = map[string]string{
	"table":          "DataSetSchemaFull",
	"dataset":        "DataSetSchemaFull",
	"cloud-source":   "DataSourceModel",
	"destination":    "Destination",
	"alert":          "AlertDetailedSchema",
	"warning":        "WarningSchema",
	"file":           "StoredItemSchemaFull",
	"error-incident": "ErrorIncidentSchema",
}

// GetSchemaForResource returns the schema info for a CLI resource name.
func GetSchemaForResource(resource string) (*SchemaInfo, error) {
	schemaName, ok := SchemaMapping[strings.ToLower(resource)]
	if !ok {
		return nil, fmt.Errorf("unknown resource %q", resource)
	}
	return GetSchema(schemaName)
}

// extractFieldInfo extracts FieldInfo from an OpenAPI schema reference.
func extractFieldInfo(name string, ref *openapi3.SchemaRef, swagger *openapi3.T, required bool) FieldInfo {
	field := FieldInfo{
		Name:     name,
		Required: required,
	}

	if ref == nil {
		return field
	}

	// Handle $ref
	if ref.Ref != "" {
		field.Ref = extractRefName(ref.Ref)
	}

	schema := resolveRef(ref, swagger)
	if schema == nil {
		return field
	}

	// Extract type
	types := schema.Type.Slice()
	if len(types) > 0 {
		field.Type = types[0]
	}
	field.Format = schema.Format
	field.Description = schema.Description
	field.Nullable = schema.Nullable
	field.Default = schema.Default
	field.ReadOnly = schema.ReadOnly

	// Extract enum values
	if len(schema.Enum) > 0 {
		for _, v := range schema.Enum {
			if s, ok := v.(string); ok {
				field.Enum = append(field.Enum, s)
			}
		}
	}

	// Handle arrays
	if field.Type == "array" && schema.Items != nil {
		itemInfo := extractFieldInfo("items", schema.Items, swagger, false)
		field.Items = &itemInfo
	}

	// Handle objects (nested properties)
	// Check for properties even if type isn't explicitly "object" (common with $ref)
	if len(schema.Properties) > 0 {
		if field.Type == "" {
			field.Type = "object"
		}
		for propName, propRef := range schema.Properties {
			prop := extractFieldInfo(propName, propRef, swagger, contains(schema.Required, propName))
			field.Properties = append(field.Properties, prop)
		}
		sort.Slice(field.Properties, func(i, j int) bool {
			return field.Properties[i].Name < field.Properties[j].Name
		})
	}

	return field
}

// resolveRef resolves a schema reference to its underlying schema.
func resolveRef(ref *openapi3.SchemaRef, swagger *openapi3.T) *openapi3.Schema {
	if ref == nil {
		return nil
	}
	if ref.Value != nil {
		return ref.Value
	}
	if ref.Ref != "" {
		refName := extractRefName(ref.Ref)
		if resolved, ok := swagger.Components.Schemas[refName]; ok && resolved.Value != nil {
			return resolved.Value
		}
	}
	return nil
}

// extractRefName extracts the schema name from a $ref path.
// e.g., "#/components/schemas/MigrationPolicy" -> "MigrationPolicy"
func extractRefName(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ref
}

// contains checks if a string slice contains a value.
func contains(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// CompareSchemas compares two schemas and returns fields that are in the first but not the second.
// This is useful for identifying read-only fields (present in Full but not in Post/Patch).
func CompareSchemas(fullSchemaName, writeSchemaName string) ([]string, error) {
	fullSchema, err := GetSchema(fullSchemaName)
	if err != nil {
		return nil, fmt.Errorf("failed to get full schema: %w", err)
	}

	writeSchema, err := GetSchema(writeSchemaName)
	if err != nil {
		return nil, fmt.Errorf("failed to get write schema: %w", err)
	}

	writeFields := make(map[string]bool)
	for _, f := range writeSchema.Fields {
		writeFields[f.Name] = true
	}

	var readOnlyFields []string
	for _, f := range fullSchema.Fields {
		if !writeFields[f.Name] {
			readOnlyFields = append(readOnlyFields, f.Name)
		}
	}

	sort.Strings(readOnlyFields)
	return readOnlyFields, nil
}

// GetDatasetReadOnlyFields returns the fields that are read-only for datasets.
// These are fields present in DataSetSchemaFull but not in DataSetPostRequestSchema.
func GetDatasetReadOnlyFields() ([]string, error) {
	return CompareSchemas("DataSetSchemaFull", "DataSetPostRequestSchema")
}

// IsFieldInSchema checks if a field exists in a schema.
func IsFieldInSchema(schemaName, fieldName string) (bool, error) {
	swagger, err := getSpec()
	if err != nil {
		return false, fmt.Errorf("failed to load OpenAPI spec: %w", err)
	}

	schemaRef, ok := swagger.Components.Schemas[schemaName]
	if !ok {
		return false, fmt.Errorf("schema %q not found", schemaName)
	}

	schema := schemaRef.Value
	if schema == nil {
		return false, fmt.Errorf("schema %q has no value", schemaName)
	}

	_, exists := schema.Properties[fieldName]
	return exists, nil
}

// GetQuickCreateFields returns the fields available for quick dataset creation.
func GetQuickCreateFields() ([]string, error) {
	schema, err := GetSchema("DataSetCreateQuickPostRequest")
	if err != nil {
		return nil, err
	}
	var fields []string
	for _, f := range schema.Fields {
		fields = append(fields, f.Name)
	}
	return fields, nil
}
