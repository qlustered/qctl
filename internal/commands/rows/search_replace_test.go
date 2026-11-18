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

const searchReplaceRoute = "/api/orgs/" + testOrgID + "/datasets/42/search-and-replace"

// recordedCalls is the group's `record` with the one thing search-replace needs
// that no other verb does: every body, in order.
//
// `record` keeps only the LAST request it saw, which is enough for the verbs
// that make one call. This verb makes TWO calls to the SAME path — the dry run
// and the write — so a last-write-wins recorder cannot tell "previewed then
// wrote" from "wrote twice", which is the single most important thing to assert
// about it.
type recordedCalls struct {
	bodies []string
}

func (r *recordedCalls) n() int { return len(r.bodies) }

// decoded is one captured body as a map, so an assertion names the wire key it
// cares about instead of pinning the whole JSON — the field order of
// api.SearchAndReplaceRequestSchema is not this command's contract.
func (r *recordedCalls) decoded(t *testing.T, i int) map[string]any {
	t.Helper()
	if i >= len(r.bodies) {
		t.Fatalf("request %d was never made; %d request(s) in total", i, len(r.bodies))
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.bodies[i]), &body); err != nil {
		t.Fatalf("decode request %d %q: %v", i, r.bodies[i], err)
	}
	return body
}

// recordSearchReplace answers the route with one response per call: the first
// response for the dry run, the second for the write. The LAST response is
// reused once the list runs out, so a test that cares about only one shape
// passes one.
func recordSearchReplace(mock *testutil.MockAPIServer, responses ...string) *recordedCalls {
	got := &recordedCalls{}
	mock.RegisterHandler("POST", searchReplaceRoute, func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = strings.TrimSpace(string(b))
		}
		got.bodies = append(got.bodies, body)
		resp := responses[len(responses)-1]
		if len(got.bodies) <= len(responses) {
			resp = responses[len(got.bodies)-1]
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(resp))
	})
	return got
}

func recordSearchReplaceStatus(mock *testutil.MockAPIServer, status int, resp string) *recordedCalls {
	got := &recordedCalls{}
	mock.RegisterHandler("POST", searchReplaceRoute, func(w http.ResponseWriter, r *http.Request) {
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

const previewOnly = `{"result":"ok","matched_rows":7,"replaced_rows":0,"skipped_rows":0,"fields_affected":[]}`
const writeDone = `{"result":"ok","matched_rows":7,"replaced_rows":5,"skipped_rows":2,"fields_affected":["city","state"]}`
const noMatch = `{"result":"ok","matched_rows":0,"replaced_rows":0,"skipped_rows":0,"fields_affected":[]}`

// --- the four invocation shapes ------------------------------------------

// --dry-run is ONE request and it is the preview. The second request is what a
// "preview then write" runner would make here, so its absence is the assertion,
// not the exit code.
func TestSearchReplace_DryRunMakesOneRequestAndWritesNothing(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, previewOnly)

	stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown", "--dry-run")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 1 {
		t.Fatalf("requests = %d, want 1 — a --dry-run must never reach the write", got.n())
	}
	if got.decoded(t, 0)["dry_run"] != true {
		t.Errorf("dry_run = %v, want true", got.decoded(t, 0)["dry_run"])
	}
	if !strings.Contains(stdout, "search-replace (dry run): 7 matching row(s) in table orders (id: 42)") {
		t.Errorf("stdout does not carry the preview line: %q", stdout)
	}
	if strings.Contains(stdout, "row(s) replaced") {
		t.Errorf("a dry run reported a write: %q", stdout)
	}
}

// The default shape: preview, prompt, write — in that order, on the same path.
func TestSearchReplace_PreviewThenWrite(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, previewOnly, writeDone)
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)

	stdout, stderr, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 2 {
		t.Fatalf("requests = %d, want 2 (the dry run, then the write)", got.n())
	}
	if got.decoded(t, 0)["dry_run"] != true {
		t.Errorf("the first request is not the preview: %q", got.bodies[0])
	}
	if got.decoded(t, 1)["dry_run"] != false {
		t.Errorf("the second request is not the write: %q", got.bodies[1])
	}
	// dry_run is the ONLY difference between them; a runner that rebuilt the
	// body from the flags for the write could drop the field scope or the
	// source narrowing and every other assertion here would still pass.
	for _, key := range []string{"search_term", "replace_term", "match_mode", "field_name", "data_source_model_id"} {
		if got.decoded(t, 0)[key] != got.decoded(t, 1)[key] {
			t.Errorf("%s differs between the preview and the write: %v vs %v",
				key, got.decoded(t, 0)[key], got.decoded(t, 1)[key])
		}
	}
	if !strings.Contains(stdout, "(dry run): 7 matching row(s)") {
		t.Errorf("stdout does not carry the preview line: %q", stdout)
	}
	if !strings.Contains(stdout, "search-replace: 5 row(s) replaced, 2 skipped (7 matched) in table orders (id: 42); fields: city, state") {
		t.Errorf("stdout does not carry the result line: %q", stdout)
	}
	if !strings.Contains(stderr, "queued for re-validation") || !strings.Contains(stderr, "revokes an approval") {
		t.Errorf("stderr does not carry both consequence lines: %q", stderr)
	}
}

