package privilege

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

var (
	reqID       = uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	targetID    = uuid.MustParse("0190bbbb-0000-4000-8000-000000000001")
	requestorID = uuid.MustParse("0190bbbb-0000-4000-8000-000000000002")
)

const justification = "INC-4411: on-call needs ops access to replay the stuck queue"

// createdRequest is the 201 body for an escalation with the given status. An
// inline approval (the sole-eligible-ops exemption) records the requestor as
// the approver and stamps the decision.
func createdRequest(status api.PrivilegeRequestStatus) privilege_requests.Request {
	r := privilege_requests.Request{
		ID: reqID, Status: status,
		PermissionKey: "is_ops", OldValue: "false", NewValue: "true",
		Justification: justification,
		RequestedAt:   time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC),
		RequestorID:   requestorID, TargetUserID: targetID,
	}
	if status == api.PrivilegeRequestStatusApproved {
		decided := r.RequestedAt
		r.ApproverID, r.DecisionAt = &requestorID, &decided
	}
	return r
}

// wireBody encodes v the way `-o json` does, so a handler serving these bytes
// lets a test assert the command's output is the wire body byte for byte.
func wireBody(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return append(raw, '\n')
}

// serveCreate answers the create route with status and body, and returns the
// raw request bodies it received.
func serveCreate(t *testing.T, mock *testutil.MockAPIServer, status int, body []byte) *[][]byte {
	t.Helper()
	var bodies [][]byte
	mock.RegisterHandler(http.MethodPost, requestsPath, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, raw)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	return &bodies
}

func serveCreated(t *testing.T, mock *testutil.MockAPIServer, status api.PrivilegeRequestStatus) *[][]byte {
	t.Helper()
	return serveCreate(t, mock, http.StatusCreated, wireBody(t, createdRequest(status)))
}

