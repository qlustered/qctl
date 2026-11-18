package apply

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func conflict(msg string) error {
	return apierror.HandleHTTPErrorFromBytes(409, []byte(`{"detail":{"msg":"`+msg+`"}}`), "patch failed")
}

func TestExplainRebindError_CandidateInFlight(t *testing.T) {
	err := explainRebindError(conflict("This rule already has a validation in progress. Wait for it to finish, edit it, or dismiss it."), manifestDoc{Source: "r.yaml"})
	got := err.Error()

	for _, want := range []string{
		"get rule-suggestions",     // the id the other two steps need is discoverable
		"describe rule-suggestion", // inspecting the candidate stays available
		"suggestion dismiss",       // so does cancelling it
		"r.yaml",                   // the manifest to re-apply is named
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}

	// This 409 is a concurrent-write race guard, not "you already have a
	// candidate": when a candidate really is pending the backend replaces it
	// and answers 202. So the recommended move is a plain re-apply, and it
	// must be offered before the destructive one — dismissing here cancels a
	// validation someone else most likely started.
	reapply := strings.Index(strings.ToLower(got), "re-apply")
	dismiss := strings.Index(got, "suggestion dismiss")
	if reapply < 0 {
		t.Errorf("must lead with re-applying the manifest:\n%s", err)
	} else if dismiss >= 0 && dismiss < reapply {
		t.Errorf("dismiss must come after the re-apply guidance, not before it:\n%s", err)
	}
	if !strings.Contains(got, "someone else") {
		t.Errorf("dismiss must be marked as cancelling a run someone else may have started:\n%s", err)
	}
}

func TestExplainRebindError_StateRidesAlong(t *testing.T) {
	err := explainRebindError(conflict("A change to 'disabled' cannot ride along with a configuration change"), manifestDoc{Source: "rules/orders.yaml"})

	if !strings.Contains(err.Error(), "two manifests") {
		t.Errorf("must explain the split:\n%s", err)
	}
	// The arm's only use of the doc parameter: the manifest to split must be
	// named, not described generically.
	if !strings.Contains(err.Error(), "rules/orders.yaml") {
		t.Errorf("must name the manifest to split:\n%s", err)
	}
}

func TestExplainRebindError_WrongFamily(t *testing.T) {
	err := explainRebindError(conflict("A rule can only be rebound to a revision in its own rule family."), manifestDoc{Source: "r.yaml"})

	if !strings.Contains(err.Error(), "get rule-revisions") {
		t.Errorf("must point at the family's revisions:\n%s", err)
	}
}

// The revision-state refusal is a distinct backend raise from the family one
// (atlas rule_revalidation_view_controller.py build_and_validate_candidate_snapshot:
// its `revision.state != RuleState.enabled` raise vs its `family_id` one); the third
// arm's second matcher exists for it and nothing else covers it.
func TestExplainRebindError_RevisionNotEnabled(t *testing.T) {
	err := explainRebindError(
		conflict("Cannot rebind to Rule release '2.1.0' — it is in 'draft' state."),
		manifestDoc{Source: "r.yaml"})

	if !strings.Contains(err.Error(), "get rule-revisions") {
		t.Errorf("must point at the family's revisions:\n%s", err)
	}
	if !strings.Contains(err.Error(), "enabled") {
		t.Errorf("must say the target revision has to be enabled:\n%s", err)
	}
}

// The exit code must survive the wrapping — a %v would silently degrade a
// 409 to exit 1 while the message still looks perfect.
func TestExplainRebindError_PreservesExitCode(t *testing.T) {
	original := conflict("A rule can only be rebound to a revision in its own rule family.")
	wrapped := explainRebindError(original, manifestDoc{Source: "r.yaml"})

	if got, want := qerrors.GetExitCode(wrapped), qerrors.GetExitCode(original); got != want {
		t.Errorf("exit code = %d, want %d (the unwrapped original's)", got, want)
	}
	if !errors.Is(wrapped, original) {
		t.Error("the Unwrap chain must reach the original error")
	}
}

// An unrelated error passes through untouched.
func TestExplainRebindError_PassesThroughUnrelated(t *testing.T) {
	original := conflict("something else entirely")
	if got := explainRebindError(original, manifestDoc{Source: "r.yaml"}); got != original {
		t.Errorf("an unrecognized error must pass through unchanged, got: %v", got)
	}
}

// The unit tests above build the 409 by hand. This one drives a real HTTP 409
// through the generated client and the domain client, so the explanation is
// proven to reach the caller from the wire — not merely from a fixture the
// matcher was written against.
//
// It asserts the exit code *and* the prose: every 4xx from this path exits 2,
// so the code alone cannot show the explanation was attached (the precedent is
// TestApplyTableRulePatchRejectsOrphanRelease).
func TestApplyTableRulePatchExplainsCandidateInFlight(t *testing.T) {
	env, mock := setupTableRuleApplyEnv(t)
	defer env.Cleanup()
	defer mock.Close()

	registerRuleDetailGet(mock, sampleDatasetRuleDetailForApply())
	mock.RegisterHandler("PATCH", "/api/orgs/"+testOrgID+"/datasets/1/dataset-rules/"+testTableRuleID,
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]any{
				"detail": map[string]any{
					"msg": "This rule already has a validation in progress. Wait for it to finish, edit it, or dismiss it.",
				},
			})
		})

	manifest := `
apiVersion: qluster.ai/v1
kind: TableRule
metadata:
  id: ` + testTableRuleID + `
spec:
  dataset_id: 1
  instance_name: renamed_rule
`
	path := env.CreateFile("patch-409-in-flight.yaml", manifest)
	_, err := execApply(t, path, "--yes")
	if err == nil {
		t.Fatal("execApply = nil, want the backend 409 surfaced")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	for _, want := range []string{
		"patch-409-in-flight.yaml",             // the runner still names the document
		"already has a validation in progress", // the server's own sentence survives
		"qctl suggestion dismiss",              // and the explanation is attached
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%s", want, err)
		}
	}
}
