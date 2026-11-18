package pii_access_request //nolint:revive // Package name mirrors the command.

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
)

const decisionsPath = "/api/orgs/" + testOrgID + "/datasets/42/pii-grant-requests/7"

func adminJSON(state string) string {
	grant := `"grant_id":null,"grant_expires_at":null`
	if state == "approved" {
		grant = `"grant_id":12,"grant_expires_at":"2026-11-04T13:00:00Z"`
	}
	return `{"id":7,"dataset_id":42,"state":"` + state + `","requested_duration":"8h","reason":"audit",` +
		`"created_at":"2026-11-04T12:00:00Z","expires_at":"2026-11-11T12:00:00Z","requester_email":"rita@corp.com",` +
		`"requested_by_agent":false,"decided_by_email":"ann@corp.com",` + grant + `}`
}

// serveDecision answers verb on request 7 and records the decoded body.
func serveDecision(t *testing.T, verb string, status int, response string) *map[string]any {
	t.Helper()
	mock := newTestEnv(t)
	body := map[string]any{}
	mock.RegisterHandler("POST", decisionsPath+"/"+verb, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing bearer token")
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	})
	t.Cleanup(func() {
		if t.Failed() {
			return
		}
		if got := mock.Requests(); !reflect.DeepEqual(got, []string{"POST " + decisionsPath + "/" + verb}) {
			t.Errorf("requests = %q", got)
		}
	})
	return &body
}

func TestApproveBodies(t *testing.T) {
	cases := []struct {
		flags []string
		want  map[string]any
	}{
		{nil, map[string]any{"reason": nil}},
		{[]string{"--duration", "1H"}, map[string]any{"duration": "1h", "reason": nil}},
		{[]string{"--reason", "  quarter close  "}, map[string]any{"reason": "quarter close"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.flags, " "), func(t *testing.T) {
			body := serveDecision(t, "approve", http.StatusOK, adminJSON("approved"))
			out, _, err := run(t, append([]string{"approve", "pii-access-request/7", "--table-id", "42", "--yes"}, c.flags...)...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*body, c.want) {
				t.Errorf("body = %v, want %v", *body, c.want)
			}
			for _, want := range []string{"pii-access-request/7 approved, pii-grant/12 created: rita@corp.com sees table/42's personal data unmasked until ",
				"Next: qctl get pii-grants --table-id 42"} {
				if !strings.Contains(out, want) {
					t.Errorf("missing %q in %q", want, out)
				}
			}
		})
	}
}

func TestDenySendsReason(t *testing.T) {
	body := serveDecision(t, "deny", http.StatusOK, adminJSON("denied"))
	out, _, err := run(t, "deny", "7", "--table-id", "42", "--reason", " use the masked export ", "--yes")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*body, map[string]any{"reason": "use the masked export"}) {
		t.Errorf("body = %v", *body)
	}
	want := "pii-access-request/7 denied (requester: rita@corp.com, table/42)\nNext: qctl get pii-access-requests --all --table-id 42\n"
	if out != want {
		t.Errorf("out = %q", out)
	}
}

func TestDecisionFormats(t *testing.T) {
	for _, verb := range []string{"approve", "deny"} {
		for _, format := range []string{"json", "yaml", "name"} {
			t.Run(verb+"/"+format, func(t *testing.T) {
				state := map[string]string{"approve": "approved", "deny": "denied"}[verb]
				serveDecision(t, verb, http.StatusOK, adminJSON(state))
				out, _, err := run(t, verb, "7", "--table-id", "42", "--reason", "r", "--yes", "-o", format)
				if err != nil {
					t.Fatal(err)
				}
				switch format {
				case "name":
					if out != "pii-access-request/7\n" {
						t.Errorf("name = %q", out)
					}
				case "json":
					var value map[string]any
					if err := json.Unmarshal([]byte(out), &value); err != nil {
						t.Fatal(err)
					}
					if value["state"] != state || value["requester_email"] != "rita@corp.com" {
						t.Errorf("json = %s", out)
					}
				default:
					if !strings.Contains(out, "state: "+state) || !strings.Contains(out, "requester_email: rita@corp.com") {
						t.Errorf("yaml = %s", out)
					}
				}
			})
		}
	}
}

