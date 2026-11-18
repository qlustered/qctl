package get

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const notificationsTestOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var notifTestUUID = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")

func setupNotificationsTestCommand() *cobra.Command {
	// SilenceUsage/SilenceErrors mirror the real root (internal/commands/root/
	// root.go), matching the notification package's own harness. Without them a
	// RunE error dumps the usage block -- which names every --type value -- into
	// the stdout these tests capture, so a Contains assertion could pass off the
	// dump rather than off the command's output.
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().String("columns", "", "Columns to display")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	getCmd := &cobra.Command{Use: "get"}
	getCmd.AddCommand(NewNotificationsCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

func newNotificationsTestEnv(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", notificationsTestOrgID)
	env.SetupCredential(endpointKey, notificationsTestOrgID, "test-token")
	return mock
}

func sampleInboxNotification() api.NotificationSchema {
	datasetName := "orders"
	return api.NotificationSchema{
		ID:               notifTestUUID,
		UserID:           uuid.MustParse("0190aaaa-1111-2222-3333-444444444444"),
		OrganizationID:   uuid.MustParse(notificationsTestOrgID),
		NotificationType: api.ReviewRequested,
		Msg:              testutil.StringPtr("please review dedupe-emails@2.1.0"),
		Actor: &api.UserInfoTinyDictSchema{
			ID: uuid.MustParse("0190cccc-1111-2222-3333-444444444444"), FirstName: "Ada", LastName: "Lovelace",
			Email: "ada@corp.com",
		},
		DatasetID:         testutil.IntPtr(42),
		DatasetName:       &datasetName,
		NotificationCount: 1,
		CreatedAt:         time.Now().Add(-3 * time.Hour),
		LastNotifiedAt:    time.Now().Add(-3 * time.Hour),
	}
}

func runNotificationsCmd(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := setupNotificationsTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(append([]string{"get", "notifications"}, args...))
	err := cmd.Execute()
	return out.String(), errOut.String(), err
}

// The default table must be readable with no follow-up lookup: the actor and
// the table name come out of the listing response itself.
func TestGetNotificationsTable(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results:   []api.NotificationSchema{sampleInboxNotification()},
				Page:      testutil.IntPtr(1),
				TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{
		"ID", "TYPE", "MSG", "ACTOR", "TABLE", "READ", "AGE",
		notifTestUUID.String(), "review_requested", "please review", "Ada Lovelace", "orders (42)", "no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "DISMISSED") {
		t.Error("DISMISSED is in the default columns; it is all-no unless --include-dismissed is set")
	}
}

// A notification nobody caused has no actor. Rendering a blank cell would read
// as a missing lookup rather than "the system raised this".
func TestGetNotificationsRendersAMachineNotification(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			n := sampleInboxNotification()
			n.Actor = nil
			n.DatasetID = nil
			n.DatasetName = nil
			n.Msg = nil
			n.NotificationType = api.ValidationFailed
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{n}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Counting "-" over the whole output would be vacuous: the id is a UUID
	// and carries four of them. Look at the row's fields instead.
	var row string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "validation_failed") {
			row = line
			break
		}
	}
	if row == "" {
		t.Fatalf("no row for the notification\n%s", out)
	}
	dashes := 0
	for _, field := range strings.Fields(row) {
		if field == "-" {
			dashes++
		}
	}
	if dashes != 3 {
		t.Errorf("row has %d '-' cells, want 3 (MSG, ACTOR, TABLE): %q", dashes, row)
	}
}

func TestGetNotificationsIncludeDismissedAddsTheColumn(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			n := sampleInboxNotification()
			dismissed := time.Now().Add(-time.Hour)
			n.DismissedAt = &dismissed
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{n}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t, "--include-dismissed")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "DISMISSED") {
		t.Errorf("--include-dismissed did not add the DISMISSED column\n%s", out)
	}
}

