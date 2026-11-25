package change_proposals //nolint:revive // Established package-path style in this repo (rule_reviews, privilege_requests).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/warnings"
)

// noneListed stands in for an empty list.
const noneListed = "(none)"

// WritePayload writes a "## PAYLOAD" section: the payload as the resolution
// manifest it carries, or as raw YAML when it does not map onto one. It
// reports whether the payload mapped. The text is not sanitized.
func WritePayload(b *strings.Builder, p *Proposal) bool {
	b.WriteString("\n## PAYLOAD\n")
	if p.Operation == api.ChangeReviewOperationDatasetRulePatch {
		return writeRulePayload(b, p)
	}
	manifest, kind, err := PayloadManifest(p)
	if err == nil {
		fmt.Fprintf(b, "The %s manifest it would resolve with (the form 'qctl %s resolve -f' reads):\n",
			kind, strings.ToLower(strings.TrimSuffix(kind, "Resolution")))
		WriteYAMLBlock(b, manifest)
		return true
	}
	fmt.Fprintf(b, "The raw request body (%v):\n", err)
	WriteYAMLBlock(b, map[string]interface{}{"payload": p.Payload})
	return false
}

// writeRulePayload writes a rule-param proposal's params, or its raw body
// when it carries anything but a params object, and reports which.
func writeRulePayload(b *strings.Builder, p *Proposal) bool {
	params, err := RuleParams(p)
	if err != nil {
		fmt.Fprintf(b, "The raw request body (%v):\n", err)
		WriteYAMLBlock(b, map[string]interface{}{"payload": p.Payload})
		return false
	}
	b.WriteString("The params the rule would be patched with (they replace its params as a whole):\n")
	WriteYAMLBlock(b, map[string]interface{}{"params": params})
	return true
}

// RuleParams is a rule-param proposal's proposed params: the payload's
// params object, which must be its only key.
func RuleParams(p *Proposal) (map[string]interface{}, error) {
	for k := range p.Payload {
		if k != "params" {
			return nil, fmt.Errorf("it carries %q, and a rule-param proposal carries only params", k)
		}
	}
	params, ok := p.Payload["params"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("its params is not an object")
	}
	return params, nil
}

// PayloadManifest decodes the payload's non-null keys strictly into the
// AlertResolution or WarningResolution document for the proposal's target,
// returning it and its kind.
func PayloadManifest(p *Proposal) (interface{}, string, error) {
	set := make(map[string]interface{}, len(p.Payload))
	for key, value := range p.Payload {
		if value != nil {
			set[key] = value
		}
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return nil, "", fmt.Errorf("payload is not JSON: %w", err)
	}
	switch {
	case p.Operation == api.ChangeReviewOperationAlertResolve && p.AlertItemID != nil:
		doc := alerts.AlertResolution{APIVersion: pkgmanifest.APIVersionV1, Kind: alerts.AlertResolutionKind,
			Metadata: pkgmanifest.Metadata{ID: strconv.Itoa(*p.AlertItemID)}}
		if err := strictJSON(raw, &doc.Spec); err != nil {
			return nil, "", fmt.Errorf("it does not map onto an %s spec: %w", alerts.AlertResolutionKind, err)
		}
		return doc, alerts.AlertResolutionKind, nil
	case p.Operation == api.ChangeReviewOperationWarningResolve && p.WarningItemID != nil:
		doc := warnings.WarningResolution{APIVersion: pkgmanifest.APIVersionV1, Kind: warnings.WarningResolutionKind,
			Metadata: pkgmanifest.Metadata{ID: strconv.Itoa(*p.WarningItemID)}}
		if err := strictJSON(raw, &doc.Spec); err != nil {
			return nil, "", fmt.Errorf("it does not map onto a %s spec: %w", warnings.WarningResolutionKind, err)
		}
		return doc, warnings.WarningResolutionKind, nil
	case p.AlertItemID == nil && p.WarningItemID == nil && TargetKind(p.Operation) != "":
		return nil, "", fmt.Errorf("its target %s was deleted, so there is no %[1]s to resolve",
			TargetKind(p.Operation))
	default:
		target := Target(p)
		if target == "" {
			target = "no target"
		}
		return nil, "", fmt.Errorf("no resolution manifest for operation %s on %s", p.Operation, target)
	}
}

func strictJSON(raw []byte, v interface{}) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// WriteYAMLBlock writes v's JSON form as a fenced YAML block.
func WriteYAMLBlock(b *strings.Builder, v interface{}) {
	var buf bytes.Buffer
	raw, err := json.Marshal(v)
	if err == nil {
		err = output.WriteJSONAsYAML(&buf, raw)
	}
	if err != nil {
		fmt.Fprintf(b, "(could not render: %v)\n", err)
		return
	}
	b.WriteString("```yaml\n")
	b.Write(buf.Bytes())
	b.WriteString("```\n")
}

