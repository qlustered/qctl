package alerts

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const testGroupID = "6f1c2d3e-4b5a-4c6d-8e7f-901234567890"

func withGroupID(a *AlertFull) {
	group := uuid.MustParse(testGroupID)
	a.Body = &api.AlertBodySchema{GroupID: &group}
}

// A hold alert whose body names its group routes to that group, and a
// person_marked_duplicate one retracts it by id; no line searches by row.
func TestFooterNamesTheGroupItsBodyCarries(t *testing.T) {
	describe := "qctl describe duplicate-group " + testGroupID + " --table-id 10\n"
	for issueType, want := range map[api.AlertType]string{
		api.AlertTypeRepeatedKey:           "decide the duplicate group: " + describe,
		api.AlertTypeIdenticalRows:         "decide the duplicate group: " + describe,
		api.AlertTypePersonMarkedDuplicate: "decide the duplicate group: " + describe,
		api.AlertTypeDuplicateNotLoaded:    "see the duplicate group's decision: " + describe,
	} {
		a := sampleAlertFull()
		a.IssueType = issueType
		a.IsRowLevel = true
		withGroupID(&a)
		out := RenderNextActionsPlain(&a, true)
		if !strings.Contains(out, want) {
			t.Errorf("%s: footer lacks %q:\n%s", issueType, want, out)
		}
		if strings.Contains(out, "get duplicate-groups") || strings.Contains(out, "<group-id>") {
			t.Errorf("%s: footer searches for a group its body names:\n%s", issueType, out)
		}
		retract := "or take the mark back: qctl duplicate-group retract " + testGroupID + " --table-id 10\n"
		if got, want := strings.Contains(out, retract), issueType == api.AlertTypePersonMarkedDuplicate; got != want {
			t.Errorf("%s: footer names retract = %v, want %v:\n%s", issueType, got, want, out)
		}
	}
}

// A duplicate-group hold alert whose body names no group routes to the search
// by row, and says resolving it releases nothing, whether or not it offers
// actions. atlas stamps body.group_id on a load's repeated_key and
// identical_rows alerts only after the load commits, and a server whose alert
// schema predates the field never sends it.
func TestFooterRoutesAHoldAlertToItsDuplicateGroup(t *testing.T) {
	want := []string{
		"decide the duplicate group: qctl get duplicate-groups --table-id 10 --row-id <a held row's id>\n",
		"  resolving this alert does not release its rows",
	}
	for _, issueType := range []api.AlertType{
		api.AlertTypeRepeatedKey, api.AlertTypeIdenticalRows, api.AlertTypePersonMarkedDuplicate,
	} {
		for name, actions := range map[string][]api.AlertAction{"none": {}, "try_again": {api.AlertActionTryAgain}} {
			a := sampleAlertFull()
			a.IssueType = issueType
			a.IsRowLevel = true
			a.AlertActionsList = &actions
			out := RenderNextActionsPlain(&a, true)
			for _, line := range want {
				if !strings.Contains(out, line) {
					t.Errorf("%s, %s: footer lacks %q:\n%s", issueType, name, line, out)
				}
			}
			retract := "qctl duplicate-group retract <group-id> --table-id 10"
			if got, want := strings.Contains(out, retract), issueType == api.AlertTypePersonMarkedDuplicate; got != want {
				t.Errorf("%s, %s: footer names retract = %v, want %v:\n%s", issueType, name, got, want, out)
			}
		}
	}
	a := sampleAlertFull()
	a.IsRowLevel = true
	if out := RenderNextActionsPlain(&a, true); strings.Contains(out, "duplicate group") {
		t.Errorf("%s footer names a duplicate group:\n%s", a.IssueType, out)
	}
}

// A not-loaded copy's alert whose body names no group routes to the search by
// row and offers a signature delete, never the decide line or retract.
func TestFooterRoutesANotLoadedAlertToItsGroupAndADelete(t *testing.T) {
	a := sampleAlertFull()
	a.IssueType = api.AlertTypeDuplicateNotLoaded
	a.IsRowLevel = true
	out := RenderNextActionsPlain(&a, true)
	for _, line := range []string{
		"see the duplicate group's decision: qctl get duplicate-groups --table-id 10 --row-id <a not-loaded row's id>\n",
		"resolving this alert\n  does not load them",
		"or delete them: qctl rows delete-quarantined --table-id 10 --signature <a not-loaded row's signature> --reason <why>\n",
	} {
		if !strings.Contains(out, line) {
			t.Errorf("footer lacks %q:\n%s", line, out)
		}
	}
	for _, absent := range []string{"decide the duplicate group", "duplicate-group retract"} {
		if strings.Contains(out, absent) {
			t.Errorf("footer names %q:\n%s", absent, out)
		}
	}
	if !IsDuplicateGroupHold(api.AlertTypeDuplicateNotLoaded) {
		t.Error("IsDuplicateGroupHold(duplicate_not_loaded) = false")
	}
}

// A duplicate-group hold alert offers no actions, so resolving one is refused
// with the command that shows its group rather than a guess at the caller's
// role; a resolved one gets the generic refusal.
func TestResolveRefusalRoutesAHoldAlertToItsGroup(t *testing.T) {
	for issueType, want := range map[api.AlertType]string{
		api.AlertTypeRepeatedKey:        "qctl get duplicate-groups --table-id 10 --row-id <a held row's id>",
		api.AlertTypeDuplicateNotLoaded: "qctl get duplicate-groups --table-id 10 --row-id <a not-loaded row's id>",
	} {
		a := alertOffering()
		a.IssueType = issueType
		err := ValidateAction(a, "overwrite_value")
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("%s: exit code = %d, want %d", issueType, got, qerrors.ExitBadUsage)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: refusal lacks %q: %v", issueType, want, err)
		}
	}

	a := alertOffering()
	a.IssueType = api.AlertTypePersonMarkedDuplicate
	withGroupID(a)
	if err := ValidateAction(a, "try_again"); err == nil ||
		!strings.Contains(err.Error(), "qctl describe duplicate-group "+testGroupID+" --table-id 10") {
		t.Errorf("refusal does not name the body's group: %v", err)
	}

	resolved := true
	a.Resolved = &resolved
	if err := ValidateAction(a, "try_again"); err == nil || strings.Contains(err.Error(), "duplicate") {
		t.Errorf("a resolved hold alert's refusal names its group: %v", err)
	}
}
