package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const prDescribeOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var (
	prDescribeID          = uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	prDescribeTargetID    = uuid.MustParse("0190bbbb-0000-4000-8000-000000000001")
	prDescribeRequestorID = uuid.MustParse("0190bbbb-0000-4000-8000-000000000002")
	prDescribeURL         = "/api/ops/" + prDescribeOrgID + "/privilege-requests/" + prDescribeID.String()
)

func setupPrivilegeRequestDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewPrivilegeRequestCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

func newPrivilegeRequestDescribeEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", prDescribeOrgID)
	env.SetupCredential(endpointKey, prDescribeOrgID, "test-token")
	return mock
}

func privilegeDetailFixture(status api.PrivilegeRequestStatus) privilege_requests.Detail {
	return privilege_requests.Detail{
		ID:            prDescribeID,
		Status:        status,
		PermissionKey: "is_ops",
		OldValue:      "false",
		NewValue:      "true",
		Justification: "incident 4411: on-call needs ops",
		RequestedAt:   time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC),
		RequestorID:   prDescribeRequestorID,
		Requestor: &privilege_requests.UserInfo{ID: prDescribeRequestorID, Email: "rita@corp.com",
			FirstName: "Rita", LastName: "Req"},
		TargetUserID: prDescribeTargetID,
		TargetUser: &privilege_requests.UserInfo{ID: prDescribeTargetID, Email: "tom@corp.com",
			FirstName: "Tom", LastName: "Target"},
		AuditEntries: &[]privilege_requests.AuditEntry{},
	}
}

func servePrivilegeDetail(mock *testutil.MockAPIServer, status int, body []byte) {
	mock.RegisterHandler("GET", prDescribeURL, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}

func privilegeDetailBody(t *testing.T, d privilege_requests.Detail) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return append(raw, '\n')
}

func runDescribePrivilegeRequest(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := setupPrivilegeRequestDescribeCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(append([]string{"describe", "privilege-request"}, args...))
	err := cmd.Execute()
	return out.String(), err
}

func assertPrivilegeDescribeExit(t *testing.T, err error, want int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error exiting %d, got nil", want)
	}
	if got := qerrors.GetExitCode(err); got != want {
		t.Errorf("exit code = %d, want %d (err: %v)", got, want, err)
	}
}

