package alerts

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/output"
)

// The two classes a rule finding can land in. They are the words the note puts
// in front of the reader, not wire values: nothing sends them anywhere.
const (
	IssueClassAlert   = "alert"
	IssueClassWarning = "warning"
)

// RenderDeclaredSeverityPlain explains `body.declared_severity` — the severity
// the RULE gave an issue, which the backend records only when the rule
// instance's `spec.severity` policy overrode it.
//
// It takes the body rather than an alert because the two resources share one
// wire type: `WarningSchema.body` is typed `AlertBodySchema | None` (atlas
// capi/capi/schemas/warning_schema.py), and the block that writes the field
// runs BEFORE the alert/warning branch
// (cettings/cettings/controllers/rule_controller/rule_checker.py
// `get_issue_exception_from_issue_context`, the `if issue_context.is_promoted:`
// block, then `if issue_context.is_blocker:`), so a blocker-declared issue that a
// `severity: warning` policy demoted arrives as a WARNING carrying
// `declared_severity: blocker`. One renderer, two callers, no second copy of
// the wording.
//
// The value alone misleads in this position: read on an alert, `warning` looks
// like the alert's own severity. So the note says whose value it is, that the
// policy overrode it, and where the policy lives.
//
// Returns "" when the field is absent, which is the common case — the backend
// sets it exactly when `severity != declared_severity` (`is_promoted`,
// common/common/exceptions.py), in EITHER direction, so its presence
// is itself the statement that the policy changed something.
func RenderDeclaredSeverityPlain(body *api.AlertBodySchema, class string) string {
	if body == nil || body.DeclaredSeverity == nil || *body.DeclaredSeverity == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("\nDECLARED SEVERITY\n")
	fmt.Fprintf(&b, "  the rule marked this issue %s, and the table rule's spec.severity\n",
		string(*body.DeclaredSeverity))
	fmt.Fprintf(&b, "  policy overrode that — the class you are reading it in (%s) is what\n", class)
	b.WriteString("  the policy resolved it to, not what the rule asked for. The backend\n")
	b.WriteString("  records the rule's own value only when the two differ, so this field's\n")
	b.WriteString("  presence IS the override.\n")
	b.WriteString("  The policy is spec.severity on the table rule, not a property of this\n")
	b.WriteString("  item; change it there (qctl explain table-rule).\n")
	return b.String()
}

// RenderDeclaredSeverity is RenderDeclaredSeverityPlain as a YAML comment
// block, for the views that print a parseable document. Same split as
// RenderNextActions / RenderNextActionsPlain: one builder, two prefixes.
func RenderDeclaredSeverity(body *api.AlertBodySchema, class string) string {
	return output.YAMLComment(RenderDeclaredSeverityPlain(body, class))
}
