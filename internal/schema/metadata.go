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
	qerrors "github.com/qlustered/qctl/internal/errors"
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

// FieldInfo contains metadata about a schema field. Empty keys are omitted.
type FieldInfo struct {
	Name             string      `json:"name,omitempty" yaml:"name,omitempty"`                         // Field name (JSON key)
	Type             string      `json:"type,omitempty" yaml:"type,omitempty"`                         // Type (string, integer, boolean, array, object, etc.)
	Format           string      `json:"format,omitempty" yaml:"format,omitempty"`                     // Format (uuid, uri, etc.)
	Description      string      `json:"description,omitempty" yaml:"description,omitempty"`           // Field description from OpenAPI spec
	Required         bool        `json:"required,omitempty" yaml:"required,omitempty"`                 // Whether the field is required
	ReadOnly         bool        `json:"readOnly,omitempty" yaml:"readOnly,omitempty"`                 // Whether the field is read-only
	WriteOnly        bool        `json:"writeOnly,omitempty" yaml:"writeOnly,omitempty"`               // Whether writes take the field and the read lacks it
	CreateOnly       bool        `json:"createOnly,omitempty" yaml:"createOnly,omitempty"`             // Whether apply takes the field only when it creates the resource
	CreateOnlyValues []string    `json:"createOnlyValues,omitempty" yaml:"createOnlyValues,omitempty"` // Accepted create-only values, when restricted
	Default          interface{} `json:"default,omitempty" yaml:"default,omitempty"`                   // Default value, if any
	Enum             []string    `json:"enum,omitempty" yaml:"enum,omitempty"`                         // Enum values, if applicable
	Nullable         bool        `json:"nullable,omitempty" yaml:"nullable,omitempty"`                 // Whether the field can be null
	Ref              string      `json:"ref,omitempty" yaml:"ref,omitempty"`                           // Name of the referenced schema, if applicable
	Items            *FieldInfo  `json:"items,omitempty" yaml:"items,omitempty"`                       // For arrays, the item type info
	Properties       []FieldInfo `json:"properties,omitempty" yaml:"properties,omitempty"`             // For objects, the nested properties
	AnyOf            []FieldInfo `json:"anyOf,omitempty" yaml:"anyOf,omitempty"`                       // For a union, its non-null alternatives
	OneOf            []FieldInfo `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`                       // As AnyOf, for a oneOf union
}

