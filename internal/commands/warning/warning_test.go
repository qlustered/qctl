package warning

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func registerWarningSubmit(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.UserSubmittedIssueCreateResponseSchema,
) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/warnings/submit",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusOK, result)
		})
}

// assertSubmitBody checks the cross product the command claims to send: one
// wire row per --row, each carrying every --field. field_names is what decides
// which cells get flagged, so a body carrying only the row ids would flag
// nothing and still satisfy a check on the ids alone.
//
// The decoded values are float64 and map[string]interface{}: encoding/json
// unmarshals every JSON number into interface{} as a float64, so comparing a
// row id against an int never matches.
func assertSubmitBody(t *testing.T, got map[string]interface{}, fields []string, rowIDs ...int) {
	t.Helper()
	gotRows, _ := got["rows"].([]interface{})
	if len(gotRows) != len(rowIDs) {
		t.Fatalf("wire rows = %v, want %d row(s)", got["rows"], len(rowIDs))
	}
	for i, wantID := range rowIDs {
		row, _ := gotRows[i].(map[string]interface{})
		if row["row_id"] != float64(wantID) {
			t.Errorf("wire rows[%d].row_id = %v, want %d", i, row["row_id"], wantID)
		}
		names, _ := row["field_names"].([]interface{})
		if len(names) != len(fields) {
			t.Errorf("wire rows[%d].field_names = %v, want %v", i, row["field_names"], fields)
			continue
		}
		for j, wantName := range fields {
			if names[j] != wantName {
				t.Errorf("wire rows[%d].field_names[%d] = %v, want %q", i, j, names[j], wantName)
			}
		}
	}
}

// A warning is advisory, so unlike alert submit it does NOT prompt: the
// absence of --yes must still submit.
func TestWarningSubmitDoesNotPrompt(t *testing.T) {
	for _, flags := range [][]string{nil, {"--yes"}, {"-y"}, {"--yes=false"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			mock := newWarningTestEnv(t)
			registerTableResolveForWarnings(mock)
			var got map[string]interface{}
			registerWarningSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
				IssueItemIds: []int{8817},
				Results: []api.UserSubmittedIssueResultSchema{
					{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
				},
			})

			stdout, stderr, err := runWarningCommandSplit(t,
				append([]string{"warning", "submit", "--table", "orders", "--row", "1042", "--field", "amount", "--comment", "looks off"}, flags...)...)
			if err != nil {
				t.Fatalf("warning submit: %v (stderr: %s)", err, stderr)
			}
			if !strings.Contains(stdout, "warning/8817 flagged row 1042") {
				t.Errorf("stdout = %q", stdout)
			}
			if got["comment"] != "looks off" {
				t.Errorf("wire comment = %v", got["comment"])
			}
			assertSubmitBody(t, got, []string{"amount"}, 1042)
		})
	}
}

func TestWarningSubmitPartialFailureIsExitZeroWithStderr(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerTableResolveForWarnings(mock)
	var got map[string]interface{}
	registerWarningSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
			{RowID: 1051, Status: api.FieldInvalid, FieldNames: []string{},
				Detail: strPtr("Unknown field name: 'amonut'.")},
		},
	})

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "submit",
		"--table", "orders", "--row", "1042", "--row", "1051", "--field", "amount", "--comment", "looks off")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	if !strings.Contains(stdout, "warning/8817 flagged row 1042") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "row 1051: field_invalid") {
		t.Errorf("stderr = %q", stderr)
	}
	assertSubmitBody(t, got, []string{"amount"}, 1042, 1051)
}

func TestWarningSubmitAllFieldInvalidIsExitTwo(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerTableResolveForWarnings(mock)
	// No body to inspect: this test is about the exit code when nothing was
	// flagged, and the payload shape is pinned by the two tests above.
	registerWarningSubmit(mock, new(map[string]interface{}), api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.FieldInvalid, FieldNames: []string{},
				Detail: strPtr("Unknown field name: 'amonut'.")},
		},
	})

	_, _, err := runWarningCommandSplit(t, "warning", "submit",
		"--table", "orders", "--row", "1042", "--field", "amonut", "--comment", "looks off")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestWarningSubmitRequiresAField(t *testing.T) {
	mock := newWarningTestEnv(t)
	lookups := registerTableResolveForWarnings(mock)

	_, _, err := runWarningCommandSplit(t, "warning", "submit",
		"--table", "orders", "--row", "1042", "--comment", "looks off")
	if err == nil {
		t.Fatal("a fieldless submit must be refused: there is no whole-row form")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if *lookups != 0 {
		t.Error("a locally-decidable rejection must cost no request")
	}
}

// userSubmittedWarning is an unresolved user_submitted warning on table 42.
func userSubmittedWarning() api.WarningSchema {
	resolved := false
	return api.WarningSchema{
		ID:          8817,
		DatasetID:   42,
		DatasetName: "orders",
		IssueType:   api.UserSubmitted,
		Msg:         "looks off",
		Count:       2,
		Resolved:    &resolved,
	}
}

func registerGetWarning8817(mock *testutil.MockAPIServer, warning api.WarningSchema) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/warnings/8817",
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			testutil.RespondJSON(w, http.StatusOK, warning)
		})
	return &calls
}