func TestGetNotificationsContextColumns(t *testing.T) {
	jobID, jobType := 123, api.IngestionJob
	rowID, fieldName := 456, "email"
	count, zero := 1204, 0
	for _, tc := range []struct {
		name string
		n    api.NotificationSchema
		want []string
	}{
		{"job", api.NotificationSchema{NotificationType: api.CorrectionsNotCarried, JobID: &jobID, JobType: &jobType},
			[]string{"123", "IngestionJob"}},
		{"quarantine", api.NotificationSchema{NotificationType: api.QuarantinedRows, QuarantinedRowsCount: &count},
			[]string{"1204"}},
		{"cleared quarantine", api.NotificationSchema{NotificationType: api.QuarantinedRows, QuarantinedRowsCount: &zero},
			[]string{"0"}},
		{"cell", api.NotificationSchema{NotificationType: api.ExternalIssueComment, RowID: &rowID, FieldName: &fieldName},
			[]string{"456", "email"}},
		{"submitted proposal", api.NotificationSchema{NotificationType: api.ChangeProposalSubmitted, ChangeProposalID: &notifTestUUID, AlertItemID: &jobID},
			[]string{notifTestUUID.String(), "123"}},
		{"decided proposal", api.NotificationSchema{NotificationType: api.ChangeProposalDecided, ChangeProposalID: &notifTestUUID, WarningItemID: &rowID},
			[]string{notifTestUUID.String(), "456"}},
		{"failed proposal", api.NotificationSchema{NotificationType: api.ChangeProposalExecutionFailed, ChangeProposalID: &notifTestUUID, WarningItemID: &rowID},
			[]string{notifTestUUID.String(), "456"}},
		{"issues report sent", api.NotificationSchema{NotificationType: api.IssuesReportSent, StoredItemID: &rowID},
			[]string{"456"}},
		{"absent context", api.NotificationSchema{NotificationType: api.ReviewRequested}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newNotificationsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
						Results: []api.NotificationSchema{tc.n}, TotalRows: testutil.IntPtr(1),
					})
				})
			out, _, err := runNotificationsCmd(t, "--no-headers",
				"--columns", "job_id,job_type,quarantined_rows_count,row_id,field_name,change_proposal_id,alert_item_id,warning_item_id,stored_item_id")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Fields(out); !slices.Equal(got, tc.want) {
				t.Errorf("context columns = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestGetNotificationsNewTypesAndPayloads(t *testing.T) {
	for _, tp := range []api.NotificationType{
		api.CorrectionsNotCarried, api.QuarantinedRows, api.ExternalIssueComment, api.EmailUndeliverable, api.FileNeedsDecision,
		api.ChangeProposalSubmitted, api.ChangeProposalDecided, api.ChangeProposalExecutionFailed,
		api.IssuesReportSent, api.PiiAccessRequested, api.PiiAccessRequestDecided,
	} {
		t.Run(string(tp), func(t *testing.T) {
			mock := newNotificationsTestEnv(t)
			n := sampleInboxNotification()
			n.NotificationType = tp
			n.JobID = testutil.IntPtr(123)
			jobType := api.IngestionJob
			n.JobType = &jobType
			n.QuarantinedRowsCount = testutil.IntPtr(0)
			n.RowID = testutil.IntPtr(456)
			n.FieldName = testutil.StringPtr("email")
			subjectUserID := uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
			n.SubjectUserID = &subjectUserID
			n.ChangeProposalID = &notifTestUUID
			n.AlertItemID = testutil.IntPtr(321)
			n.WarningItemID = testutil.IntPtr(654)
			mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
				func(w http.ResponseWriter, r *http.Request) {
					if got := r.URL.Query()["notification_type"]; !slices.Equal(got, []string{string(tp)}) {
						t.Errorf("notification_type = %v, want [%s]", got, tp)
					}
					testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
						Results: []api.NotificationSchema{n}, TotalRows: testutil.IntPtr(1),
					})
				})

			jsonOut, jsonErr, err := runNotificationsCmd(t, "--type", string(tp), "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			var got api.NotificationListSchema
			if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Results) != 1 {
				t.Fatalf("got %d notifications, want 1", len(got.Results))
			}
			row := got.Results[0]
			if row.NotificationType != tp || row.JobID == nil || *row.JobID != 123 ||
				row.JobType == nil || *row.JobType != jobType ||
				row.QuarantinedRowsCount == nil || *row.QuarantinedRowsCount != 0 ||
				row.RowID == nil || *row.RowID != 456 || row.FieldName == nil || *row.FieldName != "email" ||
				row.SubjectUserID == nil || *row.SubjectUserID != subjectUserID ||
				row.ChangeProposalID == nil || *row.ChangeProposalID != notifTestUUID ||
				row.AlertItemID == nil || *row.AlertItemID != 321 || row.WarningItemID == nil || *row.WarningItemID != 654 {
				t.Errorf("notification context missing from JSON:\n%s", jsonOut)
			}
			yamlOut, yamlErr, err := runNotificationsCmd(t, "--type", string(tp), "-o", "yaml")
			if err != nil {
				t.Fatal(err)
			}
			testutil.AssertYAMLReadsAsJSON(t, yamlOut, jsonOut)
			if strings.Contains(jsonErr+yamlErr, "qctl describe change-proposal") {
				t.Error("structured output printed a proposal hint")
			}
		})
	}
}

