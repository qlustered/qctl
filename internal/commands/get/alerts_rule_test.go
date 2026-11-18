package get

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// alertRuleEnv records every alert-list query and every rule-list request.
type alertRuleEnv struct {
	mock      *testutil.MockAPIServer
	queries   []url.Values
	ruleLists int
}

func newAlertRuleEnv(t *testing.T) *alertRuleEnv {
	t.Helper()
	e := &alertRuleEnv{mock: newAlertsTestEnv(t)}
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dataset-rules", func(w http.ResponseWriter, _ *http.Request) {
		e.ruleLists++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":2,"page":1,"results":[`+
			`{"id":"`+ignoreEmailRule+`","instance_name":"email_check","position":1,"state":"enabled"},`+
			`{"id":"`+ignorePhoneRule+`","instance_name":"phone_check","position":2,"state":"enabled"}]}`))
	})
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, r *http.Request) {
		e.queries = append(e.queries, r.URL.Query())
		testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{})
	})
	return e
}

func (e *alertRuleEnv) onlyQuery(t *testing.T) url.Values {
	t.Helper()
	if len(e.queries) != 1 {
		t.Fatalf("alert list requests = %d, want 1", len(e.queries))
	}
	return e.queries[0]
}

func TestGetAlertsExcludeIssueTypeIsRepeatable(t *testing.T) {
	e := newAlertRuleEnv(t)
	if err := runGetAlerts(t, "--exclude-issue-type", "severe_rule_bug", "--exclude-issue-type", "late_file"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := e.onlyQuery(t)["exclude_issue_types"]
	if !slices.Equal(got, []string{"severe_rule_bug", "late_file"}) {
		t.Errorf("exclude_issue_types = %q, want both values", got)
	}
}

func TestGetAlertsWithoutExcludeIssueTypeSendsNoFilter(t *testing.T) {
	e := newAlertRuleEnv(t)
	if err := runGetAlerts(t); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := e.onlyQuery(t)
	if q.Has("exclude_issue_types") || q.Has("dataset_rule_id") {
		t.Errorf("unset flags must send no filter, query: %v", q)
	}
}

func TestGetAlertsExcludingTheSelectedIssueTypeIsBadUsage(t *testing.T) {
	e := newAlertRuleEnv(t)
	err := runGetAlerts(t, "--issue-type", "late_file", "--exclude-issue-type", "late_file")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if len(e.queries) != 0 {
		t.Errorf("listed anyway: %v", e.queries)
	}
}

func TestExcludeIssueTypeFlagHasCompletionRegistered(t *testing.T) {
	cmd := NewAlertsCommand()
	if _, ok := cmd.GetFlagCompletionFunc("exclude-issue-type"); !ok {
		t.Error("--exclude-issue-type has no completion function")
	}
}

func TestGetAlertsRuleUUIDNeedsNoTable(t *testing.T) {
	e := newAlertRuleEnv(t)
	if err := runGetAlerts(t, "--rule", ignorePhoneRule); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := e.onlyQuery(t)
	if got := q.Get("dataset_rule_id"); got != ignorePhoneRule {
		t.Errorf("dataset_rule_id = %q, want %s", got, ignorePhoneRule)
	}
	if q.Has("is_row_level") || q.Has("resolve_after_migration") {
		t.Errorf("--rule must leave the server's lifted defaults alone, query: %v", q)
	}
	if e.ruleLists != 0 {
		t.Errorf("a UUID without a table listed the rules %d times", e.ruleLists)
	}
}

func TestGetAlertsRuleNameResolvesOnTheTable(t *testing.T) {
	e := newAlertRuleEnv(t)
	if err := runGetAlerts(t, "--table-id", "42", "--rule", "email_check"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := e.onlyQuery(t)
	if q.Get("dataset_rule_id") != ignoreEmailRule || q.Get("dataset_id") != "42" {
		t.Errorf("query = %v, want dataset_rule_id=%s and dataset_id=42", q, ignoreEmailRule)
	}
}

func TestGetAlertsRuleAliasNeedsNoTable(t *testing.T) {
	e := newAlertRuleEnv(t)
	key, _ := config.NormalizeEndpointKey(e.mock.URL())
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open alias store: %v", err)
	}
	alias, err := store.Allocate(key, aliasstore.ResourceDatasetRule, ignorePhoneRule)
	store.Close()
	if err != nil {
		t.Fatalf("allocate: %v", err)
	}

	if err := runGetAlerts(t, "--rule", alias); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := e.onlyQuery(t).Get("dataset_rule_id"); got != ignorePhoneRule {
		t.Errorf("dataset_rule_id = %q, want %s", got, ignorePhoneRule)
	}
}

func TestGetAlertsRuleUsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	for name, args := range map[string][]string{
		"name without a table": {"--rule", "email_check"},
		"empty":                {"--rule", " "},
	} {
		t.Run(name, func(t *testing.T) {
			e := newAlertRuleEnv(t)
			err := runGetAlerts(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
			}
			if len(e.queries) != 0 || e.ruleLists != 0 {
				t.Errorf("requests sent: %d alert lists, %d rule lists", len(e.queries), e.ruleLists)
			}
		})
	}
}

func TestGetAlertsRuleNotOnTheTableIsNotListed(t *testing.T) {
	e := newAlertRuleEnv(t)
	err := runGetAlerts(t, "--table-id", "42", "--rule", "zip_check")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want %d", code, err, qerrors.ExitNotFound)
	}
	if len(e.queries) != 0 {
		t.Errorf("listed anyway: %v", e.queries)
	}
}