// serveUsers answers the users directory. Two users share the name "Tom
// Target", so that name is ambiguous while each email is not.
func serveUsers(mock *testutil.MockAPIServer) {
	mock.RegisterHandler(http.MethodGet, usersPath, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[` +
			`{"id":"` + targetID.String() + `","email":"tom@corp.com","first_name":"Tom","last_name":"Target"},` +
			`{"id":"` + requestorID.String() + `","email":"tom.t@corp.com","first_name":"Tom","last_name":"Target"}` +
			`],"next":null}`))
	})
}

func assertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("requests = %q, want %q", got, want)
	}
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", raw, err)
	}
	return body
}

// The wire body is exactly the three fields, with the action in its canonical
// spelling whatever case it was typed in.
func TestRequestSendsExactlyTheThreeFields(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	bodies := serveCreated(t, mock, api.PrivilegeRequestStatusPending)

	_, err := runPrivilege(t, "", "request",
		"--target-user", targetID.String(), "--action", "ESCALATE", "--justification", justification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// A UUID target costs no directory request.
	assertRequests(t, mock, "POST "+requestsPath)
	if len(*bodies) != 1 {
		t.Fatalf("made %d create calls, want 1", len(*bodies))
	}
	body := decodeBody(t, (*bodies)[0])
	keys := make([]string, 0, len(body))
	for k := range body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if want := []string{"action", "justification", "target_user_id"}; !reflect.DeepEqual(keys, want) {
		t.Errorf("body keys = %v, want %v", keys, want)
	}
	want := map[string]any{
		"action":         string(api.Escalate),
		"target_user_id": targetID.String(),
		"justification":  justification,
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("body = %v, want %v", body, want)
	}
}

func TestRequestReadsTheJustificationFromAFile(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	bodies := serveCreated(t, mock, api.PrivilegeRequestStatusPending)
	path := filepath.Join(t.TempDir(), "why.md")
	text := "Left the on-call rotation on 2026-09-15.\nNo open incidents need ops access.\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := runPrivilege(t, "", "request",
		"--target-user", targetID.String(), "--action", "demote", "--justification-file", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("made %d create calls, want 1", len(*bodies))
	}
	body := decodeBody(t, (*bodies)[0])
	if body["justification"] != text {
		t.Errorf("justification = %q, want the file's text verbatim", body["justification"])
	}
	if body["action"] != string(api.Demote) {
		t.Errorf("action = %v, want %q", body["action"], api.Demote)
	}
}

func TestRequestReadsTheJustificationFromStdin(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	bodies := serveCreated(t, mock, api.PrivilegeRequestStatusPending)
	text := "piped: INC-4411 needs ops access\n"

	_, err := runPrivilege(t, text, "request",
		"--target-user", targetID.String(), "--action", "escalate", "--justification-file", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("made %d create calls, want 1", len(*bodies))
	}
	if got := decodeBody(t, (*bodies)[0])["justification"]; got != text {
		t.Errorf("justification = %q, want the piped text %q", got, text)
	}
}

// Everything the user can get wrong on the command line exits 2 and costs
// no request.
func TestRequestUsageErrorsMakeNoRequest(t *testing.T) {
	target := targetID.String()
	tests := []struct {
		name string
		args []string
	}{
		{"both justification sources", []string{
			"--target-user", target, "--action", "escalate",
			"--justification", justification, "--justification-file", "why.md",
		}},
		{"no justification source", []string{"--target-user", target, "--action", "escalate"}},
		{"empty --justification-file", []string{
			"--target-user", target, "--action", "escalate", "--justification-file", "",
		}},
		{"unknown action", []string{
			"--target-user", target, "--action", "promote", "--justification", justification,
		}},
		{"missing --action", []string{"--target-user", target, "--justification", justification}},
		{"missing --target-user", []string{"--action", "escalate", "--justification", justification}},
		{"blank --target-user", []string{
			"--target-user", "  ", "--action", "escalate", "--justification", justification,
		}},
		{"positional argument", []string{
			target, "--target-user", target, "--action", "escalate", "--justification", justification,
		}},
		{"unknown flag", []string{
			"--target-user", target, "--action", "escalate", "--justification", justification, "--reason", "x",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveUsers(mock)
			serveCreated(t, mock, api.PrivilegeRequestStatusPending)

			_, err := runPrivilege(t, "", append([]string{"request"}, tt.args...)...)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertRequests(t, mock)
		})
	}
}

// A target the directory cannot pin down is refused after the directory read
// and before any POST.
func TestRequestUnresolvableTargetMakesNoPOST(t *testing.T) {
	for name, target := range map[string]string{
		"no match":  "nobody@corp.com",
		"ambiguous": "Tom Target",
	} {
		t.Run(name, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveUsers(mock)
			serveCreated(t, mock, api.PrivilegeRequestStatusPending)

			_, err := runPrivilege(t, "", "request",
				"--target-user", target, "--action", "escalate", "--justification", justification)
			assertExitCode(t, err, qerrors.ExitBadUsage)
			assertRequests(t, mock, "GET "+usersPath)
		})
	}
}

func TestRequestResolvesAnEmailTarget(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveUsers(mock)
	bodies := serveCreated(t, mock, api.PrivilegeRequestStatusPending)

	out, err := runPrivilege(t, "", "request",
		"--target-user", "TOM@corp.com", "--action", "escalate", "--justification", justification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRequests(t, mock, "GET "+usersPath, "POST "+requestsPath)
	if got := decodeBody(t, (*bodies)[0])["target_user_id"]; got != targetID.String() {
		t.Errorf("target_user_id = %v, want %s", got, targetID)
	}
	// The result line names the target as typed.
	if !strings.Contains(out, "target: TOM@corp.com)") {
		t.Errorf("output does not name the target as typed:\n%s", out)
	}
}

func TestRequestPendingOutput(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveUsers(mock)
	serveCreated(t, mock, api.PrivilegeRequestStatusPending)

	out, err := runPrivilege(t, "", "request",
		"--target-user", "tom@corp.com", "--action", "escalate", "--justification", justification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "privilege-request/" + reqID.String() + " created (status: Pending, action: escalate, target: tom@corp.com)\n" +
		"Next: qctl describe privilege-request " + reqID.String() + "\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

// The sole-eligible-ops exemption: the 201 is already Approved, and the
// output must say the change happened, not that it awaits an approver.
func TestRequestApprovedOutput(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveUsers(mock)
	serveCreated(t, mock, api.PrivilegeRequestStatusApproved)

	out, err := runPrivilege(t, "", "request",
		"--target-user", "tom@corp.com", "--action", "escalate", "--justification", justification)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "privilege-request/" + reqID.String() + " created and applied (status: Approved, action: escalate, target: tom@corp.com)\n" +
		"Applied immediately: the server found you to be the sole eligible ops user, so the two-person rule did not apply.\n" +
		"Next: qctl describe privilege-request " + reqID.String() + "\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestRequestServerRefusals(t *testing.T) {
	const duplicate = "A pending privilege request already exists for this user (ID: 0190cccc-0000-4000-8000-000000000009)"
	tests := []struct {
		name   string
		status int
		body   string
		want   int
		// passes names text the server sent that must reach the user unparsed.
		passes string
	}{
		{"self-targeting", http.StatusBadRequest,
			`{"detail":{"msg":"Cannot request privilege changes for yourself"}}`,
			qerrors.ExitBadUsage, "Cannot request privilege changes for yourself"},
		{"duplicate pending", http.StatusBadRequest,
			`{"detail":{"msg":"` + duplicate + `"}}`,
			qerrors.ExitBadUsage, "0190cccc-0000-4000-8000-000000000009"},
		{"justification too short", http.StatusUnprocessableEntity,
			`{"detail":[{"type":"string_too_short","loc":["body","justification"],` +
				`"msg":"String should have at least 10 characters","ctx":{"min_length":10}}]}`,
			qerrors.ExitBadUsage, "at least 10 characters"},
		{"not ops", http.StatusForbidden,
			`{"detail":{"msg":"This feature is restricted to operations staff."}}`,
			qerrors.ExitUnauthorized, "restricted to operations staff"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := newPrivilegeTestEnv(t)
			serveCreate(t, mock, tt.status, []byte(tt.body))

			out, err := runPrivilege(t, "", "request",
				"--target-user", targetID.String(), "--action", "escalate", "--justification", "short")
			assertExitCode(t, err, tt.want)
			if !strings.Contains(err.Error(), tt.passes) {
				t.Errorf("error does not carry the server's text %q: %v", tt.passes, err)
			}
			if out != "" {
				t.Errorf("a refused request printed a result:\n%s", out)
			}
		})
	}
}

func TestRequestNamePrintsOnlyTheRef(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveCreated(t, mock, api.PrivilegeRequestStatusPending)

	out, err := runPrivilege(t, "", "request",
		"--target-user", targetID.String(), "--action", "escalate", "--justification", justification, "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "privilege-request/" + reqID.String() + "\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

func TestRequestJSONIsTheWireBody(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	wire := wireBody(t, createdRequest(api.PrivilegeRequestStatusApproved))
	serveCreate(t, mock, http.StatusCreated, wire)

	out, err := runPrivilege(t, "", "request",
		"--target-user", targetID.String(), "--action", "escalate", "--justification", justification, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != string(wire) {
		t.Errorf("-o json =\n%s\nwant the wire body\n%s", out, wire)
	}
}

// YAML keeps the wire keys (a JSON round-trip), not yaml.v3's Go field names.
func TestRequestYAMLKeepsTheWireKeys(t *testing.T) {
	mock := newPrivilegeTestEnv(t)
	serveCreated(t, mock, api.PrivilegeRequestStatusPending)

	out, err := runPrivilege(t, "", "request",
		"--target-user", targetID.String(), "--action", "escalate", "--justification", justification, "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("-o yaml is not YAML: %v\n%s", err, out)
	}
	for _, key := range []string{"id", "status", "target_user_id", "requestor_id", "old_value", "new_value"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("-o yaml has no %q key:\n%s", key, out)
		}
	}
	if doc["status"] != string(api.PrivilegeRequestStatusPending) {
		t.Errorf("status = %v, want %q", doc["status"], api.PrivilegeRequestStatusPending)
	}
}