func TestGetNotificationsProposalHints(t *testing.T) {
	otherID := uuid.MustParse("0190dddd-1111-2222-3333-444444444444")
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{
					{ID: notifTestUUID, NotificationType: api.ChangeProposalSubmitted, ChangeProposalID: &notifTestUUID},
					{ID: notifTestUUID, NotificationType: api.ChangeProposalDecided, ChangeProposalID: &notifTestUUID},
					{ID: otherID, NotificationType: api.ChangeProposalExecutionFailed, ChangeProposalID: &otherID},
					{ID: notifTestUUID, NotificationType: api.ChangeProposalSubmitted},
					sampleInboxNotification(),
				}, TotalRows: testutil.IntPtr(5),
			})
		})
	for _, format := range []string{"", "table", "name"} {
		t.Run("format="+format, func(t *testing.T) {
			var args []string
			if format != "" {
				args = []string{"-o", format}
			}
			out, errOut, err := runNotificationsCmd(t, args...)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(out, "qctl describe") {
				t.Errorf("hint leaked into stdout: %s", out)
			}
			wantCount := 2
			if format == "name" {
				wantCount = 0
			}
			if got := strings.Count(errOut, "qctl describe change-proposal"); got != wantCount {
				t.Fatalf("got %d hints, want %d: %s", got, wantCount, errOut)
			}
			if wantCount > 0 {
				for _, id := range []uuid.UUID{notifTestUUID, otherID} {
					if !strings.Contains(errOut, "qctl describe change-proposal "+id.String()) {
						t.Errorf("missing hint for %s: %s", id, errOut)
					}
				}
			}
		})
	}
}

