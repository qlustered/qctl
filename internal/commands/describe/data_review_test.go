package describe

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const drDescribeOrgID = "c3d4e5f6-a7b8-9012-cdef-345678901234"

var (
	drReviewID    = uuid.MustParse("0190cccc-0000-4000-8000-000000000001")
	drGroupID     = uuid.MustParse("0190cccc-0000-4000-8000-000000000002")
	drThreadID    = uuid.MustParse("0190cccc-0000-4000-8000-000000000003")
	drCommentID   = uuid.MustParse("0190cccc-0000-4000-8000-000000000004")
	drReplyID     = uuid.MustParse("0190cccc-0000-4000-8000-000000000005")
	drRequesterID = uuid.MustParse("0190dddd-0000-4000-8000-000000000001")
	drAliceID     = uuid.MustParse("0190dddd-0000-4000-8000-000000000002")
	drBobID       = uuid.MustParse("0190dddd-0000-4000-8000-000000000003")
	drVetterID    = uuid.MustParse("0190dddd-0000-4000-8000-000000000004")
)

const drTableID = 42

func drPtr[T any](v T) *T { return &v }

func drUser(id uuid.UUID, first, last string) api.UserInfoTinyDictSchema {
	return api.UserInfoTinyDictSchema{ID: id, FirstName: first, LastName: last,
		Email: strings.ToLower(first) + "@corp.com"}
}

func drOrgPath(suffix string) string { return "/api/orgs/" + drDescribeOrgID + suffix }

var (
	drReviewPath    = drOrgPath("/data-reviews/" + drReviewID.String())
	drDecisionsPath = drReviewPath + "/decisions"
	drByRowsPath    = drOrgPath(fmt.Sprintf("/datasets/%d/data-reviews/by-rows", drTableID))
	drMePath        = drOrgPath("/users/me")
	drUsersPath     = drOrgPath("/users")
	drThreadPath    = drOrgPath(fmt.Sprintf("/datasets/%d/comment-threads/%s", drTableID, drThreadID))
)

// drFixture is the acceptance fixture: two changes (one decided with a comment
// and a suggestion, one undecided), three decisions across two reviewers (one
// re-decided), one unassigned vet, and a thread with a reply. Bob is the caller.
type drFixture struct {
	review        api.DataReviewResponseSchema
	decisions     []api.DataReviewDecisionResponseSchema
	thread        api.CommentThreadResponseSchema
	overlays      []api.RowReviewOverlaySchema
	overlayStatus int

	reviewStatus int
	meStatus     int
	usersStatus  int
	threadStatus int

	// The query the detail GET was sent with.
	include, changesSince string
}

