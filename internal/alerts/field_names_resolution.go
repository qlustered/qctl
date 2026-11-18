package alerts

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/actionfields"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"gopkg.in/yaml.v3"
)

// FieldNameResolutionSpec is one column mapping in an AlertResolution.
type FieldNameResolutionSpec struct {
	IncomingFieldName *string `yaml:"incoming_field_name" json:"incoming_field_name"`
	FieldName         *string `yaml:"field_name" json:"field_name"`
	Operation         string  `yaml:"operation" json:"operation"`
	Scope             string  `yaml:"scope" json:"scope"`
	Expand            *bool   `yaml:"expand,omitempty" json:"expand,omitempty"`
}

func (s AlertResolutionSpec) validateFieldNames() error {
	fields := api.ActionFields{Required: []string{"field_names_resolution"}}
	if err := actionfields.Validate("field_names_resolution", fields, s.PresentFields()); err != nil {
		return err
	}
	for i, field := range s.FieldNamesResolution {
		path := fmt.Sprintf("spec.field_names_resolution[%d]", i)
		if field.IncomingFieldName == nil || field.FieldName == nil {
			return qerrors.NewBadUsagef("%s requires incoming_field_name and field_name (field_name may be an empty string)", path)
		}
		if !slices.Contains(api.EnumValues[api.FieldNameResolutionOperator](), field.Operation) {
			return qerrors.NewBadUsagef("%s.operation must be one of: %s", path,
				strings.Join(api.EnumValues[api.FieldNameResolutionOperator](), ", "))
		}
		if !slices.Contains(api.EnumValues[api.Scope](), field.Scope) {
			return qerrors.NewBadUsagef("%s.scope must be one of: %s", path,
				strings.Join(api.EnumValues[api.Scope](), ", "))
		}
	}
	return nil
}

func (f FieldNameResolutionSpec) toAPI() api.FieldNameResolutionParams {
	out := api.FieldNameResolutionParams{
		Operation: api.FieldNameResolutionOperator(f.Operation),
		Scope:     api.Scope(f.Scope),
	}
	if f.IncomingFieldName != nil {
		out.IncomingFieldName = *f.IncomingFieldName
	}
	if f.FieldName != nil {
		out.FieldName = *f.FieldName
	}
	if f.Expand != nil {
		expand := *f.Expand
		out.Expand = &expand
	}
	return out
}

// CanResolveFieldNames reports whether the alert awaits column mapping decisions.
func CanResolveFieldNames(a *AlertFull) bool {
	return a != nil && (a.IssueType == api.AlertTypeInitialFields || a.IssueType == api.AlertTypeUnexpectedFieldName) &&
		(a.Resolved == nil || !*a.Resolved) && !isParked(a)
}

// ValidateAlert checks the spec's resolution path against the fetched alert.
func (s AlertResolutionSpec) ValidateAlert(a *AlertFull) error {
	if s.Action != "" {
		return ValidateAction(a, s.Action)
	}
	if !CanResolveFieldNames(a) {
		return qerrors.NewBadUsage("field_names_resolution requires an open initial_fields or unexpected_field_name alert awaiting column mapping decisions")
	}
	return nil
}

// ValidateScopes refuses a mapping that writes at a scope a.FieldMappingScopes
// leaves out: a delete, a map naming a column, or an expand. It does nothing
// when the alert carries no field_mapping_scopes.
func (s AlertResolutionSpec) ValidateScopes(a *AlertFull) error {
	if a == nil || a.FieldMappingScopes == nil {
		return nil
	}
	open := *a.FieldMappingScopes
	for i, field := range s.FieldNamesResolution {
		if slices.Contains(open, api.Scope(field.Scope)) {
			continue
		}
		write := scopedWrite(field)
		if write == "" {
			continue
		}
		return qerrors.NewBadUsagef("spec.field_names_resolution[%d]: scope %s is not open to you on alert %d for %s; use one of: %s",
			i, field.Scope, a.ID, write, joinScopes(open, ", "))
	}
	return nil
}

// scopedWrite names what field writes at its scope, or "" when it writes
// nothing there.
func scopedWrite(field FieldNameResolutionSpec) string {
	switch {
	case field.Operation == string(api.FieldNameResolutionOperatorNew):
		return ""
	case field.Expand != nil && *field.Expand:
		return "expand"
	case field.Operation == string(api.FieldNameResolutionOperatorDelete):
		return string(api.FieldNameResolutionOperatorDelete)
	case field.Operation == string(api.FieldNameResolutionOperatorMap) && field.FieldName != nil && *field.FieldName != "":
		return "a map to a column"
	}
	return ""
}

func scopeNames(scopes []api.Scope) []string {
	names := make([]string, len(scopes))
	for i, scope := range scopes {
		names[i] = string(scope)
	}
	return names
}

func joinScopes(scopes []api.Scope, sep string) string {
	return strings.Join(scopeNames(scopes), sep)
}

