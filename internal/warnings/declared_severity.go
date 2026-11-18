package warnings

import (
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/output"
)

// RenderDeclaredSeverityPlain explains this warning's `body.declared_severity`
// as plain lines, for the views that are not YAML documents.
//
// The wording lives in internal/alerts because the field does: `body` is the
// SAME wire type on both resources (`WarningSchema.body: AlertBodySchema |
// None`, atlas capi/capi/schemas/warning_schema.py), and the backend writes
// `declared_severity` before it decides which of the two the issue becomes, so
// a warning carries it exactly as an alert does — with the value going the
// other way, `blocker` demoted rather than `warning` promoted. Delegating is
// what keeps one sentence in one place; the two packages otherwise mirror each
// other by hand, which is how a reworded half drifts from its twin.
func RenderDeclaredSeverityPlain(w *WarningFull) string {
	if w == nil {
		return ""
	}
	return alerts.RenderDeclaredSeverityPlain(w.Body, alerts.IssueClassWarning)
}

// RenderDeclaredSeverity is RenderDeclaredSeverityPlain as a YAML comment
// block, for `describe warning`'s default document view and its `-o json`-less
// siblings. Same split as RenderNextActions / RenderNextActionsPlain.
func RenderDeclaredSeverity(w *WarningFull) string {
	return output.YAMLComment(RenderDeclaredSeverityPlain(w))
}
