package rows

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const setCellRoute = "/api/orgs/" + testOrgID + "/datasets/42/modify-clean-cell"

// recordSetCell answers the route and keeps every body, in order. This verb
// makes exactly one call, so `record` would do — the list is here so a test can
// assert the WRITE NEVER HAPPENED without reading a count that a second call
// would silently overwrite.
func recordSetCell(mock *testutil.MockAPIServer, status int, resp string) *recordedCalls {
	got := &recordedCalls{}
	mock.RegisterHandler("POST", setCellRoute, func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = strings.TrimSpace(string(b))
		}
		got.bodies = append(got.bodies, body)
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	return got
}

// The route's whole answer, and the reason this verb reports no count.
const setCellOK = `{"result":"ok"}`

// --- the request body ------------------------------------------------------

// Byte-exact, and the KEY SET is as much of the contract as the values: an
// extra `"current_value": null` would overwrite the schema's NOT_PROVIDED
// sentinel, and a `data_source_model_id` or `map_value_once` would be a key
// nobody reads. internal/datasets pins the same four for the client; this pins
// what the COMMAND sends, which is the half a flag wired to the wrong argument
// would break.
func TestSetCell_BodyIsTheFourKeys(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSetCell(mock, http.StatusOK, setCellOK)

	if _, _, err := runRows(t, "rows", setCellVerb, "orders",
		"--row-id", "7", "--row-id", "9", "--field", "state", "--value", "California", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 1 {
		t.Fatalf("requests = %d, want 1", got.n())
	}
	const want = `{"field_name":"state","new_value":"California","action":"overwrite_value","ids":[7,9]}`
	if got.bodies[0] != want {
		t.Errorf("body = %s\nwant       %s", got.bodies[0], want)
	}
}

// The empty value is the documented way to blank a cell, so it has to survive
// as "" on the wire. A validator that tested the VALUE rather than the flag's
// presence would make it unreachable, and the ticket's premise — that the
// server's "Please enter a new value" 422 governs here — would have written
// exactly that validator: new_value is a required non-nullable str, so the
// message is unreachable from this command.
func TestSetCell_EmptyValueReachesTheWire(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSetCell(mock, http.StatusOK, setCellOK)

	stdout, _, err := runRows(t, "rows", setCellVerb, "orders",
		"--row-id", "7", "--field", "notes", "--value", "", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatalf("decode %q: %v", got.bodies[0], err)
	}
	if v, present := body["new_value"]; !present || v != "" {
		t.Errorf("new_value = %v (present=%v), want the empty string", v, present)
	}
	// The read-back names the row, not the empty value: on a money or percent
	// column an equals filter on '' is a 400.
	want := "qctl get data orders --is-bad-data --filter-by-field-name id --filter-by-field-value 7"
	if !strings.Contains(stdout, want) {
		t.Errorf("stdout does not carry the row-id recipe\n  %q\ngot: %q", want, stdout)
	}
}

// --- usage refusals, none of which may cost a request ----------------------

// Every refusal decidable from the command line exits 2 and costs NOTHING —
// the write request and the table resolve alike. The resolve is counted
// because it is the one request the ordering contract is about: before
// resolveTarget grew its localChecks hook, a verb's own flag checks ran after
// Bootstrap and a missing --field on a NAMED table spent a GET /datasets first.
func TestSetCell_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// want is a fragment of the message, so a refusal that fires for the
		// wrong reason (a different guard, or cobra's generic one) fails here
		// rather than passing on the exit code alone.
		want string
	}{
		{"no selector", []string{"--field", "state", "--value", "CA"}, "needs a row selector: --row-id"},
		{"no --field", []string{"--row-id", "7", "--value", "CA"}, "needs --field"},
		{"empty --field", []string{"--row-id", "7", "--field", "", "--value", "CA"}, "--field cannot be empty"},
		{"no --value", []string{"--row-id", "7", "--field", "state"}, "needs --value"},
		{"zero --row-id", []string{"--row-id", "0", "--field", "state", "--value", "CA"}, "--row-id must be a positive integer"},
		{"negative --row-id", []string{"--row-id", "-3", "--field", "state", "--value", "CA"}, "--row-id must be a positive integer"},
		// Registered in order to be refused: the message names the fix, where
		// cobra's unknown-flag error would name only the flag that failed.
		{"--signature", []string{"--signature", "ab", "--field", "state", "--value", "CA"},
			"--signature is not supported by 'qctl rows set-cell' at table scope; use --row-id."},
		{"--status", []string{"--status", "failed_validation", "--field", "state", "--value", "CA"},
			"--status is not supported by 'qctl rows set-cell' at table scope; use --row-id."},
		{"two selectors", []string{"--row-id", "7", "--status", "failed_validation", "--field", "state", "--value", "CA"},
			"takes exactly one row selector"},
		// Registered in order to be refused too, through the generic renderer
		// setCellSupport declares — see TestSetCell_AllIsRefusedWithNoRequest.
		{"--all", []string{"--all", "--field", "state", "--value", "CA"},
			"--all is not supported by 'qctl rows set-cell' at table scope; use --row-id."},
		{"--cloud-source", []string{"--row-id", "7", "--field", "s", "--value", "CA", "--cloud-source", "s3"},
			cobraUnknownFlag(t, "cloud-source")},
		{"--cloud-source-id", []string{"--row-id", "7", "--field", "s", "--value", "CA", "--cloud-source-id", "7"},
			cobraUnknownFlag(t, "cloud-source-id")},
		{"--file-id", []string{"--row-id", "7", "--field", "s", "--value", "CA", "--file-id", "3"},
			cobraUnknownFlag(t, "file-id")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSetCell(mock, http.StatusOK, setCellOK)
			hits := countTableResolves(mock)

			_, _, err := runRows(t, append([]string{"rows", setCellVerb, "orders"}, tc.args...)...)
			if err == nil {
				t.Fatal("expected the refusal")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got.n() != 0 {
				t.Errorf("the refusal spent %d modify-clean-cell request(s)", got.n())
			}
			if *hits != 0 {
				t.Errorf("the refusal spent %d table resolve(s)", *hits)
			}
		})
	}
}

