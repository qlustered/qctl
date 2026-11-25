package alerts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/actionfields"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/prompt"
)

// AlertResolutionKind is the manifest kind `qctl alert resolve -f` accepts.
//
// It is deliberately NOT a `qctl apply -f` kind: apply reconciles resources
// declaratively, while a resolution is a one-shot imperative action that is not
// idempotent (upgrade_rule_revision answers 409 RULE_UPGRADE_REPLACEMENT_EXISTS
// on a second send). Two consequences follow and both are enforced by tests:
// internal/resources/pages/alert.md keeps `kind:` empty in its frontmatter
// (internal/catalog/pages_test.go TestFrontmatterReferences and
// TestApplyKindsComplete), and the resolve command keeps catalog.For("alert")
// rather than catalog.Dispatcher (pages_test.go TestDispatcherKindsResolve).
const AlertResolutionKind = "AlertResolution"

// resolutionFieldDenylist names the AlertResolveSchema fields no manifest can
// set, with the reason. TestEveryResolveFieldIsMappedOrDenied fails on a field
// that is in neither this list nor AlertResolutionSpec.
var resolutionFieldDenylist = map[string]bool{
	// action is handled outside the field map.
	"action": true,
	// comment_thread_id must name a thread whose target is this alert (any
	// other is a 422), and no qctl command creates such a thread.
	"comment_thread_id": true,
}

// CantSniffHeaderParamsSpec is the manifest form of api.CantSniffHeaderParams:
// the one nested object any action needs, and six flat scalars at that.
type CantSniffHeaderParamsSpec struct {
	RawHeaders                  []string `yaml:"raw_headers,omitempty" json:"raw_headers,omitempty"`
	HeaderLineNumberForFile     *int     `yaml:"header_line_number_for_file,omitempty" json:"header_line_number_for_file,omitempty"`
	RowNumberForFirstLineOfData *int     `yaml:"row_number_for_first_line_of_data,omitempty" json:"row_number_for_first_line_of_data,omitempty"`
	CsvDelimiter                *string  `yaml:"csv_delimiter,omitempty" json:"csv_delimiter,omitempty"`
	CsvQuotechar                *string  `yaml:"csv_quotechar,omitempty" json:"csv_quotechar,omitempty"`
	CsvEscapechar               *string  `yaml:"csv_escapechar,omitempty" json:"csv_escapechar,omitempty"`
}

// IsEmpty reports whether the object sets nothing at all. The backend answers
// such a body with a 400 (cant_sniff_header, file_header_handlers.py), so an
// all-empty object is treated as ABSENT rather than as a supplied field.
func (p *CantSniffHeaderParamsSpec) IsEmpty() bool {
	if p == nil {
		return true
	}
	return len(p.RawHeaders) == 0 &&
		p.HeaderLineNumberForFile == nil &&
		p.RowNumberForFirstLineOfData == nil &&
		p.CsvDelimiter == nil &&
		p.CsvQuotechar == nil &&
		p.CsvEscapechar == nil
}

// ToAPI converts to the generated type, or nil when the object sets nothing.
func (p *CantSniffHeaderParamsSpec) ToAPI() *api.CantSniffHeaderParams {
	if p.IsEmpty() {
		return nil
	}
	out := &api.CantSniffHeaderParams{
		HeaderLineNumberForFile:     p.HeaderLineNumberForFile,
		RowNumberForFirstLineOfData: p.RowNumberForFirstLineOfData,
		CsvDelimiter:                p.CsvDelimiter,
		CsvQuotechar:                p.CsvQuotechar,
		CsvEscapechar:               p.CsvEscapechar,
	}
	if len(p.RawHeaders) > 0 {
		headers := append([]string(nil), p.RawHeaders...)
		out.RawHeaders = &headers
	}
	return out
}

