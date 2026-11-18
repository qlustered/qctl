package alert

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// runAlertCommandSplit keeps stdout and stderr apart. runAlertCommand merges
// them, and a merged buffer cannot prove the partial-failure block went to
// stderr — which is the whole contract of a partial success.
func runAlertCommandSplit(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupAlertTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

// registerTableResolveForAlerts answers the name-only lookup `--table orders`
// makes, resolving it to dataset 42 — the id the routes below are registered
// on — and counts it so a test can assert it never happened.
func registerTableResolveForAlerts(mock *testutil.MockAPIServer) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	return &calls
}

func registerAlertSubmit(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.UserSubmittedIssueCreateResponseSchema,
) {
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submit",
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(gotBody)
			testutil.RespondJSON(w, http.StatusOK, result)
		})
}

func TestAlertSubmitSendsTheCrossProduct(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
			{RowID: 1043, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
		},
	})

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--row", "1043",
		"--field", "amount", "--comment", "wrong FX rate", "--yes")
	if err != nil {
		t.Fatalf("alert submit: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "alert/8817 flagged rows 1042, 1043") {
		t.Errorf("stdout = %q", stdout)
	}
	assertSubmitBody(t, got, []string{"amount"}, 1042, 1043)
}

func TestAlertSubmitCarriesSuggestions(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
		},
	})

	if _, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount",
		"--comment", "wrong FX rate", "--suggest", "1420.00", "--suggest", "1425.00", "--yes"); err != nil {
		t.Fatalf("alert submit: %v", err)
	}
	suggested, _ := got["suggested_values"].([]interface{})
	if len(suggested) != 2 {
		t.Fatalf("suggested_values = %v", got["suggested_values"])
	}
	first, _ := suggested[0].(map[string]interface{})
	if first["value"] != "1420.00" {
		t.Errorf("first suggestion = %v", first)
	}
}

func TestAlertSubmitPartialFailureIsExitZeroWithStderr(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
			{RowID: 1050, Status: api.RowNotFound, FieldNames: []string{"amount"},
				Detail: strPtr("Not a live quarantined row of this dataset, or its flagged values changed, or it is being reprocessed.")},
		},
	})

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--row", "1050",
		"--field", "amount", "--comment", "wrong FX rate", "--yes")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	if !strings.Contains(stdout, "alert/8817 flagged row 1042") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stderr, "row 1050 (amount): row_not_found") {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestAlertSubmitAllRowsNotFoundIsExitThree(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1050, Status: api.RowNotFound, FieldNames: []string{"amount"}},
		},
	})

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1050", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// A cap decidable locally must cost no request at all — not the table lookup
// and not the submit.
func TestAlertSubmitValidatesBeforeAnyRequest(t *testing.T) {
	mock := newAlertTestEnv(t)
	lookups := registerTableResolveForAlerts(mock)
	submitted := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submit",
		func(w http.ResponseWriter, _ *http.Request) {
			submitted = true
			testutil.RespondJSON(w, http.StatusOK, api.UserSubmittedIssueCreateResponseSchema{})
		})

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--row", "1042",
		"--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("a duplicate --row must be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if *lookups != 0 || submitted {
		t.Errorf("nothing should have been requested (lookups=%d, submitted=%v)", *lookups, submitted)
	}
}

// Submitting an alert holds production rows in quarantine, so it confirms.
// Tests run with stdin not a terminal, which is the non-interactive path.
func TestAlertSubmitWithoutYesIsNonInteractiveError(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	submitted := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submit",
		func(w http.ResponseWriter, _ *http.Request) {
			submitted = true
			testutil.RespondJSON(w, http.StatusOK, api.UserSubmittedIssueCreateResponseSchema{})
		})

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x")
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
	if submitted {
		t.Error("must not submit without confirmation")
	}
}

func intPtr(v int) *int       { return &v }
func strPtr(v string) *string { return &v }

