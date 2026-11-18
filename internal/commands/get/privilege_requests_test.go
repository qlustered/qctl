package get

import (
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
)

const (
	privilegeRequestsURL = "/api/ops/" + testOrgID + "/privilege-requests"
	privilegeAuditURL    = "/api/ops/" + testOrgID + "/privilege-audit"
	privilegeUsersURL    = "/api/orgs/" + testOrgID + "/users"
)

var (
	privPendingID   = uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	privApprovedID  = uuid.MustParse("0190aaaa-0000-4000-8000-000000000002")
	privTargetID    = uuid.MustParse("0190bbbb-0000-4000-8000-000000000001")
	privRequestorID = uuid.MustParse("0190bbbb-0000-4000-8000-000000000002")
	privApproverID  = uuid.MustParse("0190bbbb-0000-4000-8000-000000000003")
)

// privilegeWireBody encodes v exactly the way `-o json` does, so a handler
// serving these bytes lets a test assert the command's output is the wire
// body byte for byte.
func privilegeWireBody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return append(raw, '\n')
}

// servePrivilege registers path to answer status with body, recording the
// query of the last request it saw.
func servePrivilege(e *dataReviewGetEnv, path string, status int, body []byte) *url.Values {
	var query url.Values
	e.mock.RegisterHandler("GET", path, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	return &query
}

func servePrivilegeForbidden(e *dataReviewGetEnv, path string) {
	servePrivilege(e, path, http.StatusForbidden, []byte(`{"detail":{"msg":"You don't have access to view privilege `+
		`requests. This feature is restricted to operations staff."}}`))
}

// servePrivilegeUsers answers the users directory with the three people the
// fixtures name.
func servePrivilegeUsers(e *dataReviewGetEnv, status int) {
	if status != http.StatusOK {
		servePrivilege(e, privilegeUsersURL, status, []byte(`{"detail":{"msg":"directory unavailable"}}`))
		return
	}
	servePrivilege(e, privilegeUsersURL, status, []byte(`{"results":[`+
		`{"id":"`+privTargetID.String()+`","email":"tom@corp.com","first_name":"Tom","last_name":"Target"},`+
		`{"id":"`+privRequestorID.String()+`","email":"rita@corp.com","first_name":"Rita","last_name":"Req"},`+
		`{"id":"`+privApproverID.String()+`","email":"ann@corp.com","first_name":"Ann","last_name":"Approver"}`+
		`],"next":null}`))
}

func privilegeListFixture(total int) privilege_requests.ListPage {
	tom, ann := "tom@corp.com", "ann@corp.com"
	decided := time.Now().Add(-1 * time.Hour)
	return privilege_requests.ListPage{
		Requests: []privilege_requests.ListItem{
			{
				// requestor_email is null: the cell falls back to the UUID.
				ID: privPendingID, Status: api.PrivilegeRequestStatusPending,
				PermissionKey: "is_ops", OldValue: "false", NewValue: "true",
				Justification: "incident 4411", RequestedAt: time.Now().Add(-3 * time.Hour),
				TargetUserID: privTargetID, TargetUserEmail: &tom,
				RequestorID: privRequestorID,
			},
			{
				ID: privApprovedID, Status: api.PrivilegeRequestStatusApproved,
				PermissionKey: "is_ops", OldValue: "true", NewValue: "false",
				Justification: "left the on-call rotation", RequestedAt: time.Now().Add(-2 * 24 * time.Hour),
				TargetUserID: privTargetID, TargetUserEmail: &tom,
				RequestorID: privRequestorID, RequestorEmail: &ann,
				ApproverID: &privApproverID, ApproverEmail: &ann, DecisionAt: &decided,
			},
		},
		Total: total, Page: 1, Limit: 20,
	}
}

func assertPrivilegeRequests(t *testing.T, e *dataReviewGetEnv, want ...string) {
	t.Helper()
	got := e.mock.Requests()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func assertExitCode(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func rowFields(t *testing.T, out, id string) []string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, id) {
			return strings.Fields(strings.ReplaceAll(line, "│", " "))
		}
	}
	t.Fatalf("no row for %s in\n%s", id, out)
	return nil
}

func TestGetPrivilegeRequestsTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	out, stderr, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests")
	if err != nil {
		t.Fatalf("unexpected error: %v\n%s", err, stderr)
	}
	for _, header := range []string{"ID", "STATUS", "ACTION", "TARGET", "REQUESTOR", "APPROVER", "REQUESTED", "DECIDED"} {
		if !strings.Contains(out, header) {
			t.Errorf("missing header %q\n%s", header, out)
		}
	}
	if strings.Contains(out, "JUSTIFICATION") {
		t.Errorf("justification is reachable through --columns, not a default column\n%s", out)
	}

	pending := strings.Join(rowFields(t, out, privPendingID.String()), " ")
	for _, want := range []string{"Pending", "escalate", "tom@corp.com", privRequestorID.String(), "3 hours ago"} {
		if !strings.Contains(pending, want) {
			t.Errorf("pending row missing %q: %s", want, pending)
		}
	}
	// An undecided request has no approver and no decision time.
	if strings.Contains(pending, "ann@corp.com") || strings.Count(pending, "ago") != 1 {
		t.Errorf("pending row should carry no approver and only the requested age: %s", pending)
	}

	approved := strings.Join(rowFields(t, out, privApprovedID.String()), " ")
	for _, want := range []string{"Approved", "demote", "ann@corp.com", "2 days ago", "1 hour ago"} {
		if !strings.Contains(approved, want) {
			t.Errorf("approved row missing %q: %s", want, approved)
		}
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeRequestsURL)
}