// --yes is ONE request and it is the write. Spending a preview nobody will see
// is the defect this pins, and only the request count can see it: the result
// line is identical either way.
func TestSearchReplace_YesMakesOneRequestAndSkipsThePreview(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, writeDone)

	stdout, stderr, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.n() != 1 {
		t.Fatalf("requests = %d, want 1 — --yes must not spend a preview", got.n())
	}
	if got.decoded(t, 0)["dry_run"] != false {
		t.Errorf("dry_run = %v, want false", got.decoded(t, 0)["dry_run"])
	}
	if strings.Contains(stdout, "dry run") {
		t.Errorf("--yes printed a preview line: %q", stdout)
	}
	// The consequences are the only warning a --yes script gets, so they still
	// print with no prompt to attach to.
	if !strings.Contains(stderr, "queued for re-validation") || !strings.Contains(stderr, "revokes an approval") {
		t.Errorf("--yes dropped the consequence lines: %q", stderr)
	}
}

// Nothing matched: stop at exit 0 with NO second request. A write here could
// only answer 0/0/0 after a second round trip, and the grid makes the same call
// by disabling Replace on a zero count.
func TestSearchReplace_ZeroMatchesShortCircuits(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, noMatch)
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)

	stdout, stderr, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown")
	if err != nil {
		t.Fatalf("a zero-match run is a no-op at exit 0, got: %v", err)
	}
	if got.n() != 1 {
		t.Fatalf("requests = %d, want 1 — nothing matched, so there is nothing to write", got.n())
	}
	if !strings.Contains(stdout, "Nothing to replace") {
		t.Errorf("stdout does not say the run changed nothing: %q", stdout)
	}
	// The consequences describe a write that will not happen, so they must not
	// print — and neither must the prompt that would have followed them.
	if strings.Contains(stderr, "queued for re-validation") {
		t.Errorf("a zero-match run warned about a write it never makes: %q", stderr)
	}
}

// --- the request body ------------------------------------------------------

// All four generated values are reachable, including the one cosmosview cannot
// express. The matrix is driven end to end rather than through matchMode alone:
// a run that computed the right value and then failed to put it on the wire
// would pass a unit test of the function.
func TestSearchReplace_EveryMatchModeReachesTheWire(t *testing.T) {
	cases := []struct {
		name  string
		flags []string
		want  string
	}{
		{"neither", nil, "contains_case_insensitive"},
		{"case-sensitive", []string{"--case-sensitive"}, "contains"},
		{"regex", []string{"--regex"}, "regex_case_insensitive"},
		{"both", []string{"--regex", "--case-sensitive"}, "regex"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSearchReplace(mock, writeDone)

			args := append([]string{"rows", searchReplaceVerb, "orders",
				"--search", "x", "--replace", "y", "--yes"}, tc.flags...)
			if _, _, err := runRows(t, args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if mode := got.decoded(t, 0)["match_mode"]; mode != tc.want {
				t.Errorf("match_mode = %v, want %v", mode, tc.want)
			}
		})
	}
}