// userSubmittedAlert is an unresolved user_submitted alert on table 42, the
// shape retract expects to find.
func userSubmittedAlert() alerts.AlertFull {
	resolved := false
	return alerts.AlertFull{
		ID:          8817,
		DatasetID:   42,
		DatasetName: "orders",
		IssueType:   api.AlertTypeUserSubmitted,
		Msg:         "wrong FX rate",
		Count:       2,
		Resolved:    &resolved,
	}
}

func registerGetAlert8817(mock *testutil.MockAPIServer, alert alerts.AlertFull) *int {
	calls := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/alerts/8817",
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			testutil.RespondJSON(w, http.StatusOK, alert)
		})
	return &calls
}

func registerAlertRetract(mock *testutil.MockAPIServer, gotBody *map[string]interface{},
	result api.UserSubmittedIssueRetractResponseSchema,
) *int {
	calls := 0
	mock.RegisterHandler("DELETE", "/api/orgs/"+testOrgID+"/datasets/42/alerts/submitted",
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
// assertSubmitBody walks EVERY row of the submit body, not just the first.
// The stdout line this test also checks ("flagged rows 1042, 1043") is rendered
// from the mock RESPONSE, so it cannot catch a request that dropped a row or
// sent it without field_names — only the body can.
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

// The alert names its own table, so retract takes no --table and the DELETE
// still lands on dataset 42.
func TestAlertRetractLearnsTheTableFromTheAlert(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert8817(mock, userSubmittedAlert())
	var got map[string]interface{}
	registerAlertRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		IssueResolved: true,
		Results: []api.UserSubmittedIssueRetractResultSchema{
			{RowID: 1042, Status: api.Retracted},
			{RowID: 1043, Status: api.Retracted},
		},
	})

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "retract", "8817",
		"--row", "1042", "--row", "1043", "--yes")
	if err != nil {
		t.Fatalf("alert retract: %v (stderr: %s)", err, stderr)
	}
	if !strings.Contains(stdout, "alert/8817 retracted from rows 1042, 1043") {
		t.Errorf("stdout = %q", stdout)
	}
	if !strings.Contains(stdout, "auto-resolved") {
		t.Errorf("issue_resolved was not reported: %q", stdout)
	}
	assertRetractBody(t, got, 8817, 1042, 1043)
}

// Retracting a rule or ingestion alert would come back as every row not_found,
// which reads as "wrong row". Refuse it before the DELETE and say why.
func TestAlertRetractRefusesANonUserSubmittedAlert(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := userSubmittedAlert()
	alert.IssueType = api.AlertTypeNeedsReviewMigration
	registerGetAlert8817(mock, alert)
	// No body to inspect: the point of this test is that the DELETE never fires.
	deletes := registerAlertRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "8817", "--row", "1042", "--yes")
	if err == nil {
		t.Fatal("expected a usage error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "alert resolve") {
		t.Errorf("the error should point at the verb that does apply, got: %v", err)
	}
	if *deletes != 0 {
		t.Error("must not send the DELETE for a non-user-submitted alert")
	}
}

// A person-marked duplicate group's hold alert is taken back on the group its
// body names.
func TestAlertRetractNamesThePersonMarkedDuplicateGroup(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := userSubmittedAlert()
	alert.IssueType = api.AlertTypePersonMarkedDuplicate
	group := uuid.MustParse("6f1c2d3e-4b5a-4c6d-8e7f-901234567890")
	alert.Body = &api.AlertBodySchema{GroupID: &group}
	registerGetAlert8817(mock, alert)
	deletes := registerAlertRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "8817", "--row", "1042", "--yes")
	if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "qctl duplicate-group retract "+group.String()+" --table-id 42") {
		t.Errorf("the error should name the group's retract, got: %v", err)
	}
	if strings.Contains(err.Error(), "--row-id") {
		t.Errorf("the error should not search by row when the body names the group, got: %v", err)
	}
	if *deletes != 0 {
		t.Error("must not send the DELETE for a person-marked duplicate alert")
	}
}

