package change_proposals //nolint:revive // Match the established package under test.

import (
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// api.StringEnumValues is regenerated with the constants, so a state the
// spec adds turns this red.
func TestStatesCoverTheSpecEnum(t *testing.T) {
	spec, ok := api.StringEnumValues["ChangeProposalState"]
	if !ok || len(spec) == 0 {
		t.Fatal(`api.StringEnumValues has no "ChangeProposalState" entry; this check cannot fail as written`)
	}
	if got := StateNames(); !slices.Equal(got, spec) {
		t.Errorf("StateNames() = %v, the spec declares %v", got, spec)
	}
}

func TestLiveStatesAreTheNonTerminalOnes(t *testing.T) {
	want := []string{"draft", "pending_review", "changes_requested", "executing"}
	if got := names(LiveStates()); !slices.Equal(got, want) {
		t.Errorf("LiveStates() = %v, want %v", got, want)
	}
	for _, s := range States() {
		if got, want := IsLive(s), slices.Contains(want, string(s)); got != want {
			t.Errorf("IsLive(%s) = %v, want %v", s, got, want)
		}
	}
}

func TestProposableOperationsAreTheSpecsProposalOperations(t *testing.T) {
	want := []string{"alert_resolve", "warning_resolve", "dataset_rule_patch"}
	if got := OperationNames(); !slices.Equal(got, want) {
		t.Errorf("OperationNames() = %v, want %v", got, want)
	}
	spec := api.StringEnumValues["ChangeReviewOperation"]
	for _, op := range want {
		if !slices.Contains(spec, op) {
			t.Errorf("%s is no longer a ChangeReviewOperation in the spec", op)
		}
	}
}

// The returned slices are copies: a caller appending to or editing one
// cannot change the next caller's.
func TestVocabularyAccessorsReturnCopies(t *testing.T) {
	LiveStates()[0] = api.ChangeProposalStateObsolete
	States()[0] = api.ChangeProposalStateObsolete
	ProposableOperations()[0] = api.ChangeReviewOperationBadRowsDelete
	if LiveStates()[0] != api.ChangeProposalStateDraft || States()[0] != api.ChangeProposalStateDraft ||
		ProposableOperations()[0] != api.ChangeReviewOperationAlertResolve {
		t.Error("an accessor returned the package's own slice")
	}
}

func TestParseStatesIsCaseInsensitiveAndDeduplicates(t *testing.T) {
	got, err := ParseStates([]string{"Draft", "pending_review", "DRAFT", "obsolete"})
	if err != nil {
		t.Fatalf("ParseStates: %v", err)
	}
	want := []State{api.ChangeProposalStateDraft, api.ChangeProposalStatePendingReview, api.ChangeProposalStateObsolete}
	if !slices.Equal(got, want) {
		t.Errorf("ParseStates = %v, want %v", got, want)
	}
}

func TestParseOperationAcceptsOnlyTheProposableOnes(t *testing.T) {
	got, err := ParseOperation("Warning_Resolve")
	if err != nil || got != api.ChangeReviewOperationWarningResolve {
		t.Errorf("ParseOperation(Warning_Resolve) = %q, %v", got, err)
	}
	// A real ChangeReviewOperation that cannot be proposed is still refused.
	if _, err := ParseOperation(string(api.ChangeReviewOperationSettingsPatch)); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("ParseOperation(settings_patch) exit %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
	}
}

func TestBadInputIsBadUsage(t *testing.T) {
	for name, err := range map[string]error{
		"state":     second(ParseState("pending")),
		"states":    second(ParseStates([]string{"draft", "nope"})),
		"operation": second(ParseOperation("")),
		"id":        second(ParseID("42")),
	} {
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("%s: exit %d, want %d (err: %v)", name, got, qerrors.ExitBadUsage, err)
		}
	}
	if _, err := ParseID(testProposalID); err != nil {
		t.Errorf("ParseID(%s): %v", testProposalID, err)
	}
}

func second[T any](_ T, err error) error { return err }

func TestTarget(t *testing.T) {
	alert, warning := 42, 9
	rule := uuid.MustParse("0190dddd-0000-4000-8000-000000000042")
	for want, p := range map[string]*Proposal{
		"alert/42":  {AlertItemID: &alert},
		"warning/9": {WarningItemID: &warning},
		"rule/0190dddd-0000-4000-8000-000000000042": {DatasetRuleID: &rule},
		"": {},
	} {
		if got := Target(p); got != want {
			t.Errorf("Target = %q, want %q", got, want)
		}
	}
}
