package linked_tables //nolint:revive // Public package name matches the established resource domain.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// JoinClause is the expanded (cross-cardinality / expression) join form.
// Mirrors api.CompositeJoinClause.
type JoinClause struct {
	SourceFields []string    `yaml:"source_fields" json:"source_fields"`
	SpineFields  []string    `yaml:"spine_fields,omitempty" json:"spine_fields,omitempty"`
	SpineExpr    *string     `yaml:"spine_expr,omitempty" json:"spine_expr,omitempty"`
	SourceExpr   *string     `yaml:"source_expr,omitempty" json:"source_expr,omitempty"`
	Constant     interface{} `yaml:"constant,omitempty" json:"constant,omitempty"`

	// constantProblems holds constantProblems' description of each value and
	// mapping key written under constant.
	constantProblems []string
}

// JoinField is a join_fields value: either a shorthand source-field name
// (string) or an expanded JoinClause (mapping).
type JoinField struct {
	Shorthand string
	Clause    *JoinClause
}

// joinClauseKeys are the keys a join clause mapping may hold.
var joinClauseKeys = yamlKeys(reflect.TypeOf(JoinClause{}))

// yamlKeys returns the mapping keys yaml.v3 decodes into struct type t's fields.
func yamlKeys(t reflect.Type) map[string]bool {
	keys := map[string]bool{}
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		if name != "-" {
			keys[name] = true
		}
	}
	return keys
}

// UnmarshalYAML accepts either a scalar string (shorthand) or a mapping
// (clause). A clause key that is not a JoinClause field is an error. The
// clause's constant holds each float literal and timestamp as wireLiterals
// replaces it.
func (j *JoinField) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&j.Shorthand)
	}
	var c JoinClause
	if err := node.Decode(&c); err != nil {
		return err
	}
	var fields map[string]yaml.Node
	if err := node.Decode(&fields); err != nil {
		return err
	}
	var unknown []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		if !joinClauseKeys[key] {
			unknown = append(unknown, fmt.Sprintf("line %d: field %s not found in type %T", fields[key].Line, key, c))
		}
	}
	if len(unknown) > 0 {
		return &yaml.TypeError{Errors: unknown}
	}
	constant := fields["constant"]
	c.constantProblems = constantProblems(&constant)
	c.Constant = wireLiterals(&constant, c.Constant)
	j.Clause = &c
	return nil
}