// --all is recognized and refused, never an unknown flag: the whole message is
// pinned, and it is the generic sentence, since set-cell's route has no bulk
// form to point at.
//
// Every request is counted, not only the two the table above watches, and the
// cases cover each way to name the table: `orders` would otherwise cost a
// resolve. The last two put the refusal ahead of the verb's own --field and
// --value checks, which is resolveTarget's step 2 before step 2b.
func TestSetCell_AllIsRefusedWithNoRequest(t *testing.T) {
	const want = "--all is not supported by 'qctl rows set-cell' at table scope; use --row-id."
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"named table", []string{"orders", "--all", "--field", "state", "--value", "CA"}},
		{"numbered table", []string{"42", "--all", "--field", "state", "--value", "CA"}},
		{"--table with a space", []string{"--table", "my orders", "--all", "--field", "state", "--value", "CA"}},
		{"--table-id", []string{"--table-id", "42", "--all", "--field", "state", "--value", "CA"}},
		{"--yes does not bypass it", []string{"orders", "--all", "--field", "state", "--value", "CA", "--yes"}},
		{"before a missing --field and --value", []string{"orders", "--all"}},
		{"before a missing --value", []string{"orders", "--all", "--field", "state"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			stdout, stderr, err := runRows(t, append([]string{"rows", setCellVerb}, tc.args...)...)
			if err == nil {
				t.Fatal("expected --all to be refused")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want 2: %v", code, err)
			}
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
			if reqs := mock.Requests(); len(reqs) != 0 {
				t.Errorf("the refusal made %d request(s): %v", len(reqs), reqs)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("the refusal printed a prompt or a result: stdout=%q stderr=%q", stdout, stderr)
			}
		})
	}
}

