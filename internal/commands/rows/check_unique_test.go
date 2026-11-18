package rows

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const checkUniqueRoute = "/api/orgs/" + testOrgID + "/datasets/42/check-column-uniqueness"

// The three answers the handler can produce, as the wire spells them
// (atlas check_column_uniqueness_core, data_view_core.py). All three carry every
// field, because the route is declared response_model_exclude_unset=False.
const (
	uniqueOK = `{"columns":["order_id"],"total_rows":1200,"null_count":0,` +
		`"is_unique":true,"reason":null,"duplicate_examples":[]}`
	uniqueNulls = `{"columns":["email"],"total_rows":500,"null_count":12,` +
		`"is_unique":false,"reason":"contains_nulls","duplicate_examples":[]}`
	uniqueDuplicates = `{"columns":["sku","region"],"total_rows":90,"null_count":0,` +
		`"is_unique":false,"reason":"has_duplicates","duplicate_examples":[` +
		`{"values":{"sku":"A1","region":"EU"},"count":4},` +
		`{"values":{"sku":"B2","region":"US"},"count":2}]}`
)

// recordCheckUnique answers the route and keeps every body, in order. The verb
// makes exactly one call, so the list is here to assert that a refused command
// line made NONE — a count a second call could silently overwrite.
func recordCheckUnique(mock *testutil.MockAPIServer, status int, resp string) *recordedCalls {
	got := &recordedCalls{}
	mock.RegisterHandler("POST", checkUniqueRoute, func(w http.ResponseWriter, r *http.Request) {
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

// --- the verdict and its exit code -----------------------------------------

// The unique answer: exit 0, both lines, and a body carrying the columns in the
// order they were typed. Order is part of the contract — the server echoes it
// and the duplicate groups are keyed by it.
func TestCheckUnique_UniqueExitsZero(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordCheckUnique(mock, http.StatusOK, uniqueOK)

	stdout, stderr, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "order_id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 1 {
		t.Fatalf("requests = %d, want 1", got.n())
	}
	if want := `{"columns":["order_id"]}`; got.bodies[0] != want {
		t.Errorf("body = %s, want %s", got.bodies[0], want)
	}
	if want := "unique on order_id: yes\n"; !strings.HasPrefix(stdout, want) {
		t.Errorf("stdout does not open with %q\ngot: %q", want, stdout)
	}
	if want := "1200 clean row(s) checked (deleted rows excluded); 0 with a NULL in any of the columns"; !strings.Contains(stdout, want) {
		t.Errorf("stdout is missing the counts line %q\ngot: %q", want, stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// The whole point of the verb for a script: a "no" is exit 1, and it is SILENT,
// because the reason is already on stdout. Asserting the code and not the text,
// per CLAUDE.md — a substring assertion passes against a broken Unwrap chain.
func TestCheckUnique_NotUniqueExitsOneSilently(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp string
		want string
	}{
		{"duplicates", uniqueDuplicates, "unique on sku, region: no (has duplicates)"},
		{"nulls", uniqueNulls, "unique on email: no (contains NULLs)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			recordCheckUnique(mock, http.StatusOK, tc.resp)

			args := []string{"rows", checkUniqueVerb, "orders", "--column", "email"}
			if tc.name == "duplicates" {
				args = []string{"rows", checkUniqueVerb, "orders", "--column", "sku", "--column", "region"}
			}
			stdout, stderr, err := runRows(t, args...)
			if err == nil {
				t.Fatal("a not-unique verdict exited 0; want exit 1")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitGenericError)
			}
			if !errors.Is(err, errNotUnique) {
				t.Errorf("error = %v, want the silent not-unique sentinel", err)
			}
			if err.Error() != "" {
				t.Errorf("the verdict error carries the message %q; it must be silent", err.Error())
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty — the verdict belongs on stdout", stderr)
			}
			if !strings.Contains(stdout, tc.want) {
				t.Errorf("stdout is missing %q\ngot: %q", tc.want, stdout)
			}
		})
	}
}

// --- the two renderings of "no" --------------------------------------------

// has_duplicates: the sample groups as a table, in SERVER order (largest
// first), with the count column beside the requested ones, and the line saying
// the sample is capped at five.
func TestCheckUnique_DuplicateGroupsRenderAsATable(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueDuplicates)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "sku", "--column", "region")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	for _, want := range []string{"SKU", "REGION", "DUP-COUNT", "A1", "EU", "B2", "US"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q\ngot: %q", want, stdout)
		}
	}
	if i, j := strings.Index(stdout, "A1"), strings.Index(stdout, "B2"); i > j {
		t.Errorf("groups are not in server order (largest first)\ngot: %q", stdout)
	}
	if want := "at most five duplicate groups"; !strings.Contains(stdout, want) {
		t.Errorf("stdout does not say the sample is capped (%q)\ngot: %q", want, stdout)
	}
}