// MarshalYAML returns a shorthand as a string, and a clause whose constant has
// each number with a fraction or an exponent that float64 holds replaced by a
// !!float node spelled by floatNumber, and each other number replaced as
// jsonnum.YAMLNumbers replaces a json.Number.
func (j JoinField) MarshalYAML() (interface{}, error) {
	if j.Clause == nil {
		return j.Shorthand, nil
	}
	clause := *j.Clause
	if clause.Constant != nil {
		// A copy decoded with UseNumber, so the clause j holds is not changed.
		encoded, err := json.Marshal(clause.Constant)
		if err != nil {
			return nil, err
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		var constant interface{}
		if err := decoder.Decode(&constant); err != nil {
			return nil, err
		}
		clause.Constant = jsonnum.YAMLNumbers(replaceLeaves(constant, floatNode))
	}
	return clause, nil
}

// floatNode returns leaf as a !!float node spelled by floatNumber when leaf is
// a json.Number with a fraction or an exponent that float64 holds, and leaf
// otherwise.
func floatNode(leaf any) any {
	n, ok := leaf.(json.Number)
	if !ok || !strings.ContainsAny(n.String(), ".eE") {
		return leaf
	}
	f, err := n.Float64()
	if err != nil {
		return leaf
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!float", Value: floatNumber(f).String()}
}

// MarshalJSON writes a shorthand as a JSON string and a clause as an object.
func (j JoinField) MarshalJSON() ([]byte, error) {
	if j.Clause == nil {
		return json.Marshal(j.Shorthand)
	}
	return json.Marshal(j.Clause)
}

// UnmarshalJSON accepts a shorthand string or an expanded join clause.
func (j *JoinField) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty join field")
	}
	if trimmed[0] == '"' {
		j.Clause = nil
		return json.Unmarshal(data, &j.Shorthand)
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("join field must be a string or object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var clause JoinClause
	if err := pkgmanifest.ValidateJSONFields(data, &clause); err != nil {
		return err
	}
	if err := decoder.Decode(&clause); err != nil {
		return err
	}
	clause.Constant = replaceLeaves(clause.Constant, func(leaf any) any {
		n, ok := leaf.(json.Number)
		if !ok || !strings.ContainsAny(n.String(), ".eE") {
			return leaf
		}
		f, err := n.Float64()
		if err != nil {
			return leaf
		}
		return floatNumber(f)
	})
	j.Shorthand = ""
	j.Clause = &clause
	return nil
}

// wireLiterals returns v, the value yaml.v3 decodes from n into an
// interface{}, with each value wireScalar changes replaced in place. It
// follows aliases and merge keys, and leaves a mapping that did not decode to
// a map[string]any as it is.
func wireLiterals(n *yaml.Node, v any) any {
	switch n.Kind {
	case yaml.AliasNode:
		return wireLiterals(n.Alias, v)
	case yaml.ScalarNode:
		return wireScalar(n, v)
	case yaml.SequenceNode:
		if items, ok := v.([]any); ok && len(items) == len(n.Content) {
			for i, item := range items {
				items[i] = wireLiterals(n.Content[i], item)
			}
		}
	case yaml.MappingNode:
		m, ok := v.(map[string]any)
		var fields map[string]yaml.Node
		if !ok || n.Decode(&fields) != nil {
			return v
		}
		for key, field := range fields {
			if item, found := m[key]; found {
				m[key] = wireLiterals(&field, item)
			}
		}
	}
	return v
}

// wireScalar returns the scalar n's text when v, its decoded value, is a
// time.Time; the json.Number floatNumber spells for v when v is a finite
// float64 and n has an explicit tag or a '.' or an exponent in its text; and v
// otherwise.
func wireScalar(n *yaml.Node, v any) any {
	if _, ok := v.(time.Time); ok {
		return n.Value
	}
	f, ok := v.(float64)
	if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
		return v
	}
	if n.Style&yaml.TaggedStyle == 0 && !strings.ContainsAny(n.Value, ".eE") {
		return v
	}
	return floatNumber(f)
}

// constantProblems returns keyProblem's description of each mapping key under
// n and scalarProblem's of each scalar value, following aliases.
func constantProblems(n *yaml.Node) []string {
	switch n.Kind {
	case yaml.AliasNode:
		return constantProblems(n.Alias)
	case yaml.ScalarNode:
		if problem := scalarProblem(n); problem != "" {
			return []string{problem}
		}
		return nil
	case yaml.MappingNode, yaml.SequenceNode:
	default:
		return nil
	}
	var problems []string
	for i, child := range n.Content {
		if n.Kind != yaml.MappingNode || i%2 == 1 {
			problems = append(problems, constantProblems(child)...)
		} else if problem := keyProblem(child); problem != "" {
			problems = append(problems, problem)
		}
	}
	return problems
}

// strTag is the short tag of a YAML string.
const strTag = "!!str"

// keyTypes names what yaml.v3 reads a mapping key as, by its short tag.
var keyTypes = map[string]string{
	"!!int":       "an integer",
	"!!float":     "a float",
	"!!bool":      "a boolean",
	"!!null":      "null",
	"!!timestamp": "a timestamp",
}

// keyProblem describes the mapping key n, following an alias, when its short
// tag is neither !!str nor !!merge, and returns "" otherwise.
func keyProblem(n *yaml.Node) string {
	for n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	tag := n.ShortTag()
	if tag == strTag || tag == "!!merge" {
		return ""
	}
	kind, ok := keyTypes[tag]
	if !ok {
		kind = "a value tagged " + tag
	}
	return fmt.Sprintf("mapping key %s is %s to YAML, not a string, and JSON object keys are strings; quote it", n.Value, kind)
}

// scalarProblem returns the first description nonFinite, inexactInteger,
// signAfterPrefix and floatBeyondRange give of the scalar n, or "".
func scalarProblem(n *yaml.Node) string {
	for _, check := range []func(*yaml.Node) string{nonFinite, inexactInteger, signAfterPrefix, floatBeyondRange} {
		if problem := check(n); problem != "" {
			return problem
		}
	}
	return ""
}

// nonFinite describes the scalar n when yaml.v3 decodes it as +Inf, -Inf or
// NaN, and returns "" otherwise.
func nonFinite(n *yaml.Node) string {
	var v any
	if n.Decode(&v) != nil {
		return ""
	}
	if f, ok := v.(float64); ok && (math.IsInf(f, 0) || math.IsNaN(f)) {
		return fmt.Sprintf("%s would be read as %v, which JSON cannot represent; quote it to send a string", n.Value, f)
	}
	return ""
}

var (
	minInt64  = big.NewInt(math.MinInt64)
	maxUint64 = new(big.Int).SetUint64(math.MaxUint64)

	// leadingZeroDecimal matches a decimal integer written with a leading 0.
	leadingZeroDecimal = regexp.MustCompile(`^[-+]?0[0-9]+$`)
	// signedPrefixed matches a 0b or 0o prefix followed by a sign and digits
	// of that base.
	signedPrefixed = regexp.MustCompile(`^(0b[-+][01]+|0o[-+][0-7]+)$`)
	// yamlStyleFloat is the float syntax yaml.v3 accepts in text that does not
	// start with '.'.
	yamlStyleFloat = regexp.MustCompile(`^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$`)
)

// plainNumber reports whether the scalar n has no tag, quotes or block style
// and text that starts with one of the bytes in starts.
func plainNumber(n *yaml.Node, starts string) bool {
	const explicit = yaml.TaggedStyle | yaml.DoubleQuotedStyle | yaml.SingleQuotedStyle | yaml.LiteralStyle | yaml.FoldedStyle
	return n.Style&explicit == 0 && n.Value != "" && strings.ContainsRune(starts, rune(n.Value[0]))
}

// readAs names what yaml.v3 reads the scalar n as when it is not an integer.
func readAs(n *yaml.Node) string {
	if n.ShortTag() == strTag {
		return "a string"
	}
	return "a float"
}

// inexactInteger describes the plain scalar n when yaml.v3 decodes it as a
// float or a string although its text, which starts with a sign or a digit,
// parses as an integer in Go's base-0 syntax once underscores are removed.
// When that text does not parse, it returns inexactLeadingZero's description.
// It returns "" for any other scalar.
func inexactInteger(n *yaml.Node) string {
	// yaml.v3 tries its number parse only on text that starts with one of these.
	if !plainNumber(n, "+-0123456789") || n.ShortTag() == "!!int" {
		return ""
	}
	text := strings.ReplaceAll(n.Value, "_", "")
	value, ok := new(big.Int).SetString(text, 0)
	if !ok {
		return inexactLeadingZero(n, text)
	}
	if value.Cmp(minInt64) < 0 || value.Cmp(maxUint64) > 0 {
		return outsideIntegerRange(n)
	}
	// In range, only a '+'-signed literal above the int64 maximum gets here:
	// yaml.v3 parses one with strconv.ParseUint, which accepts no sign.
	return fmt.Sprintf("%s would be read as %s, not an integer, because of its leading '+'; remove the '+'", n.Value, readAs(n))
}

// inexactLeadingZero describes the scalar n, whose text without underscores
// is text, when text is a decimal integer with a leading 0 that is outside
// the integer range or whose nearest float64 encoding/json spells with other
// digits. It returns "" otherwise.
func inexactLeadingZero(n *yaml.Node, text string) string {
	if !leadingZeroDecimal.MatchString(text) {
		return ""
	}
	value, _ := new(big.Int).SetString(text, 10)
	if value.Cmp(minInt64) < 0 || value.Cmp(maxUint64) > 0 {
		return outsideIntegerRange(n)
	}
	f, _ := strconv.ParseFloat(text, 64)
	sent := string(plainJSON(f))
	if sent == value.String() {
		return ""
	}
	return fmt.Sprintf("%s would be read as a float and sent as %s, not the integer written, because of its leading 0; remove the leading 0", n.Value, sent)
}

// outsideIntegerRange describes the scalar n as an integer outside the range
// minInt64..maxUint64.
func outsideIntegerRange(n *yaml.Node) string {
	return fmt.Sprintf("%s is outside the integer range %s..%s and would be read as %s, not an integer; quote it to send a string", n.Value, minInt64, maxUint64, readAs(n))
}

// signAfterPrefix describes the plain scalar n when its text without
// underscores is a 0b or 0o prefix, a sign and digits of that base, and
// returns "" otherwise.
func signAfterPrefix(n *yaml.Node) string {
	if !plainNumber(n, "0") {
		return ""
	}
	text := strings.ReplaceAll(n.Value, "_", "")
	if !signedPrefixed.MatchString(text) {
		return ""
	}
	kind := "a string"
	var v any
	if n.Decode(&v) == nil && n.ShortTag() == "!!int" {
		kind = fmt.Sprintf("the integer %v", v)
	}
	return fmt.Sprintf("%s has its sign after the %s prefix and would be read as %s; write the sign first for an integer, or quote it to send a string", n.Value, text[:2], kind)
}

// floatBeyondRange describes the plain scalar n when yaml.v3 decodes it as a
// string although its text is a float literal beyond the float64 range, and
// returns "" otherwise.
func floatBeyondRange(n *yaml.Node) string {
	if !plainNumber(n, "+-.0123456789") || n.ShortTag() != strTag {
		return ""
	}
	text := n.Value
	// yaml.v3 parses text starting with '.' as it is, and other text without
	// underscores and only when it matches yamlStyleFloat.
	if text[0] != '.' {
		text = strings.ReplaceAll(text, "_", "")
		if !yamlStyleFloat.MatchString(text) {
			return ""
		}
	}
	if _, err := strconv.ParseFloat(text, 64); !errors.Is(err, strconv.ErrRange) {
		return ""
	}
	return fmt.Sprintf("%s is beyond the float64 range and would be read as a string, not a number; quote it to send a string", n.Value)
}

// SourceSpec describes one upstream source in the linked table.
type SourceSpec struct {
	Dataset    string               `yaml:"dataset" json:"dataset"`
	Arity      *string              `yaml:"arity,omitempty" json:"arity,omitempty"`
	JoinFields map[string]JoinField `yaml:"join_fields,omitempty" json:"join_fields,omitempty"`
}

// SpineSpec describes the spine source and its natural key.
type SpineSpec struct {
	Source                  string            `yaml:"source" json:"source"`
	NaturalKeyColumns       []string          `yaml:"natural_key_columns" json:"natural_key_columns"`
	NaturalKeyNormalization map[string]string `yaml:"natural_key_normalization,omitempty" json:"natural_key_normalization,omitempty"`
	IdentityHashVersion     *int              `yaml:"identity_hash_version,omitempty" json:"identity_hash_version,omitempty"`
}

// TableSpec describes the downstream destination table.
type TableSpec struct {
	Database string  `yaml:"database" json:"database"`
	Schema   *string `yaml:"schema,omitempty" json:"schema,omitempty"`
	Name     string  `yaml:"name" json:"name"`
}

// OutputMapping is a single downstream-field mapping.
type OutputMapping struct {
	Source string `yaml:"source" json:"source"`
	Field  string `yaml:"field" json:"field"`
}

// DryRunOptions are pass-through knobs for the composite dry-run JOB.
// Distinct from the --validate-only flag (validate_only query param).
type DryRunOptions struct {
	MaxSpineRows *int `yaml:"max_spine_rows,omitempty" json:"max_spine_rows,omitempty"`
	Seed         *int `yaml:"seed,omitempty" json:"seed,omitempty"`
}

// LinkedTableSpec is the spec body of a linked table manifest.
type LinkedTableSpec struct {
	Destination    string                   `yaml:"destination" json:"destination"`
	BackupSettings *string                  `yaml:"backup_settings,omitempty" json:"backup_settings,omitempty"`
	Table          TableSpec                `yaml:"table" json:"table"`
	Spine          SpineSpec                `yaml:"spine" json:"spine"`
	Sources        map[string]SourceSpec    `yaml:"sources" json:"sources"`
	Output         map[string]OutputMapping `yaml:"output" json:"output"`
	DesiredState   *string                  `yaml:"desired_state,omitempty" json:"desired_state,omitempty"`
	Release        *string                  `yaml:"release,omitempty" json:"release,omitempty"`
	Description    *string                  `yaml:"description,omitempty" json:"description,omitempty"`
	DryRun         *DryRunOptions           `yaml:"dry_run,omitempty" json:"dry_run,omitempty"`
}

// LinkedTableManifest is the declarative representation used by apply.
type LinkedTableManifest struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       LinkedTableSpec      `yaml:"spec" json:"spec"`
}