func registerWarningRetract(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.UserSubmittedIssueRetractResponseSchema,
) *int {
	calls := 0
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/warnings/submitted",
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusOK, result)
		})
	return &calls
}

// assertRetractBody checks that BOTH wire fields of the retract schema arrived,
// in the order the flags named them. row_ids is what actually selects the rows
// to withdraw, so a body carrying only issue_item_id would retract nothing and
// still satisfy a check on the id alone.
//
// The decoded values are float64: encoding/json unmarshals every JSON number
// into interface{} as a float64, so comparing against an int never matches.
//
//nolint:unparam // issueItemID is the helper's contract, not this test's constants.
func assertRetractBody(t *testing.T, got map[string]interface{}, issueItemID int, rows ...int) {
	t.Helper()
	if got["issue_item_id"] != float64(issueItemID) {
		t.Errorf("wire issue_item_id = %v, want %d", got["issue_item_id"], issueItemID)
	}
	gotRows, _ := got["row_ids"].([]interface{})
	if len(gotRows) != len(rows) {
		t.Fatalf("wire row_ids = %v, want %v", got["row_ids"], rows)
	}
	for i, want := range rows {
		if gotRows[i] != float64(want) {
			t.Errorf("wire row_ids[%d] = %v, want %d", i, gotRows[i], want)
		}
	}
}

// The warning names its own table, so retract takes no --table and the DELETE
// still lands on dataset 42.
func TestWarningRetractLearnsTheTableFromTheWarning(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning8817(mock, userSubmittedWarning())
	var got map[string]interface{}
	registerWarningRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		IssueResolved: true,
		Results:       []api.UserSubmittedIssueRetractResultSchema{{RowID: 1042, Status: api.Retracted}},
	})

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "retract", "8817", "--row", "1042", "--yes")
	if err != nil {
		t.Fatalf("warning retract: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "warning/8817 retracted from row 1042") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "auto-resolved") {
		t.Errorf("issue_resolved was not reported: %q", stdout)
	}
	assertRetractBody(t, got, 8817, 1042)
}

// retract is the wrong verb for a warning a RULE raised — 'qctl warning resolve'
// is the right one. Retracting it would come back as every row not_found, which
// reads as "wrong row" rather than "wrong verb", so refuse it before the DELETE
// and point the caller at the verb that does work.
func TestWarningRetractRefusesARuleWarning(t *testing.T) {
	mock := newWarningTestEnv(t)
	warning := userSubmittedWarning()
	warning.IssueType = api.Anomaly
	registerGetWarning8817(mock, warning)
	// No body to inspect: the point of this test is that the DELETE never fires.
	deletes := registerWarningRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runWarningCommandSplit(t, "warning", "retract", "8817", "--row", "1042", "--yes")
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "table rule") {
		t.Errorf("the error should point at the table rule, got: %v", err)
	}
	if !strings.Contains(err.Error(), "qctl warning resolve") {
		t.Errorf("the error should point at the verb that does work, got: %v", err)
	}
	if *deletes != 0 {
		t.Error("must not send the DELETE for a rule warning")
	}
}

func TestWarningRetractNothingRetractedIsExitThree(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning8817(mock, userSubmittedWarning())
	var got map[string]interface{}
	registerWarningRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		Results: []api.UserSubmittedIssueRetractResultSchema{{RowID: 1050, Status: api.NotFound}},
	})

	_, _, err := runWarningCommandSplit(t, "warning", "retract", "8817", "--row", "1050", "--yes")
	if err == nil {
		t.Fatal("expected an error when nothing was retracted")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	assertRetractBody(t, got, 8817, 1050)
}