// AlertResolutionSpec mirrors api.AlertResolveSchema field for field, in the
// snake_case the wire uses, so a manifest key, its x-alert-action-fields name
// and its name in every error message are the SAME string.
//
// Scalars are pointers on purpose: an omitted new_value and an explicitly empty
// one are different requests to _handle_composite_conflict_reapply
// (atlas capi/capi/controllers/alert_controller.py), which branches on
// `request_body.new_value is not None`.
//
// Lists and maps are plain, not pointers: presence is emptiness for those (see
// actionfields.Present), so a nil and an empty slice mean the same thing here.
//
// file_type and scope are plain strings and are NOT validated against the
// generated enums. That is deliberate: the enum a value must belong to is not
// the same question as which value this ACTION accepts (resolve_sniff_header
// rejects the perfectly valid `dataset` with a 400), so a client-side enum
// check would be half an answer bought with a hand-transcribed list. The
// backend rejects a bad value and the caller prints the action's note with it.
type AlertResolutionSpec struct {
	Action                string                     `yaml:"action,omitempty" json:"action,omitempty"`
	NewValue              *string                    `yaml:"new_value,omitempty" json:"new_value,omitempty"`
	AnotherFieldNewValue  *string                    `yaml:"another_field_new_value,omitempty" json:"another_field_new_value,omitempty"`
	NewDatetimeFormat     *string                    `yaml:"new_datetime_format,omitempty" json:"new_datetime_format,omitempty"`
	SheetName             *string                    `yaml:"sheet_name,omitempty" json:"sheet_name,omitempty"`
	FileType              *string                    `yaml:"file_type,omitempty" json:"file_type,omitempty"`
	Scope                 *string                    `yaml:"scope,omitempty" json:"scope,omitempty"`
	MapValueOnce          *bool                      `yaml:"map_value_once,omitempty" json:"map_value_once,omitempty"`
	IDs                   []int                      `yaml:"ids,omitempty" json:"ids,omitempty"`
	StoredItemID          *int                       `yaml:"stored_item_id,omitempty" json:"stored_item_id,omitempty"`
	Comment               *string                    `yaml:"comment,omitempty" json:"comment,omitempty"`
	Reason                *string                    `yaml:"reason,omitempty" json:"reason,omitempty"`
	InitialSignatures     []string                   `yaml:"initial_signatures,omitempty" json:"initial_signatures,omitempty"`
	FieldNameToValue      map[string]string          `yaml:"field_name_to_value,omitempty" json:"field_name_to_value,omitempty"`
	CantSniffHeaderParams *CantSniffHeaderParamsSpec `yaml:"cant_sniff_header_params,omitempty" json:"cant_sniff_header_params,omitempty"`
	FieldNamesResolution  []FieldNameResolutionSpec  `yaml:"field_names_resolution,omitempty" json:"field_names_resolution,omitempty"`
	KeyColumns            []string                   `yaml:"key_columns,omitempty" json:"key_columns,omitempty"`
	NoKeyColumns          *bool                      `yaml:"no_key_columns,omitempty" json:"no_key_columns,omitempty"`
	AcceptSuggestionIDs   []openapi_types.UUID       `yaml:"accept_suggestion_ids,omitempty" json:"accept_suggestion_ids,omitempty"`
	DismissSuggestionIDs  []openapi_types.UUID       `yaml:"dismiss_suggestion_ids,omitempty" json:"dismiss_suggestion_ids,omitempty"`
}

