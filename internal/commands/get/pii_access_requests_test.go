package get

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_access_requests"
	"github.com/qlustered/qctl/internal/testutil"
)

const piiAccessRequestsURL = "/api/orgs/" + testOrgID + "/datasets/42/pii-access-requests"

func piiAccessFixture() pii_access_requests.ListPage {
	reason := "needs justification"
	fixture := pii_access_requests.ListPage{Total: 11, Requests: []pii_access_requests.Request{}}
	for i, state := range []api.PiiAccessRequestState{"open", "approved", "denied", "withdrawn", "expired"} {
		r := pii_access_requests.Request{ID: 9 - i, DatasetID: 42, State: state, RequestedDuration: api.N8H, Reason: "audit"}
		if state == "denied" {
			r.DenyReason = &reason
		}
		fixture.Requests = append(fixture.Requests, r)
	}
	return fixture
}

func TestGetPiiAccessRequestsProvidersAndFormats(t *testing.T) {
	for _, shared := range []bool{false, true} {
		for _, format := range []string{"table", "json", "yaml", "name"} {
			t.Run(format+map[bool]string{true: "/shared", false: "/own"}[shared], func(t *testing.T) {
				e := newDataReviewGetEnv(t)
				path := piiAccessRequestsURL
				if shared {
					path = strings.Replace(path, "/datasets/", "/third-party/datasets/", 1)
				}
				body, err := json.Marshal(piiAccessFixture())
				if err != nil {
					t.Fatal(err)
				}
				query := servePrivilege(e, path, 200, body)
				args := []string{"--table-id", "42", "--page", "2", "--limit", "5", "-o", format}
				if shared {
					args = append(args, "--shared")
				}
				out, stderr, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", args...)
				if err != nil {
					t.Fatal(err)
				}
				if query.Get("page") != "2" || query.Get("limit") != "5" {
					t.Errorf("query = %v", query)
				}
				if len(e.mock.Requests()) != 1 {
					t.Errorf("requests = %v", e.mock.Requests())
				}
				switch format {
				case "table":
					for _, want := range []string{"open", "approved", "denied", "withdrawn", "expired", "needs justification"} {
						if !strings.Contains(out, want) {
							t.Errorf("missing %q in %s", want, out)
						}
					}
					if !strings.Contains(stderr, "--page 3") {
						t.Errorf("missing pagination: %s", stderr)
					}
				case "name":
					if out != "pii-access-request/9\npii-access-request/8\npii-access-request/7\npii-access-request/6\npii-access-request/5\n" {
						t.Errorf("name = %q", out)
					}
				case "json":
					var list struct {
						Items    []map[string]any `json:"items"`
						NextPage *int             `json:"next_page"`
						Total    int              `json:"total"`
					}
					if err := json.Unmarshal([]byte(out), &list); err != nil {
						t.Fatal(err)
					}
					if len(list.Items) != 5 || list.Items[2]["deny_reason"] != "needs justification" || list.Total != 11 || list.NextPage == nil || *list.NextPage != 3 {
						t.Errorf("list = %s", out)
					}
					if _, ok := list.Items[0]["grant_expires_at"]; !ok {
						t.Error("missing grant_expires_at")
					}
				case "yaml":
					page, limit, total, next := 2, 5, 11, 3
					expected, err := json.Marshal(output.NewList(piiAccessFixture().Requests, &page, &limit, &total, &next))
					if err != nil {
						t.Fatal(err)
					}
					testutil.AssertYAMLReadsAsJSON(t, out, string(expected))
				}
			})
		}
	}
}

func TestGetPiiAccessRequestsUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{}, {"--table-id", "42", "--limit", "101"}, {"--table-id", "42", "--page", "0"},
		{"--table-id", "42", "--limit", "0"}, {"--table-id", "-2"},
	} {
		e := newDataReviewGetEnv(t)
		_, _, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", args...)
		if err == nil || qerrors.GetExitCode(err) != 2 {
			t.Errorf("args %v: err = %v", args, err)
		}
		if len(e.mock.Requests()) != 0 {
			t.Errorf("requests = %v", e.mock.Requests())
		}
	}
}