// PayloadAction is the payload's action, "" when it names none.
func PayloadAction(p *Proposal) string {
	action, _ := p.Payload["action"].(string)
	return action
}

var validityMeaning = map[api.ChangeProposalReviewSchemaValidity]string{
	api.ChangeProposalReviewSchemaValidityCurrent:  "it applies as submitted",
	api.ChangeProposalReviewSchemaValidityStale:    "it needs an edit before it applies; approve is refused with 409 CHANGE_PROPOSAL_STALE",
	api.ChangeProposalReviewSchemaValidityObsolete: "it can no longer apply; its next step turns it obsolete",
}

// WriteReview writes a "## REVIEW" section: the server's review of the
// proposal now, with what approving it changes and how that moved since it
// was submitted. The text is not sanitized.
func WriteReview(b *strings.Builder, d *Detail) {
	b.WriteString("\n## REVIEW\n")
	r := d.Review
	if r == nil {
		b.WriteString("(none — the server reviews only a draft, pending_review or changes_requested proposal)\n")
		return
	}
	validity := string(r.Validity)
	if meaning, ok := validityMeaning[r.Validity]; ok {
		validity += " — " + meaning
	}
	fmt.Fprintf(b, "Validity: %s\n", validity)
	dependsOn := DependsOn(&d.Proposal)
	if len(r.Reasons) > 0 {
		b.WriteString("Reasons:\n")
		for _, reason := range r.Reasons {
			fmt.Fprintf(b, "- %s: %s\n", reason.Reason, reason.Message)
			writeDependencyStep(b, reason.Reason, d.ID.String(), dependsOn)
		}
	}
	if len(r.Blockers) == 0 {
		b.WriteString("Blockers: (none)\n")
	} else {
		b.WriteString("Blockers (the operation would be refused; approve answers 409 CHANGE_PROPOSAL_BLOCKED):\n")
		for _, blocker := range r.Blockers {
			fmt.Fprintf(b, "- %s\n", BlockerLine(blocker))
			name, _ := blocker["blocker"].(string)
			writeDependencyStep(b, name, d.ID.String(), dependsOn)
		}
	}
	if d.Operation == api.ChangeReviewOperationDatasetRulePatch {
		writeRuleImpact(b, d)
	} else {
		writeImpact(b, r.Impact)
	}
	fmt.Fprintf(b, "Resource keys: %s\n", keyList(r.ResourceKeys))
	writeImpactDiff(b, d)
	if r.ImpactDigest == nil {
		b.WriteString("Impact digest: (withheld — your view of the payload is masked, so you cannot approve it)\n")
		return
	}
	fmt.Fprintf(b, "Impact digest: %s (approve sends it; a different one now is refused with 409 CHANGE_PROPOSAL_IMPACT_CHANGED)\n",
		*r.ImpactDigest)
}

// The review entries a dependency adds: the reason a failed dependency
// gives, and the blocker of one not executed yet.
const (
	ReasonDependencyFailed       = "dependency_failed"
	BlockerDependencyNotExecuted = "dependency_not_executed"
)

// DependsOn is the id of the proposal p depends on, "" when none.
func DependsOn(p *Proposal) string {
	if p.DependsOnID == nil {
		return ""
	}
	return p.DependsOnID.String()
}

// DependsOnLine is the "Depends on:" line for p.
func DependsOnLine(p *Proposal) string {
	id := DependsOn(p)
	if id == "" {
		return "Depends on: " + noneListed + "\n"
	}
	return fmt.Sprintf("Depends on: change-proposal/%s (approve is refused until it is executed or obsolete)\n", id)
}

// NoteLine is the "Proposer note:" line of p.
func NoteLine(p *Proposal) string {
	if p.Note == nil || *p.Note == "" {
		return "Proposer note: " + noneListed + "\n"
	}
	return fmt.Sprintf("Proposer note: %q\n", *p.Note)
}

// writeDependencyStep writes the step that clears entry, a review reason or
// blocker name, on proposal id when entry is one a dependency adds;
// dependsOn is the dependency's id.
func writeDependencyStep(b *strings.Builder, entry, id, dependsOn string) {
	switch entry {
	case ReasonDependencyFailed:
		fmt.Fprintf(b, "  drop the dependency, or point it at another proposal with --depends-on:\n"+
			"  qctl change-proposal edit %s --remove-depends-on\n", id)
	case BlockerDependencyNotExecuted:
		if dependsOn == "" {
			return
		}
		fmt.Fprintf(b, "  it clears once the dependency is approved and executed:\n"+
			"  qctl describe change-proposal %s\n", dependsOn)
	}
}

