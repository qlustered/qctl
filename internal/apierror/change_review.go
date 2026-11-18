package apierror

import (
	"encoding/json"
	stderrors "errors"
	"fmt"
	"strings"
)

// CodeChangeProposalRequired is the 403 refusing a change that the change
// review policy in force lets the caller's role propose but not make.
const CodeChangeProposalRequired = "CHANGE_PROPOSAL_REQUIRED"

// CodeChangeReviewPolicyDenied is the 403 refusing a change that the change
// review policy in force does not let the caller's role make.
const CodeChangeReviewPolicyDenied = "CHANGE_REVIEW_POLICY_DENIED"

// changeReviewCodes are the change review 403s. The server raises them after
// it found the target and evaluated the policy, so buildHTTPError attaches no
// organization-scope note to them.
var changeReviewCodes = map[string]bool{
	CodeChangeProposalRequired:   true,
	CodeChangeReviewPolicyDenied: true,
}

// The change review operations a hint names a command for.
const (
	OperationAlertResolve     = "alert_resolve"
	OperationWarningResolve   = "warning_resolve"
	OperationDatasetRulePatch = "dataset_rule_patch"
	// OperationDataReviewApprove governs data review approvals and every
	// duplicate-group reviewer route; none of them has a proposal path.
	OperationDataReviewApprove = "data_review_approve"
)

// organizationOperations are the operations governed by the organization's
// own change review policy rather than a table's.
var organizationOperations = map[string]bool{
	"rule_revision_submit": true,
	"rule_revision_delete": true,
	"rule_revision_patch":  true,
}

// ChangeReviewRefusal is the context of a change review 403. A field the
// server sent malformed or unsafe to print is left empty.
type ChangeReviewRefusal struct {
	Code      string
	Operation string
	Verb      string
	// DeniedKeys are the refused policy keys, each key's parts joined with
	// "/". Meaningful only when KeysKnown.
	DeniedKeys []string
	// KeysKnown reports whether denied_keys was present and well formed.
	KeysKnown bool
}

// DeniesKey reports whether key is one of the refused keys. It is true for
// every key when the refused keys are unknown.
func (r ChangeReviewRefusal) DeniesKey(key string) bool {
	if !r.KeysKnown {
		return true
	}
	for _, denied := range r.DeniedKeys {
		if denied == key {
			return true
		}
	}
	return false
}

// ChangeReviewRefusalOf returns the refusal carried by the *OperationalError
// in err's chain, and false when that error carries neither change review
// code.
func ChangeReviewRefusalOf(err error) (ChangeReviewRefusal, bool) {
	var opErr *OperationalError
	if !stderrors.As(err, &opErr) {
		return ChangeReviewRefusal{}, false
	}
	if !changeReviewCodes[opErr.ErrorCode] {
		return ChangeReviewRefusal{}, false
	}
	return parseChangeReviewRefusal(opErr), true
}

func parseChangeReviewRefusal(e *OperationalError) ChangeReviewRefusal {
	refusal := ChangeReviewRefusal{Code: e.ErrorCode}
	var ctx struct {
		Operation  json.RawMessage `json:"operation"`
		Verb       json.RawMessage `json:"verb"`
		DeniedKeys json.RawMessage `json:"denied_keys"`
	}
	if err := json.Unmarshal(e.Context, &ctx); err != nil {
		return refusal
	}
	refusal.Operation = policyToken(ctx.Operation)
	refusal.Verb = policyToken(ctx.Verb)
	refusal.DeniedKeys, refusal.KeysKnown = parseDeniedKeys(ctx.DeniedKeys)
	return refusal
}

// policyToken returns raw as a string when it is a JSON string isPolicyToken
// accepts, and "" otherwise.
func policyToken(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil || !isPolicyToken(s) {
		return ""
	}
	return s
}

func parseDeniedKeys(raw json.RawMessage) ([]string, bool) {
	var keys [][]string
	if err := json.Unmarshal(raw, &keys); err != nil || keys == nil {
		return nil, false
	}
	joined := make([]string, 0, len(keys))
	for _, key := range keys {
		if len(key) == 0 {
			return nil, false
		}
		for _, part := range key {
			if !isPolicyToken(part) {
				return nil, false
			}
		}
		joined = append(joined, strings.Join(key, "/"))
	}
	return joined, true
}

// isPolicyToken reports whether s is a non-empty identifier of letters,
// digits, '_', '.' and '-', and so safe to print in a terminal.
func isPolicyToken(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		ok := r == '_' || r == '.' || r == '-' ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}

func changeProposalRequiredHint(e *OperationalError) string {
	switch parseChangeReviewRefusal(e).Operation {
	case OperationAlertResolve:
		return "Propose the resolution for review instead: `qctl alert propose <alert-id> --action <action>`, " +
			"or `qctl alert propose -f <file>` with the AlertResolution manifest."
	case OperationWarningResolve:
		return "Propose the resolution for review instead: `qctl warning propose <warning-id> --action <action>`, " +
			"or `qctl warning propose -f <file>` with the WarningResolution manifest."
	case OperationDatasetRulePatch:
		return "Propose the params change for review instead: " +
			"`qctl table-rule propose <rule> --table-id <id> --set-param <key>=<value>`."
	case OperationDataReviewApprove:
		return "This change has no proposal path: someone whose role the policy lists must make it. " +
			changeReviewPolicyPointer(OperationDataReviewApprove)
	}
	return "Propose the change for review instead, with `qctl alert propose`, `qctl warning propose` or `qctl table-rule propose`."
}

func changeReviewPolicyDeniedHint(e *OperationalError) string {
	refusal := parseChangeReviewRefusal(e)
	pointer := changeReviewPolicyPointer(refusal.Operation)
	if denied := deniedSentence(refusal); denied != "" {
		return denied + " " + pointer
	}
	return pointer
}

// deniedSentence says what the refusal denied, or "" when its context does
// not say.
func deniedSentence(r ChangeReviewRefusal) string {
	if r.Operation == "" || !r.KeysKnown {
		return ""
	}
	verb := r.Verb
	if verb == "" {
		verb = "change"
	}
	switch {
	case len(r.DeniedKeys) == 1:
		return fmt.Sprintf("The policy does not let your role %s %s key %s.", verb, r.Operation, r.DeniedKeys[0])
	case len(r.DeniedKeys) > 1:
		return fmt.Sprintf("The policy does not let your role %s %s keys %s.", verb, r.Operation, strings.Join(r.DeniedKeys, ", "))
	case verb == "resolve" || verb == "propose":
		return fmt.Sprintf("The change maps to no %s key the policy can list, so the policy refuses it for every role.", r.Operation)
	}
	return ""
}

// changeReviewPolicyPointer names the get change-review-policy command that
// shows the policy governing operation.
func changeReviewPolicyPointer(operation string) string {
	switch {
	case organizationOperations[operation]:
		return "`qctl get change-review-policy` shows the roles the organization's policy lists for each key."
	case operation == "":
		return "`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key; " +
			"with no selector it shows the organization's policy."
	}
	return "`qctl get change-review-policy --table <table>` shows the roles the policy in force lists for each key."
}