// Unlike 'warning submit', retract DOES prompt: it erases someone else's
// objection, and alert:resolve lets any resolver do it.
func TestWarningRetractWithoutYesIsNonInteractiveError(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning8817(mock, userSubmittedWarning())
	// No body to inspect: the point of this test is that the DELETE never fires.
	deletes := registerWarningRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runWarningCommandSplit(t, "warning", "retract", "8817", "--row", "1042")
	if err == nil {
		t.Fatal("expected a non-interactive confirmation error")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("error should mention --yes, got: %v", err)
	}
	// Test the code, not the text: a missing confirmation on a non-interactive
	// run is always fixed by changing the command line, so it is bad usage.
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if *deletes != 0 {
		t.Error("must not retract without confirmation")
	}
}

// The plural render is a different branch of ReportRetract than the singular
// one the test above exercises, and it is the only place the row order the
// flags named is visible in the output.
func TestWarningRetractRendersEveryRowItRetracted(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning8817(mock, userSubmittedWarning())
	var got map[string]interface{}
	registerWarningRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		IssueResolved: true,
		Results: []api.UserSubmittedIssueRetractResultSchema{
			{RowID: 1042, Status: api.Retracted},
			{RowID: 1043, Status: api.Retracted},
		},
	})

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "retract", "8817",
		"--row", "1042", "--row", "1043", "--yes")
	if err != nil {
		t.Fatalf("warning retract: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "warning/8817 retracted from rows 1042, 1043") {
		t.Errorf("stdout = %q", stdout)
	}
	assertRetractBody(t, got, 8817, 1042, 1043)
}

func TestWarningRetractPartialFailureIsExitZeroWithStderr(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerGetWarning8817(mock, userSubmittedWarning())
	var got map[string]interface{}
	registerWarningRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		IssueResolved: false,
		Results: []api.UserSubmittedIssueRetractResultSchema{
			{RowID: 1042, Status: api.Retracted},
			{RowID: 1050, Status: api.NotFound},
		},
	})

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "retract", "8817",
		"--row", "1042", "--row", "1050", "--yes")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	if !strings.Contains(stdout, "still open") {
		t.Errorf("stdout should say the warning stays open: %q", stdout)
	}
	if !strings.Contains(stderr, "row 1050: not_found") {
		t.Errorf("stderr = %q", stderr)
	}
	assertRetractBody(t, got, 8817, 1042, 1050)
}

func TestWarningRetractRejectsANonNumericID(t *testing.T) {
	mock := newWarningTestEnv(t)
	reads := registerGetWarning8817(mock, userSubmittedWarning())

	_, _, err := runWarningCommandSplit(t, "warning", "retract", "abc", "--row", "1042", "--yes")
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if *reads != 0 {
		t.Error("a malformed id must cost no request")
	}
}

// The warning side shares the submit guard with the alert side — same routes,
// same controller — so it needs the same explanation even though a warning
// holds nothing. This is the one difference worth pinning per verb: the guard
// is about the ROW's state, not about what the flag would do to it.
func TestWarningSubmitExplainsARowStillBeingReprocessed(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerTableResolveForWarnings(mock)
	var got map[string]interface{}
	registerWarningSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"},
				Detail: strPtr("Not a live quarantined row of this dataset, or its flagged values changed, or it is being reprocessed.")},
		},
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DataWithCellHistorySchema{
				DatasetName: "orders",
				Fields:      []string{"id", "awaiting_processing_"},
				Labels:      []string{"id", "awaiting_processing_"},
				FieldTypes:  []api.FieldType{"integer", "boolean"},
				Results:     [][]interface{}{{float64(1042), true}},
				TotalRows:   1,
			})
		})

	_, _, err := runWarningCommandSplit(t, "warning", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "looks off")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	if want := "row 1042 IS in quarantine, but is awaiting processing"; !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q:\n%s", want, err.Error())
	}
}

// -o name prints warning/<id> per aggregate a flagged row joined, and the
// headline and failure block move to stderr.
func TestWarningSubmitNamePrintsEachWarning(t *testing.T) {
	mock := newWarningTestEnv(t)
	registerTableResolveForWarnings(mock)
	var got map[string]interface{}
	registerWarningSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
			{RowID: 1051, Status: api.FieldInvalid, FieldNames: []string{}, Detail: strPtr("Unknown field.")},
		},
	})

	stdout, stderr, err := runWarningCommandSplit(t, "warning", "submit", "--table", "orders",
		"--row", "1042", "--row", "1051", "--field", "amount", "--comment", "looks off", "-o", "name")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	if stdout != "warning/8817\n" {
		t.Errorf("stdout = %q, want %q", stdout, "warning/8817\n")
	}
	for _, want := range []string{"warning/8817 flagged row 1042", "row 1051: field_invalid"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}
