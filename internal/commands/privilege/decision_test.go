package privilege

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/testutil"
)

var approverID = uuid.MustParse("0190bbbb-0000-4000-8000-000000000003")

var (
	detailPath  = requestsPath + "/" + reqID.String()
	approvePath = detailPath + "/approve"
	rejectPath  = detailPath + "/reject"
)

// decisionVerbs is every verb that reads the request before deciding it.
// approve runs with --yes so that nothing these shared tests assert can be the
// prompt's doing.
var decisionVerbs = []struct {
	verb  string
	path  string
	extra []string
}{
	{"approve", approvePath, []string{"--yes"}},
	{"reject", rejectPath, nil},
}

// requestDetail is the detail of an escalation of Tom that Rita asked for two
// hours ago.
func requestDetail(status api.PrivilegeRequestStatus) privilege_requests.Detail {
	return privilege_requests.Detail{
		ID: reqID, Status: status,
		PermissionKey: "is_ops", OldValue: "false", NewValue: "true",
		Justification: justification,
		RequestedAt:   time.Now().Add(-2 * time.Hour),
		RequestorID:   requestorID, TargetUserID: targetID,
		Requestor: &privilege_requests.UserInfo{
			ID: requestorID, Email: "rita@corp.com", FirstName: "Rita", LastName: "Requestor", IsOps: true,
		},
		TargetUser: &privilege_requests.UserInfo{
			ID: targetID, Email: "tom@corp.com", FirstName: "Tom", LastName: "Target",
		},
	}
}

// decided is the request as a decision leaves it.
func decided(status api.PrivilegeRequestStatus) privilege_requests.Request {
	r := createdRequest(api.PrivilegeRequestStatusPending)
	at := r.RequestedAt.Add(time.Hour)
	r.Status, r.ApproverID, r.DecisionAt = status, &approverID, &at
	return r
}

func approvalResult() privilege_requests.ApprovalResult {
	msg := "Privilege change approved and applied"
	return privilege_requests.ApprovalResult{
		Request: decided(api.PrivilegeRequestStatusApproved),
		TargetUser: privilege_requests.UserInfo{
			ID: targetID, Email: "tom@corp.com", FirstName: "Tom", LastName: "Target", IsOps: true,
		},
		Message: &msg,
	}
}

func rejectionResult() privilege_requests.RejectionResult {
	msg := "Privilege request rejected"
	return privilege_requests.RejectionResult{Request: decided(api.PrivilegeRequestStatusRejected), Message: &msg}
}

func serveDetail(t *testing.T, mock *testutil.MockAPIServer, status api.PrivilegeRequestStatus) {
	t.Helper()
	serveJSON(mock, http.MethodGet, detailPath, http.StatusOK, wireBody(t, requestDetail(status)))
}

// serveJSON answers method+path with code and body, and returns the raw
// request bodies it received. The mock hands the handler the client's own
// request, whose Body is nil when nothing was sent.
func serveJSON(mock *testutil.MockAPIServer, method, path string, code int, body []byte) *[][]byte {
	var bodies [][]byte
	mock.RegisterHandler(method, path, func(w http.ResponseWriter, r *http.Request) {
		var raw []byte
		if r.Body != nil {
			raw, _ = io.ReadAll(r.Body)
		}
		bodies = append(bodies, raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	})
	return &bodies
}

// serveDecisions answers both decision routes with their success bodies, so a
// test that expects neither to be reached still sees it recorded if it is.
func serveDecisions(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	serveJSON(mock, http.MethodPost, approvePath, http.StatusOK, wireBody(t, approvalResult()))
	serveJSON(mock, http.MethodPost, rejectPath, http.StatusOK, wireBody(t, rejectionResult()))
}

func runDecision(t *testing.T, verb string, args ...string) (string, error) {
	t.Helper()
	return runPrivilege(t, "", append([]string{verb}, args...)...)
}

// The decision routes would answer an unknown id with 400. The read first is
// what makes it exit 3, as describe does — and nothing is decided.
func TestDecisionUnknownIDExitsThree(t *testing.T) {
	for _, v := range decisionVerbs {
		t.Run(v.verb, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveJSON(mock, http.MethodGet, detailPath, http.StatusNotFound,
				[]byte(`{"detail":{"msg":"Privilege request `+reqID.String()+` not found"}}`))
			serveDecisions(t, mock)

			out, err := runDecision(t, v.verb, append([]string{reqID.String()}, v.extra...)...)
			assertExitCode(t, err, qerrors.ExitNotFound)
			assertRequests(t, mock, "GET "+detailPath)
			if out != "" {
				t.Errorf("a refused %s printed a result:\n%s", v.verb, out)
			}
		})
	}
}