// The phrase names the pattern the run will SEND, not a Go rendering of it.
// %q doubles a backslash, so the page's own regex example `^\s+` came back as
// "^\\s+" -- a pattern meaning a literal backslash then an s. A user asked to
// confirm that is being shown a different regex than the one about to run.
func TestSearchReplace_PhraseShowsThePatternAsTyped(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, previewOnly)

	_, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", `^\s+`, "--replace", "", "--regex", "--field", "city")
	if err == nil {
		t.Fatal("expected the non-interactive refusal")
	}
	msg := err.Error()
	if !strings.Contains(msg, `'^\s+'`) {
		t.Errorf("the phrase does not show the pattern as typed: %q", msg)
	}
	if strings.Contains(msg, `\\s`) {
		t.Errorf("the phrase doubled the backslash, so it names a different pattern: %q", msg)
	}
	// An empty replacement has to stay visible; bare, it would read as though
	// the sentence were missing a word.
	if !strings.Contains(msg, "with ''") {
		t.Errorf("the empty replacement is not visible in the phrase: %q", msg)
	}
	if term := got.decoded(t, 0)["search_term"]; term != `^\s+` {
		t.Errorf("search_term = %q, want the pattern verbatim", term)
	}
}

// A whitespace term is a real search in every mode and reaches the wire as
// typed. Nothing server-side trims it: contains_like_pattern and
// excel_wildcard_regex_pattern keep every space, so `--search "  "` matches
// double spaces rather than every cell.
func TestSearchReplace_WhitespaceTermIsSentVerbatim(t *testing.T) {
	cases := []struct {
		name string
		mode string
		args []string
	}{
		{"substring", "contains_case_insensitive", nil},
		{"case-sensitive substring", "contains", []string{"--case-sensitive"}},
		{"regex", "regex_case_insensitive", []string{"--regex"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSearchReplace(mock, writeDone)

			args := append([]string{"rows", searchReplaceVerb, "orders",
				"--search", "  ", "--replace", " ", "--yes"}, tc.args...)
			if _, _, err := runRows(t, args...); err != nil {
				t.Fatalf("a whitespace term must be legal: %v", err)
			}
			if got.n() != 1 {
				t.Fatalf("requests = %d, want 1", got.n())
			}
			body := got.decoded(t, 0)
			if term := body["search_term"]; term != "  " {
				t.Errorf("search_term = %q, want the term verbatim", term)
			}
			if mode := body["match_mode"]; mode != tc.mode {
				t.Errorf("match_mode = %q, want %q", mode, tc.mode)
			}
		})
	}
}

// A nil field must reach the wire as JSON NULL, because null is the value that
// means "every non-deleted schema field". An omitted key would take the
// schema's default, which happens to be the same null — but "" would not, and
// that is what a naive string field would send.
func TestSearchReplace_FieldNameIsNullOrTheName(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		got := recordSearchReplace(mock, writeDone)
		if _, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "x", "--replace", "y", "--yes"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		body := got.decoded(t, 0)
		if v, present := body["field_name"]; !present || v != nil {
			t.Errorf("field_name = %v (present=%v), want an explicit null", v, present)
		}
	})
	t.Run("named", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		got := recordSearchReplace(mock, writeDone)
		if _, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "x", "--replace", "y", "--field", "city", "--yes"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if v := got.decoded(t, 0)["field_name"]; v != "city" {
			t.Errorf("field_name = %v, want city", v)
		}
	})
}

// The empty replacement is the documented way to DELETE the matched text, so it
// has to survive as "" on the wire. A validator that tested the value rather
// than the flag's presence, or a body builder with omitempty on the field,
// would make it unreachable.
func TestSearchReplace_EmptyReplaceTermSurvives(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, writeDone)

	if _, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := got.decoded(t, 0)
	if v, present := body["replace_term"]; !present || v != "" {
		t.Errorf("replace_term = %v (present=%v), want the empty string", v, present)
	}
}

// Both cloud-source flags reach the same body key. The NAME form costs a lookup
// that the ID form does not, so a runner that read sc.cloudSourceID before
// hydrateScope filled it would send 0 for one of the two and pass the other.
func TestSearchReplace_CloudSourceScopeNarrowsTheBody(t *testing.T) {
	for _, tc := range []struct{ name, flag, value string }{
		{"by id", "--cloud-source-id", "7"},
		{"by name", "--cloud-source", "s3-drop"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			registerCloudSourceResolve(mock)
			registerCloudSourceByName(mock)
			got := recordSearchReplace(mock, writeDone)

			if _, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
				"--search", "x", "--replace", "y", tc.flag, tc.value, "--yes"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if v := got.decoded(t, 0)["data_source_model_id"]; v != float64(testCloudSourceID) {
				t.Errorf("data_source_model_id = %v, want %d", v, testCloudSourceID)
			}
		})
	}
}

