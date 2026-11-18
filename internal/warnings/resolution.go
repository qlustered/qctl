package warnings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/actionfields"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/prompt"
)

// WarningResolutionKind is the manifest kind `qctl warning resolve -f` accepts.
// Like AlertResolutionKind it is deliberately not a `qctl apply -f` kind; see
// alerts.AlertResolutionKind for the two test-enforced consequences.
const WarningResolutionKind = "WarningResolution"

// warningResolutionFieldDenylist names the WarningsResolveSchema fields no
// manifest can set. TestEveryWarningResolveFieldIsMappedOrDenied fails on a
// field in neither this list nor WarningResolutionSpec.
var warningResolutionFieldDenylist = map[string]bool{
	// action is the spec's own required key, handled outside the field map.
	"action": true,
}

// WarningResolutionSpec mirrors api.WarningsResolveSchema. It is deliberately
// SHORTER than alerts.AlertResolutionSpec: the warning wire schema carries no
// new_datetime_format, sheet_name, file_type, scope or cant_sniff_header_params,
// and offering a key the wire cannot carry would be a lie in --help.
type WarningResolutionSpec struct {
	Action               string            `yaml:"action" json:"action"`
	NewValue             *string           `yaml:"new_value,omitempty" json:"new_value,omitempty"`
	AnotherFieldNewValue *string           `yaml:"another_field_new_value,omitempty" json:"another_field_new_value,omitempty"`
	MapValueOnce         *bool             `yaml:"map_value_once,omitempty" json:"map_value_once,omitempty"`
	IDs                  []int             `yaml:"ids,omitempty" json:"ids,omitempty"`
	InitialSignatures    []string          `yaml:"initial_signatures,omitempty" json:"initial_signatures,omitempty"`
	FieldNameToValue     map[string]string `yaml:"field_name_to_value,omitempty" json:"field_name_to_value,omitempty"`
	Reason               *string           `yaml:"reason,omitempty" json:"reason,omitempty"`
}