func keyList(keys []string) string {
	if len(keys) == 0 {
		return noneListed
	}
	return strings.Join(keys, ", ")
}

// WriteOverlaps writes a "## OVERLAPS" section: the other live proposals of
// the table that act on a resource this one acts on, with the resource keys
// they share. The text is not sanitized.
func WriteOverlaps(b *strings.Builder, d *Detail) {
	b.WriteString("\n## OVERLAPS\n")
	if d.Overlaps == nil {
		b.WriteString("(none — the server computes overlaps only for a draft, pending_review or changes_requested proposal)\n")
		return
	}
	if len(*d.Overlaps) == 0 {
		b.WriteString("(none — no other draft, pending_review or changes_requested proposal of this table that you " +
			"can see acts on the same resources)\n")
		return
	}
	b.WriteString("Other live proposals of this table acting on the same resources, oldest first:\n")
	dependsOn := DependsOn(&d.Proposal)
	for _, o := range *d.Overlaps {
		line := fmt.Sprintf("- change-proposal/%s (%s, %s on %s): %s", o.ID, o.State, o.Operation, overlapTarget(o),
			strings.Join(o.ResourceKeys, ", "))
		if o.ID.String() == dependsOn {
			line += " — this proposal depends on it"
		}
		b.WriteString(line + "\n")
	}
	for _, action := range d.AllowedActions {
		if action == api.ChangeProposalActionEdit {
			fmt.Fprintf(b, "To approve this one only after another: qctl change-proposal edit %s --depends-on <proposal-id>\n", d.ID)
			break
		}
	}
}

// overlapTarget names an overlapping proposal's target as alert/<id>,
// warning/<id> or rule/<uuid>.
func overlapTarget(o Overlap) string {
	switch {
	case o.AlertItemID != nil:
		return "alert/" + strconv.Itoa(*o.AlertItemID)
	case o.WarningItemID != nil:
		return "warning/" + strconv.Itoa(*o.WarningItemID)
	case o.DatasetRuleID != nil:
		return RuleRef(*o.DatasetRuleID)
	default:
		return "no target"
	}
}

// Impact keys writeImpact renders as lines.
var (
	renderedBound   = map[string]bool{"operation": true, "target": true, "issue_type": true, "payload": true, "addressed_ids": true, "keys": true, "key_columns": true}
	renderedContext = map[string]bool{"files": true, "same_issue_sweep_ids": true}
)

func writeImpact(b *strings.Builder, impact Impact) {
	if t, ok := impact.Bound["issue_type"].(string); ok && t != "" {
		fmt.Fprintf(b, "Issue type: %s\n", t)
	}
	b.WriteString("Changes (by policy key):\n")
	keys := contextMaps(impact.Bound["keys"])
	if len(keys) == 0 {
		b.WriteString("- (none)\n")
	}
	for _, k := range keys {
		fmt.Fprintf(b, "- %s\n", keyLine(k))
	}
	b.WriteString(keyColumnsLine(impact.Bound["key_columns"]))
	fmt.Fprintf(b, "Linked files: %s\n", fileList(impact.Context["files"]))
	kind := "issue"
	if target, ok := impact.Bound["target"].(map[string]interface{}); ok {
		if k, ok := target["kind"].(string); ok && k != "" {
			kind = k
		}
	}
	fmt.Fprintf(b, "Same-issue sweep (also resolved): %s\n", refList(kind, impact.Context["same_issue_sweep_ids"]))
	rest := map[string]interface{}{}
	for k, v := range impact.Bound {
		if !renderedBound[k] {
			rest[k] = v
		}
	}
	for k, v := range impact.Context {
		if !renderedContext[k] {
			rest[k] = v
		}
	}
	if len(rest) > 0 {
		b.WriteString("Other impact fields:\n")
		WriteYAMLBlock(b, rest)
	}
}

// keyColumnsLine renders bound.key_columns, {current, new, no_key_columns},
// as one "Key columns:" line; "" when it is absent.
func keyColumnsLine(v interface{}) string {
	k, ok := v.(map[string]interface{})
	if !ok {
		return ""
	}
	current := joinList(k["current"])
	var outcome string
	switch {
	case k["no_key_columns"] == true:
		outcome = "none (the table is recorded as keyless)"
	case k["new"] != nil:
		outcome = joinList(k["new"])
	default:
		outcome = "kept"
	}
	return fmt.Sprintf("Key columns: %s -> %s (applying locks them)\n", current, outcome)
}