func TestGetNotificationsEscalationThreadAndSupportColumns(t *testing.T) {
	threadID := uuid.MustParse("0190eeee-1111-2222-3333-444444444444")
	commentID := uuid.MustParse("0190ffff-1111-2222-3333-444444444444")
	step := "fix-mapping"
	seriesID := uuid.MustParse("0190abab-1111-2222-3333-444444444444")
	refusal := api.BrokerReportRefusalNothingNew
	const columns = "trigger,period_key,cloud_source_id,series_id,thread_id,comment_id,support_step,broker_report_refusal"
	for _, tc := range []struct {
		name string
		n    api.NotificationSchema
		want []string
	}{
		{"series escalation", api.NotificationSchema{NotificationType: api.SeriesEscalation,
			SeriesEscalation: &api.SeriesEscalationTinySchema{DataSourceModelID: 42, PeriodKey: "2026-09", SeriesID: &seriesID, Trigger: api.StillFailing}},
			[]string{"still_failing", "2026-09", "42", seriesID.String()}},
		{"series escalation without a series", api.NotificationSchema{NotificationType: api.SeriesEscalation,
			SeriesEscalation: &api.SeriesEscalationTinySchema{DataSourceModelID: 42, PeriodKey: "2026-09", Trigger: api.NotLoaded}},
			[]string{"not_loaded", "2026-09", "42"}},
		{"comment reply", api.NotificationSchema{NotificationType: api.CommentReply, ThreadID: &threadID, CommentID: &commentID},
			[]string{threadID.String(), commentID.String()}},
		{"support request", api.NotificationSchema{NotificationType: api.SupportRequest, SupportStep: &step},
			[]string{step}},
		{"refused issues report", api.NotificationSchema{NotificationType: api.FileDownloadFailed, BrokerReportRefusal: &refusal},
			[]string{"nothing_new"}},
		{"failed issues report", api.NotificationSchema{NotificationType: api.FileDownloadFailed}, nil},
		{"absent context", api.NotificationSchema{NotificationType: api.ProblemOnOurSide}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newNotificationsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
						Results: []api.NotificationSchema{tc.n}, TotalRows: testutil.IntPtr(1),
					})
				})
			out, _, err := runNotificationsCmd(t, "--no-headers", "--columns", columns)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Fields(out); !slices.Equal(got, tc.want) {
				t.Errorf("columns = %v, want %v", got, tc.want)
			}
		})
	}
}

// The new columns are reachable through --columns only; the default set is
// unchanged.
func TestGetNotificationsDefaultColumnsOmitTheContextColumns(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			n := sampleInboxNotification()
			n.NotificationType = api.SeriesEscalation
			n.SeriesEscalation = &api.SeriesEscalationTinySchema{DataSourceModelID: 42, PeriodKey: "2026-09", Trigger: api.NotLoaded}
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{n}, TotalRows: testutil.IntPtr(1),
			})
		})
	out, _, err := runNotificationsCmd(t)
	if err != nil {
		t.Fatal(err)
	}
	var header []string
	for _, line := range strings.Split(out, "\n") {
		if header = strings.Fields(line); len(header) > 0 {
			break
		}
	}
	if want := []string{"ID", "TYPE", "MSG", "ACTOR", "TABLE", "READ", "AGE"}; !slices.Equal(header, want) {
		t.Errorf("default header = %v, want %v", header, want)
	}
}

