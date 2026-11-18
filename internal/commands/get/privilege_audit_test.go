package get

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
)

var (
	privEntryID      = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	privOtherEntryID = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
)

func privilegeAuditFixture(total int) privilege_requests.AuditPage {
	performed := time.Now().Add(-2 * time.Hour)
	entry := privilege_requests.AuditEntry{
		ID: privEntryID, RequestID: privApprovedID,
		RequestorID: privRequestorID, ApproverID: privApproverID, TargetUserID: privTargetID,
		PerformedByID: privApproverID, PerformedByIP: "10.0.0.7", PerformedByUserAgent: "qctl/1.2",
		PermissionKey: "is_ops", OldValue: "false", NewValue: "true",
		Justification: "incident 4411",
		RequestedAt:   performed.Add(-time.Hour), ApprovedAt: performed, PerformedAt: performed,
	}
	other := entry
	other.ID, other.OldValue, other.NewValue = privOtherEntryID, "true", "false"
	return privilege_requests.AuditPage{
		AuditEntries: []privilege_requests.AuditEntry{entry, other},
		Total:        total, Page: 1, Limit: 20,
	}
}

func TestGetPrivilegeAuditTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(2)))
	servePrivilegeUsers(e, http.StatusOK)

	out, stderr, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	for _, header := range []string{"PERFORMED", "REQUEST", "ACTION", "TARGET", "APPROVER", "REQUESTOR", "IP"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing header %q\n%s", header, out)
		}
	}
	// The entry id is not a default column: rows are found by their request.
	if strings.Contains(out, privEntryID.String()) {
		t.Errorf("the entry id is reachable through --columns / -o name, not a default column\n%s", out)
	}
	row := strings.Join(rowFields(t, out, privApprovedID.String()), " ")
	for _, want := range []string{"2 hours ago", "escalate", "Tom Target", "Ann Approver", "Rita Req", "10.0.0.7"} {
		if !strings.Contains(row, want) {
			t.Errorf("row missing %q: %s", want, row)
		}
	}
	if !strings.Contains(out, "demote") {
		t.Errorf("second entry should render as a demotion\n%s", out)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeAuditURL, "GET "+privilegeUsersURL)
}

// The ledger is the load-bearing half. A directory that cannot be read costs
// the names, not the rows: UUIDs, one warning, exit 0.
func TestGetPrivilegeAuditDirectoryFailureStillRenders(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(2)))
	servePrivilegeUsers(e, http.StatusInternalServerError)

	out, stderr, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit",
		"--columns", "request,target,approver,requestor")
	if err != nil {
		t.Fatalf("a directory failure must not fail the listing: %v", err)
	}
	for _, id := range []uuid.UUID{privTargetID, privApproverID, privRequestorID} {
		if !strings.Contains(out, id.String()) {
			t.Errorf("row should name %s by UUID\n%s", id, out)
		}
	}
	if strings.Contains(out, "Tom Target") {
		t.Errorf("no directory, no names\n%s", out)
	}
	if n := strings.Count(stderr, "Warning: user directory unavailable"); n != 1 {
		t.Errorf("want exactly one directory warning for two rows, got %d\n%s", n, stderr)
	}
}

// -o json is the compliance contract: the wire body verbatim, and no
// directory request, which could only ever change the table.
func TestGetPrivilegeAuditJSONIsTheWireBodyWithoutADirectory(t *testing.T) {
	e := newDataReviewGetEnv(t)
	body := privilegeWireBody(t, privilegeAuditFixture(2))
	servePrivilege(e, privilegeAuditURL, http.StatusOK, body)
	servePrivilegeUsers(e, http.StatusOK)

	out, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(body) {
		t.Errorf("-o json is not the wire body\n got: %s\nwant: %s", out, body)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeAuditURL)
}

func TestGetPrivilegeAuditYAMLWithoutADirectory(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(2)))
	servePrivilegeUsers(e, http.StatusOK)

	out, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"audit_entries:", "performed_by_ip: 10.0.0.7", "performed_by_id: " + privApproverID.String()} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n%s", want, out)
		}
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeAuditURL)
}

func TestGetPrivilegeAuditPaginationHint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		total int
		want  bool
	}{
		{"more pages", 21, true},
		{"last page", 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(tc.total)))
			servePrivilegeUsers(e, http.StatusOK)

			_, stderr, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hint := "More audit entries — fetch the next page with --page 2."
			if got := strings.Contains(stderr, hint); got != tc.want {
				t.Errorf("hint present = %t, want %t\n%s", got, tc.want, stderr)
			}
		})
	}
}

func TestGetPrivilegeAuditRejectsBadFlagsWithoutARequest(t *testing.T) {
	for _, args := range [][]string{
		{"--page", "0"},
		{"--limit", "0"},
		{"--request-id", "not-a-uuid"},
		{"--request-id", "0190aaaa"},
		{"--request-id", ""},
		{"--target-user", ""},
	} {
		t.Run(strings.Join(args, "="), func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", args...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertPrivilegeRequests(t, e)
		})
	}
}

func TestGetPrivilegeAuditSendsFilters(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(2)))

	if _, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", "-o", "json",
		"--request-id", privApprovedID.String(), "--target-user", privTargetID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{
		"request_id":     privApprovedID.String(),
		"target_user_id": privTargetID.String(),
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeAuditURL)
}

// A name-typed --target-user sweeps the directory once, and the table reuses
// that sweep instead of making a second one.
func TestGetPrivilegeAuditTargetEmailSweepsTheDirectoryOnce(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, privilegeAuditURL, http.StatusOK, privilegeWireBody(t, privilegeAuditFixture(2)))
	servePrivilegeUsers(e, http.StatusOK)

	out, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", "--target-user", "Tom Target")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query.Get("target_user_id"); got != privTargetID.String() {
		t.Errorf("target_user_id = %q, want %q", got, privTargetID)
	}
	if !strings.Contains(out, "Ann Approver") {
		t.Errorf("table should still name people\n%s", out)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeUsersURL, "GET "+privilegeAuditURL)
}

func TestGetPrivilegeAuditForbidden(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilegeForbidden(e, privilegeAuditURL)

	_, _, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit")
	assertExitCode(t, err, qerrors.ExitUnauthorized)
}

// An empty page costs no directory request, and a page past the end names
// the last one in the singular the ledger's noun needs.
func TestGetPrivilegeAuditPastTheLastPage(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeAuditURL, http.StatusOK,
		[]byte(`{"audit_entries":[],"total":1,"page":3,"limit":20}`))
	servePrivilegeUsers(e, http.StatusOK)

	out, stderr, err := e.run(t, NewPrivilegeAuditCommand(), "privilege-audit", "--page", "3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
	if want := "The last page is 1 (1 privilege audit entry, 20 per page)."; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q\n%s", want, stderr)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeAuditURL)
}