func TestGetPrivilegeRequestsJSONIsTheWireBody(t *testing.T) {
	e := newDataReviewGetEnv(t)
	body := privilegeWireBody(t, privilegeListFixture(2))
	servePrivilege(e, privilegeRequestsURL, http.StatusOK, body)

	out, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(body) {
		t.Errorf("-o json is not the wire body\n got: %s\nwant: %s", out, body)
	}
}

func TestGetPrivilegeRequestsYAMLKeepsWireKeys(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	out, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"requests:", "target_user_email: tom@corp.com", "requestor_email: null", "total: 2"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n%s", want, out)
		}
	}
}

func TestGetPrivilegeRequestsNamePrintsIDsOnly(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	out, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "privilege-request/" + privPendingID.String() + "\nprivilege-request/" + privApprovedID.String() + "\n"
	if out != want {
		t.Errorf("-o name = %q, want %q", out, want)
	}
}

func TestGetPrivilegeRequestsPaginationHint(t *testing.T) {
	for _, tc := range []struct {
		name  string
		total int
		want  bool
	}{
		{"more pages", 45, true},
		{"last page", 20, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(tc.total)))

			_, stderr, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			hint := "More requests — fetch the next page with --page 2."
			if got := strings.Contains(stderr, hint); got != tc.want {
				t.Errorf("hint present = %t, want %t\n%s", got, tc.want, stderr)
			}
		})
	}
}

// Every locally decidable mistake exits 2 before config, credentials or any
// request: the mock would 404 anything that got through, and Requests()
// proves nothing did.
func TestGetPrivilegeRequestsRejectsBadFlagsWithoutARequest(t *testing.T) {
	for _, args := range [][]string{
		{"--page", "0"},
		{"--limit", "-1"},
		{"--status", "approvedd"},
		{"--status", ""},
		{"--target-user", ""},
		{"--target-user", "  "},
	} {
		t.Run(strings.Join(args, "="), func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", args...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertPrivilegeRequests(t, e)
		})
	}
}

func TestGetPrivilegeRequestsSendsCanonicalStatus(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	if _, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests",
		"--status", "pending", "--page", "2", "--limit", "5"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{"status": "Pending", "page": "2", "limit": "5"} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// A UUID is sent as typed: no directory sweep, no membership check.
func TestGetPrivilegeRequestsTargetUUIDSkipsTheDirectory(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	if _, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests",
		"--target-user", privTargetID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query.Get("target_user_id"); got != privTargetID.String() {
		t.Errorf("target_user_id = %q, want %q", got, privTargetID)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeRequestsURL)
}

func TestGetPrivilegeRequestsTargetEmailResolves(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilegeUsers(e, http.StatusOK)
	query := servePrivilege(e, privilegeRequestsURL, http.StatusOK, privilegeWireBody(t, privilegeListFixture(2)))

	if _, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests",
		"--target-user", "TOM@corp.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query.Get("target_user_id"); got != privTargetID.String() {
		t.Errorf("target_user_id = %q, want %q", got, privTargetID)
	}
	assertPrivilegeRequests(t, e, "GET "+privilegeUsersURL, "GET "+privilegeRequestsURL)
}

func TestGetPrivilegeRequestsUnknownTargetIsBadUsage(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilegeUsers(e, http.StatusOK)

	_, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", "--target-user", "nobody@corp.com")
	assertExitCode(t, err, qerrors.ExitBadUsage)
	assertPrivilegeRequests(t, e, "GET "+privilegeUsersURL)
}

func TestGetPrivilegeRequestsForbidden(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilegeForbidden(e, privilegeRequestsURL)

	_, _, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests")
	assertExitCode(t, err, qerrors.ExitUnauthorized)
	if err != nil && !strings.Contains(err.Error(), "restricted to operations staff") {
		t.Errorf("the backend's 403 message should pass through: %v", err)
	}
}

// The standard empty-result path names the current org, and the routes are
// not org-filtered, so the hint has to say so.
func TestGetPrivilegeRequestsEmpty(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, privilegeRequestsURL, http.StatusOK,
		[]byte(`{"requests":[],"total":0,"page":1,"limit":20}`))

	out, stderr, err := e.run(t, NewPrivilegeRequestsCommand(), "privilege-requests", "--status", "expired")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("stdout should be empty, got %q", out)
	}
	for _, want := range []string{"No privilege requests matched --status \"expired\"", "platform-wide"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q\n%s", want, stderr)
		}
	}
}