func TestDecisionRefusalsKeepTheirExitCode(t *testing.T) {
	for _, verb := range []string{"approve", "deny"} {
		for status, want := range map[int]int{400: 2, 403: 4, 404: 3, 409: 2, 422: 2, 500: 1} {
			t.Run(verb+"/"+http.StatusText(status), func(t *testing.T) {
				serveDecision(t, verb, status, `{"detail":{"msg":"server refusal"}}`)
				_, _, err := run(t, verb, "7", "--table-id", "42", "--reason", "r", "--yes")
				assertExitCode(t, err, want)
				if err != nil && !strings.Contains(err.Error(), "server refusal") {
					t.Errorf("lost message: %v", err)
				}
			})
		}
	}
}

func TestDecisionUsageErrorsMakeNoRequests(t *testing.T) {
	cases := [][]string{
		{"approve", "0", "--table-id", "42", "--yes"},
		{"approve", "abc", "--table-id", "42", "--yes"},
		{"approve", "7", "--yes"},
		{"approve", "7", "--table-id", "42", "--table", "orders", "--yes"},
		{"approve", "7", "--table-id", "42", "--duration", "2h", "--yes"},
		{"approve", "7", "--table-id", "42", "--duration", "", "--yes"},
		{"approve", "7", "--table-id", "42", "--reason", " ", "--yes"},
		{"approve", "7", "--table-id", "42", "--reason", strings.Repeat("x", 4001), "--yes"},
		{"approve", "7", "--table-id", "42", "--shared", "--yes"},
		{"deny", "7", "--table-id", "42", "--yes"},
		{"deny", "7", "--table-id", "42", "--reason", "  ", "--yes"},
		{"deny", "7", "--table-id", "42", "--reason", strings.Repeat("x", 4001), "--yes"},
		{"deny", "-1", "--table-id", "42", "--reason", "r", "--yes"},
		{"deny", "7", "--reason", "r", "--yes"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " ")[:min(40, len(strings.Join(args, " ")))], func(t *testing.T) {
			mock := newTestEnv(t)
			_, _, err := run(t, args...)
			assertExitCode(t, err, 2)
			assertRequests(t, mock)
		})
	}
}

func TestDecisionsRequireConfirmation(t *testing.T) {
	for _, verb := range []string{"approve", "deny"} {
		mock := newTestEnv(t)
		_, _, err := run(t, verb, "7", "--table-id", "42", "--reason", "r")
		assertExitCode(t, err, 2)
		assertRequests(t, mock)
	}
	mock := newTestEnv(t)
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()
	_, stderr, err := run(t, "approve", "7", "--table-id", "42", "--duration", "1h")
	if !strings.Contains(stderr, "Approve pii-access-request/7 on table/42, granting its requester unmasked access for 1h?") {
		t.Errorf("prompt = %q", stderr)
	}
	if !errors.Is(err, cmdutil.ErrDeclined) {
		t.Errorf("err = %v", err)
	}
	assertRequests(t, mock)
}

func TestOnlyDenyAndWithdrawAreDestructive(t *testing.T) {
	root := setupTestCommand()
	for _, verb := range []string{"request", "withdraw", "approve", "deny"} {
		cmd, _, err := root.Find([]string{"pii-access-request", verb})
		if err != nil {
			t.Fatal(err)
		}
		if cmdutil.IsDestructive(cmd) != (verb == "withdraw" || verb == "deny") {
			t.Errorf("destructive %s = %v", verb, cmdutil.IsDestructive(cmd))
		}
	}
}