// A request that is no longer Pending is refused locally, with the one
// message these tests pin by text: it is qctl's own, not the server's.
func TestDecisionOfADecidedRequestIsRefusedLocally(t *testing.T) {
	for _, v := range decisionVerbs {
		for _, status := range []api.PrivilegeRequestStatus{
			api.PrivilegeRequestStatusApproved,
			api.PrivilegeRequestStatusRejected,
			api.PrivilegeRequestStatusExpired,
		} {
			t.Run(v.verb+"/"+string(status), func(t *testing.T) {
				mock := newPrivilegeTestEnv(t)
				serveDetail(t, mock, status)
				serveDecisions(t, mock)

				_, err := runDecision(t, v.verb, append([]string{reqID.String()}, v.extra...)...)
				assertExitCode(t, err, qerrors.ExitBadUsage)
				assertRequests(t, mock, "GET "+detailPath)
				want := "privilege-request/" + reqID.String() + " is already " + string(status) +
					"; nothing to " + v.verb
				if err.Error() != want {
					t.Errorf("error = %q, want %q", err.Error(), want)
				}
			})
		}
	}
}

// Everything wrong with the command line exits 2 and costs no request.
func TestDecisionUsageErrorsMakeNoRequest(t *testing.T) {
	for _, v := range decisionVerbs {
		for name, args := range map[string][]string{
			"not a uuid":     {"not-a-uuid"},
			"short id":       {"0190aaaa"},
			"empty id":       {""},
			"no id":          {},
			"two ids":        {reqID.String(), reqID.String()},
			"unknown flag":   {reqID.String(), "--reason", "duplicate"},
			"unknown output": {reqID.String(), "-o", "wide"},
		} {
			t.Run(v.verb+"/"+name, func(t *testing.T) {
				mock := newPrivilegeTestEnv(t)
				serveDetail(t, mock, api.PrivilegeRequestStatusPending)
				serveDecisions(t, mock)

				_, err := runDecision(t, v.verb, append(args, v.extra...)...)
				assertExitCode(t, err, qerrors.ExitBadUsage)
				assertRequests(t, mock)
			})
		}
	}
}

// A decision takes the privilege-request/<id> that -o name prints, and prints it.
func TestDecisionNameRoundTrips(t *testing.T) {
	ref := "privilege-request/" + reqID.String()
	for _, v := range decisionVerbs {
		t.Run(v.verb, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveDetail(t, mock, api.PrivilegeRequestStatusPending)
			serveDecisions(t, mock)

			out, err := runDecision(t, v.verb, append([]string{ref, "-o", "name"}, v.extra...)...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertRequests(t, mock, "GET "+detailPath, "POST "+v.path)
			if want := ref + "\n"; out != want {
				t.Errorf("output = %q, want %q", out, want)
			}
		})
	}
}

func TestDecisionJSONIsTheWireBody(t *testing.T) {
	for _, tt := range []struct {
		verb, path string
		extra      []string
		result     any
	}{
		{"approve", approvePath, []string{"--yes"}, approvalResult()},
		{"reject", rejectPath, nil, rejectionResult()},
	} {
		t.Run(tt.verb, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveDetail(t, mock, api.PrivilegeRequestStatusPending)
			wire := wireBody(t, tt.result)
			serveJSON(mock, http.MethodPost, tt.path, http.StatusOK, wire)

			out, err := runDecision(t, tt.verb, append([]string{reqID.String(), "-o", "json"}, tt.extra...)...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out != string(wire) {
				t.Errorf("-o json =\n%s\nwant the wire body\n%s", out, wire)
			}
		})
	}
}

// Without a terminal and without --yes, approve refuses after the read and
// before the POST, and says how to fix it.
func TestApproveNonInteractiveWithoutYesIsExitTwo(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	serveDecisions(t, mock)

	_, err := runDecision(t, "approve", reqID.String())
	assertExitCode(t, err, qerrors.ExitBadUsage)
	assertRequests(t, mock, "GET "+detailPath)
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error must name --yes, got: %v", err)
	}
}