// The refusal comes before Bootstrap, not merely before the network: with no
// credential stored at all, --all is still the answer. The control proves the
// environment really has none — a command line that gets PAST the selector
// checks fails on the missing login instead.
func TestSetCell_AllIsRefusedBeforeCredentialsAreRead(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)

	_, _, err := runRows(t, "rows", setCellVerb, "orders", "--all", "--field", "state", "--value", "CA", "--yes")
	if want := "--all is not supported by 'qctl rows set-cell' at table scope; use --row-id."; err == nil || err.Error() != want {
		t.Errorf("error = %v, want %q", err, want)
	}

	_, _, control := runRows(t, "rows", setCellVerb, "orders", "--row-id", "7", "--field", "state", "--value", "CA", "--yes")
	if control == nil || qerrors.GetExitCode(control) == qerrors.ExitBadUsage || !strings.Contains(control.Error(), "not logged in") {
		t.Fatalf("control: a valid command line with no credential = %v (exit %d); "+
			"the environment is not credential-free, so the case above proves nothing",
			control, qerrors.GetExitCode(control))
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("made %d request(s) with no credential: %v", len(reqs), reqs)
	}
}

func TestSetCell_MissingTableIsRefusedBeforeCredentialsAreRead(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)

	_, _, err := runRows(t, "rows", setCellVerb, "--row-id", "7", "--field", "state", "--value", "CA", "--yes")
	if err == nil || !strings.Contains(err.Error(), "table is required") {
		t.Fatalf("missing table with no credential = %v, want table-required error", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}

	_, _, control := runRows(t, "rows", setCellVerb, "--table-id", "8", "--row-id", "7", "--field", "state", "--value", "CA", "--yes")
	if control == nil || !strings.Contains(control.Error(), "not logged in") {
		t.Fatalf("control with a valid selector = %v, want not-logged-in error", control)
	}
	if got := qerrors.GetExitCode(control); got == qerrors.ExitBadUsage {
		t.Fatalf("control exit code = %d (ExitBadUsage); credential-free environment was not proved", got)
	}
	if reqs := mock.Requests(); len(reqs) != 0 {
		t.Errorf("made %d request(s) without a credential: %v", len(reqs), reqs)
	}
}

// --help lists --all as refused and names --row-id, and does not describe an
// operation on every quarantined row: the flag list is what a user scans before
// any Long text.
func TestSetCell_HelpListsAllAsRefused(t *testing.T) {
	newRowsTestEnv(t)
	stdout, _, err := runRows(t, "rows", setCellVerb, "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	var allLine string
	for _, l := range strings.Split(stdout, "\n") {
		if strings.Contains(l, "--all ") || strings.HasSuffix(l, "--all") {
			allLine = l
		}
	}
	if allLine == "" {
		t.Fatalf("--help lists no --all flag:\n%s", stdout)
	}
	if !strings.Contains(allLine, "Not supported") || !strings.Contains(allLine, "--row-id") {
		t.Errorf("--all help line = %q, want it to say it is unsupported and name --row-id", allLine)
	}
	if strings.Contains(stdout, "every quarantined row") {
		t.Errorf("--help advertises an operation on every quarantined row:\n%s", stdout)
	}
}

// --- the prompt ------------------------------------------------------------

// Declining is a CHOICE, not a usage error: exit 1 through ErrDeclined, with no
// request made.
func TestSetCell_DeclineExitsOneAndDoesNotWrite(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSetCell(mock, http.StatusOK, setCellOK)
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)

	_, _, err := runRows(t, "rows", setCellVerb, "orders",
		"--row-id", "7", "--field", "state", "--value", "CA")
	if err == nil {
		t.Fatal("expected the decline")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Errorf("GetExitCode = %d, want 1", code)
	}
	if got.n() != 0 {
		t.Errorf("a declined run wrote anyway: %d request(s)", got.n())
	}
}