func TestGetPiiAccessRequestsEmptyAndForbidden(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusForbidden} {
		e := newDataReviewGetEnv(t)
		body := []byte(`{"requests":[],"total":0}`)
		if status == 403 {
			body = []byte(`{"detail":{"msg":"access denied"}}`)
		}
		servePrivilege(e, piiAccessRequestsURL, status, body)
		out, _, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", "--table-id", "42", "-o", "json")
		if status == 403 {
			if err == nil || qerrors.GetExitCode(err) != 4 {
				t.Errorf("err = %v", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Items []any `json:"items"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.Items == nil || len(result.Items) != 0 {
				t.Errorf("out = %s", out)
			}
		}
	}
}

const piiGrantRequestsURL = "/api/orgs/" + testOrgID + "/datasets/42/pii-grant-requests"

func TestGetPiiAccessRequestsAllUsesTheAdminRoute(t *testing.T) {
	for _, format := range []string{"table", "json", "name"} {
		t.Run(format, func(t *testing.T) {
			e := newDataReviewGetEnv(t)
			query := servePrivilege(e, piiGrantRequestsURL, 200, []byte(`{"total":2,"requests":[`+
				`{"id":8,"dataset_id":42,"state":"approved","requested_duration":"8h","reason":"audit",`+
				`"created_at":"2026-11-04T12:00:00Z","expires_at":"2026-11-11T12:00:00Z","requester_email":"rita@corp.com",`+
				`"requested_by_agent":true,"decided_by_email":"ann@corp.com","grant_id":12,"grant_expires_at":"2026-11-04T20:00:00Z"},`+
				`{"id":7,"dataset_id":42,"state":"open","requested_duration":"1h","reason":"refunds",`+
				`"created_at":"2026-11-04T12:00:00Z","expires_at":"2026-11-11T12:00:00Z","requester_email":"tom@corp.com",`+
				`"requested_by_agent":false,"decided_by_email":null,"grant_id":null}]}`))
			out, _, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", "--table-id", "42", "--all", "-o", format)
			if err != nil {
				t.Fatal(err)
			}
			if query.Get("page") != "1" || query.Get("limit") != "50" {
				t.Errorf("query = %v", query)
			}
			switch format {
			case "table":
				for _, want := range []string{"REQUESTER", "DECIDED-BY", "GRANT", "rita@corp.com", "ann@corp.com", "pii-grant/12", "tom@corp.com", "refunds"} {
					if !strings.Contains(out, want) {
						t.Errorf("missing %q in %s", want, out)
					}
				}
			case "json":
				var list struct {
					Items []map[string]any `json:"items"`
				}
				if err := json.Unmarshal([]byte(out), &list); err != nil {
					t.Fatal(err)
				}
				if len(list.Items) != 2 || list.Items[0]["requester_email"] != "rita@corp.com" || list.Items[0]["grant_id"] != float64(12) {
					t.Errorf("json = %s", out)
				}
			case "name":
				if out != "pii-access-request/8\npii-access-request/7\n" {
					t.Errorf("name = %q", out)
				}
			}
		})
	}
}

func TestGetPiiAccessRequestsAllRefusesShared(t *testing.T) {
	e := newDataReviewGetEnv(t)
	_, _, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", "--table-id", "42", "--all", "--shared")
	if err == nil || qerrors.GetExitCode(err) != 2 {
		t.Errorf("err = %v", err)
	}
	if len(e.mock.Requests()) != 0 {
		t.Errorf("requests = %v", e.mock.Requests())
	}
}

func TestGetPiiAccessRequestsAllForbidden(t *testing.T) {
	e := newDataReviewGetEnv(t)
	servePrivilege(e, piiGrantRequestsURL, http.StatusForbidden, []byte(`{"detail":{"msg":"access denied"}}`))
	_, _, err := e.run(t, NewPiiAccessRequestsCommand(), "pii-access-requests", "--table-id", "42", "--all")
	if err == nil || qerrors.GetExitCode(err) != 4 {
		t.Errorf("err = %v", err)
	}
}
