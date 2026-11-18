package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func ptr[T any](v T) *T { return &v }

func recordID() *openapi_types.UUID {
	id := uuid.MustParse("0190aaaa-1111-2222-3333-444444444444")
	return &id
}

func TestReportSubmitGroupsRowsByAggregate(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817, 8901},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, ID: recordID(), IssueItemID: ptr(8817)},
			{RowID: 1043, Status: api.Created, FieldNames: []string{"amount"}, ID: recordID(), IssueItemID: ptr(8817)},
			{RowID: 1044, Status: api.Created, FieldNames: []string{"amount"}, ID: recordID(), IssueItemID: ptr(8901)},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("ReportSubmit: %v", err)
	}
	got := out.String()
	for _, want := range []string{"alert/8817 flagged rows 1042, 1043", "alert/8901 flagged row 1044"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr should be empty on a clean submit, got:\n%s", errOut.String())
	}
}

// already_existed is a success — the row IS flagged — but it means nothing
// changed, and a caller re-running a script deserves to be told.
func TestReportSubmitNamesAlreadyExisted(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			{RowID: 1043, Status: api.AlreadyExisted, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("ReportSubmit: %v", err)
	}
	if !strings.Contains(out.String(), "row 1043 already carried this flag") {
		t.Errorf("stdout did not name the already_existed row:\n%s", out.String())
	}
}

// Re-running a script is the likeliest real shape: every row comes back
// already_existed. That is still a success (exit 0), and the note has to NAME
// the rows — the headline lists all of them, so a bare count would leave the
// reader unable to tell which ones were no-ops.
func TestReportSubmitAllAlreadyExisted(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1043, Status: api.AlreadyExisted, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			{RowID: 1042, Status: api.AlreadyExisted, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("a re-run that changed nothing is still a success, got: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "alert/8817 flagged rows 1042, 1043") {
		t.Errorf("stdout missing the headline:\n%s", got)
	}
	if !strings.Contains(got, "rows 1042, 1043 already carried this flag") {
		t.Errorf("the note must name the rows, not just count them:\n%s", got)
	}
	if errOut.Len() != 0 {
		t.Errorf("nothing failed, so stderr must be empty, got:\n%s", errOut.String())
	}
}

// issue_item_id is optional on the wire and the route sets
// response_model_exclude_unset=True, so a flagged row without one must still be
// reported rather than silently dropped from the output.
func TestReportSubmitKeepsFlaggedRowsWithNoAggregate(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{Results: []Result{
		{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}},
	}}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("ReportSubmit: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "alert flagged row 1042 (the server did not name the aggregate)") {
		t.Errorf("a flagged row with no issue_item_id was not reported:\n%s", got)
	}
}

// A partial failure is exit 0 with the failures on stderr — the same shape as
// comment-thread create's dropped targets.
func TestReportSubmitPartialFailureIsExitZeroOnStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			{RowID: 1050, Status: api.RowNotFound, FieldNames: []string{"amount"},
				Detail: ptr("Not a live quarantined row of this dataset, or its flagged values changed, or it is being reprocessed.")},
			{RowID: 1051, Status: api.FieldInvalid, FieldNames: []string{},
				Detail: ptr("Unknown field name: 'amonut'.")},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("a partial success must not be an error, got: %v", err)
	}
	if !strings.Contains(out.String(), "alert/8817 flagged row 1042") {
		t.Errorf("stdout missing the success line:\n%s", out.String())
	}
	stderr := errOut.String()
	for _, want := range []string{
		"2 of 3",
		"row 1050 (amount): row_not_found",
		"its flagged values changed",
		"row 1051: field_invalid",
		"Unknown field name: 'amonut'.",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// All rows failed and one reason is a bad field name: that is a typo, exit 2.
func TestReportSubmitAllFailedWithFieldInvalidIsExitTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{Results: []Result{
		{RowID: 1042, Status: api.FieldInvalid, FieldNames: []string{}, Detail: ptr("Unknown field name: 'amonut'.")},
		{RowID: 1043, Status: api.RowNotFound, FieldNames: []string{"amount"}},
	}}
	err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil)
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "Unknown field name") {
		t.Errorf("the error must carry the per-row detail, got: %v", err)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an all-fail submit writes nothing; the error carries it.\nstdout: %q\nstderr: %q",
			out.String(), errOut.String())
	}
}