// WarningResolution is one `-f` document.
type WarningResolution struct {
	APIVersion string                `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                `yaml:"kind" json:"kind"`
	Metadata   pkgmanifest.Metadata  `yaml:"metadata" json:"metadata"`
	Spec       WarningResolutionSpec `yaml:"spec" json:"spec"`
}

// ParseResolution strict-parses one document.
func ParseResolution(data []byte) (*WarningResolution, error) {
	var doc WarningResolution
	if err := pkgmanifest.StrictUnmarshal(data, &doc); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse WarningResolution manifest")
	}
	return &doc, nil
}

// WarningID reads metadata.id as the positive integer the warning routes take.
func (r *WarningResolution) WarningID() (int, error) {
	if r.Metadata.ID == "" {
		return 0, qerrors.NewBadUsage("metadata.id is required: it names the warning to resolve")
	}
	id, err := strconv.Atoi(r.Metadata.ID)
	if err != nil || id <= 0 {
		return 0, qerrors.NewBadUsagef("invalid metadata.id %q: expected a positive warning id", r.Metadata.ID)
	}
	return id, nil
}

// PresentFields reports which wire fields this spec actually supplies.
func (s WarningResolutionSpec) PresentFields() actionfields.Present {
	p := actionfields.Present{}
	p.Set("new_value", s.NewValue != nil)
	p.Set("another_field_new_value", s.AnotherFieldNewValue != nil)
	p.Set("map_value_once", s.MapValueOnce != nil)
	p.Set("ids", len(s.IDs) > 0)
	p.Set("initial_signatures", len(s.InitialSignatures) > 0)
	p.Set("field_name_to_value", len(s.FieldNameToValue) > 0)
	p.Set("reason", s.Reason != nil)
	return p
}

// Validate checks the spec against the backend's contract for its action.
//
// spec.map_value_once has no arm of its own: from atlas #1059
// x-warning-action-fields lists it for no warning action, so actionfields
// refuses it on presence like any other unread key, whatever its value and
// whatever the action. TestWarningActionFieldsListNoAlertOnlyField is what
// notices if the backend ever lists it again.
func (s WarningResolutionSpec) Validate() error {
	if s.Action == "" {
		return qerrors.NewBadUsage("spec.action is required: name the action to resolve with")
	}
	return actionfields.Validate(s.Action, api.WarningActionFields[s.Action], s.PresentFields())
}

// ToRequest builds the wire body, copying every slice and map.
func (s WarningResolutionSpec) ToRequest() api.WarningsResolveSchema {
	action := api.WarningAction(s.Action)
	req := api.WarningsResolveSchema{
		Action:               &action,
		NewValue:             s.NewValue,
		AnotherFieldNewValue: s.AnotherFieldNewValue,
		MapValueOnce:         s.MapValueOnce,
		Reason:               s.Reason,
	}
	if len(s.IDs) > 0 {
		ids := append([]int(nil), s.IDs...)
		req.Ids = &ids
	}
	if len(s.InitialSignatures) > 0 {
		sigs := append([]string(nil), s.InitialSignatures...)
		req.InitialSignatures = &sigs
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

// Skeleton renders the manifest for one action of one warning: an envelope, the
// action, its required and one-of keys emitted BARE, its optional keys
// commented out, and a comment block carrying everything the warning already
// knows. It is the warning half of alerts.Skeleton — see that copy for why
// nothing is guessed, why the required keys are emitted bare, and why every
// warning-derived string in the comment block goes through
// pkgmanifest.CommentSafe while spec.action does not.
func Skeleton(w *WarningFull, action string) string {
	return skeleton(w, action, "qctl warning resolve -f <file>"+YesFlag(action, prompt.CanPrompt()))
}

// ProposeSkeleton is Skeleton with a footer naming `qctl warning propose -f`.
func ProposeSkeleton(w *WarningFull, action string) string {
	return skeleton(w, action, "qctl warning propose -f <file>")
}

func skeleton(w *WarningFull, action, next string) string {
	fields := api.WarningActionFields[action]
	var b strings.Builder

	fmt.Fprintf(&b, "# warning %d", w.ID)
	if w.DatasetName != "" {
		fmt.Fprintf(&b, " on table %s (id %d)", pkgmanifest.CommentSafe(w.DatasetName), w.DatasetID)
	}
	b.WriteString("\n")
	writeSkeletonFact(&b, "field_name", w.FieldName)
	writeSkeletonFact(&b, "field_value", w.FieldValue)
	writeSkeletonFact(&b, "another_field_name", w.AnotherFieldName)
	writeSkeletonFact(&b, "another_field_value", w.AnotherFieldValue)
	if action == string(api.OverwriteRuleOutputs) && w.RuleOutputColumns != nil {
		writeSkeletonFact(&b, "rule_output_columns", ptr(strings.Join(*w.RuleOutputColumns, ", ")))
	}
	b.WriteString(alerts.PrefixCandidatesComment(w.Body))
	b.WriteString(alerts.MaskedFactsComment(alerts.SkeletonFacts{
		MaskedFields: w.MaskedFields, FieldName: w.FieldName, FieldValue: w.FieldValue,
		AnotherFieldName: w.AnotherFieldName, AnotherFieldValue: w.AnotherFieldValue, Body: w.Body,
	}))
	fmt.Fprintf(&b, "#\n# Fill in every key below, then:  %s\n", next)

	fmt.Fprintf(&b, "apiVersion: %s\n", pkgmanifest.APIVersionV1)
	fmt.Fprintf(&b, "kind: %s\n", WarningResolutionKind)
	fmt.Fprintf(&b, "metadata:\n  id: \"%d\"\n", w.ID)
	fmt.Fprintf(&b, "spec:\n  action: %s\n", action)
	for _, name := range fields.Required {
		fmt.Fprintf(&b, "  %s:\n", name)
	}
	for _, group := range fields.OneOf {
		fmt.Fprintf(&b, "  # set exactly one of: %s\n", strings.Join(group, ", "))
		for _, name := range group {
			fmt.Fprintf(&b, "  # %s:\n", name)
		}
	}
	for _, name := range fields.Optional {
		fmt.Fprintf(&b, "  # %s:\n", name)
	}
	if fields.Note != "" {
		fmt.Fprintf(&b, "#\n# note from the backend: %s\n", pkgmanifest.CommentSafe(fields.Note))
	}
	return b.String()
}

// writeSkeletonFact prints one warning fact as a comment, and nothing when the
// warning does not carry it. The value is cell data, so it is rendered with
// pkgmanifest.CommentSafe and can never leave the one line it is written on.
func writeSkeletonFact(b *strings.Builder, name string, value *string) {
	if value == nil || *value == "" {
		return
	}
	fmt.Fprintf(b, "# %s: %s\n", name, pkgmanifest.CommentSafe(*value))
}

// ptr returns a pointer to s.
func ptr(s string) *string {
	return &s
}