// A piped group table is not cut: a value wider than the 80 cells a terminal
// gets prints whole, so it can be copied into the next command. A passed
// --max-column-width still cuts it.
func TestCheckUnique_PipedGroupValuesAreNotCut(t *testing.T) {
	long := strings.Repeat("customer-reference-", 6) + "end"
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK,
		`{"columns":["ref"],"total_rows":9,"null_count":0,"is_unique":false,`+
			`"reason":"has_duplicates","duplicate_examples":[{"values":{"ref":"`+long+`"},"count":2}]}`)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "ref")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, long) {
		t.Errorf("piped: stdout does not carry the %d-cell value whole\ngot: %q", len(long), stdout)
	}

	stdout, _, err = runRows(t, "rows", checkUniqueVerb, "orders", "--column", "ref", "--max-column-width=40")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if strings.Contains(stdout, long) || !strings.Contains(stdout, long[:37]+"...") {
		t.Errorf("--max-column-width=40: the value was not cut to 40 cells\ngot: %q", stdout)
	}
}

// contains_nulls: NO table, and the line that stops an empty group list being
// read as "no duplicates". The short-circuit is the single most misleading
// thing about this route, so the rendering says it rather than leaving two
// identical-looking outputs to mean opposite things.
func TestCheckUnique_ContainsNullsSaysGroupsWereNotComputed(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueNulls)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "email")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if want := "500 clean row(s) checked (deleted rows excluded); 12 with a NULL in any of the columns"; !strings.Contains(stdout, want) {
		t.Errorf("stdout is missing the counts line %q\ngot: %q", want, stdout)
	}
	if want := "Duplicate groups are not computed when a column contains NULLs"; !strings.Contains(stdout, want) {
		t.Errorf("stdout does not explain the empty group list\ngot: %q", stdout)
	}
	if strings.Contains(stdout, "DUP-COUNT") {
		t.Errorf("a group table was rendered under contains_nulls\ngot: %q", stdout)
	}
	if strings.Contains(stdout, "at most five") {
		t.Errorf("the five-group caveat was printed with no groups\ngot: %q", stdout)
	}
}

// A unique verdict prints neither the table nor either caveat: the two lines
// are the whole answer.
func TestCheckUnique_UniqueVerdictPrintsNoCaveats(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueOK)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "order_id")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, unwanted := range []string{"DUP-COUNT", "at most five", "not computed"} {
		if strings.Contains(stdout, unwanted) {
			t.Errorf("a unique verdict printed %q\ngot: %q", unwanted, stdout)
		}
	}
}

// A reason this build does not know is printed VERBATIM rather than mapped onto
// one of the two it does — the honest-default rule scope.kindName follows. The
// verdict half (`no`) comes from is_unique, which is always on the wire, so a
// new reason still exits 1 and still says why as far as it can.
func TestCheckUnique_UnknownReasonIsPrintedVerbatim(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK,
		`{"columns":["sku"],"total_rows":9,"null_count":0,"is_unique":false,`+
			`"reason":"collides_with_partition","duplicate_examples":[]}`)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "sku")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if want := "unique on sku: no (collides_with_partition)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout is missing %q\ngot: %q", want, stdout)
	}
}