// A non-interactive stdin is BAD USAGE: the only fix is to change the command
// line, and the message has to name the flag that does it.
//
// THE CASE OF THE VALUE IS THE POINT. The lead of this sentence is lowercased
// so it reads under "pass --yes to …", and it used to be lowercased with
// strings.ToLower — which was correct only while every gerund was a single
// capitalized word. Here the gerund carries USER TEXT, so ToLower told the user
// to pass --yes to set state to "ca" while the prompt one line up said "CA":
// two different values for one write. lowerLead touches the first rune only.
func TestSetCell_NonInteractiveNamesYesAndKeepsTheValuesCase(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	mock := newRowsTestEnv(t)
	got := recordSetCell(mock, http.StatusOK, setCellOK)

	_, stderr, err := runRows(t, "rows", setCellVerb, "orders",
		"--row-id", "7", "--row-id", "9", "--field", "State", "--value", "CA")
	if err == nil {
		t.Fatal("expected the non-interactive refusal")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("GetExitCode = %d, want 2: %v", code, err)
	}
	want := `pass --yes to set State to 'CA' on 2 row(s) in table orders (id: 42)`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err.Error(), want)
	}
	if strings.Contains(err.Error(), `'ca'`) || strings.Contains(err.Error(), "state to") {
		t.Errorf("the refusal lowercased user text: %q", err.Error())
	}
	if got.n() != 0 {
		t.Errorf("the refusal wrote anyway: %d request(s)", got.n())
	}
	if !strings.Contains(stderr, setCellIssueNote) {
		t.Errorf("the consequences did not print before the refusal: %q", stderr)
	}
}

// The consequences are the only warning a --yes script gets, so they still
// print with no prompt to attach to — and they print on stderr, so a script
// reading the result line off stdout is unaffected.
func TestSetCell_ConsequencesPrintUnderYes(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordSetCell(mock, http.StatusOK, setCellOK)

	stdout, stderr, err := runRows(t, "rows", setCellVerb, "orders",
		"--row-id", "7", "--field", "state", "--value", "CA", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, note := range []string{
		setCellReprocessNote, setCellSkipNote, setCellIssueNote, setCellHoldNote, setCellCompositeNote,
	} {
		if !strings.Contains(stderr, note) {
			t.Errorf("--yes dropped a consequence line:\n  %q\nstderr: %q", note, stderr)
		}
	}
	// sel.count() is exact for --row-id, so unsizedClause returns "" and
	// neither confirmAction lead may appear — not the prompt's "No count is
	// shown: …" and not the --yes path's "The prompt --yes skipped had no
	// count: …". Matched on those two leads and not on the words "no count",
	// which setCellSkipNote legitimately contains.
	for _, lead := range []string{"No count is shown", "The prompt --yes skipped had no count"} {
		if strings.Contains(stderr, lead) {
			t.Errorf("a counted selector printed %q: %q", lead, stderr)
		}
	}
	if strings.Contains(stdout, "?") {
		t.Errorf("--yes printed a prompt: %q", stdout)
	}
}

// --- the server's own failures ---------------------------------------------

func TestSetCell_ServerStatusesKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		exit   int
		want   string
	}{
		{
			// A reserved or unknown field is a 400 on BOTH branches, and the
			// server's own message is the only thing that says which.
			name: "400 unknown field", status: http.StatusBadRequest,
			body: `{"detail":{"msg":"Field \"nope\" does not exist in this dataset.","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "does not exist in this dataset",
		},
		{
			name: "400 reserved field", status: http.StatusBadRequest,
			body: `{"detail":{"msg":"Cannot overwrite the reserved column \"id\".","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "reserved column",
		},
		{
			name: "422 empty ids", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"No ids are passed.","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "No ids are passed",
		},
		{
			// The composite branch's live-row check, and the table-not-found
			// answer, share this code.
			name: "404 no such row", status: http.StatusNotFound,
			body: `{"detail":{"msg":"Rows [7] are not found."}}`,
			exit: qerrors.ExitNotFound, want: "",
		},
		{
			name: "401 unauthenticated", status: http.StatusUnauthorized,
			body: `{"detail":{"msg":"Not authenticated"}}`,
			exit: qerrors.ExitUnauthorized, want: "",
		},
		{
			name: "500 server error", status: http.StatusInternalServerError,
			body: `{"detail":{"msg":"Internal Server Error"}}`,
			exit: qerrors.ExitGenericError, want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSetCell(mock, tc.status, tc.body)

			_, _, err := runRows(t, "rows", setCellVerb, "orders",
				"--row-id", "7", "--field", "state", "--value", "CA", "--yes")
			if err == nil {
				t.Fatal("expected the server error to be reported")
			}
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Errorf("GetExitCode = %d, want %d: %v", code, tc.exit, err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want the server's own message %q", err.Error(), tc.want)
			}
			if got.n() != 1 {
				t.Errorf("requests = %d, want 1", got.n())
			}
		})
	}
}