// At table scope the key must be an explicit null, not 0: the route reads null
// as "every source" and would read 0 as a source id that matches nothing.
func TestSearchReplace_TableScopeSendsNullDataSource(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, writeDone)

	if _, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "x", "--replace", "y", "--yes"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	body := got.decoded(t, 0)
	if v, present := body["data_source_model_id"]; !present || v != nil {
		t.Errorf("data_source_model_id = %v (present=%v), want an explicit null", v, present)
	}
}

// --- usage refusals, none of which may cost a request ----------------------

func TestSearchReplace_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// want is a fragment of the message, so a refusal that fires for the
		// wrong reason (a different guard, or cobra's generic one) fails here
		// rather than passing on the exit code alone.
		want string
	}{
		{"no --search", []string{"--replace", "y"}, "needs --search"},
		{"empty --search", []string{"--search", "", "--replace", "y"}, "--search cannot be empty"},
		{"no --replace", []string{"--search", "x"}, "needs --replace"},
		{"empty --field", []string{"--search", "x", "--replace", "y", "--field", ""}, "--field cannot be empty"},
		{"--dry-run with --yes", []string{"--search", "x", "--replace", "y", "--dry-run", "--yes"}, "cannot be combined"},
		// No stored-item form of the route, declared as a nil `file` on the
		// support row, so addScopeFlags never registers the flag and cobra
		// refuses the word itself.
		{"--file-id", []string{"--search", "x", "--replace", "y", "--file-id", "3", "--yes"}, cobraUnknownFlag(t, "file-id")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSearchReplace(mock, writeDone)
			hits := countTableResolves(mock)

			_, _, err := runRows(t, append([]string{"rows", searchReplaceVerb, "orders"}, tc.args...)...)
			if err == nil {
				t.Fatal("expected the refusal")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got.n() != 0 {
				t.Errorf("the refusal spent %d search-and-replace request(s)", got.n())
			}
			// A client-side refusal costs NO request at all, table resolve
			// included — the ordering contract resolveTarget's doc records.
			if *hits != 0 {
				t.Errorf("the refusal spent %d table resolve(s)", *hits)
			}
		})
	}
}

// The four selectors of the other nine verbs are not words here: the search
// term is the selector, so offering them would promise a narrowing the route
// cannot do.
func TestSearchReplace_SelectorsAreUnregistered(t *testing.T) {
	for _, flag := range []string{"--row-id", "--signature", "--status", "--all"} {
		t.Run(flag, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			got := recordSearchReplace(mock, writeDone)

			args := []string{"rows", searchReplaceVerb, "orders", "--search", "x", "--replace", "y", "--yes", flag}
			if flag != "--all" {
				args = append(args, "7")
			}
			_, _, err := runRows(t, args...)
			if err == nil {
				t.Fatalf("%s was accepted", flag)
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2: %v", code, err)
			}
			if !strings.Contains(err.Error(), cobraUnknownFlag(t, strings.TrimPrefix(flag, "--"))) {
				t.Errorf("error = %q, want cobra's unknown-flag refusal", err.Error())
			}
			if got.n() != 0 {
				t.Errorf("the refusal spent %d request(s)", got.n())
			}
		})
	}
}

// --- the prompt ------------------------------------------------------------

// Declining is a CHOICE, not a usage error: exit 1 through ErrDeclined, after
// the preview and before the write.
func TestSearchReplace_DeclineExitsOneAndDoesNotWrite(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, previewOnly, writeDone)
	cmdutil.SetReader(strings.NewReader("n\n"))
	t.Cleanup(cmdutil.ResetReader)

	_, _, err := runRows(t, "rows", searchReplaceVerb, "orders", "--search", "N/A", "--replace", "y")
	if err == nil {
		t.Fatal("expected the decline")
	}
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Errorf("GetExitCode = %d, want 1", code)
	}
	if got.n() != 1 {
		t.Errorf("requests = %d, want 1 — the decline must land between the preview and the write", got.n())
	}
}

