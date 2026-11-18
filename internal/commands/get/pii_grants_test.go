package get

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pii_grants"
)

const piiGrantsURL = "/api/orgs/" + testOrgID + "/datasets/42/pii-grants"

func piiGrantsFixture() pii_grants.ListPage {
	now := time.Now()
	revokedAt := now.Add(-time.Hour)
	admin, note := "admin@example.com", "laptop lost"
	reason := api.PiiAccessGrantRevokeReasonCompromised
	grantee := uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	grantor := uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
	return pii_grants.ListPage{Total: 2, Grants: []pii_grants.Grant{
		{
			ID: 9, DatasetID: 42, State: api.PiiGrantStateActive,
			GranteeUserID: grantee, GranteeEmail: "alice@example.com",
			GrantedByUserID: grantor, GrantedByEmail: admin, Reason: "INC-4411",
			CreatedAt: now.Add(-10 * time.Minute), ExpiresAt: now.Add(50 * time.Minute),
		},
		{
			ID: 7, DatasetID: 42, State: api.PiiGrantStateRevoked,
			GranteeUserID: grantee, GranteeEmail: "alice@example.com",
			GrantedByUserID: grantor, GrantedByEmail: admin, Reason: "audit",
			CreatedAt: now.Add(-2 * time.Hour), ExpiresAt: now.Add(6 * time.Hour),
			RevokedAt: &revokedAt, RevokedByUserID: &grantor, RevokedByEmail: &admin,
			RevokeReason: &reason, RevokeNote: &note,
		},
	}}
}

func servePiiGrants(t *testing.T, e *dataReviewGetEnv, status int, body any) *url.Values {
	t.Helper()
	raw, ok := body.([]byte)
	if !ok {
		var err error
		if raw, err = json.Marshal(body); err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
	}
	return servePrivilege(e, piiGrantsURL, status, raw)
}

func TestGetPiiGrantsTable(t *testing.T) {
	e := newDataReviewGetEnv(t)
	query := servePiiGrants(t, e, http.StatusOK, piiGrantsFixture())

	out, _, err := e.run(t, NewPiiGrantsCommand(), "pii-grants", "--table-id", "42",
		"--columns", "id,state,grantee,revoke_reason,revoke_note")
	if err != nil {
		t.Fatalf("get pii-grants: %v", err)
	}
	if got := query.Get("page") + "," + query.Get("limit"); got != "1,50" {
		t.Errorf("page,limit = %s, want 1,50", got)
	}
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 3 {
		t.Fatalf("want a header and two rows, got:\n%s", out)
	}
	for _, want := range []string{"9", "active", "alice@example.com"} {
		if !strings.Contains(lines[1], want) {
			t.Errorf("row %q lacks %q", lines[1], want)
		}
	}
	for _, want := range []string{"7", "revoked", "compromised", "laptop lost"} {
		if !strings.Contains(lines[2], want) {
			t.Errorf("row %q lacks %q", lines[2], want)
		}
	}
}

func TestGetPiiGrantsNamePrintsEachRef(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePiiGrants(t, e, http.StatusOK, piiGrantsFixture())
	out, _, err := e.run(t, NewPiiGrantsCommand(), "pii-grants", "--table-id", "42", "-o", "name")
	if err != nil {
		t.Fatalf("get pii-grants: %v", err)
	}
	if out != "pii-grant/9\npii-grant/7\n" {
		t.Errorf("output = %q", out)
	}
}

func TestGetPiiGrantsJSONIsAListOfTheWireRecords(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePiiGrants(t, e, http.StatusOK, piiGrantsFixture())
	out, _, err := e.run(t, NewPiiGrantsCommand(), "pii-grants", "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("get pii-grants: %v", err)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if len(list.Items) != 2 || list.Items[1]["revoke_note"] != "laptop lost" || list.Items[0]["grantee_email"] != "alice@example.com" {
		t.Errorf("items = %v", list.Items)
	}
}

func TestGetPiiGrantsUsageErrorsMakeNoRequest(t *testing.T) {
	cases := map[string][]string{
		"no table":      {},
		"limit too big": {"--table-id", "42", "--limit", "101"},
		"zero page":     {"--table-id", "42", "--page", "0"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			_, _, err := e.run(t, NewPiiGrantsCommand(), "pii-grants", args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit = %d, want 2 (err: %v)", got, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %q, want none", reqs)
			}
		})
	}
}

func TestGetPiiGrantsForbiddenExitsFour(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePiiGrants(t, e, http.StatusForbidden,
		[]byte(`{"detail":{"msg":"You don't have access to grant or revoke access to personal data."}}`))
	_, _, err := e.run(t, NewPiiGrantsCommand(), "pii-grants", "--table-id", "42")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit = %d, want 4 (err: %v)", got, err)
	}
}
