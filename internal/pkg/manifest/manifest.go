package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// API version constants for manifest schemas
const (
	// APIVersionV1 is the current manifest schema version
	APIVersionV1 = "qluster.ai/v1"
)

// Metadata holds resource metadata
type Metadata struct {
	Labels      map[string]string `yaml:"labels,omitempty" json:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty" json:"annotations,omitempty"`
	Name        string            `yaml:"name,omitempty" json:"name,omitempty"`
	Slug        string            `yaml:"slug,omitempty" json:"slug,omitempty"`
	ID          string            `yaml:"id,omitempty" json:"id,omitempty"`
}

// EnvelopeMetadata is a Manifest's metadata block: Metadata plus the keys only
// some kinds take.
type EnvelopeMetadata struct {
	Metadata `yaml:",inline"`
	// InstanceName is a TableRule's metadata.instance_name.
	InstanceName *string `yaml:"instance_name,omitempty" json:"instance_name,omitempty"`
}

// Manifest is the universal wrapper for all resources
type Manifest = envelope[EnvelopeMetadata]

// envelope is the wrapper every manifest shares, over the metadata block M.
type envelope[M any] struct {
	Spec       interface{} `yaml:"spec" json:"spec"`
	Metadata   M           `yaml:"metadata" json:"metadata"`
	Kind       string      `yaml:"kind" json:"kind"`
	APIVersion string      `yaml:"apiVersion" json:"apiVersion"`
	Status     interface{} `yaml:"status,omitempty" json:"status,omitempty"`
}

// kindsWithInstanceName lists the kinds whose metadata may carry instance_name.
var kindsWithInstanceName = map[string]bool{
	"TableRule": true,
}

// ValidationError represents a manifest validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation error: %s: %s", e.Field, e.Message)
}

// StrictUnmarshal parses YAML or JSON and rejects unknown fields.
func StrictUnmarshal(data []byte, v interface{}) error {
	if isJSONDocument(data) {
		if err := validateJSONFields(data, reflect.TypeOf(v)); err != nil {
			return fmt.Errorf("JSON parsing failed: %w", err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		if err := decoder.Decode(v); err != nil {
			return fmt.Errorf("JSON parsing failed: %w", err)
		}
		return nil
	}
	if err := validateYAMLFields(data, v); err != nil {
		return err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return formatYAMLError(err)
	}
	if err := ValidateYAMLIntegers(&node, v); err != nil {
		return err
	}
	if err := rejectYAMLNullStringElements(&node, reflect.TypeOf(v), ""); err != nil {
		return err
	}
	stringifyLooseTimestamps(&node, reflect.TypeOf(v))
	if err := node.Decode(v); err != nil {
		return formatYAMLError(err)
	}
	return nil
}

func validateYAMLFields(data []byte, v interface{}) error {
	typ := reflect.TypeOf(v)
	if typ == nil || typ.Kind() != reflect.Pointer || typ.Elem().Kind() == reflect.Invalid {
		return fmt.Errorf("manifest destination must be a pointer")
	}
	check := reflect.New(typ.Elem()).Interface()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	if err := decoder.Decode(check); err != nil {
		if err == io.EOF {
			return fmt.Errorf("empty YAML document")
		}
		// Enhance error messages for common issues
		return formatYAMLError(err)
	}

	return nil
}

func stringifyLooseTimestamps(node *yaml.Node, typ reflect.Type) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() == reflect.Interface && typ.NumMethod() == 0 {
		stringifyTimestamps(node)
		return
	}
	switch typ.Kind() {
	case reflect.Struct:
		stringifyStructTimestamps(node, typ)
	case reflect.Map:
		if node.Kind == yaml.MappingNode {
			for i := 1; i < len(node.Content); i += 2 {
				stringifyLooseTimestamps(node.Content[i], typ.Elem())
			}
		}
	case reflect.Slice, reflect.Array:
		if node.Kind == yaml.SequenceNode {
			for _, child := range node.Content {
				stringifyLooseTimestamps(child, typ.Elem())
			}
		}
	}
}

func stringifyStructTimestamps(node *yaml.Node, typ reflect.Type) {
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		stringifyStructTimestamps(node.Content[0], typ)
		return
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		parts := strings.Split(field.Tag.Get("yaml"), ",")
		if len(parts) > 1 && slices.Contains(parts[1:], "inline") {
			stringifyLooseTimestamps(node, field.Type)
			continue
		}
		name := parts[0]
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		for j := 0; j < len(node.Content); j += 2 {
			if node.Content[j].Value == name {
				stringifyLooseTimestamps(node.Content[j+1], field.Type)
			}
		}
	}
}