// LinkedTableStatus is the state of an exported linked table and revision.
type LinkedTableStatus struct {
	TableID              int       `yaml:"table_id" json:"table_id"`
	CompositeSpecGroupID uuid.UUID `yaml:"composite_spec_group_id" json:"composite_spec_group_id"`
	Slug                 string    `yaml:"slug" json:"slug"`
	RevisionID           uuid.UUID `yaml:"revision_id" json:"revision_id"`
	Release              string    `yaml:"release" json:"release"`
	State                string    `yaml:"state" json:"state"`
	BackupSettingsID     int       `yaml:"backup_settings_id" json:"backup_settings_id"`
}

// LinkedTableManifestWithStatus is a manifest with its status block.
type LinkedTableManifestWithStatus struct {
	LinkedTableManifest `yaml:",inline" json:",inline"`
	Status              *LinkedTableStatus `yaml:"status,omitempty" json:"status,omitempty"`
}

// ValidationError represents a manifest validation error.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Accepted manifest kinds. kindCompositeDataset is both a CLI alias and the
// kind always written on the wire — see ToAPIRequest.
const (
	kindLinkedTable      = "LinkedTable"
	kindCompositeDataset = "CompositeDataset"
)

// LoadManifest strictly parses a one-document linked table manifest and
// validates it. Every error it returns exits 2.
func LoadManifest(data []byte) (*LinkedTableManifest, error) {
	manifest, err := parseManifest(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if errs := manifest.Validate(); len(errs) > 0 {
		messages := make([]string, len(errs))
		for i, e := range errs {
			messages[i] = e.Error()
		}
		return nil, qerrors.NewBadUsagef("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}
	return manifest, nil
}

func parseManifest(data []byte) (*LinkedTableManifest, error) {
	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}
	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, qerrors.NewBadUsagef("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != kindLinkedTable && generic.Kind != kindCompositeDataset {
		return nil, qerrors.NewBadUsagef("expected kind 'LinkedTable' or 'CompositeDataset', got '%s'", generic.Kind)
	}

	var manifest LinkedTableManifestWithStatus
	if err := pkgmanifest.StrictUnmarshal(data, &manifest); err != nil {
		return nil, err
	}
	return &manifest.LinkedTableManifest, nil
}

var validArity = map[string]bool{"exactly_one": true, "zero_or_one": true, "at_least_one": true}
var validDesiredState = map[string]bool{"enabled": true, "draft": true}

// Validate checks the manifest contents and returns all problems found.
func (m *LinkedTableManifest) Validate() []ValidationError {
	errs := validateManifestIdentity(m)
	errs = append(errs, validateRequiredSpecFields(m)...)
	errs = append(errs, validateSources(m.Spec.Sources)...)
	errs = append(errs, validateSpineSource(m.Spec.Spine, m.Spec.Sources)...)
	errs = append(errs, validateOutput(m.Spec.Output)...)
	errs = append(errs, validateDesiredState(m.Spec.DesiredState)...)
	return errs
}

func validateManifestIdentity(m *LinkedTableManifest) []ValidationError {
	var errs []ValidationError
	if m.APIVersion == "" {
		errs = append(errs, ValidationError{"apiVersion", "required field is missing"})
	} else if m.APIVersion != pkgmanifest.APIVersionV1 {
		errs = append(errs, ValidationError{"apiVersion", "must be 'qluster.ai/v1'"})
	}

	if m.Kind == "" {
		errs = append(errs, ValidationError{"kind", "required field is missing"})
	} else if m.Kind != kindLinkedTable && m.Kind != kindCompositeDataset {
		errs = append(errs, ValidationError{"kind", "must be 'LinkedTable' or 'CompositeDataset'"})
	}
	return errs
}

func validateRequiredSpecFields(m *LinkedTableManifest) []ValidationError {
	var errs []ValidationError
	if m.Metadata.Name == "" {
		errs = append(errs, ValidationError{"metadata.name", "required field is missing"})
	}
	if m.Spec.Destination == "" {
		errs = append(errs, ValidationError{"spec.destination", "required field is missing"})
	}
	if m.Spec.Table.Database == "" {
		errs = append(errs, ValidationError{"spec.table.database", "required field is missing"})
	}
	if m.Spec.Table.Name == "" {
		errs = append(errs, ValidationError{"spec.table.name", "required field is missing"})
	}
	if m.Spec.Spine.Source == "" {
		errs = append(errs, ValidationError{"spec.spine.source", "required field is missing"})
	}
	if len(m.Spec.Spine.NaturalKeyColumns) == 0 {
		errs = append(errs, ValidationError{"spec.spine.natural_key_columns", "at least one column is required"})
	}
	return errs
}

func validateSources(sources map[string]SourceSpec) []ValidationError {
	var errs []ValidationError
	if len(sources) == 0 {
		errs = append(errs, ValidationError{"spec.sources", "at least one source is required"})
	}
	for key, src := range sources {
		if src.Dataset == "" {
			errs = append(errs, ValidationError{"spec.sources." + key + ".dataset", "required field is missing"})
		}
		if src.Arity != nil && !validArity[*src.Arity] {
			errs = append(errs, ValidationError{"spec.sources." + key + ".arity", "must be one of: exactly_one, zero_or_one, at_least_one"})
		}
		for col, jf := range src.JoinFields {
			if jf.Clause == nil {
				continue
			}
			path := "spec.sources." + key + ".join_fields." + col
			if len(jf.Clause.SourceFields) == 0 {
				errs = append(errs, ValidationError{path + ".source_fields", "at least one source field is required"})
			}
			for _, problem := range jf.Clause.constantProblems {
				errs = append(errs, ValidationError{path + ".constant", problem})
			}
		}
	}
	return errs
}

func validateSpineSource(spine SpineSpec, sources map[string]SourceSpec) []ValidationError {
	var errs []ValidationError
	if spine.Source != "" && len(sources) > 0 {
		if _, ok := sources[spine.Source]; !ok {
			errs = append(errs, ValidationError{"spec.spine.source", "must reference a key in spec.sources"})
		}
	}
	return errs
}

func validateOutput(output map[string]OutputMapping) []ValidationError {
	var errs []ValidationError
	if len(output) == 0 {
		errs = append(errs, ValidationError{"spec.output", "at least one output mapping is required"})
	}
	for field, om := range output {
		if om.Source == "" {
			errs = append(errs, ValidationError{"spec.output." + field + ".source", "required field is missing"})
		}
		if om.Field == "" {
			errs = append(errs, ValidationError{"spec.output." + field + ".field", "required field is missing"})
		}
	}
	return errs
}

func validateDesiredState(desiredState *string) []ValidationError {
	var errs []ValidationError
	if desiredState != nil && !validDesiredState[*desiredState] {
		errs = append(errs, ValidationError{"spec.desired_state", "must be one of: enabled, draft"})
	}
	return errs
}

func (c JoinClause) toAPI() api.CompositeJoinClause {
	out := api.CompositeJoinClause{
		SourceFields: c.SourceFields,
		SpineExpr:    c.SpineExpr,
		SourceExpr:   c.SourceExpr,
	}
	if len(c.SpineFields) > 0 {
		sf := append([]string(nil), c.SpineFields...)
		out.SpineFields = &sf
	}
	if c.Constant != nil {
		v := c.Constant
		out.Constant = &v
	}
	return out
}

// ToAPIRequest maps the CLI manifest onto the generated request body. The wire
// kind is always "CompositeDataset" regardless of the CLI alias used.
func (m *LinkedTableManifest) ToAPIRequest() (api.CompositeDatasetApplyManifest, error) {
	spec := m.Spec

	out := api.CompositeDatasetApplyManifest{
		APIVersion: pkgmanifest.APIVersionV1,
		Kind:       kindCompositeDataset,
		Metadata:   api.ApplyMetadata{Name: m.Metadata.Name},
		Spec: api.CompositeDatasetApplySpec{
			Destination:    spec.Destination,
			BackupSettings: spec.BackupSettings,
			Description:    spec.Description,
			Release:        spec.Release,
			Table: api.ApplyTableSpec{
				Database: spec.Table.Database,
				Name:     spec.Table.Name,
				Schema:   spec.Table.Schema,
			},
			Spine: api.ApplySpineSpec{
				Source:              spec.Spine.Source,
				NaturalKeyColumns:   spec.Spine.NaturalKeyColumns,
				IdentityHashVersion: spec.Spine.IdentityHashVersion,
			},
			Sources: map[string]api.ApplySourceSpec{},
			Output:  map[string]api.CompositeOutputMapping{},
		},
	}

	if len(spec.Spine.NaturalKeyNormalization) > 0 {
		nkn := spec.Spine.NaturalKeyNormalization
		out.Spec.Spine.NaturalKeyNormalization = &nkn
	}

	if spec.DesiredState != nil {
		ds := api.CompositeDatasetApplySpecDesiredState(*spec.DesiredState)
		out.Spec.DesiredState = &ds
	}

	if spec.DryRun != nil {
		out.Spec.DryRun = &api.ApplyDryRunOptions{
			MaxSpineRows: spec.DryRun.MaxSpineRows,
			Seed:         spec.DryRun.Seed,
		}
	}

	for key, src := range spec.Sources {
		as := api.ApplySourceSpec{Dataset: src.Dataset}
		if src.Arity != nil {
			a := api.ApplySourceSpecArity(*src.Arity)
			as.Arity = &a
		}
		if len(src.JoinFields) > 0 {
			jf := map[string]api.ApplySourceSpec_JoinFields_AdditionalProperties{}
			for col, val := range src.JoinFields {
				var ap api.ApplySourceSpec_JoinFields_AdditionalProperties
				if val.Clause != nil {
					if err := ap.FromCompositeJoinClause(val.Clause.toAPI()); err != nil {
						return out, err
					}
				} else {
					if err := ap.FromApplySourceSpecJoinFields0(val.Shorthand); err != nil {
						return out, err
					}
				}
				jf[col] = ap
			}
			as.JoinFields = &jf
		}
		out.Spec.Sources[key] = as
	}

	for field, om := range spec.Output {
		out.Spec.Output[field] = api.CompositeOutputMapping{Source: om.Source, Field: om.Field}
	}

	return out, nil
}