// A person-marked hold alert whose body names no group, as from a server whose
// alert schema predates body.group_id, is pointed at the search by row.
func TestAlertRetractPointsAPersonMarkedDuplicateAtItsGroup(t *testing.T) {
	mock := newAlertTestEnv(t)
	alert := userSubmittedAlert()
	alert.IssueType = api.AlertTypePersonMarkedDuplicate
	registerGetAlert8817(mock, alert)
	deletes := registerAlertRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "8817", "--row", "1042", "--yes")
	if code := qerrors.GetExitCode(err); err == nil || code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
	if !strings.Contains(err.Error(), "qctl duplicate-group retract <group-id> --table-id") {
		t.Errorf("the error should point at duplicate-group retract, got: %v", err)
	}
	if *deletes != 0 {
		t.Error("must not send the DELETE for a person-marked duplicate alert")
	}
}

func TestAlertRetractPartialFailureIsExitZeroWithStderr(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert8817(mock, userSubmittedAlert())
	var got map[string]interface{}
	registerAlertRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		IssueResolved: false,
		Results: []api.UserSubmittedIssueRetractResultSchema{
			{RowID: 1042, Status: api.Retracted},
			{RowID: 1050, Status: api.NotFound},
		},
	})

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "retract", "8817",
		"--row", "1042", "--row", "1050", "--yes")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	if !strings.Contains(stdout, "still open") {
		t.Errorf("stdout should say the alert stays open: %q", stdout)
	}
	if !strings.Contains(stderr, "row 1050: not_found") {
		t.Errorf("stderr = %q", stderr)
	}
	assertRetractBody(t, got, 8817, 1042, 1050)
}

func TestAlertRetractNothingRetractedIsExitThree(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert8817(mock, userSubmittedAlert())
	var got map[string]interface{}
	registerAlertRetract(mock, &got, api.UserSubmittedIssueRetractResponseSchema{
		Results: []api.UserSubmittedIssueRetractResultSchema{{RowID: 1050, Status: api.NotFound}},
	})

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "8817", "--row", "1050", "--yes")
	if err == nil {
		t.Fatal("expected an error when nothing was retracted")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	assertRetractBody(t, got, 8817, 1050)
}

func TestAlertRetractRejectsANonNumericID(t *testing.T) {
	mock := newAlertTestEnv(t)
	reads := registerGetAlert8817(mock, userSubmittedAlert())

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "abc", "--row", "1042", "--yes")
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

// Retract erases someone else's objection — ALERT_RESOLVE lets any resolver do
// it — so it confirms.
func TestAlertRetractWithoutYesIsNonInteractiveError(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerGetAlert8817(mock, userSubmittedAlert())
	// No body to inspect: the point of this test is that the DELETE never fires.
	deletes := registerAlertRetract(mock, new(map[string]interface{}),
		api.UserSubmittedIssueRetractResponseSchema{})

	_, _, err := runAlertCommandSplit(t, "alert", "retract", "8817", "--row", "1042")
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

// registerRowProbe answers the get_data reads the failure reporter makes for a
// row_not_found row, and records the query each one was asked.
//
// ONE handler serves both reads — the mock keys on method and path — so it
// branches on is_bad_data exactly as the route does, and the recorded queries
// are the only evidence of how many reads were made and in what order.
// quarantined and clean are the parallel-array payload of their own side: nil
// for "no live row of that side carries that id". The clean side is given no
// awaiting_processing_ column, because it does not have one.
func registerRowProbe(mock *testutil.MockAPIServer, quarantined, clean [][]interface{}, probes *[]url.Values) {
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data",
		func(w http.ResponseWriter, r *http.Request) {
			query := r.URL.Query()
			if probes != nil {
				*probes = append(*probes, query)
			}
			rows := clean
			fields := []string{"id", "row_number_"}
			types := []api.FieldType{"integer", "integer"}
			if query.Get("is_bad_data") == "true" {
				rows = quarantined
				fields = []string{"id", "awaiting_processing_"}
				types = []api.FieldType{"integer", "boolean"}
			}
			testutil.RespondJSON(w, http.StatusOK, api.DataWithCellHistorySchema{
				DatasetName: "orders",
				Fields:      fields,
				Labels:      fields,
				FieldTypes:  types,
				Results:     rows,
				TotalRows:   len(rows),
			})
		})
}