// A non-interactive stdin is BAD USAGE: the only fix is to change the command
// line, and the message has to name the flag that does it.
func TestSearchReplace_NonInteractiveNamesYes(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	mock := newRowsTestEnv(t)
	got := recordSearchReplace(mock, previewOnly, writeDone)

	_, stderr, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "N/A", "--replace", "unknown", "--field", "city")
	if err == nil {
		t.Fatal("expected the non-interactive refusal")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Fatalf("GetExitCode = %d, want 2: %v", code, err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("the refusal does not name --yes: %q", err.Error())
	}
	// The refusal describes the write it did not make, terms and mode included,
	// because that sentence is what the user is being asked to authorize.
	// Single quotes, not Go's %q: the phrase renders user text the way the
	// shell would, so the regex a user typed is the regex they are shown.
	for _, want := range []string{`'N/A'`, `'unknown'`, `field 'city'`, "table orders (id: 42)", "contains_case_insensitive"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %s: %q", want, err.Error())
		}
	}
	if got.n() != 1 {
		t.Errorf("requests = %d, want 1 — the preview runs, the write does not", got.n())
	}
	if !strings.Contains(stderr, "queued for re-validation") {
		t.Errorf("the consequences did not print before the refusal: %q", stderr)
	}
}

// --- the server's own failures ---------------------------------------------

// Every status keeps the exit code the contract gives it, and the 422 carries
// the server's own message: qctl never compiles a pattern, so the server's text
// is the only explanation of an invalid one there is.
func TestSearchReplace_ServerStatusesKeepTheirExitCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		exit   int
		want   string
	}{
		{
			name: "422 invalid regex", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Invalid regex pattern: missing ), unterminated subpattern at position 0","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "Invalid regex pattern",
		},
		{
			name: "422 untranslatable regex", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Regex pattern uses a backreference in a case-insensitive pattern, which cannot be translated for PostgreSQL.","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "cannot be translated for PostgreSQL",
		},
		{
			name: "422 regex PostgreSQL cannot compile", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Regex pattern cannot be run in PostgreSQL (invalid regular expression: regular expression is too complex).","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "cannot be run in PostgreSQL",
		},
		{
			name: "422 invalid replacement template", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Invalid replacement template: invalid group reference 1 at position 1","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "Invalid replacement template",
		},
		{
			name: "422 unknown field", status: http.StatusUnprocessableEntity,
			body: `{"detail":{"msg":"Field \"nope\" does not exist in this dataset.","error_code":"invalid_input"}}`,
			exit: qerrors.ExitBadUsage, want: "does not exist in this dataset",
		},
		{
			// atlas validate_pii_write_values: a mask-shaped --replace aimed at a
			// PII column (any of them, when --field is absent). No error_code.
			name: "400 masked value on a PII column", status: http.StatusBadRequest,
			body: `{"detail":{"msg":"A masked value cannot be written to a PII column. Enter a new value."}}`,
			exit: qerrors.ExitBadUsage, want: "A masked value cannot be written to a PII column",
		},
		{
			name: "404 no such table", status: http.StatusNotFound,
			body: `{"detail":{"msg":"Dataset not found"}}`,
			exit: qerrors.ExitNotFound, want: "",
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
			got := recordSearchReplaceStatus(mock, tc.status, tc.body)

			_, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
				"--search", "(", "--replace", "y", "--regex", "--yes")
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

// A failed PREVIEW must not be swallowed into a write. Without --yes the dry
// run is the first thing that happens, so its error is the command's error and
// the write never runs.
func TestSearchReplace_AFailedPreviewStopsTheRun(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplaceStatus(mock, http.StatusUnprocessableEntity,
		`{"detail":{"msg":"Invalid regex pattern: bad escape","error_code":"invalid_input"}}`)

	_, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", `\q`, "--replace", "y", "--regex")
	if err == nil {
		t.Fatal("expected the preview's error")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2: %v", code, err)
	}
	if got.n() != 1 {
		t.Errorf("requests = %d, want 1 — a failed preview must not fall through to the write", got.n())
	}
}

