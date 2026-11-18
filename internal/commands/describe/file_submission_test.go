package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/testutil"
	"gopkg.in/yaml.v3"
)

// describeFileWithProgress serves file 1 and the submission-progress route
// with status and body, and returns the command's stdout and stderr.
func describeFileWithProgress(t *testing.T, status int, body string, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleStoredItemFull())
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/submissions/files/1",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, status, json.RawMessage(body))
		})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupFileDescribeTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"describe", "file", "1"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

const proposalID = "0190cccc-1111-2222-3333-444444444444"

const blockedProgress = `{"id":1,"name":"orders.csv","created_at":"2026-10-01T09:00:00Z","data_source_model_id":22,` +
	`"dataset_id":11,"state":"blocked","outcome":null,"has_open_replacement_question":false,` +
	`"series_id":"0190dddd-1111-2222-3333-444444444444","series_name":"Bordereaux",` +
	`"period_key":"2026-09","period_start":"2026-09-01","period_end":"2026-09-30",` +
	`"row_count":120,"clean_row_count":null,"bad_row_count":null,` +
	`"blocking_alert":{"id":77,"issue_type":"needs_review_migration"},"replaced_by":null,"unchanged_delivery":null,` +
	`"steps":[` +
	`{"step":"received","state":"done","actions":[],"alert_id":null},` +
	`{"step":"read","state":"done","actions":[],"alert_id":null},` +
	`{"step":"period","state":"done","actions":[],"alert_id":null},` +
	`{"step":"checked","state":"waiting_for_review","actions":[],"alert_id":77,` +
	`"change_proposal":{"id":"` + proposalID + `","state":"pending_review","approver_roles":["admin","ops"],"author_ids":[]}},` +
	`{"step":"loaded","state":"not_reached","actions":[],"alert_id":null}]}`

func TestDescribeFileShowsSubmissionProgress(t *testing.T) {
	stdout, stderr, err := describeFileWithProgress(t, http.StatusOK, blockedProgress)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var manifest stored_items.FileManifest
	if err := yaml.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	sub := manifest.Status.Submission
	if sub == nil {
		t.Fatalf("status.submission missing:\n%s", stdout)
	}
	if sub.State != "blocked" || sub.Outcome != nil || sub.SeriesName != "Bordereaux" ||
		sub.SeriesID == nil || *sub.SeriesID != "0190dddd-1111-2222-3333-444444444444" ||
		sub.PeriodKey == nil || *sub.PeriodKey != "2026-09" ||
		sub.PeriodStart == nil || *sub.PeriodStart != "2026-09-01" ||
		sub.PeriodEnd == nil || *sub.PeriodEnd != "2026-09-30" {
		t.Errorf("status.submission = %+v", sub)
	}
	if a := sub.BlockingAlert; a == nil || a.ID != 77 || a.IssueType != "needs_review_migration" {
		t.Errorf("blocking_alert = %+v", a)
	}
	if len(sub.Steps) != 5 {
		t.Fatalf("steps = %+v", sub.Steps)
	}
	checked := sub.Steps[3]
	if checked.Step != "checked" || checked.State != "waiting_for_review" || checked.AlertID == nil || *checked.AlertID != 77 {
		t.Errorf("checked step = %+v", checked)
	}
	w := checked.ChangeProposal
	if w == nil || w.ID != proposalID || w.State != "pending_review" ||
		strings.Join(w.ApproverRoles, ",") != "admin,ops" || w.AuthorIDs == nil || len(w.AuthorIDs) != 0 {
		t.Errorf("change_proposal = %+v", w)
	}
	if sub.Steps[0].ChangeProposal != nil || sub.Steps[0].AlertID != nil {
		t.Errorf("received step = %+v", sub.Steps[0])
	}
	want := "Step checked of file 1 waits on change proposal " + proposalID +
		" (pending_review): qctl describe change-proposal " + proposalID
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
	if strings.Contains(stdout, "qctl describe") {
		t.Errorf("the hint leaked into stdout:\n%s", stdout)
	}
}