// The case this whole path exists for: `rows send-to-quarantine` moved the row,
// the row kept its id, and the submit is refused only until the queued
// reprocess settles. Without the probe that is indistinguishable from a typo.
func TestAlertSubmitExplainsARowStillBeingReprocessed(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"},
				Detail: strPtr("Not a live quarantined row of this dataset, or its flagged values changed, or it is being reprocessed.")},
		},
	})
	var probes []url.Values
	registerRowProbe(mock, [][]interface{}{{float64(1042), true}}, nil, &probes)

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d — the probe explains the failure, it never re-grades it",
			code, qerrors.ExitNotFound)
	}
	if want := "row 1042 IS in quarantine, but is awaiting processing"; !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q:\n%s", want, err.Error())
	}
	// The quarantine side and the row filter travel together: get_data
	// validates filter_by_field_name against the field set is_bad_data picks,
	// and the clean table has no awaiting_processing_ column at all.
	if len(probes) != 1 {
		t.Fatalf("probes = %d, want 1 — a row the quarantine side HOLDS needs no clean read", len(probes))
	}
	for param, want := range map[string]string{
		"is_bad_data":           "true",
		"filter_by_field_name":  "id",
		"filter_by_field_value": "1042",
	} {
		if probes[0].Get(param) != want {
			t.Errorf("probe query %s = %q, want %q", param, probes[0].Get(param), want)
		}
	}
}

// registerSubmitRefusingRow sets up a submit whose only row comes back
// row_not_found, which is the input every probe test below shares.
func registerSubmitRefusingRow(t *testing.T, mock *testutil.MockAPIServer) {
	t.Helper()
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"}},
		},
	})
}

// The other half of the same read, and the case the clean-side read was added
// for: the quarantine side does not hold the id because the row is still
// sitting on the clean side. The fix is the move, and the probe has now READ
// the row it sends the caller to move.
func TestAlertSubmitSendsACleanRowToTheMove(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	registerSubmitRefusingRow(t, mock)
	var probes []url.Values
	registerRowProbe(mock, nil, [][]interface{}{{float64(1042), 7}}, &probes)

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d — the probe explains the failure, it never re-grades it",
			code, qerrors.ExitNotFound)
	}
	for _, want := range []string{
		"live row on the CLEAN side",
		"qctl rows send-to-quarantine <table> --row-id 1042",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err.Error())
		}
	}
	// Having read the row, the note must not fall back to offering the manual
	// check the second read exists to replace.
	if strings.Contains(err.Error(), "or this table has no such row") {
		t.Errorf("the clean read settled this; the note still hedges:\n%s", err.Error())
	}
	if len(probes) != 2 {
		t.Fatalf("probes = %d, want 2 — an empty quarantine read owes a clean one", len(probes))
	}
	// The second read is the SAME row filter against the other side. The pair
	// travels together either way: filter_by_field_name is validated against
	// the field set is_bad_data selects.
	for param, want := range map[string]string{
		"is_bad_data":           "false",
		"filter_by_field_name":  "id",
		"filter_by_field_value": "1042",
	} {
		if probes[1].Get(param) != want {
			t.Errorf("clean probe query %s = %q, want %q", param, probes[1].Get(param), want)
		}
	}
}

// Neither side holds the id. Sending the caller to `rows send-to-quarantine`
// here would be a write against nothing — which is exactly what the pre-split
// note could only warn about, because it could not rule the case out.
func TestAlertSubmitRefusesToSuggestAMoveForAnIDOnNeitherSide(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	registerSubmitRefusingRow(t, mock)
	var probes []url.Values
	registerRowProbe(mock, nil, nil, &probes)

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	for _, want := range []string{
		"no live row of this table carries id 1042 on either side",
		"the id is wrong, or the row has been deleted",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err.Error())
		}
	}
	if strings.Contains(err.Error(), "send-to-quarantine <table> --row-id 1042") {
		t.Errorf("nothing to move, so nothing to suggest moving:\n%s", err.Error())
	}
	if len(probes) != 2 {
		t.Fatalf("probes = %d, want 2", len(probes))
	}
}