func stringifyTimestamps(node *yaml.Node) {
	stringifyTimestampsSeen(node, make(map[*yaml.Node]bool))
}

func stringifyTimestampsSeen(node *yaml.Node, seen map[*yaml.Node]bool) {
	if seen[node] {
		return
	}
	seen[node] = true
	if node.Kind == yaml.AliasNode && node.Alias != nil {
		*node = *cloneYAMLNode(node.Alias, make(map[*yaml.Node]*yaml.Node))
	}
	if node.Kind == yaml.ScalarNode && node.Tag == "!!timestamp" {
		node.Tag = "!!str"
	}
	for _, child := range node.Content {
		stringifyTimestampsSeen(child, seen)
	}
}

func cloneYAMLNode(node *yaml.Node, cloned map[*yaml.Node]*yaml.Node) *yaml.Node {
	if clonedNode, ok := cloned[node]; ok {
		return clonedNode
	}
	clonedNode := *node
	cloned[node] = &clonedNode
	clonedNode.Content = make([]*yaml.Node, len(node.Content))
	for i, child := range node.Content {
		clonedNode.Content[i] = cloneYAMLNode(child, cloned)
	}
	if node.Alias != nil {
		clonedNode.Alias = cloneYAMLNode(node.Alias, cloned)
	}
	return &clonedNode
}

func isJSONDocument(data []byte) bool {
	trimmed := bytes.TrimSpace(data)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

// formatYAMLError converts YAML parsing errors into user-friendly messages
func formatYAMLError(err error) error {
	errStr := err.Error()

	// Check for unknown field errors from yaml.v3
	// Format: "yaml: unmarshal errors:\n  line X: field Y not found in type Z"
	if contains(errStr, "field") && contains(errStr, "not found in type") {
		return &ValidationError{
			Field:   extractFieldName(errStr),
			Message: fmt.Sprintf("unknown field (possible typo). %s", getSuggestion(errStr)),
		}
	}

	// Check for type mismatch errors
	if contains(errStr, "cannot unmarshal") {
		return fmt.Errorf("type error: %s. Check that field values match expected types (string, integer, etc.)", errStr)
	}

	// Check for syntax errors
	if contains(errStr, "yaml:") && (contains(errStr, "did not find expected") || contains(errStr, "mapping values")) {
		return fmt.Errorf("YAML syntax error: %s. Check indentation and formatting", errStr)
	}

	return fmt.Errorf("YAML parsing failed: %w", err)
}

// extractFieldName attempts to extract the field name from a yaml error
func extractFieldName(errStr string) string {
	// Look for pattern "field X not found"
	if idx := indexOf(errStr, "field "); idx != -1 {
		start := idx + len("field ")
		end := indexOf(errStr[start:], " not found")
		if end != -1 {
			return errStr[start : start+end]
		}
	}
	return "unknown"
}

// getSuggestion returns helpful suggestions for common typos
func getSuggestion(errStr string) string {
	suggestions := map[string]string{
		"usernmae":       "Did you mean 'user'?",
		"username":       "Did you mean 'user'?",
		"passwd":         "Did you mean 'password'?",
		"pass":           "Did you mean 'password'?",
		"dbname":         "Did you mean 'database_name'?",
		"db_name":        "Did you mean 'database_name'?",
		"database":       "Did you mean 'database_name'?",
		"db":             "Did you mean 'database_name'?",
		"hostname":       "Did you mean 'host'?",
		"server":         "Did you mean 'host'?",
		"addr":           "Did you mean 'host'?",
		"address":        "Did you mean 'host'?",
		"timeout":        "Did you mean 'connect_timeout'?",
		"connexTimeout":  "Did you mean 'connect_timeout'?",
		"apiversion":     "Did you mean 'apiVersion'? (note the capital V)",
		"api_version":    "Did you mean 'apiVersion'?",
		"databaseName":   "Did you mean 'database_name'? (use snake_case for spec fields)",
		"connectTimeout": "Did you mean 'connect_timeout'? (use snake_case for spec fields)",
	}

	errLower := toLower(errStr)
	for typo, suggestion := range suggestions {
		if contains(errLower, toLower(typo)) {
			return suggestion
		}
	}

	return "Run 'qctl explain <resource>' to see valid field names."
}

// Helper functions to avoid strings import (keep package small)
func contains(s, substr string) bool {
	return indexOf(s, substr) != -1
}

func indexOf(s, substr string) int {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

// LoadFile reads and parses a manifest file with strict validation.
// Returns an error if the file contains unknown fields or is missing required fields.
func LoadFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %s: %w", path, err)
	}
	return LoadBytes(data)
}