// Declining is a choice, not bad usage: exit 1, and nothing is approved.
func TestApproveDeclinedAtThePromptApprovesNothing(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	serveDecisions(t, mock)
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	out, err := runDecision(t, "approve", reqID.String())
	assertExitCode(t, err, qerrors.ExitGenericError)
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v, want cmdutil.ErrDeclined", err)
	}
	assertRequests(t, mock, "GET "+detailPath)
	if out != "" {
		t.Errorf("a declined approval printed a result:\n%s", out)
	}
}

const approvedOutput = "privilege-request/0190aaaa-0000-4000-8000-000000000001 approved " +
	"(status: Approved, action: escalate, target: tom@corp.com is_ops now true)\n" +
	"Next: qctl describe privilege-request 0190aaaa-0000-4000-8000-000000000001\n"

func TestApproveWithYesReadsThenApproves(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	bodies := serveJSON(mock, http.MethodPost, approvePath, http.StatusOK, wireBody(t, approvalResult()))

	out, err := runDecision(t, "approve", reqID.String(), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, "GET "+detailPath, "POST "+approvePath)
	if len(*bodies) != 1 || len((*bodies)[0]) != 0 {
		t.Errorf("approve bodies = %q, want one empty body (the route takes none)", *bodies)
	}
	if out != approvedOutput {
		t.Errorf("output =\n%s\nwant\n%s", out, approvedOutput)
	}
}

func TestApproveConfirmedAtThePromptApproves(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	serveDecisions(t, mock)
	cmdutil.SetReader(strings.NewReader("y\n"))
	defer cmdutil.ResetReader()

	out, err := runDecision(t, "approve", reqID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, "GET "+detailPath, "POST "+approvePath)
	if out != approvedOutput {
		t.Errorf("output =\n%s\nwant\n%s", out, approvedOutput)
	}
}

func TestApproveNameOutputKeepsPromptOnStderr(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	serveDecisions(t, mock)
	cmdutil.SetReader(strings.NewReader("y\n"))
	defer cmdutil.ResetReader()

	stdout, stderr, err := runPrivilegeStreams(t, "", "approve", reqID.String(), "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, "GET "+detailPath, "POST "+approvePath)
	if stdout != "privilege-request/"+reqID.String()+"\n" {
		t.Errorf("stdout = %q, want only the request ref", stdout)
	}
	if !strings.Contains(stderr, "Approve privilege-request/"+reqID.String()) || !strings.Contains(stderr, "[y/N]:") {
		t.Errorf("stderr does not contain the confirmation prompt: %q", stderr)
	}
}

// A demotion reports the target's is_ops as the server left it.
func TestApproveDemotionOutput(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	result := approvalResult()
	result.Request.OldValue, result.Request.NewValue = "true", "false"
	result.TargetUser.IsOps = false
	serveJSON(mock, http.MethodPost, approvePath, http.StatusOK, wireBody(t, result))

	out, err := runDecision(t, "approve", reqID.String(), "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "(status: Approved, action: demote, target: tom@corp.com is_ops now false)\n"
	if !strings.Contains(out, want) {
		t.Errorf("output =\n%s\nwant a line ending %q", out, want)
	}
}

// The server's refusals pass through with their own exit codes and text:
// the two-person rule is a 403 (exit 4), everything else here a 400 (exit 2).
func TestApproveServerRefusals(t *testing.T) {
	tests := []struct {
		name   string
		status int
		msg    string
		want   int
	}{
		{"own request", http.StatusForbidden,
			"Two-person rule violation: cannot approve your own request", qerrors.ExitUnauthorized},
		{"targets the approver", http.StatusForbidden,
			"Two-person rule violation: cannot approve a privilege request targeting yourself", qerrors.ExitUnauthorized},
		{"expired", http.StatusBadRequest, "Privilege request has expired", qerrors.ExitBadUsage},
		{"stale is_ops", http.StatusBadRequest,
			"Target user's current is_ops value no longer matches the privilege request", qerrors.ExitBadUsage},
		{"decided in a race", http.StatusBadRequest, "Request has already been rejected", qerrors.ExitBadUsage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveDetail(t, mock, api.PrivilegeRequestStatusPending)
			serveJSON(mock, http.MethodPost, approvePath, tt.status, []byte(`{"detail":{"msg":"`+tt.msg+`"}}`))

			out, err := runDecision(t, "approve", reqID.String(), "--yes")
			assertExitCode(t, err, tt.want)
			assertRequests(t, mock, "GET "+detailPath, "POST "+approvePath)
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("error does not carry the server's text %q: %v", tt.msg, err)
			}
			if out != "" {
				t.Errorf("a refused approval printed a result:\n%s", out)
			}
		})
	}
}