// The quarantine read SUCCEEDED and said no; only the refinement of it failed.
// Reporting nothing here would throw away a true answer, so the report falls
// back to the message that names both possibilities rather than to silence.
func TestAlertSubmitKeepsTheQuarantineVerdictWhenTheCleanReadFails(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	registerSubmitRefusingRow(t, mock)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/42/data",
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("is_bad_data") != "true" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			testutil.RespondJSON(w, http.StatusOK, api.DataWithCellHistorySchema{
				DatasetName: "orders",
				Fields:      []string{"id", "awaiting_processing_"},
				Labels:      []string{"id", "awaiting_processing_"},
				FieldTypes:  []api.FieldType{"integer", "boolean"},
			})
		})

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	for _, want := range []string{
		"no live quarantined row of this table carries id 1042",
		"clean-side read that would tell you more did not answer",
		"--filter-by-field-name id --filter-by-field-value 1042",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err.Error())
		}
	}
	// A failed SECOND read must not be reported as a settled first one.
	if strings.Contains(err.Error(), "on either side") {
		t.Errorf("the clean read failed; the note claims it answered:\n%s", err.Error())
	}
}

// A probe that cannot run leaves the report exactly as it was. The mock
// answers no get_data route here, so the read fails.
func TestAlertSubmitSurvivesAFailedProbe(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.RowNotFound, FieldNames: []string{"amount"}},
		},
	})

	_, _, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--field", "amount", "--comment", "x", "--yes")
	if err == nil {
		t.Fatal("expected an error when no row was flagged")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
	if strings.Contains(err.Error(), "re-read just now") {
		t.Errorf("a failed probe must add nothing:\n%s", err.Error())
	}
	if !strings.Contains(err.Error(), "row 1042 (amount): row_not_found") {
		t.Errorf("the original report was lost:\n%s", err.Error())
	}
}

// Under -o json stdout is the server's response, which is where each row's own
// record id lives; the headline and the failure block go to stderr.
func TestAlertSubmitJSONPrintsTheResponse(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	recordID := uuid.MustParse("0190aaaa-1111-2222-3333-444444444444")
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817),
				ID: &recordID},
			{RowID: 1050, Status: api.RowNotFound, FieldNames: []string{"amount"}, Detail: strPtr("gone")},
		},
	})

	stdout, stderr, err := runAlertCommandSplit(t, "alert", "submit",
		"--table", "orders", "--row", "1042", "--row", "1050",
		"--field", "amount", "--comment", "wrong FX rate", "--yes", "-o", "json")
	if err != nil {
		t.Fatalf("a partial success must exit 0, got: %v", err)
	}
	var doc struct {
		IssueItemIDs []int `json:"issue_item_ids"`
		Results      []struct {
			RowID int     `json:"row_id"`
			ID    *string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if len(doc.IssueItemIDs) != 1 || doc.IssueItemIDs[0] != 8817 {
		t.Errorf("issue_item_ids = %v, want [8817]", doc.IssueItemIDs)
	}
	if len(doc.Results) != 2 || doc.Results[0].ID == nil || *doc.Results[0].ID != recordID.String() {
		t.Errorf("results = %+v, want row 1042 carrying record %s", doc.Results, recordID)
	}
	for _, want := range []string{"alert/8817 flagged row 1042", "row 1050 (amount): row_not_found"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// -o name names each aggregate a flagged row joined, once, in id order.
func TestAlertSubmitNamePrintsEachAlert(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8818, 8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8818)},
			{RowID: 1043, Status: api.AlreadyExisted, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817)},
			{RowID: 1044, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8818)},
		},
	})

	stdout, _, err := runAlertCommandSplit(t, "alert", "submit", "--table", "orders",
		"--row", "1042", "--row", "1043", "--row", "1044", "--field", "amount",
		"--comment", "wrong FX rate", "--yes", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "alert/8817\nalert/8818\n"; stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// A submit that flagged nothing is still an error, and stdout stays empty: no
// document stands for "nothing happened".
func TestAlertSubmitAllFailedPrintsNoDocument(t *testing.T) {
	mock := newAlertTestEnv(t)
	registerTableResolveForAlerts(mock)
	var got map[string]interface{}
	registerAlertSubmit(mock, &got, api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1050, Status: api.RowNotFound, FieldNames: []string{"amount"}, Detail: strPtr("gone")},
		},
	})

	stdout, _, err := runAlertCommandSplit(t, "alert", "submit", "--table", "orders",
		"--row", "1050", "--field", "amount", "--comment", "x", "--yes", "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit = %d (%v), want %d", code, err, qerrors.ExitNotFound)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}