// FieldNamesSkeleton renders an editable column mapping manifest. Its scope
// comment and prefilled scope follow a.FieldMappingScopes when present.
func FieldNamesSkeleton(a *AlertFull) string {
	var scopes []api.Scope
	if a.FieldMappingScopes != nil {
		scopes = *a.FieldMappingScopes
	}
	return fieldNamesSkeleton(a, "qctl alert resolve -f <file>", scopes)
}

// ProposeFieldNamesSkeleton is FieldNamesSkeleton with a footer naming
// `qctl alert propose -f`, listing every scope and prefilling none.
func ProposeFieldNamesSkeleton(a *AlertFull) string {
	return fieldNamesSkeleton(a, "qctl alert propose -f <file>", nil)
}

func fieldNamesSkeleton(a *AlertFull, next string, scopes []api.Scope) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# alert %d on table %s (id %d)\n", a.ID, pkgmanifest.CommentSafe(a.DatasetName), a.DatasetID)
	fmt.Fprintf(&b, "# Fill in each mapping, then: %s\n", next)
	prefill := ""
	if len(scopes) > 0 {
		prefill = string(scopes[0])
		fmt.Fprintf(&b, "# operation: new | map | delete; scope open to you: %s\n", joinScopes(scopes, " | "))
	} else {
		b.WriteString("# operation: new | map | delete; scope: dataset | data_source_model | stored_item\n")
	}
	b.WriteString("# Use field_name: \"\" for a deletion or to keep the normalized incoming name with new.\n")
	fmt.Fprintf(&b, "apiVersion: %s\nkind: %s\nmetadata:\n  id: \"%d\"\nspec:\n  field_names_resolution:\n",
		pkgmanifest.APIVersionV1, AlertResolutionKind, a.ID)
	info := a.FieldNamesSuggestionsInfo
	if info == nil || len(info.Suggestions) == 0 {
		b.WriteString("    - incoming_field_name:\n")
		writeFieldNamesSkeletonKeys(&b, prefill)
	} else {
		for _, suggestion := range info.Suggestions {
			name, _ := jsonnum.MarshalYAML(&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: suggestion.IncomingFieldName, Style: yaml.DoubleQuotedStyle})
			fmt.Fprintf(&b, "    - incoming_field_name: %s", name)
			fmt.Fprintf(&b, "      # %s\n", recommendation(suggestion))
			if suggestion.BlankInFile != nil && *suggestion.BlankInFile {
				b.WriteString("      # blank in this file\n")
			}
			writeFieldNamesSkeletonKeys(&b, prefill)
		}
	}
	if info != nil {
		writeTableColumns(&b, info)
	}
	return b.String()
}

// recommendation describes the server's suggestion for one incoming column.
func recommendation(s api.FieldNameSuggestion) string {
	target := pkgmanifest.CommentSafe(s.RecommendedFieldNameToMapTo)
	switch {
	case s.RecommendedOperation != nil && target != "":
		return fmt.Sprintf("recommended: %s %s", *s.RecommendedOperation, target)
	case s.RecommendedOperation != nil:
		return fmt.Sprintf("recommended: %s", *s.RecommendedOperation)
	case target != "":
		return "recommended field_name: " + target
	}
	return "no recommendation"
}

// writeTableColumns lists info.DatasetFields as comments, flagging each
// required column absent from the file that no suggestion with data is
// recommended to map to.
func writeTableColumns(b *strings.Builder, info *api.FieldNamesSuggestionsInfo) {
	if info.DatasetFields == nil || len(*info.DatasetFields) == 0 {
		return
	}
	unfed := unfedColumns(info)
	b.WriteString("# table columns: name (label) [required]\n")
	for _, field := range *info.DatasetFields {
		line := "#   " + pkgmanifest.CommentSafe(field.FieldName)
		if field.Label != nil && *field.Label != "" && *field.Label != field.FieldName {
			line += " (" + pkgmanifest.CommentSafe(*field.Label) + ")"
		}
		if field.Required != nil && *field.Required {
			line += " [required]"
			if unfed[field.FieldName] {
				line += " not in this file, and no column with data is recommended for it: map one or the server refuses with 422"
			}
		}
		b.WriteString(line + "\n")
	}
}

// unfedColumns returns the table columns absent from the file that no
// suggestion with data is recommended to map to.
func unfedColumns(info *api.FieldNamesSuggestionsInfo) map[string]bool {
	unfed := map[string]bool{}
	for _, name := range info.DatasetFieldNamesNotInFile {
		unfed[name] = true
	}
	for _, s := range info.Suggestions {
		mapsTo := s.RecommendedOperation == nil || *s.RecommendedOperation == api.FieldNameResolutionOperatorMap
		if mapsTo && s.RecommendedFieldNameToMapTo != "" && (s.BlankInFile == nil || !*s.BlankInFile) {
			delete(unfed, s.RecommendedFieldNameToMapTo)
		}
	}
	return unfed
}

func writeFieldNamesSkeletonKeys(b *strings.Builder, scope string) {
	b.WriteString("      field_name:\n      operation:\n")
	if scope == "" {
		b.WriteString("      scope:\n")
	} else {
		fmt.Fprintf(b, "      scope: %s\n", scope)
	}
	b.WriteString("      # expand: false\n")
}
