package get

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/composite_run_jobs"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/stats"
	"github.com/qlustered/qctl/internal/testutil"
)

// Each view tested here renders the backend message it prints: a long
// value arrives whole inside ~*value*~ (atlas docs/message_value_markers.md),
// and a human view shows the value, not the transport. The rendered form is
// short enough for the 80-column cap a terminal gets, so nothing would be
// shortened even there; fitting is internal/markdown's and the printers' to
// test.
//
// -o json keeps the message as the server sent it, marker included: that is
// the wire contract a script reads.
const (
	markedMessage   = `Repair failed: ~|"~{~*Hyundai Sonata Hybrid Limited*~}~"|~ is not a valid **model**`
	renderedMessage = `Repair failed: "Hyundai Sonata Hybrid Limited" is not a valid model`
	wireMessageJSON = `Repair failed: ~|\"~{~*Hyundai Sonata Hybrid Limited*~}~\"|~ is not a valid **model**`
)

func assertRenderedMessage(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, renderedMessage) {
		t.Errorf("output does not show the rendered message %q:\n%s", renderedMessage, out)
	}
	for _, markup := range []string{"~*", "*~", "~|", "|~", "**", "~{", "}~"} {
		if strings.Contains(out, markup) {
			t.Errorf("output still carries %q:\n%s", markup, out)
		}
	}
}

func assertWireMessage(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, wireMessageJSON) {
		t.Errorf("-o json does not carry the message as sent:\n%s", out)
	}
}

func TestGetNotificationsRendersAMarkedMessage(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	n := sampleInboxNotification()
	n.Msg = testutil.StringPtr(markedMessage)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{n}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRenderedMessage(t, out)

	out, _, err = runNotificationsCmd(t, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertWireMessage(t, out)
}

// runDryRunViews runs a dry-run command against one job whose summary and
// error_message both carry the marked message.
func runDryRunViews(t *testing.T, args ...string) string {
	t.Helper()
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()
	mock := testutil.NewMockAPIServer()
	defer mock.Close()
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	job := map[string]interface{}{
		"id": 7, "dataset_id": 42, "dataset_name": "orders", "state": "error",
		"summary": markedMessage, "error_message": markedMessage,
	}
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/dry-runs", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]interface{}{"total_rows": 1, "page": 1, "results": []interface{}{job}})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/dry-runs/7", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, job)
	})

	root := setupTestCommandForDryRunJobs()
	if args[0] == "dry-run-job" {
		root = setupDryRunJobTestCommand()
	}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"get"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("get %v: %v\n%s", args, err, buf.String())
	}
	return buf.String()
}

func TestGetDryRunJobsRendersMarkedMessages(t *testing.T) {
	assertRenderedMessage(t, runDryRunViews(t, "dry-run-jobs", "--table-id", "42"))
	assertWireMessage(t, runDryRunViews(t, "dry-run-jobs", "--table-id", "42", "-o", "json"))
}

func TestGetDryRunJobRendersMarkedMessages(t *testing.T) {
	out := runDryRunViews(t, "dry-run-job", "7", "-o", "table", "--columns", "id,summary,error_message")
	assertRenderedMessage(t, out)
	if strings.Count(out, renderedMessage) != 2 {
		t.Errorf("want both summary and error_message rendered:\n%s", out)
	}
	assertWireMessage(t, runDryRunViews(t, "dry-run-job", "7", "-o", "json"))
}

func TestGetCompositeRunJobsRendersAMarkedErrorMessage(t *testing.T) {
	job := sampleCompositeRunJobTiny()
	job.ErrorMessage = testutil.StringPtr(markedMessage)
	jobs := []composite_run_jobs.CompositeRunJobTiny{job}

	out, _, err := runCompositeRunJobs(t, jobs, "--columns", "id,error_message")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertRenderedMessage(t, out)

	out, _, err = runCompositeRunJobs(t, jobs, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertWireMessage(t, out)
}

func TestLatestAlertRendersAMarkedMessage(t *testing.T) {
	stats := menuStatsWithAlert()
	stats.LatestAlertItemMsg = testutil.StringPtr(markedMessage)

	out, err := runLatestAlert(t, stats, "--table-id", "42")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertRenderedMessage(t, out)

	out, err = runLatestAlert(t, stats, "--table-id", "42", "-o", "json")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	assertWireMessage(t, out)
}

// The watch view's event line renders the message too, whole and on one line.
func TestLatestAlertWatchRendersAMarkedMessage(t *testing.T) {
	changed := menuStats(4999, 3)
	changed.LatestAlertItemMsg = testutil.StringPtr(markedMessage)
	script := &scriptedFetch{responses: []*stats.MenuStats{menuStats(4821, 2), changed}}
	var out, errOut bytes.Buffer
	if err := runWatchLoop(t.Context(), testDeps(script, &out, &errOut), testWatchOptions("table", true)); err != nil {
		t.Fatalf("runWatchLoop: %v", err)
	}
	assertRenderedMessage(t, out.String())
}

// runListWithMarkedMessage serves one alert or warning whose msg carries the
// marked message and runs `get <resource>` on the table.
func runListWithMarkedMessage(t *testing.T, resource string, args ...string) string {
	t.Helper()
	mock := newAlertsTestEnv(t)
	one := testutil.IntPtr(1)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts", func(w http.ResponseWriter, _ *http.Request) {
		a := sampleAlertTiny()
		a.Msg = markedMessage
		testutil.RespondJSON(w, http.StatusOK, alerts.AlertsListResponse{Results: []alerts.AlertTiny{a}, Page: one, TotalRows: one})
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, api.WarningsListSchema{
			Results: []api.WarningTinySchema{{ID: 55, DatasetID: "42", DatasetName: "orders", IssueType: api.IssueTypeEnumAnomaly, Count: 1, Msg: markedMessage}},
			Page:    one, TotalRows: one,
		})
	})

	root := setupAlertsTestCommand()
	if resource == "warnings" {
		root = setupWarningsTestCommand()
	}
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(append([]string{"get", resource, "--table-id", "42"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("get %s %v: %v\n%s", resource, args, err, buf.String())
	}
	return buf.String()
}

// get alerts and get warnings name msg as their message column: the table
// renders it, and -o json keeps it as the server sent it.
func TestGetAlertsAndWarningsRenderAMarkedMessage(t *testing.T) {
	for _, resource := range []string{"alerts", "warnings"} {
		t.Run(resource, func(t *testing.T) {
			assertRenderedMessage(t, runListWithMarkedMessage(t, resource, "--columns", "id,msg"))
			assertWireMessage(t, runListWithMarkedMessage(t, resource, "-o", "json"))
		})
	}
}
