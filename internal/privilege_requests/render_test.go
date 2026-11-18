package privilege_requests //nolint:revive // Match the established package under test.

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
)

var (
	renderRequestorID = uuid.MustParse("99999999-0000-0000-0000-000000000001")
	renderApproverID  = uuid.MustParse("99999999-0000-0000-0000-000000000002")
	renderEntryID     = uuid.MustParse("99999999-0000-0000-0000-0000000000e1")
)

func pendingDetail() *Detail {
	return &Detail{
		ID:            reqID,
		Status:        api.PrivilegeRequestStatusPending,
		PermissionKey: "is_ops",
		OldValue:      "false",
		NewValue:      "true",
		Justification: "incident 4411: on-call needs ops",
		RequestedAt:   time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC),
		RequestorID:   renderRequestorID,
		Requestor: &UserInfo{ID: renderRequestorID, Email: "rita@corp.com",
			FirstName: "Rita", LastName: "Req"},
		TargetUserID: targetID,
		TargetUser: &UserInfo{ID: targetID, Email: "tom@corp.com",
			FirstName: "Tom", LastName: "Target"},
	}
}

func approvedDetail() *Detail {
	d := pendingDetail()
	decided := time.Date(2026, 9, 16, 11, 0, 0, 0, time.UTC)
	d.Status = api.PrivilegeRequestStatusApproved
	d.ApproverID = &renderApproverID
	d.Approver = &UserInfo{ID: renderApproverID, Email: "ann@corp.com", FirstName: "Ann", LastName: "Approver"}
	d.DecisionAt = &decided
	d.TargetUser.IsOps = true
	d.AuditEntries = &[]AuditEntry{{
		ID:                   renderEntryID,
		RequestID:            reqID,
		RequestorID:          renderRequestorID,
		ApproverID:           renderApproverID,
		TargetUserID:         targetID,
		PermissionKey:        "is_ops",
		OldValue:             "false",
		NewValue:             "true",
		PerformedAt:          decided,
		PerformedByID:        renderApproverID,
		PerformedByIP:        "10.0.0.7",
		PerformedByUserAgent: "qctl/1.2",
	}}
	return d
}

func TestRenderDetailPending(t *testing.T) {
	out := RenderDetail(pendingDetail())
	for _, want := range []string{
		"# privilege-request/" + testReqID + "\n",
		"Status: Pending\n",
		"Action: escalate (is_ops: false → true)\n",
		"Target: tom@corp.com (Tom Target, " + testTargetID + ") — is_ops now: false\n",
		"Requestor: rita@corp.com (Rita Req, " + renderRequestorID.String() + ")\n",
		"Approver: pending (no decision yet)\n",
		"Requested: 2026-09-16T10:00:00Z (",
		"Justification:\n  incident 4411: on-call needs ops\n",
		"## AUDIT (0)\n(none — no change has been applied)\n",
		"## NEXT ACTIONS\n",
		"qctl privilege approve " + testReqID + " --yes\n",
		"qctl privilege reject " + testReqID + "\n",
		"neither the requestor nor the target",
		"The target cannot reject it either",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "Decided:") {
		t.Errorf("a pending request has no decision to show\n%s", out)
	}
}

// NEXT ACTIONS names verbs that only a Pending request can take; on any other
// status the server answers "already decided", so the section must not
// appear at all.
func TestRenderDetailNextActionsOnlyForPending(t *testing.T) {
	for _, status := range []api.PrivilegeRequestStatus{
		api.PrivilegeRequestStatusApproved,
		api.PrivilegeRequestStatusRejected,
		api.PrivilegeRequestStatusExpired,
	} {
		t.Run(string(status), func(t *testing.T) {
			d := approvedDetail()
			d.Status = status
			out := RenderDetail(d)
			for _, banned := range []string{"NEXT ACTIONS", "qctl privilege approve", "qctl privilege reject"} {
				if strings.Contains(out, banned) {
					t.Errorf("status %s must not offer %q\n%s", status, banned, out)
				}
			}
		})
	}
}

func TestRenderDetailApprovedShowsDecisionAndAudit(t *testing.T) {
	out := RenderDetail(approvedDetail())
	for _, want := range []string{
		"Approver: ann@corp.com (Ann Approver, " + renderApproverID.String() + ")\n",
		"Decided: 2026-09-16T11:00:00Z (",
		"— is_ops now: true\n",
		"## AUDIT (1)\n",
		"- 2026-09-16T11:00:00Z (",
		"[entry " + renderEntryID.String() + "]\n",
		"  change: escalate (is_ops: false → true)\n",
		// performed_by is a bare UUID on the wire; the detail's embedded
		// approver names it without a directory request.
		"  performed by: ann@corp.com (Ann Approver, " + renderApproverID.String() + ")\n",
		"  from: 10.0.0.7, user agent \"qctl/1.2\"\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
}

// One approver_id serves both outcomes, so a rejected request's "approver" is
// the rejector and must not be called an approver.
func TestRenderDetailRejectedNamesTheRejector(t *testing.T) {
	d := approvedDetail()
	d.Status = api.PrivilegeRequestStatusRejected
	d.AuditEntries = &[]AuditEntry{}
	out := RenderDetail(d)
	if !strings.Contains(out, "Rejected by: ann@corp.com") {
		t.Errorf("rejected request should name the rejector\n%s", out)
	}
	if strings.Contains(out, "Approver:") {
		t.Errorf("a rejected request has no approver\n%s", out)
	}
}

func TestRenderDetailExpiredHasNobodyOnIt(t *testing.T) {
	d := approvedDetail()
	d.Status = api.PrivilegeRequestStatusExpired
	d.ApproverID, d.Approver, d.AuditEntries = nil, nil, nil
	out := RenderDetail(d)
	for _, want := range []string{"Approver: (none recorded)\n", "Decided: 2026-09-16T11:00:00Z (", "## AUDIT (0)\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
}

// Every embedded user is nullable on the wire. A detail without them must
// render, naming each person by the UUID the request row always carries.
func TestRenderDetailNullUsersFallBackToUUIDs(t *testing.T) {
	d := approvedDetail()
	d.Requestor, d.TargetUser, d.Approver = nil, nil, nil

	out := RenderDetail(d)
	for _, want := range []string{
		"Target: " + testTargetID + "\n",
		"Requestor: " + renderRequestorID.String() + "\n",
		"Approver: " + renderApproverID.String() + "\n",
		"  performed by: " + renderApproverID.String() + "\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "is_ops now") {
		t.Errorf("is_ops now is unknown without the target user object\n%s", out)
	}
}

// The justification is free text a requestor typed; a terminal escape in it
// must reach the screen inert, and each of its lines stays inside the block.
func TestRenderDetailSanitizesAndIndentsTheJustification(t *testing.T) {
	d := pendingDetail()
	d.Justification = "line one\n\x1b[31mline two"
	out := RenderDetail(d)
	if strings.Contains(out, "\x1b") {
		t.Errorf("raw escape reached the payload: %q", out)
	}
	if !strings.Contains(out, "Justification:\n  line one\n  \\x1b[31mline two\n") {
		t.Errorf("justification not indented and escaped\n%s", out)
	}
}

func TestRenderDetailNeverMentionsExpiry(t *testing.T) {
	for _, d := range []*Detail{pendingDetail(), approvedDetail()} {
		if out := strings.ToLower(RenderDetail(d)); strings.Contains(out, "expire") {
			t.Errorf("qctl never computes expiry, yet the payload mentions it\n%s", out)
		}
	}
}