// AlertResolution is one `-f` document.
type AlertResolution struct {
	APIVersion string               `yaml:"apiVersion" json:"apiVersion"`
	Kind       string               `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata `yaml:"metadata" json:"metadata"`
	Spec       AlertResolutionSpec  `yaml:"spec" json:"spec"`
}

// ParseResolution strict-parses one document. An unknown key is a typo and is
// refused here rather than silently dropped.
func ParseResolution(data []byte) (*AlertResolution, error) {
	var doc AlertResolution
	if err := pkgmanifest.StrictUnmarshal(data, &doc); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse AlertResolution manifest")
	}
	return &doc, nil
}

// AlertID reads metadata.id as the positive integer the alert routes take.
func (r *AlertResolution) AlertID() (int, error) {
	if r.Metadata.ID == "" {
		return 0, qerrors.NewBadUsage("metadata.id is required: it names the alert to resolve")
	}
	id, err := strconv.Atoi(r.Metadata.ID)
	if err != nil || id <= 0 {
		return 0, qerrors.NewBadUsagef("invalid metadata.id %q: expected a positive alert id", r.Metadata.ID)
	}
	return id, nil
}

// PresentFields reports which wire fields this spec actually supplies.
func (s AlertResolutionSpec) PresentFields() actionfields.Present {
	p := actionfields.Present{}
	p.Set("new_value", s.NewValue != nil)
	p.Set("another_field_new_value", s.AnotherFieldNewValue != nil)
	p.Set("new_datetime_format", s.NewDatetimeFormat != nil)
	p.Set("sheet_name", s.SheetName != nil)
	p.Set("file_type", s.FileType != nil)
	p.Set("scope", s.Scope != nil)
	p.Set("map_value_once", s.MapValueOnce != nil)
	p.Set("ids", len(s.IDs) > 0)
	p.Set("stored_item_id", s.StoredItemID != nil)
	p.Set("comment", s.Comment != nil)
	p.Set("reason", s.Reason != nil)
	p.Set("initial_signatures", len(s.InitialSignatures) > 0)
	p.Set("field_name_to_value", len(s.FieldNameToValue) > 0)
	p.Set("cant_sniff_header_params", !s.CantSniffHeaderParams.IsEmpty())
	p.Set("field_names_resolution", len(s.FieldNamesResolution) > 0)
	p.Set("key_columns", len(s.KeyColumns) > 0)
	p.Set("no_key_columns", s.noKeyColumns())
	p.Set("accept_suggestion_ids", len(s.AcceptSuggestionIDs) > 0)
	p.Set("dismiss_suggestion_ids", len(s.DismissSuggestionIDs) > 0)
	return p
}

// Validate checks the spec's request fields.
func (s AlertResolutionSpec) Validate() error {
	if s.Action == "" {
		return s.validateFieldNames()
	}
	if s.FieldNamesResolution != nil {
		return qerrors.NewBadUsage("spec.action cannot be combined with spec.field_names_resolution: column mappings take no action")
	}
	if err := actionfields.Validate(s.Action, api.AlertActionFields[s.Action], s.PresentFields()); err != nil {
		return err
	}
	if s.Comment != nil {
		if err := cmdutil.ValidateCommentText("spec.comment", *s.Comment); err != nil {
			return err
		}
	}
	if len(s.KeyColumns) > 0 && s.noKeyColumns() {
		return qerrors.NewBadUsage("spec.key_columns and spec.no_key_columns: true cannot be combined: set the key columns or record the table as keyless")
	}
	for _, id := range s.AcceptSuggestionIDs {
		if slices.Contains(s.DismissSuggestionIDs, id) {
			return qerrors.NewBadUsagef("rule suggestion %s is in both spec.accept_suggestion_ids and spec.dismiss_suggestion_ids", id)
		}
	}
	return nil
}

// noKeyColumns reports whether spec.no_key_columns is true, the only value
// the wire counts.
func (s AlertResolutionSpec) noKeyColumns() bool {
	return s.NoKeyColumns != nil && *s.NoKeyColumns
}

// ToRequest builds the wire body. Slices are copied so the request never
// aliases the parsed manifest.
func (s AlertResolutionSpec) ToRequest() api.AlertResolveSchema {
	req := api.AlertResolveSchema{
		NewValue:              s.NewValue,
		AnotherFieldNewValue:  s.AnotherFieldNewValue,
		NewDatetimeFormat:     s.NewDatetimeFormat,
		SheetName:             s.SheetName,
		MapValueOnce:          s.MapValueOnce,
		StoredItemID:          s.StoredItemID,
		Comment:               s.Comment,
		Reason:                s.Reason,
		CantSniffHeaderParams: s.CantSniffHeaderParams.ToAPI(),
	}
	if s.Action != "" {
		action := api.AlertAction(s.Action)
		req.Action = &action
	}
	if len(s.FieldNamesResolution) > 0 {
		fields := make([]api.FieldNameResolutionParams, len(s.FieldNamesResolution))
		for i, field := range s.FieldNamesResolution {
			fields[i] = field.toAPI()
		}
		req.FieldNamesResolution = &fields
	}
	if s.FileType != nil {
		fileType := api.FileTypes(*s.FileType)
		req.FileType = &fileType
	}
	if s.Scope != nil {
		scope := api.Scope(*s.Scope)
		req.Scope = &scope
	}
	if len(s.IDs) > 0 {
		ids := append([]int(nil), s.IDs...)
		req.Ids = &ids
	}
	if len(s.InitialSignatures) > 0 {
		sigs := append([]string(nil), s.InitialSignatures...)
		req.InitialSignatures = &sigs
	}
	if len(s.KeyColumns) > 0 {
		keys := append([]string(nil), s.KeyColumns...)
		req.KeyColumns = &keys
	}
	if s.noKeyColumns() {
		noKeys := true
		req.NoKeyColumns = &noKeys
	}
	if len(s.AcceptSuggestionIDs) > 0 {
		ids := append([]openapi_types.UUID(nil), s.AcceptSuggestionIDs...)
		req.AcceptSuggestionIds = &ids
	}
	if len(s.DismissSuggestionIDs) > 0 {
		ids := append([]openapi_types.UUID(nil), s.DismissSuggestionIDs...)
		req.DismissSuggestionIds = &ids
	}
	if len(s.FieldNameToValue) > 0 {
		values := make(map[string]string, len(s.FieldNameToValue))
		for k, v := range s.FieldNameToValue {
			values[k] = v
		}
		req.FieldNameToValue = &values
	}
	return req
}

// Skeleton renders the manifest for one action of one alert: an envelope, the
// action, its required and one-of keys emitted BARE, the optional keys a
// manifest can set commented out, and a comment block carrying everything the
// alert already knows.
//
// Nothing is guessed, and new_value in particular is never prefilled from the
// alert's field_value: handle_one_value_mapping (atlas
// capi/capi/controllers/map_value_controller_mixin.py) returns early when
// current_value == new_value, so a prefilled-and-unedited manifest would
// resolve nothing and report success.
//
// Bare keys are what make an unedited skeleton safe: YAML reads them as null,
// the pointer stays nil, and AlertResolutionSpec.Validate refuses it. Emitting
// `new_value: ""` instead would send the explicit-empty sentinel that
// _handle_composite_conflict_reapply reads as a deliberate submission.
//
// EVERY alert-derived string in the comment block goes through
// pkgmanifest.CommentSafe: the dataset name, the four facts, each of the body's
// prefix_candidates, and the backend's note. They are customer cell data or
// derived from it (or, for the note, spec prose regenerated on every
// `make generate-client`), and a raw newline in any of them ends the
// comment and hands the rest of the value to the YAML parser — see CommentSafe
// for the two failures that reproduced. spec.action is deliberately NOT run
// through it: it is not cell data, it is checked against the alert's own
// AlertActionsList by ValidateAction before Skeleton is reached, and it is
// emitted as a YAML value rather than inside a comment, where CommentSafe is
// the wrong tool.
//
// For apply_migration, detail is the table's migration details, nil when they
// were not read: at a set key step the skeleton carries the key candidates,
// and at the "required" step key_columns is bare and no_key_columns: true
// commented out.
func Skeleton(a *AlertFull, action string, detail *datasets.TableSchemaDetail) string {
	return skeleton(a, action, detail, "qctl alert resolve -f <file>"+YesFlag(action, prompt.CanPrompt()))
}

// ProposeSkeleton is Skeleton with a footer naming `qctl alert propose -f`.
func ProposeSkeleton(a *AlertFull, action string, detail *datasets.TableSchemaDetail) string {
	return skeleton(a, action, detail, "qctl alert propose -f <file>")
}

func skeleton(a *AlertFull, action string, detail *datasets.TableSchemaDetail, next string) string {
	fields := api.AlertActionFields[action]
	var b strings.Builder

	fmt.Fprintf(&b, "# alert %d", a.ID)
	if a.DatasetName != "" {
		fmt.Fprintf(&b, " on table %s (id %d)", pkgmanifest.CommentSafe(a.DatasetName), a.DatasetID)
	}
	b.WriteString("\n")
	writeSkeletonFact(&b, "field_name", a.FieldName)
	writeSkeletonFact(&b, "field_value", a.FieldValue)
	writeSkeletonFact(&b, "another_field_name", a.AnotherFieldName)
	writeSkeletonFact(&b, "another_field_value", a.AnotherFieldValue)
	b.WriteString(PrefixCandidatesComment(a.Body))
	b.WriteString(MaskedFactsComment(SkeletonFacts{
		MaskedFields: a.MaskedFields, FieldName: a.FieldName, FieldValue: a.FieldValue,
		AnotherFieldName: a.AnotherFieldName, AnotherFieldValue: a.AnotherFieldValue, Body: a.Body,
	}))
	keyStep := migrationKeyStep(action, detail)
	b.WriteString(keyStep.comment(detail))
	fmt.Fprintf(&b, "#\n# Fill in every key below, then:  %s\n", next)

	fmt.Fprintf(&b, "apiVersion: %s\n", pkgmanifest.APIVersionV1)
	fmt.Fprintf(&b, "kind: %s\n", AlertResolutionKind)
	fmt.Fprintf(&b, "metadata:\n  id: \"%d\"\n", a.ID)
	fmt.Fprintf(&b, "spec:\n  action: %s\n", action)
	for _, name := range fields.Required {
		fmt.Fprintf(&b, "  %s:\n", name)
	}
	b.WriteString(keyStep.fields())
	for _, group := range fields.OneOf {
		fmt.Fprintf(&b, "  # set exactly one of: %s\n", strings.Join(group, ", "))
		for _, name := range group {
			fmt.Fprintf(&b, "  # %s:\n", name)
		}
	}
	for _, name := range fields.Optional {
		if resolutionFieldDenylist[name] || keyStep != keyStepUnread && keyFields[name] {
			continue
		}
		fmt.Fprintf(&b, "  # %s:\n", name)
	}
	if fields.Note != "" {
		fmt.Fprintf(&b, "#\n# note from the backend: %s\n", pkgmanifest.CommentSafe(fields.Note))
	}
	return b.String()
}

// writeSkeletonFact prints one alert fact as a comment, and nothing when the
// alert does not carry it. The value is cell data, so it is rendered with
// pkgmanifest.CommentSafe and can never leave the one line it is written on.
func writeSkeletonFact(b *strings.Builder, name string, value *string) {
	if value == nil || *value == "" {
		return
	}
	fmt.Fprintf(b, "# %s: %s\n", name, pkgmanifest.CommentSafe(*value))
}

// SkeletonFacts is the part of an alert or warning that MaskedFactsComment
// reads.
type SkeletonFacts struct {
	MaskedFields      *[]string
	FieldName         *string
	FieldValue        *string
	AnotherFieldName  *string
	AnotherFieldValue *string
	Body              *api.AlertBodySchema
}

// MaskedFactsComment renders one skeleton comment line naming the facts
// written above it that carry masked text, or "" when none does. field_value
// and prefix_candidates are masked when masked_fields names one of the
// comma-separated field_name entries; another_field_value when it names
// another_field_name.
func MaskedFactsComment(f SkeletonFacts) string {
	if f.MaskedFields == nil || len(*f.MaskedFields) == 0 {
		return ""
	}
	masked := *f.MaskedFields
	fieldMasked := anyFieldMasked(masked, f.FieldName)
	anotherMasked := f.AnotherFieldName != nil && slices.Contains(masked, *f.AnotherFieldName)

	var names []string
	if fieldMasked && f.FieldValue != nil && *f.FieldValue != "" {
		names = append(names, "field_value")
	}
	if anotherMasked && f.AnotherFieldValue != nil && *f.AnotherFieldValue != "" {
		names = append(names, "another_field_value")
	}
	if fieldMasked && PrefixCandidatesComment(f.Body) != "" {
		names = append(names, "prefix_candidates")
	}
	if len(names) == 0 {
		return ""
	}
	return "# masked for you: " + strings.Join(names, ", ") +
		" (display text, not the stored value; a PII column refuses it as a value)\n"
}

// anyFieldMasked reports whether masked names one of fieldName's
// comma-separated entries.
func anyFieldMasked(masked []string, fieldName *string) bool {
	if fieldName == nil {
		return false
	}
	for name := range strings.SplitSeq(*fieldName, ",") {
		if slices.Contains(masked, strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

// PrefixCandidatesComment renders body.prefix_candidates as one skeleton
// comment line, "# prefix_candidates: <c1>, <c2>\n", each candidate through
// pkgmanifest.CommentSafe and in the body's order. It returns "" when body is
// nil or carries no candidates.
func PrefixCandidatesComment(body *api.AlertBodySchema) string {
	if body == nil || body.PrefixCandidates == nil || len(*body.PrefixCandidates) == 0 {
		return ""
	}
	safe := make([]string, len(*body.PrefixCandidates))
	for i, candidate := range *body.PrefixCandidates {
		safe[i] = pkgmanifest.CommentSafe(candidate)
	}
	return "# prefix_candidates: " + strings.Join(safe, ", ") + "\n"
}