// The prompt names the action, the target and the requestor, with the
// justification's lines above it.
func TestApprovePromptNamesWhatIsAboutToHappen(t *testing.T) {
	d := requestDetail(api.PrivilegeRequestStatusPending)
	d.Justification = "INC-4411: replay the stuck queue\nOn-call until Friday.\n"
	want := "Justification:\n" +
		"  INC-4411: replay the stuck queue\n" +
		"  On-call until Friday.\n" +
		"Approve privilege-request/" + reqID.String() +
		": escalate tom@corp.com (requested by rita@corp.com 2 hours ago)?"
	if got := approvePrompt(&d); got != want {
		t.Errorf("prompt =\n%s\nwant\n%s", got, want)
	}

	// Every embedded user is nullable; the prompt falls back to the UUIDs.
	d.TargetUser, d.Requestor = nil, nil
	d.OldValue, d.NewValue = "true", "false"
	got := approvePrompt(&d)
	wantTail := ": demote " + targetID.String() + " (requested by " + requestorID.String() + " 2 hours ago)?"
	if !strings.HasSuffix(got, wantTail) {
		t.Errorf("prompt =\n%s\nwant it to end %q", got, wantTail)
	}
}

// The reject body is the schema with no reason in it: the server discards
// one, so qctl never sends one.
func TestRejectReadsThenRejectsWithNoReason(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveDetail(t, mock, api.PrivilegeRequestStatusPending)
	bodies := serveJSON(mock, http.MethodPost, rejectPath, http.StatusOK, wireBody(t, rejectionResult()))

	out, err := runDecision(t, "reject", reqID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, "GET "+detailPath, "POST "+rejectPath)
	if len(*bodies) != 1 {
		t.Fatalf("made %d reject calls, want 1", len(*bodies))
	}
	dec := json.NewDecoder(bytes.NewReader((*bodies)[0]))
	dec.DisallowUnknownFields()
	var body api.RejectPrivilegeRequestSchema
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("reject body %q does not decode as RejectPrivilegeRequestSchema: %v", (*bodies)[0], err)
	}
	if body.RejectionReason != nil {
		t.Errorf("rejection_reason = %q, want none", *body.RejectionReason)
	}

	want := "privilege-request/" + reqID.String() + " rejected (status: Rejected, action: escalate, target: tom@corp.com)\n" +
		"Next: qctl describe privilege-request " + reqID.String() + "\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestRejectServerRefusals(t *testing.T) {
	tests := []struct {
		name   string
		status int
		msg    string
		want   int
	}{
		{"decided in a race", http.StatusBadRequest, "Request has already been approved", qerrors.ExitBadUsage},
		{"targets the rejector", http.StatusForbidden,
			"Cannot reject a privilege request targeting yourself", qerrors.ExitUnauthorized},
		{"not ops", http.StatusForbidden, "This feature is restricted to operations staff.", qerrors.ExitUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveDetail(t, mock, api.PrivilegeRequestStatusPending)
			serveJSON(mock, http.MethodPost, rejectPath, tt.status, []byte(`{"detail":{"msg":"`+tt.msg+`"}}`))

			out, err := runDecision(t, "reject", reqID.String())
			assertExitCode(t, err, tt.want)
			assertRequests(t, mock, "GET "+detailPath, "POST "+rejectPath)
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("error does not carry the server's text %q: %v", tt.msg, err)
			}
			if out != "" {
				t.Errorf("a refused rejection printed a result:\n%s", out)
			}
		})
	}
}

// reject takes no reason and no confirmation: the server discards a reason,
// and a rejection changes nobody's access.
func TestRejectHasNoReasonOrYesFlag(t *testing.T) {
	reject, _, err := NewCommand().Find([]string{"reject"})
	if err != nil || reject.Name() != "reject" {
		t.Fatalf("no reject subcommand: %v", err)
	}
	for _, name := range []string{"reason", "rejection-reason", "yes"} {
		if reject.Flags().Lookup(name) != nil {
			t.Errorf("reject has a --%s flag", name)
		}
	}
}