// keyLine renders one bound key's catalog description.
func keyLine(k map[string]interface{}) string {
	line := fmt.Sprint(k["key"])
	if _, described := k["modifies_data"]; !described {
		return line + ": (the action catalog does not describe it)"
	}
	parts := []string{
		"scope " + joinList(k["scope"]),
		fmt.Sprintf("modifies data: %v", k["modifies_data"]),
		"follow-on " + joinList(k["follow_on"]),
		fmt.Sprintf("reversible: %v", k["reversible"]),
	}
	return line + ": " + strings.Join(parts, "; ")
}

func joinList(v interface{}) string {
	items, _ := v.([]interface{})
	if len(items) == 0 {
		return noneListed
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = fmt.Sprint(item)
	}
	return strings.Join(out, ", ")
}

func fileList(v interface{}) string {
	files := contextMaps(v)
	if len(files) == 0 {
		return noneListed
	}
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = fmt.Sprint(f["id"])
		if key, _ := f["key"].(string); key != "" {
			out[i] += " (" + key + ")"
		}
	}
	return strings.Join(out, ", ")
}

func refList(kind string, v interface{}) string {
	items, _ := v.([]interface{})
	if len(items) == 0 {
		return noneListed
	}
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = fmt.Sprintf("%s/%v", kind, item)
	}
	return strings.Join(out, ", ")
}

func writeImpactDiff(b *strings.Builder, d *Detail) {
	switch {
	case d.ImpactAtSubmit == nil:
		b.WriteString("Changed since submit: (no impact was stored when it went to review)\n")
	case d.ImpactDiff == nil && len(MaskedFields(&d.Proposal)) > 0:
		b.WriteString("Changed since submit: (withheld — your view of the payload is masked)\n")
	case d.ImpactDiff == nil || len(*d.ImpactDiff) == 0:
		b.WriteString("Changed since submit: nothing\n")
	default:
		keys := make([]string, 0, len(*d.ImpactDiff))
		for k := range *d.ImpactDiff {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fmt.Fprintf(b, "Changed since submit (%s):\n", strings.Join(keys, ", "))
		WriteYAMLBlock(b, *d.ImpactDiff)
	}
}

// ExecutionError renders the recorded error as "<status> <code>: <message>",
// falling back to its JSON.
func ExecutionError(v interface{}) string {
	m, ok := v.(map[string]interface{})
	if !ok {
		raw, _ := json.Marshal(v)
		return string(raw)
	}
	var parts []string
	if s, ok := m["status_code"]; ok && s != nil {
		parts = append(parts, fmt.Sprint(s))
	}
	if c, ok := m["error_code"].(string); ok && c != "" {
		parts = append(parts, c)
	}
	head := strings.Join(parts, " ")
	msg, _ := m["message"].(string)
	switch {
	case head != "" && msg != "":
		return head + ": " + msg
	case msg != "":
		return msg
	case head != "":
		return head
	}
	raw, _ := json.Marshal(m)
	return string(raw)
}

// nextCommands spell the command for each step on a proposal; %s is its id.
var nextCommands = map[Action]string{
	api.ChangeProposalActionEdit:           "qctl change-proposal edit %s -f <file.yaml>",
	api.ChangeProposalActionSubmit:         "qctl change-proposal submit %s",
	api.ChangeProposalActionWithdraw:       "qctl change-proposal withdraw %s",
	api.ChangeProposalActionApprove:        "qctl change-proposal approve %s",
	api.ChangeProposalActionReject:         `qctl change-proposal reject %s --reason "..."`,
	api.ChangeProposalActionRequestChanges: `qctl change-proposal request-changes %s --reason "..."`,
	api.ChangeProposalActionRerunDryRun:    "qctl change-proposal rerun-dry-run %s",
}

// ruleEditCommand spells edit for a rule-param proposal; %s is its id.
const ruleEditCommand = "qctl change-proposal edit %s --set-param <key>=<value>"

// NextCommand is the command that takes action on p; false when qctl has
// none. approve carries --yes when stdin cannot answer its prompt.
func NextCommand(action Action, p *Proposal) (string, bool) {
	format, ok := nextCommands[action]
	if !ok {
		return "", false
	}
	if action == api.ChangeProposalActionEdit && p.Operation == api.ChangeReviewOperationDatasetRulePatch {
		format = ruleEditCommand
	}
	command := fmt.Sprintf(format, p.ID)
	if action == api.ChangeProposalActionApprove {
		command += prompt.YesFlag(false)
	}
	return command, true
}