// The discussion recipe names the table the submit resolved, and -o json
// keeps the raw result — thread_id and submitted_by included — on stdout while
// the prose moves to stderr.
func TestAlertSubmitNamesTheDiscussionThread(t *testing.T) {
	thread := uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")
	original := uuid.MustParse("0190cccc-1111-2222-3333-444444444444")
	result := api.UserSubmittedIssueCreateResponseSchema{
		IssueItemIds: []int{8817},
		Results: []api.UserSubmittedIssueResultSchema{
			{RowID: 1042, Status: api.Created, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817),
				ThreadID: &thread},
			{RowID: 1050, Status: api.AlreadyExisted, FieldNames: []string{"amount"}, IssueItemID: intPtr(8817),
				ThreadID:    &original,
				SubmittedBy: &api.UserInfoTinySchema{FirstName: "Dana", LastName: "Ray", Email: "dana@x.io"}},
		},
	}
	args := []string{"alert", "submit", "--table", "orders", "--row", "1042", "--row", "1050",
		"--field", "amount", "--comment", "wrong FX rate", "--yes"}
	wantProse := []string{
		"discussion: comment-thread/" + thread.String() + " (your --comment is its first comment)\n",
		"  Next: qctl describe comment-thread " + thread.String() + " --table-id 42\n",
		"  row 1050: flagged by Dana Ray <dana@x.io> — comment-thread/" + original.String() + "\n",
	}

	t.Run("text", func(t *testing.T) {
		mock := newAlertTestEnv(t)
		registerTableResolveForAlerts(mock)
		var got map[string]interface{}
		registerAlertSubmit(mock, &got, result)
		stdout, stderr, err := runAlertCommandSplit(t, args...)
		if err != nil {
			t.Fatalf("alert submit: %v (stderr: %s)", err, stderr)
		}
		for _, want := range wantProse {
			if !strings.Contains(stdout, want) {
				t.Errorf("stdout missing %q:\n%s", want, stdout)
			}
		}
	})

	t.Run("json", func(t *testing.T) {
		mock := newAlertTestEnv(t)
		registerTableResolveForAlerts(mock)
		var got map[string]interface{}
		registerAlertSubmit(mock, &got, result)
		stdout, stderr, err := runAlertCommandSplit(t, append(args, "-o", "json")...)
		if err != nil {
			t.Fatalf("alert submit: %v (stderr: %s)", err, stderr)
		}
		var doc api.UserSubmittedIssueCreateResponseSchema
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
			t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
		}
		if len(doc.Results) != 2 || doc.Results[0].ThreadID == nil || *doc.Results[0].ThreadID != thread ||
			doc.Results[1].SubmittedBy == nil || doc.Results[1].SubmittedBy.Email != "dana@x.io" {
			t.Errorf("results = %+v, want the raw thread_id and submitted_by", doc.Results)
		}
		for _, want := range wantProse {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr missing %q:\n%s", want, stderr)
			}
		}
	})
}