// Every row came back row_not_found, so nothing was flagged: exit 3. The
// status does NOT mean "no such quarantined row" — one of the three causes it
// folds together is a live quarantined row with a reprocess queued — which is
// what the prober disambiguates; here it is nil, so the report is the bare one.
func TestReportSubmitAllRowsNotFoundIsExitThree(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{Results: []Result{
		{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"}},
		{RowID: 1043, Status: api.RowNotFound, FieldNames: []string{"amount"}},
	}}
	err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil)
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an all-fail submit writes nothing; the error carries it.\nstdout: %q\nstderr: %q",
			out.String(), errOut.String())
	}
}

func TestReportRetractReportsIssueResolved(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &RetractResult{
		IssueResolved: true,
		Results: []RetractRow{
			{RowID: 1042, Status: api.Retracted, ID: recordID()},
			{RowID: 1043, Status: api.Retracted, ID: recordID()},
		},
	}
	if err := ReportRetract(&out, &errOut, KindAlert, 8817, result); err != nil {
		t.Fatalf("ReportRetract: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "alert/8817 retracted from rows 1042, 1043") {
		t.Errorf("stdout missing the headline:\n%s", got)
	}
	if !strings.Contains(got, "auto-resolved") {
		t.Errorf("issue_resolved was not reported:\n%s", got)
	}
}

func TestReportRetractSaysWhenTheIssueStaysOpen(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &RetractResult{
		IssueResolved: false,
		Results:       []RetractRow{{RowID: 1042, Status: api.Retracted, ID: recordID()}},
	}
	if err := ReportRetract(&out, &errOut, KindWarning, 8817, result); err != nil {
		t.Fatalf("ReportRetract: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "warning/8817 retracted from row 1042") {
		t.Errorf("stdout missing the headline:\n%s", got)
	}
	if !strings.Contains(got, "still open") {
		t.Errorf("an unresolved aggregate must say so:\n%s", got)
	}
}

func TestReportRetractPartialFailureIsExitZeroOnStderr(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &RetractResult{Results: []RetractRow{
		{RowID: 1042, Status: api.Retracted, ID: recordID()},
		{RowID: 1050, Status: api.NotFound},
	}}
	if err := ReportRetract(&out, &errOut, KindAlert, 8817, result); err != nil {
		t.Fatalf("a partial success must not be an error, got: %v", err)
	}
	if !strings.Contains(errOut.String(), "row 1050: not_found") {
		t.Errorf("stderr missing the not_found row:\n%s", errOut.String())
	}
}

func TestReportRetractAllNotFoundIsExitThree(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &RetractResult{Results: []RetractRow{
		{RowID: 1042, Status: api.NotFound},
		{RowID: 1043, Status: api.NotFound},
	}}
	err := ReportRetract(&out, &errOut, KindAlert, 8817, result)
	if err == nil {
		t.Fatal("expected an error when nothing was retracted")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if out.Len() != 0 || errOut.Len() != 0 {
		t.Errorf("an all-fail retract writes nothing; the error carries it.\nstdout: %q\nstderr: %q",
			out.String(), errOut.String())
	}
}

// Every status the two response schemas can carry must reach the user. A
// renderer that prints "submitted" and exits 0 loses three of the four.
func TestEveryStatusIsRendered(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1, Status: api.Created, FieldNames: []string{"a"}, IssueItemID: ptr(8817)},
			{RowID: 2, Status: api.AlreadyExisted, FieldNames: []string{"a"}, IssueItemID: ptr(8817)},
			{RowID: 3, Status: api.RowNotFound, FieldNames: []string{"a"}},
			{RowID: 4, Status: api.FieldInvalid, FieldNames: []string{}},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("ReportSubmit: %v", err)
	}
	combined := out.String() + errOut.String()
	for _, want := range []string{"1, 2", "already carried", "row_not_found", "field_invalid"} {
		if !strings.Contains(combined, want) {
			t.Errorf("output does not render %q:\n%s", want, combined)
		}
	}
}

// The runtime half of the org-scope guarantee for the client-side
// not-founds: qctl builds these messages itself, so no HTTP status ever
// reaches apierror.buildHTTPError and only the explicit WithScopeNote call
// in report.go's submitAllFailedError can put the note on screen.
//
// internal/catalog's TestEveryClientSideNotFoundCarriesTheScopeNote pins every
// such constructor, but it reads the AST: it cannot see whether the note ever
// reaches a user. This test covers one representative runtime path,
// submitAllFailedError reached through ReportSubmit; the inventory test keeps
// the remaining constructors from silently losing their explicit note.
func TestReportSubmitAllRowsNotFoundCarriesTheScopeNote(t *testing.T) {
	apierror.SetOrgScope("Acme Corp", string(config.OrgSourceFlag))
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	var out, errOut bytes.Buffer
	result := &CreateResult{
		Results: []Result{
			{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"}},
		},
	}
	err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil)
	if err == nil {
		t.Fatal("ReportSubmit: want an error when no row was flagged")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitNotFound)
	}
	if !strings.Contains(err.Error(), "Scoped to organization Acme Corp (from the --org flag).") {
		t.Errorf("error = %q, want it to name the organization it was scoped to", err)
	}
	if !strings.HasSuffix(err.Error(), "\n\n"+apierror.ScopeNote()) {
		t.Errorf("error = %q, want it to END with a blank line and the rendered note", err)
	}
}

