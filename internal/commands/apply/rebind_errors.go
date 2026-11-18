package apply

import (
	"fmt"
	"strings"
)

// explainRebindError appends qctl-side next steps to the backend refusals a
// table-rule patch can hit — the two a rebind runs into (the rule already has
// a candidate in flight; the target revision is not in this rule's family or
// is not enabled) and the one a state change riding along with a
// configuration change hits.
//
// It matches on message text because these refusals carry no error_code:
// atlas raises them as a bare APIError(409, "..."), and APIError.to_dict
// drops error_code when it is None (common/common/api_exceptions.py),
// so the body is {"detail":{"msg":"..."}} and nothing machine-readable
// distinguishes them. The prose does reach Error(): with no error_code the
// classification is Passthrough, and OperationalError.Format renders the
// server's own message (internal/apierror/parser.go).
//
// The wrap is %w, never %v: GetExitCode walks the Unwrap chain, and a %v
// here would silently degrade a 409 to exit 1 while the message still looks
// perfect. An unrecognized error is returned unchanged.
//
// The fourth rebind refusal -- the table's review policy,
// the `if not can_enable:` raise in build_and_validate_candidate_snapshot
// (rule_revalidation_view_controller.py) -- is NOT matched here, and
// not because it is machine-readable: it is raised just as bare as these
// three. It is left alone because can_enable_revision_sync already returns a
// `reason` qctl cannot improve on: it names the cause, and in the two
// review-missing cases (rule_review_controller.py) the next step as well. The
// five _dry_run_requirements_met returns name a cause only -- so passthrough
// says more than anything qctl would append, but not because every reason is
// actionable. (The ErrorCode.REVIEW_REQUIRED raise in rule_view.py's
// patch_dataset_rule is a different door -- it sits inside `if becoming_enabled:`,
// which the `if rides_a_candidate:` branch returns before on any rebind.)
//
// Matching on prose has no compile-time tripwire, so the drift alarm is
// documentary: all four refusals are a [backend]-tagged GOTCHAS bullet in
// internal/resources/pages/table-rule.md ("a patch-mode rebind can be refused
// with 409 in four ways"), keyed into tools/gotchassweep/baseline.yaml.
// `make gotchas-sweep` re-raises it whenever a new OpenAPI spec lands, which
// is the moment to re-read these atlas raises and confirm the strings below
// still appear in them.
func explainRebindError(err error, doc manifestDoc) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	switch {
	// atlas capi/capi/controllers/rule_revalidation_view_controller.py
	// create_revalidation_candidate.
	//
	// This is a concurrent-write race guard, not the everyday "you already
	// have a candidate" state. Its only production caller is the call in
	// _start_revalidation_candidate's _stage (capi/capi/views/rule_view.py), which
	// sits inside `if candidate is None:` — and when a candidate really is pending
	// the patch takes apply_candidate_replacement (rule_view.py) instead, which
	// replaces the candidate, restarts the dry run and answers 202. So
	// reaching this refusal means another writer inserted a candidate between
	// qctl's read and its write. Re-applying is the correct cheap move: the
	// retry finds the candidate and replaces it. Do not lead with dismiss —
	// it cancels a validation someone else most likely started.
	case strings.Contains(msg, "already has a validation in progress"):
		return fmt.Errorf("%w\n\n"+
			"Another request started validating this rule between qctl's read and its write.\n"+
			"Re-apply %s to replace what is being validated and restart the dry run.\n"+
			"To look at the validation in flight first, or to stop it:\n"+
			"  qctl get rule-suggestions --table-id <id>                    # find its id\n"+
			"  qctl describe rule-suggestion <sug-id> --table-id <id>       # inspect it\n"+
			"  qctl suggestion dismiss <sug-id> --table-id <id>             # cancels a validation someone else may have started",
			err, doc.Source)
	// atlas capi/capi/views/rule_view.py patch_dataset_rule: the paused-rule
	// raise under `changes_state and edits_in_place and material`, and the
	// candidate raise under `rides_a_candidate and changes_state`.
	case strings.Contains(msg, "cannot ride along with a configuration change"):
		//nolint:staticcheck // Preserve the established multi-line CLI guidance verbatim.
		return fmt.Errorf("%w\n\n"+
			"Split %s into two manifests and apply them in order: the configuration\n"+
			"change first, the spec.state change once that validation has passed.",
			err, doc.Source)
	// atlas capi/capi/controllers/rule_revalidation_view_controller.py
	// build_and_validate_candidate_snapshot
	case strings.Contains(msg, "own rule family"),
		strings.Contains(msg, "Cannot rebind to Rule release"):
		return fmt.Errorf("%w\n\n"+
			"A rule can only be rebound within its own family, to a revision that is\n"+
			"enabled and passes the table's review policy. List the candidates with:\n"+
			"  qctl get rule-revisions --search <rule-name>",
			err)
	}
	return err
}