// LoadFileInto reads and parses a manifest into a typed struct.
// The type T should match the expected manifest structure.
func LoadFileInto[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest file %s: %w", path, err)
	}

	var result T
	if err := StrictUnmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// LoadBytes parses manifest data from bytes with strict validation.
func LoadBytes(data []byte) (*Manifest, error) {
	var manifest Manifest
	if err := StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}
	// The kind is known only once the document is decoded. Any other kind is
	// decoded again without instance_name, so it is refused there exactly as
	// an unknown key is.
	if !kindsWithInstanceName[manifest.Kind] {
		if err := StrictUnmarshal(data, &envelope[Metadata]{}); err != nil {
			return nil, err
		}
	}

	// Validate required fields
	if err := validateManifest(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// LoadBytesInto parses manifest data from bytes into a typed struct.
func LoadBytesInto[T any](data []byte) (*T, error) {
	var result T
	if err := StrictUnmarshal(data, &result); err != nil {
		return nil, err
	}

	return &result, nil
}

// kindsWithSpecIdentity lists kinds whose identity lives in spec fields rather
// than metadata (a TableRule instantiate is identified by spec.rule_revision_id
// + spec.dataset_id, and a patch by metadata.id), so metadata.name is never
// used and neither metadata.name nor metadata.id is required up front. The
// kind-specific apply path enforces its own mode requirements.
var kindsWithSpecIdentity = map[string]bool{
	"TableRule": true,
}

// kindsWithSlugIdentity lists kinds that require metadata.slug in place of
// metadata.name or metadata.id.
var kindsWithSlugIdentity = map[string]bool{
	"Deliverable":   true,
	"StandardField": true,
	"TableKind":     true,
}

// kindAliases maps an alternative spelling of a manifest kind to the kind it
// names. CompositeDataset is the backend's name for a LinkedTable; Concept is
// StandardField's former name.
var kindAliases = map[string]string{"CompositeDataset": "LinkedTable", "Concept": "StandardField"}

// CanonicalKind returns the kind that kind names: an alias's target, otherwise
// kind itself.
func CanonicalKind(kind string) string {
	if canonical, ok := kindAliases[kind]; ok {
		return canonical
	}
	return kind
}

// validateManifest validates required fields in a manifest
func validateManifest(m *Manifest) error {
	if m.APIVersion == "" {
		return &ValidationError{Field: "apiVersion", Message: "required field is missing"}
	}
	if m.Kind == "" {
		return &ValidationError{Field: "kind", Message: "required field is missing"}
	}
	if kindsWithSlugIdentity[CanonicalKind(m.Kind)] {
		if m.Metadata.Slug == "" {
			return &ValidationError{Field: "metadata.slug", Message: "required field is missing"}
		}
		return nil
	}
	if m.Metadata.Name == "" && m.Metadata.ID == "" && !kindsWithSpecIdentity[m.Kind] {
		return &ValidationError{Field: "metadata.name", Message: "required field is missing (or provide metadata.id)"}
	}
	return nil
}