// A "no" with no reason at all still exits 1 and says the server gave none,
// rather than silently rendering an empty parenthesis or inventing a cause.
func TestCheckUnique_MissingReasonIsReportedNotGuessed(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK,
		`{"columns":["sku"],"total_rows":9,"null_count":0,"is_unique":false,"reason":null,"duplicate_examples":[]}`)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "sku")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if want := "no (the server gave no reason)"; !strings.Contains(stdout, want) {
		t.Errorf("stdout is missing %q\ngot: %q", want, stdout)
	}
}

// --- usage refusals, none of which may cost a request ----------------------

// Every refusal decidable from the command line exits 2 and costs NOTHING —
// the check request and the table resolve alike. The three column checks mirror
// the route's pydantic validator, whose object-shaped 422 the generated client
// cannot decode; spending a request to get a worse message is the defect they
// exist to prevent.
func TestCheckUnique_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"no --column", []string{}, "needs --column"},
		{"empty --column", []string{"--column", ""}, "--column cannot be empty"},
		{"whitespace --column", []string{"--column", "   "}, "--column cannot be empty"},
		{"duplicate --column", []string{"--column", "sku", "--column", "sku"}, "--column sku is repeated"},
		// The route has no data_source_model_id and no selector, so neither
		// flag is registered and cobra's own unknown-flag error answers — at
		// exit 2, through the root's FlagErrorFunc.
		{"--cloud-source", []string{"--column", "sku", "--cloud-source", "s3"}, cobraUnknownFlag(t, "cloud-source")},
		{"--file-id", []string{"--column", "sku", "--file-id", "3"}, cobraUnknownFlag(t, "file-id")},
		{"--row-id", []string{"--column", "sku", "--row-id", "7"}, cobraUnknownFlag(t, "row-id")},
		{"--all", []string{"--column", "sku", "--all"}, cobraUnknownFlag(t, "all")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			resolves := countTableResolves(mock)
			got := recordCheckUnique(mock, http.StatusOK, uniqueOK)

			args := append([]string{"rows", checkUniqueVerb, "orders"}, tc.args...)
			_, _, err := runRows(t, args...)
			if err == nil {
				t.Fatal("expected a refusal")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (%v)", code, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err.Error(), tc.want)
			}
			if got.n() != 0 {
				t.Errorf("the refusal spent %d uniqueness request(s); want 0", got.n())
			}
			if *resolves != 0 {
				t.Errorf("the refusal spent %d table resolve(s); want 0", *resolves)
			}
		})
	}
}