// JSON keeps the wire's nulls for a file in no series, and a step that needs
// the caller lists its actions.
func TestDescribeFileSubmissionProgressJSON(t *testing.T) {
	progress := `{"id":1,"name":"orders.csv","created_at":"2026-10-01T09:00:00Z","data_source_model_id":22,` +
		`"dataset_id":11,"state":"blocked","outcome":null,"has_open_replacement_question":false,` +
		`"series_id":null,"series_name":"orders","period_key":null,"period_start":null,"period_end":null,` +
		`"row_count":null,"clean_row_count":null,"bad_row_count":null,` +
		`"blocking_alert":null,"replaced_by":null,"unchanged_delivery":null,` +
		`"steps":[{"step":"received","state":"done","actions":[],"alert_id":null},` +
		`{"step":"read","state":"needs_you","actions":["choose_header_row","discard_file"],"alert_id":5}]}`
	stdout, stderr, err := describeFileWithProgress(t, http.StatusOK, progress, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Status struct {
			Submission map[string]json.RawMessage `json:"submission"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	for _, key := range []string{"outcome", "series_id", "period_key", "period_start", "period_end"} {
		if string(got.Status.Submission[key]) != "null" {
			t.Errorf("%s = %s, want null", key, got.Status.Submission[key])
		}
	}
	if _, ok := got.Status.Submission["blocking_alert"]; ok {
		t.Errorf("a null blocking_alert was rendered:\n%s", stdout)
	}
	var steps []stored_items.FileSubmissionStep
	if err := json.Unmarshal(got.Status.Submission["steps"], &steps); err != nil {
		t.Fatalf("steps: %v", err)
	}
	if len(steps) != 2 || strings.Join(steps[1].Actions, ",") != "choose_header_row,discard_file" {
		t.Errorf("steps = %+v", steps)
	}
	if strings.Contains(stderr, "change-proposal") {
		t.Errorf("no step waits on a proposal, yet stderr names one:\n%s", stderr)
	}
}

// The progress is one extra read: a file of no source granted to the caller
// (404) or a caller without source-data read (403) still gets the file and no
// warning, while any other failure warns.
func TestDescribeFileSurvivesUnreadableSubmissionProgress(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		stdout, stderr, err := describeFileWithProgress(t, status, `{"detail":{"msg":"File not found."}}`)
		if err != nil {
			t.Fatalf("%d: unexpected error: %v", status, err)
		}
		if stderr != "" {
			t.Errorf("%d: stderr = %q, want nothing", status, stderr)
		}
		if strings.Contains(stdout, "submission:") {
			t.Errorf("%d: status.submission was rendered:\n%s", status, stdout)
		}
	}

	_, stderr, err := describeFileWithProgress(t, http.StatusInternalServerError, `{"detail":{"msg":"boom"}}`)
	if err != nil {
		t.Fatalf("500: unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "warning: submission progress unavailable") {
		t.Errorf("500: stderr lacks the warning: %q", stderr)
	}
}

// describeFileWithQuarantine serves file 1 and the stored-items progress route
// with status and body, and returns the command's stdout and stderr.
func describeFileWithQuarantine(t *testing.T, status int, body string, args ...string) (string, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, sampleStoredItemFull())
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1/progress",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, status, json.RawMessage(body))
		})

	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	rootCmd := setupFileDescribeTestCommand()
	var stdout, stderr bytes.Buffer
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetArgs(append([]string{"describe", "file", "1"}, args...))
	err := rootCmd.Execute()
	return stdout.String(), stderr.String(), err
}

// fileProgress is a stored-items progress body with the given quarantine
// counts, each a JSON value.
func fileProgress(waiting, toFix string) string {
	return `{"id":1,"name":"orders.csv","dataset_id":11,"series_id":null,"series_name":"orders",` +
		`"period_key":null,"period_start":null,"period_end":null,"is_locked":false,` +
		`"received":{"created_at":"2026-10-01T09:00:00Z","arrival":null,"submitted_by":null,` +
		`"data_source":{"id":22,"name":"s3-bucket"},"submission_comment_thread_id":null,"parent":null},` +
		`"steps":[],"stopping_alert":null,"ending":null,"hold_decision":null,"latest_import":null,"parts":null,` +
		`"waiting_for_approval_count":` + waiting + `,"rows_to_fix_count":` + toFix + `}`
}

func TestDescribeFileShowsQuarantineSplit(t *testing.T) {
	stdout, stderr, err := describeFileWithQuarantine(t, http.StatusOK, fileProgress("4", "7"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var manifest stored_items.FileManifest
	if err := yaml.Unmarshal([]byte(stdout), &manifest); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, stdout)
	}
	q := manifest.Status.Quarantine
	if q == nil || q.WaitingForApprovalCount == nil || *q.WaitingForApprovalCount != 4 ||
		q.RowsToFixCount == nil || *q.RowsToFixCount != 7 {
		t.Errorf("status.quarantine = %+v\n%s", q, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// Uncounted rows keep the wire's nulls.
func TestDescribeFileQuarantineSplitKeepsNulls(t *testing.T) {
	stdout, _, err := describeFileWithQuarantine(t, http.StatusOK, fileProgress("null", "null"), "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var got struct {
		Status struct {
			Quarantine map[string]json.RawMessage `json:"quarantine"`
		} `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout)
	}
	for _, key := range []string{"waiting_for_approval_count", "rows_to_fix_count"} {
		if v, ok := got.Status.Quarantine[key]; !ok || string(v) != "null" {
			t.Errorf("%s = %s (present %v), want null", key, v, ok)
		}
	}
}

// A refused read (an external caller, or no source-data read) or a 404 leaves
// status.quarantine out silently; any other failure warns.
func TestDescribeFileSurvivesUnreadableQuarantineSplit(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden} {
		stdout, stderr, err := describeFileWithQuarantine(t, status, `{"detail":{"msg":"no"}}`)
		if err != nil {
			t.Fatalf("%d: unexpected error: %v", status, err)
		}
		if stderr != "" {
			t.Errorf("%d: stderr = %q, want nothing", status, stderr)
		}
		if strings.Contains(stdout, "quarantine:") {
			t.Errorf("%d: status.quarantine was rendered:\n%s", status, stdout)
		}
	}

	_, stderr, err := describeFileWithQuarantine(t, http.StatusInternalServerError, `{"detail":{"msg":"boom"}}`)
	if err != nil {
		t.Fatalf("500: unexpected error: %v", err)
	}
	if !strings.Contains(stderr, "warning: quarantine counts unavailable") {
		t.Errorf("500: stderr lacks the warning: %q", stderr)
	}
}