func newDRFixture() *drFixture {
	now := time.Now()
	alice, bob := drUser(drAliceID, "Alice", "Chen"), drUser(drBobID, "Bob", "Ng")
	requester := drUser(drRequesterID, "Rita", "Req")
	changes := []api.DataReviewChangeSchema{
		{
			FieldName: "amount", HistoryID: 811,
			OriginalValue: drPtr("10"), EditedValue: drPtr("100"),
			AffectedRowCount: drPtr(3),
			Decision:         drPtr(api.ReviewDecisionEnumRequestChanges),
			DecidedByID:      &drBobID, DecidedBy: &bob,
			DecidedAt:      drPtr(now.Add(-2 * time.Hour)),
			Comment:        drPtr("too large"),
			SuggestedValue: drPtr("50"),
		},
		{FieldName: "email", HistoryID: 812, EditedValue: drPtr("a@b.co"), AffectedRowCount: drPtr(1)},
	}
	reply := []api.CommentResponseSchema{{ID: drReplyID, Body: "agreed", AuthorID: drAliceID, Author: &alice,
		CreatedAt: now.Add(-time.Hour)}}
	thread := []api.CommentResponseSchema{{ID: drCommentID, Body: "why 100?", AuthorID: drBobID, Author: &bob,
		CreatedAt: now.Add(-3 * time.Hour), Replies: &reply}}
	return &drFixture{
		review: api.DataReviewResponseSchema{
			ID: drReviewID, RowID: 7, DatasetID: drTableID,
			Status: api.ReviewRequestStatusChangesRequested, RequiredApprovals: 2,
			Requester:          &requester,
			AssignedReviewers:  &[]api.UserInfoTinyDictSchema{alice, bob},
			ReleaseBlockReason: drPtr(api.CellVeto),
			Changes:            &changes,
			UnassignedVets: &[]api.UnassignedFieldVetSchema{
				{HistoryID: 812, DecidedByID: drVetterID, Decision: api.ReviewDecisionEnumCommentOnly},
			},
			ReviewGroups: &[]api.ReviewGroupRefSchema{
				{ID: drGroupID, Title: "March fixes", Description: drPtr("Fixes from the audit."), CreatedAt: now.Add(-48 * time.Hour)},
			},
			ThreadID: &drThreadID, CommentCount: drPtr(2),
			CreatedAt: now.Add(-48 * time.Hour), UpdatedAt: now,
		},
		decisions: []api.DataReviewDecisionResponseSchema{
			{ReviewerID: drAliceID, Decision: api.ReviewDecisionEnumReject, CreatedAt: now.Add(-5 * time.Hour), Comment: drPtr("first look")},
			{ReviewerID: drBobID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now.Add(-4 * time.Hour)},
			{ReviewerID: drAliceID, Decision: api.ReviewDecisionEnumApprove, CreatedAt: now.Add(-1 * time.Hour), Comment: drPtr("fine now")},
		},
		thread:        api.CommentThreadResponseSchema{ID: drThreadID, Comments: &thread},
		overlays:      []api.RowReviewOverlaySchema{{ID: drReviewID, RowID: 7, CurrentApprovals: drPtr(1), RequiredApprovals: drPtr(2)}},
		overlayStatus: http.StatusOK,
		reviewStatus:  http.StatusOK,
		meStatus:      http.StatusOK,
		usersStatus:   http.StatusOK,
		threadStatus:  http.StatusOK,
	}
}

// serve registers every route describe data-review may reach and returns the
// mock, whose Requests() is the request boundary under test.
func (f *drFixture) serve(t *testing.T) *testutil.MockAPIServer {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", drDescribeOrgID)
	env.SetupCredential(endpointKey, drDescribeOrgID, "test-token")

	respond := func(status int, body interface{}) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if status != http.StatusOK {
				testutil.RespondJSON(w, status, map[string]interface{}{"detail": map[string]string{"msg": "refused"}})
				return
			}
			testutil.RespondJSON(w, status, body)
		}
	}
	mock.RegisterHandler("GET", drReviewPath, func(w http.ResponseWriter, r *http.Request) {
		f.include, f.changesSince = r.URL.Query().Get("include"), r.URL.Query().Get("changes_since")
		respond(f.reviewStatus, f.review)(w, r)
	})
	mock.RegisterHandler("GET", drDecisionsPath, respond(http.StatusOK, f.decisions))
	mock.RegisterHandler("POST", drByRowsPath, func(w http.ResponseWriter, r *http.Request) {
		var body api.BulkRowReviewsRequestSchema
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode by-rows body: %v", err)
		}
		if !reflect.DeepEqual(body.RowIds, []int{f.review.RowID}) {
			t.Errorf("by-rows row_ids = %v, want [%d]", body.RowIds, f.review.RowID)
		}
		respond(f.overlayStatus, api.BulkRowReviewsResponseSchema{Reviews: f.overlays})(w, r)
	})
	mock.RegisterHandler("GET", drMePath, respond(f.meStatus, api.MyUserSchema{ID: drBobID, Email: "bob@corp.com"}))
	mock.RegisterHandler("GET", drUsersPath, respond(f.usersStatus, map[string]interface{}{
		"results": []api.UserListItemSchema{{ID: drVetterID, Email: "val@corp.com", FirstName: "Val", LastName: "Vetter"}},
		"next":    nil,
	}))
	mock.RegisterHandler("GET", drThreadPath, respond(f.threadStatus, f.thread))
	return mock
}

func setupDataReviewDescribeCommand() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow http://")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	describeCmd := &cobra.Command{Use: "describe"}
	describeCmd.AddCommand(NewDataReviewCommand())
	rootCmd.AddCommand(describeCmd)
	return rootCmd
}