func TestDescribePrivilegeRequestPendingPayload(t *testing.T) {
	mock := newPrivilegeRequestDescribeEnv(t)
	servePrivilegeDetail(mock, http.StatusOK,
		privilegeDetailBody(t, privilegeDetailFixture(api.PrivilegeRequestStatusPending)))

	out, err := runDescribePrivilegeRequest(t, prDescribeID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"# privilege-request/" + prDescribeID.String(),
		"Status: Pending",
		"Action: escalate",
		"Target: tom@corp.com",
		"is_ops now: false",
		"Requestor: rita@corp.com",
		"Approver: pending",
		"Requested: 2026-09-16T10:00:00Z (",
		"incident 4411: on-call needs ops",
		"(none — no change has been applied)",
		"## NEXT ACTIONS",
		"qctl privilege approve " + prDescribeID.String(),
		"qctl privilege reject " + prDescribeID.String(),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
	if got, want := mock.Requests(), []string{"GET " + prDescribeURL}; !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func TestDescribePrivilegeRequestDecidedHasNoNextActions(t *testing.T) {
	for _, status := range []api.PrivilegeRequestStatus{
		api.PrivilegeRequestStatusApproved,
		api.PrivilegeRequestStatusRejected,
		api.PrivilegeRequestStatusExpired,
	} {
		t.Run(string(status), func(t *testing.T) {
			mock := newPrivilegeRequestDescribeEnv(t)
			servePrivilegeDetail(mock, http.StatusOK, privilegeDetailBody(t, privilegeDetailFixture(status)))

			out, err := runDescribePrivilegeRequest(t, prDescribeID.String())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(out, "Status: "+string(status)) {
				t.Errorf("payload missing the status\n%s", out)
			}
			if strings.Contains(out, "NEXT ACTIONS") {
				t.Errorf("a %s request has nothing left to decide\n%s", status, out)
			}
		})
	}
}

// The wire sends each embedded user as null when the row is gone; the
// payload must still render, by UUID.
func TestDescribePrivilegeRequestNullUsers(t *testing.T) {
	mock := newPrivilegeRequestDescribeEnv(t)
	body := `{"id":"` + prDescribeID.String() + `","status":"Approved","permission_key":"is_ops",` +
		`"old_value":"true","new_value":"false","justification":"left the rotation",` +
		`"requested_at":"2026-09-16T10:00:00Z","decision_at":"2026-09-16T11:00:00Z",` +
		`"requestor_id":"` + prDescribeRequestorID.String() + `","requestor":null,` +
		`"target_user_id":"` + prDescribeTargetID.String() + `","target_user":null,` +
		`"approver_id":"0190bbbb-0000-4000-8000-000000000003","approver":null,"audit_entries":[]}`
	servePrivilegeDetail(mock, http.StatusOK, []byte(body))

	out, err := runDescribePrivilegeRequest(t, prDescribeID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"Action: demote",
		"Target: " + prDescribeTargetID.String() + "\n",
		"Requestor: " + prDescribeRequestorID.String() + "\n",
		"Approver: 0190bbbb-0000-4000-8000-000000000003\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("payload missing %q\n%s", want, out)
		}
	}
}

func TestDescribePrivilegeRequestJSONIsTheWireBody(t *testing.T) {
	mock := newPrivilegeRequestDescribeEnv(t)
	body := privilegeDetailBody(t, privilegeDetailFixture(api.PrivilegeRequestStatusPending))
	servePrivilegeDetail(mock, http.StatusOK, body)

	out, err := runDescribePrivilegeRequest(t, prDescribeID.String(), "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(body) {
		t.Errorf("-o json is not the wire body\n got: %s\nwant: %s", out, body)
	}
}

func TestDescribePrivilegeRequestYAMLKeepsWireKeys(t *testing.T) {
	mock := newPrivilegeRequestDescribeEnv(t)
	servePrivilegeDetail(mock, http.StatusOK,
		privilegeDetailBody(t, privilegeDetailFixture(api.PrivilegeRequestStatusPending)))

	out, err := runDescribePrivilegeRequest(t, prDescribeID.String(), "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"target_user:", "target_user_id: " + prDescribeTargetID.String(), "audit_entries: []"} {
		if !strings.Contains(out, want) {
			t.Errorf("yaml missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "NEXT ACTIONS") {
		t.Errorf("structured output must not carry the text payload\n%s", out)
	}
}

func TestDescribePrivilegeRequestRejectsANonUUIDWithoutARequest(t *testing.T) {
	for _, arg := range []string{"42", "0190aaaa", "not-a-uuid"} {
		t.Run(arg, func(t *testing.T) {
			mock := newPrivilegeRequestDescribeEnv(t)
			_, err := runDescribePrivilegeRequest(t, arg)
			assertPrivilegeDescribeExit(t, err, qerrors.ExitBadUsage)
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("a malformed id must cost no request, got %q", got)
			}
		})
	}
}

func TestDescribePrivilegeRequestErrorExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   int
	}{
		{"unknown id", http.StatusNotFound,
			`{"detail":{"msg":"Privilege request ` + prDescribeID.String() + ` not found"}}`, qerrors.ExitNotFound},
		{"not ops", http.StatusForbidden,
			`{"detail":{"msg":"You don't have access to view privilege requests."}}`, qerrors.ExitUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newPrivilegeRequestDescribeEnv(t)
			servePrivilegeDetail(mock, tc.status, []byte(tc.body))

			_, err := runDescribePrivilegeRequest(t, prDescribeID.String())
			assertPrivilegeDescribeExit(t, err, tc.want)
		})
	}
}