// SchemaInfo contains metadata about a schema, keyed as FieldInfo is.
type SchemaInfo struct { //nolint:revive // Established exported schema API.
	Name        string      `json:"name,omitempty" yaml:"name,omitempty"`               // Schema name
	Description string      `json:"description,omitempty" yaml:"description,omitempty"` // Schema description
	Type        string      `json:"type,omitempty" yaml:"type,omitempty"`               // Schema type (object, etc.)
	Fields      []FieldInfo `json:"properties,omitempty" yaml:"properties,omitempty"`   // All fields in the schema
	Required    []string    `json:"required,omitempty" yaml:"required,omitempty"`       // Required field names
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
		Required:    schema.Required,
	}
	if types := schema.Type.Slice(); len(types) > 0 {
		info.Type = types[0]
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

// MergeWriteSchema marks read's top-level fields against write, the schema
// requests take: a field write lacks becomes read-only, and each field only
// write has is added, write-only, keeping the fields in name order.
func MergeWriteSchema(read, write *SchemaInfo) {
	writable := map[string]bool{}
	for _, f := range write.Fields {
		writable[f.Name] = true
	}
	readable := map[string]bool{}
	for i := range read.Fields {
		readable[read.Fields[i].Name] = true
		if !writable[read.Fields[i].Name] {
			read.Fields[i].ReadOnly = true
		}
	}
	for _, f := range write.Fields {
		if !readable[f.Name] {
			f.WriteOnly = true
			read.Fields = append(read.Fields, f)
		}
	}
	sort.Slice(read.Fields, func(i, j int) bool {
		return read.Fields[i].Name < read.Fields[j].Name
	})
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
	currentRequired := schema.Required

	for i, part := range parts {
		propRef, ok := currentSchema.Properties[part]
		if !ok {
			return nil, qerrors.NewBadUsagef("field %q not found in schema (at %q)", part, strings.Join(parts[:i+1], "."))
		}

		isLast := i == len(parts)-1
		if isLast {
			field := extractFieldInfo(part, propRef, swagger, contains(currentRequired, part))
			return &field, nil
		}

		// Navigate into nested schema
		resolved := resolveRef(propRef, swagger)
		if branch, _, ok := nullableBranch(resolved, swagger); ok {
			resolved = resolveRef(branch, swagger)
		}
		if resolved == nil || (!resolved.Type.Includes("object") && len(resolved.Properties) == 0) {
			return nil, qerrors.NewBadUsagef("cannot navigate into non-object field %q", part)
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
//
// Nothing resolves through it at runtime any more: `qctl explain` reads the
// component out of the resource page's `schema:` frontmatter
// (internal/resources/pages/*.md). This map is what those pages are checked
// against — internal/catalog's TestFrontmatterReferences and
// TestSchemaMappingIsHonoredByPages assert both directions, so a page that
// points at the wrong component fails CI. It is therefore a frozen baseline,
// not dead code: change an entry only when a resource's component genuinely
// changed, and change the page in the same commit.
//
// Two entries are deliberately shadowed: data-review-policy and
// rule-review-policy are served from static manifest schemas compiled into the
// binary, as they were before pages existed.
var SchemaMapping = map[string]string{
	"table":                "DataSetSchemaFull",
	"table-config":         "SettingsSchema",
	"dataset":              "DataSetSchemaFull",
	"backup-settings":      "BackupSettingsSchemaFull",
	"cloud-source":         "DataSourceModelAllOptionalSchema",
	"destination":          "DestinationSchemaFull",
	"alert":                "AlertSchema",
	"warning":              "WarningSchema",
	"history":              "HistoryTinySchema",
	"file":                 "StoredItemFullSchema",
	"error-incident":       "ErrorIncidentSchema",
	"latest-alert":         "MenuStatsSchema",
	"notification":         "NotificationSchema",
	"standard-field":       "ConceptSchemaFull",
	"table-kind":           "ProblemDomainWithConceptsSchema",
	"table-series":         "TableSeriesSchema",
	"submission-status":    "SubmissionBoardRowSchema",
	"table-member":         "DataSetMemberSchema",
	"tables-overview":      "TablesOverviewListSchema",
	"soft-deleted-tables":  "SoftDeletedTablesReportSchema",
	"deliverable":          "DeliverableResponse",
	"deliverable-revision": "DeliverableRevisionResponse",
	"deliverable-run":      "DeliverableRunResponse",
	"deliverable-template": "SkeletonFinalizeResponse",
	"rule-upgrade":         "RuleFamilyUpgradeDetailsSchema",
	"rule-review-policy":   "ReviewPolicyRequestSchema",
	"data-review-policy":   "DataReviewPolicySchema",
	"linked-table":         "CompositeDatasetApplyManifest",
	"composite-dataset":    "CompositeDatasetApplyManifest",
	"queue-message":        "QueueSchema",
	"privilege-request":    "PrivilegeRequestDetailSchema",
	"pii-access-request":   "PiiAccessRequestSchema",
	"duplicate-group":      "DuplicateGroupDetailSchema",
	"pii-grant":            "PiiGrantSchema",
	"email-sender":         "EmailIntakeSenderSchema",
	"pii-reveal":           "PiiRevealSchema",
	"change-proposal":      "ChangeProposalDetailSchema",
	"table-copy":           "DatasetCopySchema",
	"rule-merge":           "RuleMergeSchema",
	"slack-config":         "OrgSlackConfig",
}

// extractFieldInfo extracts FieldInfo from an OpenAPI schema reference.
func extractFieldInfo(name string, ref *openapi3.SchemaRef, swagger *openapi3.T, required bool) FieldInfo {
	return extractField(name, ref, swagger, required, map[string]bool{})
}

// extractField is extractFieldInfo for a field nested inside the components
// named in expanding. A $ref to one of those comes back unexpanded, carrying
// only its name, required and ref.
func extractField(name string, ref *openapi3.SchemaRef, swagger *openapi3.T, required bool,
	expanding map[string]bool) FieldInfo {
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
		if expanding[field.Ref] {
			return field
		}
		expanding[field.Ref] = true
		defer delete(expanding, field.Ref)
	}

	schema := resolveRef(ref, swagger)
	if schema == nil {
		return field
	}

	// A nullable wrapper is its one non-null branch, described by the wrapper.
	if branch, nullable, ok := nullableBranch(schema, swagger); ok {
		inner := extractField(name, branch, swagger, required, expanding)
		return describedByWrapper(inner, field.Ref, schema, nullable)
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

	field.Enum = stringEnum(schema.Enum)

	// Handle arrays
	if field.Type == "array" && schema.Items != nil {
		itemInfo := extractField("items", schema.Items, swagger, false, expanding)
		field.Items = &itemInfo
	}

	// Handle objects (nested properties)
	// Check for properties even if type isn't explicitly "object" (common with $ref)
	if len(schema.Properties) > 0 {
		if field.Type == "" {
			field.Type = "object"
		}
		for propName, propRef := range schema.Properties {
			prop := extractField(propName, propRef, swagger, contains(schema.Required, propName), expanding)
			field.Properties = append(field.Properties, prop)
		}
		sort.Slice(field.Properties, func(i, j int) bool {
			return field.Properties[i].Name < field.Properties[j].Name
		})
	}

	extractUnion(&field, schema, swagger, expanding)

	return field
}

// describedByWrapper returns inner, the field a nullable wrapper resolved to,
// marked nullable and given the wrapper's description, default and readOnly
// where the wrapper sets them. ref names inner when inner has no ref of its own.
func describedByWrapper(inner FieldInfo, ref string, wrapper *openapi3.Schema, nullable bool) FieldInfo {
	if inner.Ref == "" {
		inner.Ref = ref
	}
	if wrapper.Description != "" {
		inner.Description = wrapper.Description
	}
	if wrapper.Default != nil {
		inner.Default = wrapper.Default
	}
	inner.Nullable = inner.Nullable || nullable || wrapper.Nullable
	inner.ReadOnly = inner.ReadOnly || wrapper.ReadOnly
	return inner
}

// stringEnum returns the string values of enum.
func stringEnum(enum []interface{}) []string {
	var values []string
	for _, v := range enum {
		if s, ok := v.(string); ok {
			values = append(values, s)
		}
	}
	return values
}

// extractUnion sets field's AnyOf or OneOf when alternatives finds two or more
// branches in schema, and marks field nullable when it dropped a {type: null}.
func extractUnion(field *FieldInfo, schema *openapi3.Schema, swagger *openapi3.T, expanding map[string]bool) {
	keyword, branches, nullable := alternatives(schema, swagger)
	if len(branches) < 2 {
		return
	}
	alts := make([]FieldInfo, len(branches))
	for i, branch := range branches {
		alts[i] = extractField("", branch, swagger, false, expanding)
	}
	if keyword == "oneOf" {
		field.OneOf = alts
	} else {
		field.AnyOf = alts
	}
	field.Nullable = field.Nullable || nullable
}

// alternatives returns the branches of a type-less schema's anyOf, or else its
// oneOf, other than {type: null}; the keyword they came from; and whether a
// {type: null} branch was dropped.
func alternatives(schema *openapi3.Schema, swagger *openapi3.T) (keyword string, branches openapi3.SchemaRefs, nullable bool) {
	if schema == nil || !schema.Type.IsEmpty() {
		return "", nil, false
	}
	keyword, all := "anyOf", schema.AnyOf
	if len(all) == 0 {
		keyword, all = "oneOf", schema.OneOf
	}
	for _, branch := range all {
		if s := resolveRef(branch, swagger); s != nil && s.Type.Is(openapi3.TypeNull) {
			nullable = true
			continue
		}
		branches = append(branches, branch)
	}
	return keyword, branches, nullable
}

// nullableBranch returns the only branch alternatives finds in schema, and
// whether alternatives dropped a {type: null} beside it. ok is false unless
// there is exactly one such branch.
func nullableBranch(schema *openapi3.Schema, swagger *openapi3.T) (branch *openapi3.SchemaRef, nullable, ok bool) {
	_, branches, nullable := alternatives(schema, swagger)
	if len(branches) != 1 {
		return nil, false, false
	}
	return branches[0], nullable, true
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