// The backend checks --replace for a mask before the dry-run branch, so the
// mandatory preview is where a masked value is refused: before any prompt,
// and with no write.
func TestSearchReplace_AMaskedReplacementIsRefusedAtThePreview(t *testing.T) {
	mock := newRowsTestEnv(t)
	got := recordSearchReplaceStatus(mock, http.StatusBadRequest,
		`{"detail":{"msg":"A masked value cannot be written to a PII column. Enter a new value."}}`)

	_, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "jane@example.com", "--replace", "•••••.com", "--field", "email")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want 2: %v", code, err)
	}
	if got.n() != 1 || !strings.Contains(got.bodies[0], `"dry_run":true`) {
		t.Errorf("want exactly the preview request, got %d: %v", got.n(), got.bodies)
	}
}

// --- the printed lines -----------------------------------------------------

// The per-field caveat is attached exactly when it is TRUE: without --field the
// controller sums len(rows) once per field, so a row matching in two fields is
// counted twice. With --field there is one field to sum and the number is rows.
func TestSearchReplace_PreviewNamesThePerFieldCount(t *testing.T) {
	t.Run("every field", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		recordSearchReplace(mock, previewOnly)
		stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "x", "--replace", "y", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "counted once per field a row matches in") {
			t.Errorf("stdout does not warn that the count is per field: %q", stdout)
		}
	})
	t.Run("one field", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		recordSearchReplace(mock, previewOnly)
		stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "x", "--replace", "y", "--field", "city", "--dry-run")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(stdout, "per field") {
			t.Errorf("a field-scoped preview claimed a per-field count: %q", stdout)
		}
	})
}

// An empty fields_affected after a REAL run means no field had a replacement
// applied — a statement, not an absence — so the line says so rather than
// trailing off after the counts.
func TestSearchReplace_ResultLineNamesAnEmptyFieldList(t *testing.T) {
	mock := newRowsTestEnv(t)
	recordSearchReplace(mock,
		`{"result":"ok","matched_rows":4,"replaced_rows":0,"skipped_rows":4,"fields_affected":[]}`)

	stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
		"--search", "x", "--replace", "y", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(stdout, "search-replace: 0 row(s) replaced, 4 skipped (4 matched) in table orders (id: 42); no field changed") {
		t.Errorf("stdout = %q", stdout)
	}
}

// The follow-up reads back what the run WROTE, so it searches for the
// replacement; and it goes through scope.getDataLine, so at cloud-source scope
// it carries the narrowing this run acted within instead of the whole table.
func TestSearchReplace_FollowUpLine(t *testing.T) {
	t.Run("table scope with a field", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		recordSearchReplace(mock, writeDone)
		stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "N/A", "--replace", "not known", "--field", "city", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := `qctl get data orders --is-bad-data --search-query 'not known' --search-by-field-name city`
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not carry %q: %q", want, stdout)
		}
	})
	t.Run("cloud-source scope narrows the line", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		registerCloudSourceResolve(mock)
		recordSearchReplace(mock, writeDone)
		stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "N/A", "--replace", "unknown", "--cloud-source-id", "7", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "qctl get data orders --data-source-model-id 7 --is-bad-data --search-query unknown"
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout does not carry %q: %q", want, stdout)
		}
	})
	// An empty replacement leaves nothing to search for. `--search-query ""` is
	// not a narrowing, so the whole recipe is dropped rather than degraded into
	// a line that reads back the entire quarantine.
	t.Run("an empty replacement has no recipe", func(t *testing.T) {
		mock := newRowsTestEnv(t)
		recordSearchReplace(mock, writeDone)
		stdout, _, err := runRows(t, "rows", searchReplaceVerb, "orders",
			"--search", "N/A", "--replace", "", "--yes")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(stdout, followUpHeading) {
			t.Errorf("an empty replacement still printed a read-back recipe: %q", stdout)
		}
	})
}

// shellArg is what makes those recipes pasteable, and the empty string is its
// one case that cannot be left bare — "" must stay two quote characters, not
// vanish into the command line.
func TestShellArg(t *testing.T) {
	cases := map[string]string{
		"unknown":    "unknown",
		"a/b-c_d.e":  "a/b-c_d.e",
		"not known":  `'not known'`,
		"":           `''`,
		`it's`:       `'it'\''s'`,
		"$(rm -rf )": `'$(rm -rf )'`,
	}
	for in, want := range cases {
		if got := shellArg(in); got != want {
			t.Errorf("shellArg(%q) = %q, want %q", in, got, want)
		}
	}
}