// --- the printed lines -----------------------------------------------------

// Between them these fixtures reach every line reportSetCellResult can write:
// the result line with one row and with many, and one recipe per distinct row
// id whatever the value.
func TestSetCell_ResultLineAndRecipe(t *testing.T) {
	const byID = "qctl get data orders --is-bad-data --filter-by-field-name id --filter-by-field-value "
	cases := []struct {
		name    string
		args    []string
		want    []string
		absent  []string
		recipes []string
	}{
		{
			name: "one row",
			args: []string{"--row-id", "7", "--field", "state", "--value", "CA"},
			want: []string{
				`set-cell: requested state = "CA" on 1 row(s) in table orders (id: 42) (this route returns no affected-row count)`,
			},
			recipes: []string{byID + "7"},
		},
		{
			// One line per row, in the order given, under one heading.
			name: "many rows",
			args: []string{"--row-id", "7", "--row-id", "9,11", "--field", "state", "--value", "CA"},
			want: []string{
				`set-cell: requested state = "CA" on 3 row(s) in table orders (id: 42) (this route returns no affected-row count)`,
			},
			recipes: []string{byID + "7", byID + "9", byID + "11"},
		},
		{
			name:    "a repeated row id",
			args:    []string{"--row-id", "7", "--row-id", "7", "--field", "state", "--value", "CA"},
			want:    []string{`set-cell: requested state = "CA" on 1 row(s) in table orders (id: 42)`},
			recipes: []string{byID + "7"},
		},
		{
			// On a money or percent column an equals filter on a value that is
			// not a number is a 400, so no recipe filters on the value.
			name:    "a value that is not a number",
			args:    []string{"--row-id", "7", "--field", "amount", "--value", "N/A"},
			want:    []string{`set-cell: requested amount = "N/A" on 1 row(s) in table orders (id: 42)`},
			absent:  []string{"--filter-by-field-name amount", "--filter-by-field-value N/A"},
			recipes: []string{byID + "7"},
		},
		{
			name:    "an empty value",
			args:    []string{"--row-id", "7", "--field", "notes", "--value", ""},
			want:    []string{`set-cell: requested notes = "" on 1 row(s) in table orders (id: 42)`},
			absent:  []string{"--filter-by-field-name notes", "--filter-by-field-value ''"},
			recipes: []string{byID + "7"},
		},
		{
			// A numbered table has no name to print, so the recipe carries the
			// id — idRef's job, and the reason getDataLine is the one builder.
			// The result line says "table 42" and not "table 42 (id: 42)": the
			// parenthesis exists to name the dataset a NAME resolved to, and
			// there is no name here. See scope.tableLabel.
			name: "a table named by id",
			args: []string{"--table-id", "42", "--row-id", "7", "--field", "state", "--value", "CA"},
			want: []string{
				`set-cell: requested state = "CA" on 1 row(s) in table 42`,
			},
			recipes: []string{"qctl get data 42 --is-bad-data --filter-by-field-name id --filter-by-field-value 7"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			recordSetCell(mock, http.StatusOK, setCellOK)

			args := []string{"rows", setCellVerb}
			if tc.name != "a table named by id" {
				args = append(args, "orders")
			}
			args = append(append(args, tc.args...), "--yes")
			stdout, _, err := runRows(t, args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, want := range tc.want {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout does not carry\n  %q\ngot: %q", want, stdout)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(stdout, absent) {
					t.Errorf("stdout carries %q and should not: %q", absent, stdout)
				}
			}
			// A suffix, so a line the case does not list fails it.
			block := "\n" + followUpHeading + ":\n  " + strings.Join(tc.recipes, "\n  ") + "\n"
			if !strings.HasSuffix(stdout, block) {
				t.Errorf("stdout does not end with the recipes\n  %q\ngot: %q", block, stdout)
			}
		})
	}
}