// The failure block on stderr is a DIRECT terminal write at exit 0, so
// qerrors.Exit's central pass never sees it. Everything the server chose in it
// — the status, the detail prose and the field names, which echo columns of
// customer data — has to arrive control-free.
func TestReportSubmitSanitizesTheFailureBlock(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{
		IssueItemIds: []int{8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: ptr(8817)},
			{
				RowID:      1050,
				Status:     api.UserSubmittedIssueResultSchemaStatus("row_not_found\x1b[31m"),
				FieldNames: []string{"amount\x1b]0;pwned\x07"},
				Detail:     ptr("the row moved\x00"),
			},
		},
	}
	if err := ReportSubmit(&out, &errOut, KindAlert, 42, result, nil); err != nil {
		t.Fatalf("a partial success must not be an error, got: %v", err)
	}
	got := errOut.String()
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x00') || strings.ContainsRune(got, '\x07') {
		t.Fatalf("the failure block reached the terminal with a live control byte: %q", got)
	}
	for _, want := range []string{`amount\x1b]0;pwned\x07`, `row_not_found\x1b[31m`, `the row moved\x00`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing the visible escape %q:\n%s", want, got)
		}
	}
}

// ReportRetract's stderr warning is the other direct write; writeRetractRows
// feeds it and the all-fail error alike, so the pass lives in the renderer.
func TestReportRetractSanitizesTheFailureBlock(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &RetractResult{Results: []RetractRow{
		{RowID: 1042, Status: api.Retracted, ID: recordID()},
		{RowID: 1050, Status: api.UserSubmittedIssueRetractResultSchemaStatus("not_found\x1b[31mred")},
	}}
	if err := ReportRetract(&out, &errOut, KindAlert, 8817, result); err != nil {
		t.Fatalf("a partial success must not be an error, got: %v", err)
	}
	got := errOut.String()
	if strings.ContainsRune(got, '\x1b') {
		t.Fatalf("the retract warning reached the terminal with a live control byte: %q", got)
	}
	if !strings.Contains(got, `row 1050: not_found\x1b[31mred`) {
		t.Errorf("missing the visible escape:\n%s", got)
	}
}

// IssueNames names the aggregates the headline names: each once, in id order,
// from flagged rows only — a failed row's aggregate, and a flagged row with
// none, contribute nothing.
func TestIssueNamesNamesEachFlaggedAggregateOnce(t *testing.T) {
	result := &CreateResult{
		IssueItemIds: []int{8901, 8817},
		Results: []Result{
			{RowID: 1042, Status: api.Created, IssueItemID: ptr(8901)},
			{RowID: 1043, Status: api.AlreadyExisted, IssueItemID: ptr(8817)},
			{RowID: 1044, Status: api.Created, IssueItemID: ptr(8901)},
			{RowID: 1045, Status: api.Created},
			{RowID: 1046, Status: api.RowNotFound, IssueItemID: ptr(9000)},
		},
	}
	got := strings.Join(IssueNames(KindWarning, result), ",")
	if want := "warning/8817,warning/8901"; got != want {
		t.Errorf("IssueNames = %q, want %q", got, want)
	}
}

func threadID(s string) *openapi_types.UUID {
	id := uuid.MustParse(s)
	return &id
}

const (
	threadA = "0190bbbb-1111-2222-3333-444444444444"
	threadB = "0190cccc-1111-2222-3333-444444444444"
)