// runDataReviewDescribe executes the command and returns stdout, stderr and
// the error.
func runDataReviewDescribe(args ...string) (string, string, error) {
	cmd := setupDataReviewDescribeCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(append([]string{"describe", "data-review"}, args...))
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

func assertRequests(t *testing.T, mock *testutil.MockAPIServer, want ...string) {
	t.Helper()
	got := mock.Requests()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	for i := range want {
		if !strings.Contains(want[i], " ") {
			want[i] = "GET " + want[i]
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

func assertOutputInOrder(t *testing.T, out string, wants ...string) {
	t.Helper()
	at := 0
	for _, want := range wants {
		i := strings.Index(out[at:], want)
		if i < 0 {
			t.Fatalf("missing %q after byte %d\n---\n%s", want, at, out)
		}
		at += i + len(want)
	}
}

func TestDescribeDataReviewFullPayload(t *testing.T) {
	f := newDRFixture()
	mock := f.serve(t)

	out, stderr, err := runDataReviewDescribe(drReviewID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stderr != "" {
		t.Errorf("no warning expected, got %q", stderr)
	}
	// Someone is unnamed (the vetter) and a thread exists: every request, in
	// the documented order.
	assertRequests(t, mock, drReviewPath, drDecisionsPath, "POST "+drByRowsPath, drMePath, drUsersPath, drThreadPath)
	if f.include != "changes" || f.changesSince != "me" {
		t.Errorf("detail GET query = include=%q changes_since=%q, want changes / me", f.include, f.changesSince)
	}

	id := drReviewID.String()
	assertOutputInOrder(t, out,
		"# DATA REVIEW "+id+"\n",
		"Status: changes_requested | Approvals required: 2 | Requester: Rita Req\n",
		"Held: 1 cell(s) need a fix (cell_veto)\n",
		"\n## SUBMISSIONS (1)\n",
		"[g: "+drGroupID.String()+`] "March fixes"`,
		"\n## CHANGES (2)\n",
		"- amount  [h: 811]\n",
		"  verdict: request_changes by Bob Ng (you) · 2 hours ago\n",
		`  comment: "too large"`,
		`  suggests: "50"`,
		"- email  [h: 812]\n",
		"  verdict: undecided\n",
		"\n## DECISIONS (2)\n",
		"- Alice Chen: approve · first decided 1 hour ago\n",
		"- Bob Ng (you): approve",
		"Approvals: 1 of 2 required (current eligible approvals)",
		"\n## ALSO WEIGHED IN (1)\n",
		"- Val Vetter: comment_only on [h: 812] (email)\n",
		"\n## COMMENTS (1 top-level, 2 total)\n",
		"[c: "+drCommentID.String()+"]",
		"[c: "+drReplyID.String()+"]",
		"\n## NEXT ACTIONS\n",
		"qctl data-review approve "+id+"\n",
		"qctl data-review comment "+id+" ",
		"qctl data-review withdraw "+id+"\n",
		"qctl comment-thread reply "+drThreadID.String()+" --table-id 42 ",
	)
	if strings.Contains(out, "first look") {
		t.Errorf("a superseded decision leaked into the payload\n%s", out)
	}
}

func TestDescribeDataReviewRequestBudget(t *testing.T) {
	t.Run("no thread and everyone embedded skips the directory and the thread", func(t *testing.T) {
		f := newDRFixture()
		f.review.ThreadID, f.review.UnassignedVets = nil, nil
		mock := f.serve(t)

		out, _, err := runDataReviewDescribe(drReviewID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRequests(t, mock, drReviewPath, drDecisionsPath, "POST "+drByRowsPath, drMePath)
		if !strings.Contains(out, "## COMMENTS (0 top-level, 0 total)\n(none yet)\n") {
			t.Errorf("a review without a thread says so\n%s", out)
		}
	})

	t.Run("no decisions does not ask who the caller is", func(t *testing.T) {
		f := newDRFixture()
		f.review.ThreadID, f.review.UnassignedVets = nil, nil
		f.review.Changes = &[]api.DataReviewChangeSchema{}
		f.decisions = []api.DataReviewDecisionResponseSchema{}
		mock := f.serve(t)

		out, _, err := runDataReviewDescribe(drReviewID.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		assertRequests(t, mock, drReviewPath, drDecisionsPath, "POST "+drByRowsPath)
		// 200 with changes: [] is also what a caller without table data
		// access gets, so the empty section explains both readings.
		assertOutputInOrder(t, out,
			"## CHANGES (0)\n(no changes visible — the row has no live edits, or you lack access to this table's data)\n",
			"## DECISIONS (0)\n")
	})
}

func TestDescribeDataReviewApprovalMeter(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*drFixture)
		want   string
		warn   bool
	}{
		{
			name:   "open review",
			mutate: func(f *drFixture) { f.review.Status = api.ReviewRequestStatusOpen },
			want:   "Approvals: 1 of 2 required (current eligible approvals)\n",
		},
		{
			name: "historical editor approval stays visible but does not count",
			mutate: func(f *drFixture) {
				// Alice's historical edit is absent from the live changes. Only
				// the server knows to exclude her recorded whole-row approval.
				f.review.Changes = &[]api.DataReviewChangeSchema{}
			},
			want: "Approvals: 1 of 2 required (current eligible approvals)\n",
		},
		{
			name: "all recorded approvals excluded or stale",
			mutate: func(f *drFixture) {
				f.overlays[0].CurrentApprovals = drPtr(0)
			},
			want: "Approvals: 0 of 2 required (current eligible approvals)\n",
		},
		{
			name: "both counts come from the same overlay",
			mutate: func(f *drFixture) {
				f.overlays[0].RequiredApprovals = drPtr(3)
			},
			want: "Approvals: 1 of 3 required (current eligible approvals)\n",
		},
		{
			name:   "review no longer active",
			mutate: func(f *drFixture) { f.overlays = nil },
			want:   "Approvals: unavailable (2 required)\n",
		},
		{
			name:   "row now has a different review",
			mutate: func(f *drFixture) { f.overlays[0].ID = drGroupID },
			want:   "Approvals: unavailable (2 required)\n",
		},
		{
			name:   "overlay belongs to another row",
			mutate: func(f *drFixture) { f.overlays[0].RowID++ },
			want:   "Approvals: unavailable (2 required)\n",
		},
		{
			name:   "missing current count",
			mutate: func(f *drFixture) { f.overlays[0].CurrentApprovals = nil },
			want:   "Approvals: unavailable (2 required)\n",
		},
		{
			name:   "missing required count",
			mutate: func(f *drFixture) { f.overlays[0].RequiredApprovals = nil },
			want:   "Approvals: unavailable (2 required)\n",
		},
		{
			name:   "no data access",
			mutate: func(f *drFixture) { f.overlayStatus = http.StatusForbidden },
			want:   "Approvals: unavailable (2 required)\n",
			warn:   true,
		},
		{
			name:   "overlay endpoint fails",
			mutate: func(f *drFixture) { f.overlayStatus = http.StatusInternalServerError },
			want:   "Approvals: unavailable (2 required)\n",
			warn:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newDRFixture()
			tt.mutate(f)
			f.serve(t)
			out, stderr, err := runDataReviewDescribe(drReviewID.String())
			if code := qerrors.GetExitCode(err); code != 0 {
				t.Fatalf("exit %d: %v", code, err)
			}
			assertOutputInOrder(t, out, "- Alice Chen: approve", "- Bob Ng (you): approve", tt.want, "## NEXT ACTIONS")
			if got := strings.Contains(stderr, "Warning: live approval count unavailable:"); got != tt.warn {
				t.Errorf("approval warning = %t, want %t; stderr: %s", got, tt.warn, stderr)
			}
		})
	}
}

func TestDescribeDataReviewTerminalReviewSkipsApprovalOverlay(t *testing.T) {
	for _, status := range []api.ReviewRequestStatus{api.ReviewRequestStatusApproved, api.ReviewRequestStatusClosed} {
		t.Run(string(status), func(t *testing.T) {
			f := newDRFixture()
			f.review.Status = status
			mock := f.serve(t)
			out, stderr, err := runDataReviewDescribe(drReviewID.String())
			if err != nil || stderr != "" {
				t.Fatalf("err=%v stderr=%s", err, stderr)
			}
			assertRequests(t, mock, drReviewPath, drDecisionsPath, drMePath, drUsersPath, drThreadPath)
			if !strings.Contains(out, "Approvals: unavailable (2 required)\n") {
				t.Errorf("terminal review must not infer a live count\n%s", out)
			}
		})
	}
}

func TestDescribeDataReviewDegradedSections(t *testing.T) {
	t.Run("thread forbidden", func(t *testing.T) {
		f := newDRFixture()
		f.threadStatus = http.StatusForbidden
		mock := f.serve(t)

		out, _, err := runDataReviewDescribe(drReviewID.String())
		if err != nil {
			t.Fatalf("a refused thread must not fail the command: %v (exit %d)", err, qerrors.GetExitCode(err))
		}
		assertRequests(t, mock, drReviewPath, drDecisionsPath, "POST "+drByRowsPath, drMePath, drUsersPath, drThreadPath)
		want := fmt.Sprintf("## COMMENTS\ncomments: 2 (thread %s not readable: access denied; "+
			"reading it needs comment read permission and access to this table's data)\n", drThreadID)
		assertOutputInOrder(t, out, want, "## NEXT ACTIONS")
	})

	t.Run("directory forbidden falls back to UUIDs", func(t *testing.T) {
		f := newDRFixture()
		f.usersStatus = http.StatusForbidden
		f.serve(t)

		out, stderr, err := runDataReviewDescribe(drReviewID.String())
		if err != nil {
			t.Fatalf("a refused directory must not fail the command: %v", err)
		}
		if strings.Count(stderr, "Warning:") != 1 || !strings.Contains(stderr, "user directory unavailable") {
			t.Errorf("want one directory warning on stderr, got %q", stderr)
		}
		// Embedded users keep their names; only the vetter degrades.
		for _, want := range []string{
			"- " + drVetterID.String() + ": comment_only on [h: 812]",
			"- Alice Chen: approve",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("missing %q\n%s", want, out)
			}
		}
		if strings.Contains(out, "Warning") {
			t.Errorf("the warning belongs on stderr, not in the payload\n%s", out)
		}
	})

	t.Run("caller unknown omits the lines about your decision", func(t *testing.T) {
		f := newDRFixture()
		f.meStatus = http.StatusInternalServerError
		f.serve(t)

		out, stderr, err := runDataReviewDescribe(drReviewID.String())
		if err != nil {
			t.Fatalf("an unknown caller must not fail the command: %v", err)
		}
		if strings.Count(stderr, "Warning:") != 1 || !strings.Contains(stderr, "could not identify you") {
			t.Errorf("want one identity warning on stderr, got %q", stderr)
		}
		if strings.Contains(out, "data-review withdraw") || strings.Contains(out, "(you)") {
			t.Errorf("without the caller's id nothing may be attributed to them\n%s", out)
		}
	})
}

// A percent column's change arrives as stored text on both sides: the cell
// shown as 12.50% held 0.125 and the 13 typed over it was recorded as 0.13. A
// cell whose source text never converted keeps that text as its original. The
// payload prints both verbatim, with nothing qctl adds about units.
func TestDescribeDataReviewPrintsAPercentChangeAsStoredText(t *testing.T) {
	f := newDRFixture()
	changes := []api.DataReviewChangeSchema{
		{FieldName: "rate", HistoryID: 813, OriginalValue: drPtr("0.125"), EditedValue: drPtr("0.13"), AffectedRowCount: drPtr(1)},
		{FieldName: "discount", HistoryID: 814, OriginalValue: drPtr("12.5%"), EditedValue: drPtr("0.13"), AffectedRowCount: drPtr(1)},
	}
	f.review.Changes = &changes
	f.review.UnassignedVets = nil
	f.serve(t)

	out, _, err := runDataReviewDescribe(drReviewID.String())
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	assertOutputInOrder(t, out,
		"\n## CHANGES (2)\n",
		"- rate  [h: 813]\n"+
			`  before: "0.125" → after: "0.13"`+"\n"+
			"  verdict: undecided\n",
		"- discount  [h: 814]\n"+
			`  before: "12.5%" → after: "0.13"`+"\n"+
			"  verdict: undecided\n",
		"\n## DECISIONS",
	)
	changesSection := out[strings.Index(out, "## CHANGES"):strings.Index(out, "## DECISIONS")]
	for _, invented := range []string{"12.50%", "13.00%", "stored", "display", "unit"} {
		if strings.Contains(changesSection, invented) {
			t.Errorf("CHANGES carries %q, which the wire did not send\n%s", invented, changesSection)
		}
	}
}

// The CHANGES section marks a side the server recorded as a number, which is
// what tells a quarantine cell's raw source text from a value in the column's
// own numeric form. Only `decimal` is marked: on a cell change the other side
// is tagged `str` whether it is source text or not, so `str` claims nothing.
func TestDescribeDataReviewMarksADecimalOperand(t *testing.T) {
	f := newDRFixture()
	decimalTag, strTag := api.HistoryValueTypesDecimal, api.HistoryValueTypesStr
	changes := []api.DataReviewChangeSchema{
		{FieldName: "rate", HistoryID: 813, OriginalValue: drPtr("0.125"), EditedValue: drPtr("0.13"),
			OldValueType: &decimalTag, ValueType: &decimalTag, AffectedRowCount: drPtr(1)},
		{FieldName: "discount", HistoryID: 814, OriginalValue: drPtr("150"), EditedValue: drPtr("0.13"),
			OldValueType: &strTag, ValueType: &decimalTag, AffectedRowCount: drPtr(1)},
	}
	f.review.Changes = &changes
	f.review.UnassignedVets = nil
	f.serve(t)

	out, _, err := runDataReviewDescribe(drReviewID.String())
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	assertOutputInOrder(t, out,
		"\n## CHANGES (2)\n",
		"([decimal] = the server recorded this side as a number; an unmarked side may still be one.)\n",
		"- rate  [h: 813]\n"+
			`  before: "0.125" [decimal] → after: "0.13" [decimal]`+"\n",
		"- discount  [h: 814]\n"+
			`  before: "150" → after: "0.13" [decimal]`+"\n",
	)
	// The values themselves are still the text the server recorded: nothing is
	// reformatted into the column's display units.
	changesSection := out[strings.Index(out, "## CHANGES"):strings.Index(out, "## DECISIONS")]
	for _, invented := range []string{"12.50%", "13.00%", "[str]"} {
		if strings.Contains(changesSection, invented) {
			t.Errorf("CHANGES carries %q, which the wire did not send\n%s", invented, changesSection)
		}
	}
}

// A viewer who sees a field masked gets its values as mask text (atlas
// capi/capi/controllers/pii_masking.py mask_data_review), and masked_fields
// names it. The payload names the masked fields once and prints each mask as
// sent, without a [decimal] mark: a mask is text, whatever the side's tag.
func TestDescribeDataReviewNamesMaskedFieldsAndPrintsTheMask(t *testing.T) {
	f := newDRFixture()
	decimalTag := api.HistoryValueTypesDecimal
	changes := []api.DataReviewChangeSchema{
		{FieldName: "salary", HistoryID: 816, OriginalValue: drPtr("•••••"), EditedValue: drPtr("•••••"),
			SuggestedValue: drPtr("•••••0000"),
			OldValueType:   &decimalTag, ValueType: &decimalTag, AffectedRowCount: drPtr(1)},
		{FieldName: "rate", HistoryID: 813, OriginalValue: drPtr("0.125"), EditedValue: drPtr("0.13"),
			OldValueType: &decimalTag, ValueType: &decimalTag, AffectedRowCount: drPtr(1)},
	}
	masked := []string{"salary"}
	f.review.Changes = &changes
	f.review.MaskedFields = &masked
	f.review.UnassignedVets = nil
	f.serve(t)

	out, _, err := runDataReviewDescribe(drReviewID.String())
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	assertOutputInOrder(t, out,
		"\n## CHANGES (2)\n",
		"([decimal] = the server recorded this side as a number; an unmarked side may still be one.)\n",
		"(Masked fields: salary. Their values are •••••, or ••••• and the last 4 characters, printed as sent and not as the column's type.)\n",
		"- salary  [h: 816]\n"+
			`  before: "•••••" → after: "•••••"`+"\n",
		`  suggests: "•••••0000"`+"\n",
		"- rate  [h: 813]\n"+
			`  before: "0.125" [decimal] → after: "0.13" [decimal]`+"\n",
	)
}

// With no tag on any side there is nothing to mark, so the legend stays out of
// the payload too.
func TestDescribeDataReviewOmitsTheLegendWithNoDecimalSide(t *testing.T) {
	f := newDRFixture()
	strTag := api.HistoryValueTypesStr
	changes := []api.DataReviewChangeSchema{
		{FieldName: "region", HistoryID: 815, OriginalValue: drPtr("West"), EditedValue: drPtr("East"),
			OldValueType: &strTag, ValueType: &strTag, AffectedRowCount: drPtr(1)},
	}
	f.review.Changes = &changes
	f.review.UnassignedVets = nil
	f.serve(t)

	out, _, err := runDataReviewDescribe(drReviewID.String())
	if err != nil {
		t.Fatalf("exit %d: %v", qerrors.GetExitCode(err), err)
	}
	if strings.Contains(out, "[decimal]") {
		t.Errorf("CHANGES carries the legend with nothing marked\n%s", out)
	}
}

func TestDescribeDataReviewExitCodes(t *testing.T) {
	t.Run("missing review exits 3 after one request", func(t *testing.T) {
		f := newDRFixture()
		f.reviewStatus = http.StatusNotFound
		mock := f.serve(t)

		_, _, err := runDataReviewDescribe(drReviewID.String())
		if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
			t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitNotFound)
		}
		assertRequests(t, mock, drReviewPath)
	})

	t.Run("malformed id exits 2 without a request", func(t *testing.T) {
		mock := newDRFixture().serve(t)

		_, _, err := runDataReviewDescribe("0190cccc")
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Fatalf("exit = %d (%v), want %d", code, err, qerrors.ExitBadUsage)
		}
		assertRequests(t, mock)
	})
}

func TestDescribeDataReviewRawJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			f := newDRFixture()
			// amount's original is source text that never converted; the
			// edit over it is a stored number.
			(*f.review.Changes)[0].OldValueType = drPtr(api.HistoryValueTypesStr)
			(*f.review.Changes)[0].ValueType = drPtr(api.HistoryValueTypesDecimal)
			mock := f.serve(t)

			out, _, err := runDataReviewDescribe(drReviewID.String(), "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// The raw review is the whole answer: no decisions, no caller,
			// no directory, no thread.
			assertRequests(t, mock, drReviewPath)
			for _, want := range []string{"release_block_reason", "unassigned_vets", "required_approvals", "history_id"} {
				if !strings.Contains(out, want) {
					t.Errorf("raw output missing wire key %q\n%s", want, out)
				}
			}
			if strings.Contains(out, "## DECISIONS") {
				t.Errorf("-o %s must be the raw response, not the composed payload\n%s", format, out)
			}

			var doc struct {
				Changes []map[string]any `json:"changes" yaml:"changes"`
			}
			if format == "json" {
				err = json.Unmarshal([]byte(out), &doc)
			} else {
				err = yaml.Unmarshal([]byte(out), &doc)
			}
			if err != nil {
				t.Fatalf("output is not %s: %v\n%s", format, err, out)
			}
			if len(doc.Changes) == 0 {
				t.Fatalf("-o %s carries no changes\n%s", format, out)
			}
			for key, want := range map[string]string{"old_value_type": "str", "value_type": "decimal"} {
				if got := doc.Changes[0][key]; got != want {
					t.Errorf("-o %s changes[0].%s = %v, want %q", format, key, got, want)
				}
			}
		})
	}
}

func TestDescribeDataReviewRawKeepsChangesSinceNumbersExact(t *testing.T) {
	wants := map[string][]string{
		"json": {`"old_value": 12345678901234567`, `"new_value": 1234567`},
		"yaml": {"old_value: 12345678901234567\n", "new_value: 1234567\n"},
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			f := newDRFixture()
			f.review.ChangesSince = &map[string][]api.ChangesSinceFieldSchema{
				"amount": {{
					Datetime: drPtr("2026-09-01T10:00:00+00:00"),
					Why:      drPtr("User edit"),
					AdditionalProperties: map[string]interface{}{
						"old_value": int64(12345678901234567),
						"new_value": int64(1234567),
					},
				}},
			}
			f.serve(t)

			out, _, err := runDataReviewDescribe(drReviewID.String(), "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range wants[format] {
				if !strings.Contains(out, want) {
					t.Errorf("-o %s missing %q\n%s", format, want, out)
				}
			}
			for _, bad := range []string{"12345678901234568", "1.234567e+06"} {
				if strings.Contains(out, bad) {
					t.Errorf("-o %s carries the float64 spelling %q\n%s", format, bad, out)
				}
			}
		})
	}
}