func TestGetNotificationsNextStepHints(t *testing.T) {
	threadID := uuid.MustParse("0190eeee-1111-2222-3333-444444444444")
	ingestion, training := api.IngestionJob, api.TrainingJob
	seriesID := uuid.MustParse("0190abab-1111-2222-3333-444444444444")
	escalation := func(sourceID int, series *uuid.UUID, period string, datasetID *int) api.NotificationSchema {
		return api.NotificationSchema{ID: notifTestUUID, NotificationType: api.SeriesEscalation, DatasetID: datasetID,
			SeriesEscalation: &api.SeriesEscalationTinySchema{DataSourceModelID: sourceID, SeriesID: series, PeriodKey: period, Trigger: api.FailingShare}}
	}
	for _, tc := range []struct {
		name string
		rows []api.NotificationSchema
		want []string
	}{
		{"series escalation", []api.NotificationSchema{escalation(42, &seriesID, "2026-09", testutil.IntPtr(7))}, []string{
			"Inspect submission statuses: qctl get submission-statuses --series " + seriesID.String() + " --period 2026-09",
			"Inspect table: qctl describe table 7",
		}},
		{"series escalation without a period", []api.NotificationSchema{escalation(42, &seriesID, "", nil)}, []string{
			"Inspect submission statuses: qctl get submission-statuses --series " + seriesID.String(),
		}},
		{"series escalation without a series or a table", []api.NotificationSchema{escalation(42, nil, "2026-09", nil)}, []string{
			"Inspect submission statuses: qctl get submission-statuses --cloud-source-id 42",
		}},
		{"series escalation naming nothing", []api.NotificationSchema{
			{NotificationType: api.SeriesEscalation, DatasetID: testutil.IntPtr(7)},
		}, []string{"Inspect table: qctl describe table 7"}},
		{"series escalations dedupe per target", []api.NotificationSchema{
			escalation(42, &seriesID, "2026-09", testutil.IntPtr(7)), escalation(43, &seriesID, "2026-09", testutil.IntPtr(8)),
			escalation(42, &seriesID, "2026-10", testutil.IntPtr(7)),
		}, []string{
			"Inspect submission statuses: qctl get submission-statuses --series " + seriesID.String() + " --period 2026-09",
			"Inspect table: qctl describe table 7",
			"Inspect table: qctl describe table 8",
			"Inspect submission statuses: qctl get submission-statuses --series " + seriesID.String() + " --period 2026-10",
		}},
		{"series-less escalations dedupe per cloud source", []api.NotificationSchema{
			escalation(42, nil, "2026-09", testutil.IntPtr(7)), escalation(42, nil, "2026-10", testutil.IntPtr(8)),
			escalation(43, nil, "2026-09", testutil.IntPtr(7)),
		}, []string{
			"Inspect submission statuses: qctl get submission-statuses --cloud-source-id 42",
			"Inspect table: qctl describe table 7",
			"Inspect table: qctl describe table 8",
			"Inspect submission statuses: qctl get submission-statuses --cloud-source-id 43",
		}},
		{"problem on our side", []api.NotificationSchema{
			{NotificationType: api.ProblemOnOurSide, JobID: testutil.IntPtr(123), JobType: &ingestion, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.ProblemOnOurSide, JobID: testutil.IntPtr(123), JobType: &ingestion},
		}, []string{"Inspect ingestion job: qctl describe ingestion-job 123"}},
		{"problem on our side naming another job type", []api.NotificationSchema{
			{NotificationType: api.ProblemOnOurSide, JobID: testutil.IntPtr(123), JobType: &training},
		}, nil},
		{"comment reply", []api.NotificationSchema{
			{NotificationType: api.CommentReply, ThreadID: &threadID, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.CommentReply, ThreadID: &threadID, DatasetID: testutil.IntPtr(7)},
		}, []string{"Inspect comment thread: qctl describe comment-thread " + threadID.String() + " --table-id 7"}},
		{"comment reply without a table", []api.NotificationSchema{
			{NotificationType: api.CommentReply, ThreadID: &threadID},
		}, nil},
		{"duplicate group rows changed and response", []api.NotificationSchema{
			{NotificationType: api.DuplicateGroupRowsChanged, DuplicateGroupID: &threadID, DatasetID: testutil.IntPtr(7),
				JobID: testutil.IntPtr(123), JobType: &ingestion},
			{NotificationType: api.DuplicateGroupResponse, DuplicateGroupID: &threadID, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.DuplicateGroupResponse, DuplicateGroupID: &threadID},
		}, []string{"Inspect duplicate group: qctl describe duplicate-group " + threadID.String() + " --table-id 7"}},
		{"duplicate thread shared", []api.NotificationSchema{
			{NotificationType: api.DuplicateThreadShared, ThreadID: &threadID, DatasetID: testutil.IntPtr(7)},
		}, []string{"Inspect comment thread: qctl describe comment-thread " + threadID.String() + " --table-id 7"}},
		// The recipients are external, whom the reviewer routes refuse, so
		// the hint is the sender view; the row names no group.
		{"duplicate group decision changed", []api.NotificationSchema{
			{NotificationType: api.DuplicateGroupDecisionChanged, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.DuplicateGroupDecisionChanged, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.DuplicateGroupDecisionChanged},
		}, []string{"Inspect duplicate groups: qctl get duplicate-groups --sender --table-id 7"}},
		{"support request", []api.NotificationSchema{
			{NotificationType: api.SupportRequest, AlertItemID: testutil.IntPtr(321), StoredItemID: testutil.IntPtr(456)},
			{NotificationType: api.SupportRequest, AlertItemID: testutil.IntPtr(321)},
		}, []string{
			"Inspect alert: qctl describe alert 321",
			"Inspect file: qctl describe file 456",
		}},
		{"file needs decision", []api.NotificationSchema{
			{NotificationType: api.FileNeedsDecision, AlertItemID: testutil.IntPtr(321), StoredItemID: testutil.IntPtr(456)},
		}, []string{
			"Inspect alert: qctl describe alert 321",
			"Inspect file: qctl describe file 456",
		}},
		// Context fields alone do not make a hint: the type decides it.
		{"other types carrying the same fields", []api.NotificationSchema{
			{NotificationType: api.CorrectionsNotCarried, JobID: testutil.IntPtr(123), JobType: &ingestion},
			{NotificationType: api.CommentMentioned, ThreadID: &threadID, DatasetID: testutil.IntPtr(7)},
			{NotificationType: api.IssuesReportSent, StoredItemID: testutil.IntPtr(456), AlertItemID: testutil.IntPtr(321)},
		}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newNotificationsTestEnv(t)
			mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
				func(w http.ResponseWriter, r *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
						Results: tc.rows, TotalRows: testutil.IntPtr(len(tc.rows)),
					})
				})
			for _, format := range []string{"", "table", "json", "yaml", "name"} {
				var args []string
				if format != "" {
					args = []string{"-o", format}
				}
				out, errOut, err := runNotificationsCmd(t, args...)
				if err != nil {
					t.Fatalf("-o %q: %v", format, err)
				}
				if strings.Contains(out, "Inspect ") {
					t.Errorf("-o %q: hint leaked into stdout: %s", format, out)
				}
				var got []string
				for _, line := range strings.Split(errOut, "\n") {
					if strings.HasPrefix(line, "Inspect ") {
						got = append(got, line)
					}
				}
				want := tc.want
				if format != "" && format != "table" {
					want = nil
				}
				if !slices.Equal(got, want) {
					t.Errorf("-o %q: hints = %q, want %q", format, got, want)
				}
			}
		})
	}
}