// The created rows' thread is announced once per distinct id with a recipe
// naming the table, and each already_existed row names the original flagger
// and thread — whichever of the two the server returned.
func TestReportSubmitNamesTheDiscussionAndOriginalFlagger(t *testing.T) {
	dana := &api.UserInfoTinySchema{FirstName: "Dana", LastName: "Ray", Email: "dana@x.io"}
	tests := []struct {
		name    string
		results []Result
		want    string
	}{
		{
			name: "created rows with a thread",
			results: []Result{
				{RowID: 1043, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadA)},
				{RowID: 1042, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadA)},
			},
			want: "alert/8817 flagged rows 1042, 1043\n" +
				"discussion: comment-thread/" + threadA + " (your --comment is its first comment)\n" +
				"  Next: qctl describe comment-thread " + threadA + " --table-id 42\n",
		},
		{
			name: "two created aggregates share one thread",
			results: []Result{
				{RowID: 1042, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadA)},
				{RowID: 1044, Status: api.Created, IssueItemID: ptr(8901), ThreadID: threadID(threadA)},
			},
			want: "alert/8817 flagged row 1042\n" +
				"alert/8901 flagged row 1044\n" +
				"discussion: comment-thread/" + threadA + " (your --comment is its first comment)\n" +
				"  Next: qctl describe comment-thread " + threadA + " --table-id 42\n",
		},
		{
			name: "repeat with flagger and thread",
			results: []Result{
				{RowID: 1042, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadA)},
				{RowID: 1050, Status: api.AlreadyExisted, IssueItemID: ptr(8817),
					SubmittedBy: dana, ThreadID: threadID(threadB)},
			},
			want: "alert/8817 flagged rows 1042, 1050\n" +
				"discussion: comment-thread/" + threadA + " (your --comment is its first comment)\n" +
				"  Next: qctl describe comment-thread " + threadA + " --table-id 42\n" +
				"row 1050 already carried this flag (same comment, same values) — nothing changed there.\n" +
				"  row 1050: flagged by Dana Ray <dana@x.io> — comment-thread/" + threadB + "\n",
		},
		{
			name: "repeats sorted by row, null thread, null flagger, both null",
			results: []Result{
				{RowID: 1052, Status: api.AlreadyExisted, IssueItemID: ptr(8817), ThreadID: threadID(threadB)},
				{RowID: 1053, Status: api.AlreadyExisted, IssueItemID: ptr(8817)},
				{RowID: 1051, Status: api.AlreadyExisted, IssueItemID: ptr(8817), SubmittedBy: dana},
			},
			want: "alert/8817 flagged rows 1051, 1052, 1053\n" +
				"rows 1051, 1052, 1053 already carried this flag (same comment, same values) — nothing changed there.\n" +
				"  row 1051: flagged by Dana Ray <dana@x.io>\n" +
				"  row 1052: comment-thread/" + threadB + "\n",
		},
		{
			name: "a line break in the flagger's name stays on the row's line",
			results: []Result{
				{RowID: 1050, Status: api.AlreadyExisted, IssueItemID: ptr(8817),
					SubmittedBy: &api.UserInfoTinySchema{FirstName: "Dana\nX", Email: "dana@x.io"}},
			},
			want: "alert/8817 flagged row 1050\n" +
				"row 1050 already carried this flag (same comment, same values) — nothing changed there.\n" +
				"  row 1050: flagged by Dana\\nX <dana@x.io>\n",
		},
		{
			name: "created row with no thread",
			results: []Result{
				{RowID: 1042, Status: api.Created, IssueItemID: ptr(8817)},
			},
			want: "alert/8817 flagged row 1042\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			if err := ReportSubmit(&out, &errOut, KindAlert, 42, &CreateResult{Results: tt.results}, nil); err != nil {
				t.Fatalf("ReportSubmit: %v", err)
			}
			if got := out.String(); got != tt.want {
				t.Errorf("stdout =\n%s\nwant\n%s", got, tt.want)
			}
			if errOut.Len() != 0 {
				t.Errorf("stderr = %q, want empty", errOut.String())
			}
		})
	}
}

// Two distinct created threads each get their own discussion line, in a
// stable order.
func TestReportSubmitAnnouncesEachDistinctThreadOnce(t *testing.T) {
	var out, errOut bytes.Buffer
	result := &CreateResult{Results: []Result{
		{RowID: 1042, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadB)},
		{RowID: 1043, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadA)},
		{RowID: 1044, Status: api.Created, IssueItemID: ptr(8817), ThreadID: threadID(threadB)},
	}}
	if err := ReportSubmit(&out, &errOut, KindWarning, 7, result, nil); err != nil {
		t.Fatalf("ReportSubmit: %v", err)
	}
	want := "warning/8817 flagged rows 1042, 1043, 1044\n" +
		"discussion: comment-thread/" + threadA + " (your --comment is its first comment)\n" +
		"  Next: qctl describe comment-thread " + threadA + " --table-id 7\n" +
		"discussion: comment-thread/" + threadB + " (your --comment is its first comment)\n" +
		"  Next: qctl describe comment-thread " + threadB + " --table-id 7\n"
	if got := out.String(); got != want {
		t.Errorf("stdout =\n%s\nwant\n%s", got, want)
	}
}