// `--column a --column A` is two columns, not a duplicate: the server's own
// check is over the exact strings (`len(set(cleaned)) != len(cleaned)`), and a
// case-folding check here would refuse a key the route accepts.
func TestCheckUnique_DuplicateCheckIsCaseSensitive(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordCheckUnique(mock, http.StatusOK, uniqueOK)

	if _, _, err := runRows(t, "rows", checkUniqueVerb, "orders",
		"--column", "sku", "--column", "SKU"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := `{"columns":["sku","SKU"]}`; got.bodies[0] != want {
		t.Errorf("body = %s, want %s", got.bodies[0], want)
	}
}

// A column name is sent exactly as typed, padding included: the server appends
// the ORIGINAL string and matches it against the table's field names, so
// trimming here would ask a different question than the one the user wrote and
// then report the answer under their spelling.
func TestCheckUnique_PaddedColumnIsSentUntrimmed(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordCheckUnique(mock, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"unknown column(s): [' sku']"}}`)

	_, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", " sku")
	if err == nil {
		t.Fatal("expected the server's unknown-column refusal")
	}
	if want := `{"columns":[" sku"]}`; got.bodies[0] != want {
		t.Errorf("body = %s, want %s", got.bodies[0], want)
	}
}

// --- output formats ---------------------------------------------------------

// -o json is the raw response, not a re-rendering of it: a script asked for the
// server's own object and the verdict's exit code still rides with it.
func TestCheckUnique_JSONIsThePassthroughAndKeepsTheExitCode(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueDuplicates)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders",
		"--column", "sku", "--column", "region", "-o", "json")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1 — the format must not change the verdict", code)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON (%v): %q", err, stdout)
	}
	if got["reason"] != "has_duplicates" || got["total_rows"] != float64(90) {
		t.Errorf("payload = %v, want the server's wire keys verbatim", got)
	}
	if _, ok := got["duplicate_examples"]; !ok {
		t.Errorf("payload = %v, want duplicate_examples carried through", got)
	}
	if strings.Contains(stdout, "unique on") {
		t.Errorf("the text verdict leaked into -o json\ngot: %q", stdout)
	}
}

func TestCheckUnique_YAMLIsThePassthrough(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueOK)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "order_id", "-o", "yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"is_unique: true", "total_rows: 1200", "null_count: 0"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q\ngot: %q", want, stdout)
		}
	}
	if strings.Contains(stdout, "unique on") {
		t.Errorf("the text verdict leaked into -o yaml\ngot: %q", stdout)
	}
}

// -o name is refused with exit 2 rather than rendering an empty line at exit 0,
// which a script would read as "unique". The declaration is what refuses it, so
// both halves are asserted: the annotation, and the run that root's
// PersistentPreRunE stops.
func TestCheckUnique_NameFormatIsRefused(t *testing.T) {
	cmd := newCheckUniqueCommand()
	if got := strings.Join(cmdutil.SupportedOutputFormats(cmd), ","); got != "table,json,yaml" {
		t.Errorf("declared formats = %q, want table,json,yaml", got)
	}

	mock := newRowsTestEnv(t)
	got := recordCheckUnique(mock, http.StatusOK, uniqueOK)
	_, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "order_id", "-o", "name")
	if err == nil {
		t.Fatal("-o name was accepted; it has no name view")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if got.n() != 0 {
		t.Errorf("-o name spent %d request(s); want 0", got.n())
	}
}

// --- server failures --------------------------------------------------------

// The two 4xx shapes that reach a user who typed something reasonable, and the
// 504 the route's own 30-second statement timeout produces. Codes, not text:
// a broken Unwrap chain exits 1 for all three while every message still reads
// perfectly.
func TestCheckUnique_ServerFailuresCarryTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		msg    string
		status int
		exit   int
	}{
		{"unknown column", `{"detail":{"msg":"unknown column(s): ['nope']"}}`,
			"unknown column(s)", http.StatusUnprocessableEntity, qerrors.ExitBadUsage},
		{"forbidden", `{"detail":{"msg":"forbidden"}}`,
			"forbidden", http.StatusForbidden, qerrors.ExitUnauthorized},
		{"statement timeout", `{"detail":{"msg":"uniqueness check timed out; reduce scope or retry"}}`,
			"timed out", http.StatusGatewayTimeout, qerrors.ExitGenericError},
		{"server error", `{"detail":{"msg":"boom"}}`,
			"boom", http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			recordCheckUnique(mock, tc.status, tc.body)

			_, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "nope")
			if err == nil {
				t.Fatal("expected a failure")
			}
			if code := qerrors.GetExitCode(err); code != tc.exit {
				t.Errorf("exit code = %d, want %d (%v)", code, tc.exit, err)
			}
			if !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("error = %q, want the server's message %q", err.Error(), tc.msg)
			}
			// A real failure must NOT look like a verdict: the silent sentinel
			// is what a script reads as "not unique", and a 504 wearing it
			// would report a timed-out check as an answer.
			if errors.Is(err, errNotUnique) {
				t.Error("a server failure was reported as the not-unique verdict")
			}
		})
	}
}

// --- the renderers ----------------------------------------------------------

// The count column is chosen against the requested columns, because the row
// maps tableui.Render reads are keyed by column NAME: a table with a column
// actually called dup_count would otherwise have its values overwritten by the
// counts, silently.
//
// The fallback has to be VISIBLY different, not merely different. The first
// version appended an underscore, which rendered as `DUP-COUNT-` next to the
// user's own `DUP-COUNT` -- two headers a reader cannot tell apart at a
// glance, which is the same failure in a new costume.
func TestCountColumnAvoidsACollision(t *testing.T) {
	for _, tc := range []struct {
		want    string
		columns []string
	}{
		{"dup_count", []string{"sku", "region"}},
		{"duplicate_rows", []string{"sku", "dup_count"}},
		{"duplicate_rows_", []string{"dup_count", "duplicate_rows"}},
		{"duplicate_rows__", []string{"dup_count", "duplicate_rows", "duplicate_rows_"}},
	} {
		if got := countColumn(tc.columns); got != tc.want {
			t.Errorf("countColumn(%v) = %q, want %q", tc.columns, got, tc.want)
		}
	}
}

// The chosen name must not be a near-miss of any column the user asked about:
// the whole point is that a reader can tell the counts from their own data.
//
// This holds while a distinct WORD is still available, which covers every real
// table. Once a caller has taken both dup_count and duplicate_rows, appending
// underscores is the last resort and is exempt -- there is no third name worth
// inventing for a table that contrived.
func TestCountColumnIsNotANearMissOfAUserColumn(t *testing.T) {
	for _, columns := range [][]string{
		{"dup_count"},
		{"sku", "dup_count"},
		{"dup_count", "region"},
	} {
		got := countColumn(columns)
		for _, c := range columns {
			if strings.TrimRight(got, "_") == strings.TrimRight(c, "_") {
				t.Errorf("countColumn(%v) = %q, which differs from %q only in trailing underscores",
					columns, got, c)
			}
		}
	}
}

// A whole number prints as an integer whether it arrives as an int64 or as a
// float64, and 1042.0000 or 1.234567e+06 is not an id anybody typed. nil is
// <null> rather than blank: on this route the empty string is an ordinary value
// and a perfectly good duplicate, so the two must not render the same.
func TestUniquenessCellFormatsWireValues(t *testing.T) {
	for _, tc := range []struct {
		in   interface{}
		want string
	}{
		{"A1", "A1"},
		{"", ""},
		{float64(1042), "1042"},
		{float64(1234567), "1234567"},
		{float64(-1234567), "-1234567"},
		{float64(1<<53 - 1), "9007199254740991"},
		{float64(10.5), "10.5"},
		{int64(12345678901234567), "12345678901234567"},
		{json.Number("123456789012345678901234"), "123456789012345678901234"},
		{true, "true"},
		{nil, "<null>"},
	} {
		if got := uniquenessCell(tc.in); got != tc.want {
			t.Errorf("uniquenessCell(%#v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Numbers survive the whole path, not just the formatter: the group table is
// where an id renders, and 1042 must not arrive as 1042.0000.
func TestCheckUnique_NumericGroupValuesRenderAsIntegers(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK,
		`{"columns":["order_id"],"total_rows":9,"null_count":0,"is_unique":false,`+
			`"reason":"has_duplicates","duplicate_examples":[{"values":{"order_id":1042},"count":3}]}`)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "order_id")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, "1042") || strings.Contains(stdout, "1042.") {
		t.Errorf("stdout does not carry the id as an integer\ngot: %q", stdout)
	}
}

// uniqueWideNumbers is a duplicate group whose values are an integer past 2^53,
// an integer float64 prints in exponent form, and the same number spelled as a
// float literal.
const uniqueWideNumbers = `{"columns":["order_id","qty","total"],"total_rows":9,"null_count":0,` +
	`"is_unique":false,"reason":"has_duplicates","duplicate_examples":[` +
	`{"values":{"order_id":12345678901234567,"qty":1234567,"total":1234567.0},"count":2}]}`

var wholeWordQty = regexp.MustCompile(`\b1234567\b`)

// The text view prints every one of those as the integer it is: no exponent
// form, and no rounding of the value past 2^53.
func TestCheckUnique_WideNumbersRenderExactlyInTheTable(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK, uniqueWideNumbers)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders",
		"--column", "order_id", "--column", "qty", "--column", "total")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if !strings.Contains(stdout, "12345678901234567") {
		t.Errorf("stdout does not carry 12345678901234567 exactly\ngot: %q", stdout)
	}
	if n := len(wholeWordQty.FindAllString(stdout, -1)); n != 2 {
		t.Errorf("stdout carries 1234567 as a whole value %d time(s), want 2 (qty and total)\ngot: %q", n, stdout)
	}
	if strings.Contains(stdout, "e+") {
		t.Errorf("stdout prints a number in exponent form\ngot: %q", stdout)
	}
}

// -o json and -o yaml carry the integer past 2^53 unrounded.
func TestCheckUnique_WideNumbersSurviveJSONAndYAML(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			recordCheckUnique(mock, http.StatusOK, uniqueWideNumbers)

			stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders",
				"--column", "order_id", "--column", "qty", "--column", "total", "-o", format)
			if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
				t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
			}
			if !strings.Contains(stdout, "12345678901234567") {
				t.Errorf("stdout does not carry 12345678901234567 exactly\ngot: %q", stdout)
			}
			if strings.Contains(stdout, "12345678901234568") {
				t.Errorf("stdout carries the float64-rounded 12345678901234568\ngot: %q", stdout)
			}
			if strings.Contains(stdout, "e+") {
				t.Errorf("stdout prints a number in exponent form\ngot: %q", stdout)
			}
		})
	}
}

// --table-id names the table without the positional, like every other verb in
// the group, and then nothing is resolved: the id is the id.
func TestCheckUnique_TableIDFlagWithoutPositional(t *testing.T) {
	mock := newRowsTestEnv(t)
	resolves := countTableResolves(mock)
	got := recordCheckUnique(mock, http.StatusOK, uniqueOK)

	if _, _, err := runRows(t, "rows", checkUniqueVerb, "--table-id", "42", "--column", "order_id"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 1 {
		t.Errorf("requests = %d, want 1", got.n())
	}
	if *resolves != 0 {
		t.Errorf("a numeric --table-id cost %d resolve(s); want 0", *resolves)
	}
}

// A viewer who sees a requested column masked gets its group values as mask
// text and masked_fields naming it (atlas capi/capi/controllers/pii_masking.py
// mask_duplicate_examples). The groups print the masks as sent, and a line
// names the masked column, since two different values can share one mask.
func TestCheckUnique_MaskedGroupValuesPrintAsSentAndAreNamed(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordCheckUnique(mock, http.StatusOK,
		`{"columns":["email","region"],"total_rows":9,"null_count":0,"is_unique":false,`+
			`"reason":"has_duplicates","masked_fields":["email"],"duplicate_examples":[`+
			`{"values":{"email":"•••••.com","region":"EU"},"count":3},`+
			`{"values":{"email":"•••••.com","region":"US"},"count":2}]}`)

	stdout, _, err := runRows(t, "rows", checkUniqueVerb, "orders", "--column", "email", "--column", "region")
	if qerrors.GetExitCode(err) != qerrors.ExitGenericError {
		t.Fatalf("exit code = %d, want 1", qerrors.GetExitCode(err))
	}
	if strings.Count(stdout, "•••••.com") != 2 {
		t.Errorf("stdout does not carry both masks as sent\ngot: %q", stdout)
	}
	if want := "Masked fields: email. Each value is •••••, or ••••• and its last 4 characters, so two groups can print alike.\n"; !strings.Contains(stdout, want) {
		t.Errorf("stdout does not name the masked column (%q)\ngot: %q", want, stdout)
	}

	recordCheckUnique(mock, http.StatusOK, uniqueDuplicates)
	stdout, _, _ = runRows(t, "rows", checkUniqueVerb, "orders", "--column", "sku", "--column", "region")
	if strings.Contains(stdout, "Masked") {
		t.Errorf("a response with no masked_fields names a masked column\ngot: %q", stdout)
	}
}