// Read and dismissed reach the wire as their own parameters, so a listing
// cannot answer one question while the user asked another.
func TestGetNotificationsSendsBothAxes(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{Results: nil, TotalRows: testutil.IntPtr(0)})
		})

	if _, _, err := runNotificationsCmd(t,
		"--unread", "--include-dismissed", "--type", "review_requested", "--type", "comment_mentioned",
	); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if query.Get("unread_only") != "true" || query.Get("include_dismissed") != "true" {
		t.Errorf("axes not sent independently: %v", query)
	}
	if got := query["notification_type"]; len(got) != 2 {
		t.Errorf("notification_type = %v, want two values", got)
	}
}

// A value the backend removed must be refused before the request, at exit 2.
func TestGetNotificationsRejectsAnUnknownType(t *testing.T) {
	newNotificationsTestEnv(t) // no handlers: any escaping request 404s and would show as exit 3
	_, _, err := runNotificationsCmd(t, "--type", "review_mentioned")
	if err == nil {
		t.Fatal("an unknown --type was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}

func TestGetNotificationsEmptyNamesTheFilters(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{}, Page: testutil.IntPtr(1), TotalRows: testutil.IntPtr(0),
			})
		})

	_, errOut, err := runNotificationsCmd(t, "--unread")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut, "No notifications matched --unread") {
		t.Errorf("empty result did not name the filter\n%s", errOut)
	}
}

func TestGetNotificationsNextPageHint(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results:   []api.NotificationSchema{sampleInboxNotification()},
				Page:      testutil.IntPtr(1),
				TotalRows: testutil.IntPtr(40),
				Next:      &api.NotificationPageCursorSchema{Page: 2},
			})
		})

	_, errOut, err := runNotificationsCmd(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(errOut, "--page 2") {
		t.Errorf("no next-page hint\n%s", errOut)
	}
}

// -o name is the pipe-into-`notification read` path, so it must print ids.
func TestGetNotificationsNamePrintsIDs(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{sampleInboxNotification()}, TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t, "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "notification/"+notifTestUUID.String() {
		t.Errorf("-o name printed %q, want notification/<id>", strings.TrimSpace(out))
	}
}

