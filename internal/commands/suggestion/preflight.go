package suggestion

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
)

// suggestionGetter is the one method the pre-flight needs, so its tests do not
// need a server.
type suggestionGetter interface {
	Get(accessToken string, datasetID int, suggestionID string) (*rule_suggestions.Suggestion, error)
}

// preflight is why the bulk path can report a bad id at all.
//
// The bulk routes select their working set with `state == pending` INSIDE the
// SQL query (atlas _resolve_pending_suggestions,
// capi/capi/views/suggestion_view.py), so an id that does not exist, or
// that is no longer pending, never enters the result set: it is
// absent from `processed`, absent from `skipped`, and absent from `errors`. The
// response is byte-identical to a clean success. The single-id routes answer
// 404 and 409 for those same two cases.
//
// Without this function `accept <a>` and `accept <a> <b>` would report the same
// typo differently, which is exactly the regression the variadic form exists to
// avoid. So every id is read back first, the whole command is refused if any is
// unusable, and NOTHING is written.
func preflight(g suggestionGetter, token string, datasetID int, ids []string, verb string) error {
	type problem struct{ id, why string }
	var problems []problem

	for _, id := range ids {
		s, err := g.Get(token, datasetID, id)
		if err != nil {
			if qerrors.GetExitCode(err) == qerrors.ExitNotFound {
				problems = append(problems, problem{id, "not found"})
				continue
			}
			// Anything else — a network failure, a 401, a 5xx — is not a
			// classification and must not be reported as a bad id. Propagate it
			// with its own exit code intact.
			return fmt.Errorf("could not check suggestion %s before writing: %w", id, err)
		}
		if s.State != api.SuggestionStatePending {
			problems = append(problems, problem{id, fmt.Sprintf("not pending (state: %s)", s.State)})
			continue
		}
		// Mirrors _raise_if_revalidation_candidate (suggestion_view.py): the
		// generic accept door refuses a candidate with 409 because its outcome
		// is decided by the validation run the rule edit launched. dismiss is
		// the verb that retires one, so it is not a problem there.
		//
		// Unlike the two cases above this one IS visible on the bulk path — a
		// pending candidate enters the result set and _apply_accept's 409 lands
		// in `errors`. Refusing it here is what makes the two routes word it the
		// same way, and costs no partial write.
		if verb == verbAccept && s.IsRevalidationCandidate != nil && *s.IsRevalidationCandidate {
			problems = append(problems, problem{id, "revalidation candidate; only dismiss applies"})
		}
	}

	if len(problems) == 0 {
		return nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d of %d suggestions cannot be %sed:", len(problems), len(ids), verb)
	for _, p := range problems {
		fmt.Fprintf(&b, "\n  %s  %s", p.id, p.why)
	}
	fmt.Fprintf(&b, "\nNothing was %sed. Re-run without them.", verb)
	return qerrors.NewBadUsage(b.String())
}