// -o json is the only view carrying review_id/thread_id/read_at, so it must be
// the raw response and not the table projection.
func TestGetNotificationsJSONIsTheRawResponse(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			n := sampleInboxNotification()
			n.ReviewID = &notifTestUUID
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results: []api.NotificationSchema{n}, TotalRows: testutil.IntPtr(1),
			})
		})

	out, _, err := runNotificationsCmd(t, "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{`"review_id"`, `"read_at"`, `"dismissed_at"`, `"results"`} {
		if !strings.Contains(out, want) {
			t.Errorf("-o json missing %s\n%s", want, out)
		}
	}
}

func TestGetNotificationsCountTable(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications/unread-count",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.UnreadCountSchema{NeedsYou: 3, TotalUnread: 11})
		})

	out, _, err := runNotificationsCmd(t, "--count")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"NEEDS-YOU", "TOTAL-UNREAD", "3", "11"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
}

// The badge is TWO numbers and the JSON view must keep the wire's object
// shape, so a script can read either without unwrapping a list.
func TestGetNotificationsCountJSONIsAnObject(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications/unread-count",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.UnreadCountSchema{NeedsYou: 3, TotalUnread: 11})
		})

	out, _, err := runNotificationsCmd(t, "--count", "-o", "json")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Errorf("-o json is not an object:\n%s", out)
	}
	for _, want := range []string{`"needs_you": 3`, `"total_unread": 11`} {
		if !strings.Contains(out, want) {
			t.Errorf("-o json missing %s\n%s", want, out)
		}
	}
}

// The unread-count endpoint takes no filter parameters at all, so accepting a
// filter alongside --count would answer a different question at exit 0 -- the
// same rule `get rule-reviews` applies to its mode-specific flags.
func TestGetNotificationsCountRejectsFilters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unread", []string{"--count", "--unread"}},
		{"include-dismissed", []string{"--count", "--include-dismissed"}},
		{"type", []string{"--count", "--type", "rule_upgrade"}},
		{"page", []string{"--count", "--page", "2"}},
		{"limit", []string{"--count", "--limit", "50"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newNotificationsTestEnv(t) // no handlers: an escaping request would 404 (exit 3)
			_, _, err := runNotificationsCmd(t, tc.args...)
			if err == nil {
				t.Fatalf("--count --%s was accepted", tc.name)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// --count answers with two numbers; there is no id to print, and an empty line
// at exit 0 would read as "nothing unread".
func TestGetNotificationsCountRejectsNameFormat(t *testing.T) {
	newNotificationsTestEnv(t)
	_, _, err := runNotificationsCmd(t, "--count", "-o", "name")
	if err == nil {
		t.Fatal("--count -o name was accepted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}

// The listing and the --count mode wrap their errors at two different sites,
// and neither had an exit-code assertion: the suite asserted only err != nil,
// which a %v satisfies while silently flooring every code at 1.
func TestGetNotificationsSurfacesA403AsExitFour(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "forbidden"})
		})

	_, _, err := runNotificationsCmd(t)
	if err == nil {
		t.Fatal("a 403 on the listing was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code %d, want %d (ExitUnauthorized) -- the listing wrap severed the Unwrap chain", got, qerrors.ExitUnauthorized)
	}
}

func TestGetNotificationsCountSurfacesA403AsExitFour(t *testing.T) {
	mock := newNotificationsTestEnv(t)
	mock.RegisterHandler("GET", "/api/orgs/"+notificationsTestOrgID+"/notifications/unread-count",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "forbidden"})
		})

	_, _, err := runNotificationsCmd(t, "--count")
	if err == nil {
		t.Fatal("a 403 on --count was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitUnauthorized {
		t.Errorf("exit code %d, want %d (ExitUnauthorized) -- the --count wrap severed the Unwrap chain", got, qerrors.ExitUnauthorized)
	}
}
