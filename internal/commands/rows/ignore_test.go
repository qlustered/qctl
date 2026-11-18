package rows

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const (
	ignoresRoute = "/api/orgs/" + testOrgID + "/datasets/42/rule-row-ignores"
	rulesRoute   = "/api/orgs/" + testOrgID + "/datasets/42/dataset-rules"
)

var (
	emailRuleID = uuid.MustParse("aaaa1111-0000-0000-0000-000000000001")
	phoneRuleID = uuid.MustParse("bbbb2222-0000-0000-0000-000000000002")
)

// ignoreEnv is the fixture both verbs run against: table 42 "orders", cloud
// source 7, two rules, and every call to the rule list and the ignore routes
// recorded in order.
type ignoreEnv struct {
	mock      *testutil.MockAPIServer
	ruleLists int
	calls     []string // "METHOD query body"
	deleted   []string // the ignore id of every DELETE, in order
	listResp  string
	// relistResp, when set, answers every listing after the first: what the
	// ignores look like once the DELETE loop has run.
	relistResp string
	// relistStatus, when set, is the status of every listing after the first.
	relistStatus int
	listCalls    int
	createSt     int
	createRsp    string
	deleteSt     map[string]int
}

func newIgnoreEnv(t *testing.T) *ignoreEnv {
	t.Helper()
	mock := newRowsTestEnv(t)
	registerCloudSourceResolve(mock)
	e := &ignoreEnv{mock: mock, listResp: "[]", createSt: http.StatusCreated, deleteSt: map[string]int{}}
	mock.RegisterHandler("GET", rulesRoute, func(w http.ResponseWriter, _ *http.Request) {
		e.ruleLists++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(fmt.Sprintf(
			`{"total_rows":2,"page":1,"results":[`+
				`{"id":"%s","instance_name":"email_check","position":1,"state":"enabled"},`+
				`{"id":"%s","instance_name":"phone_check","position":2,"state":"enabled"}]}`,
			emailRuleID, phoneRuleID)))
	})
	record := func(r *http.Request) string {
		b := []byte{}
		if r.Body != nil {
			b, _ = io.ReadAll(r.Body)
		}
		e.calls = append(e.calls, r.Method+" "+r.URL.RawQuery+" "+strings.TrimSpace(string(b)))
		return r.URL.Path
	}
	mock.RegisterHandler("GET", ignoresRoute, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		e.listCalls++
		resp := e.listResp
		if e.listCalls > 1 && e.relistStatus != 0 {
			testutil.RespondJSON(w, e.relistStatus, map[string]any{"detail": map[string]string{"msg": "Dataset 42 not found."}})
			return
		}
		if e.listCalls > 1 && e.relistResp != "" {
			resp = e.relistResp
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(resp))
	})
	mock.RegisterHandler("POST", ignoresRoute, func(w http.ResponseWriter, r *http.Request) {
		record(r)
		testutil.RespondJSON(w, e.createSt, json.RawMessage(e.createRsp))
	})
	for i := 1; i <= 3; i++ {
		id := ignoreID(i)
		mock.RegisterHandler("DELETE", ignoresRoute+"/"+id, func(w http.ResponseWriter, r *http.Request) {
			record(r)
			e.deleted = append(e.deleted, id)
			st := http.StatusOK
			if s, ok := e.deleteSt[id]; ok {
				st = s
			}
			if st == http.StatusOK {
				testutil.RespondJSON(w, st, map[string]string{"result": "ok"})
				return
			}
			testutil.RespondJSON(w, st, map[string]any{"detail": map[string]string{"msg": "nope"}})
		})
	}
	return e
}

func ignoreID(i int) string { return fmt.Sprintf("1111111%d-0000-0000-0000-000000000000", i) }

func ignoreRecord(i, row int, rule uuid.UUID) string {
	return fmt.Sprintf(`{"id":"%s","data_source_model_id":7,"row_id":%d,"dataset_rule_id":"%s",`+
		`"decided_by_id":"cccc0000-0000-0000-0000-000000000003","reason":null,`+
		`"created_at":"2026-09-14T00:00:00Z","updated_at":"2026-09-14T00:00:00Z","discarded_at":null,"discarded_by_id":null}`,
		ignoreID(i), row, rule)
}

func (e *ignoreEnv) methods(m string) []string {
	var out []string
	for _, c := range e.calls {
		if strings.HasPrefix(c, m+" ") {
			out = append(out, c)
		}
	}
	return out
}

// --- ignore-rule ------------------------------------------------------------

// Byte-exact body: the cloud source from the scope, the rule resolved from its
// NAME to its id, the repeated row id sent once (the route would echo it
// twice), and the reason as typed.
func TestIgnoreRule_Body(t *testing.T) {
	e := newIgnoreEnv(t)
	e.createRsp = "[" + ignoreRecord(1, 1042, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "]"

	stdout, stderr, err := runRows(t, "rows", ignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--row-id", "1042",
		"--reason", "known test accounts", "--yes")
	if err != nil {
		t.Fatalf("ignore-rule: %v\n%s", err, stderr)
	}
	posts := e.methods("POST")
	want := `POST  {"data_source_model_id":7,"dataset_rule_id":"` + emailRuleID.String() +
		`","reason":"known test accounts","row_ids":[1042,1043]}`
	if len(posts) != 1 || posts[0] != want {
		t.Fatalf("POST = %q\nwant   %q", posts, want)
	}
	for _, line := range []string{
		"ignore-rule: rule email_check ignored on 2 row(s) in cloud source s3-nightly (id: 7) of table orders",
		"qctl get rule-ignores orders --cloud-source-id 7 --rule " + emailRuleID.String(),
	} {
		if !strings.Contains(stdout, line) {
			t.Errorf("stdout lacks %q\n%s", line, stdout)
		}
	}
	for _, note := range []string{ignoreReprocessNote, ignoreExistingNote} {
		if !strings.Contains(stderr, note) {
			t.Errorf("--yes did not print the consequence %q", note)
		}
	}
}

func TestIgnoreRule_NoReasonSendsNull(t *testing.T) {
	e := newIgnoreEnv(t)
	e.createRsp = "[" + ignoreRecord(1, 1042, phoneRuleID) + "]"
	if _, stderr, err := runRows(t, "rows", ignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "bbbb2222", "--row-id", "1042", "--yes"); err != nil {
		t.Fatalf("ignore-rule: %v\n%s", err, stderr)
	}
	posts := e.methods("POST")
	if len(posts) != 1 || !strings.Contains(posts[0], `"reason":null`) || !strings.Contains(posts[0], phoneRuleID.String()) {
		t.Errorf("POST = %q", posts)
	}
}

// Every refusal decidable from the command line exits 2 and costs nothing:
// no table resolve, no rule list, no write.
func TestIgnoreVerbs_UsageRefusalsExitTwoWithNoRequest(t *testing.T) {
	scope := []string{"--cloud-source-id", "7"}
	cases := []struct {
		name string
		verb string
		args []string
		want string
	}{
		{"no cloud source", ignoreRuleVerb, []string{"--rule", "email_check", "--row-id", "1"}, "needs --cloud-source <name> or --cloud-source-id <id>"},
		{"no cloud source, un", unignoreRuleVerb, []string{"--rule", "email_check", "--row-id", "1"}, "needs --cloud-source <name> or --cloud-source-id <id>"},
		{"no rule", ignoreRuleVerb, append([]string{"--row-id", "1"}, scope...), "needs --rule"},
		{"empty rule", unignoreRuleVerb, append([]string{"--rule", "", "--row-id", "1"}, scope...), "--rule cannot be empty"},
		{"empty reason", ignoreRuleVerb, append([]string{"--rule", "email_check", "--row-id", "1", "--reason", " "}, scope...), "--reason cannot be empty"},
		{"no row id", ignoreRuleVerb, append([]string{"--rule", "email_check"}, scope...), "needs a row selector: --row-id"},
		{"zero row id", unignoreRuleVerb, append([]string{"--rule", "email_check", "--row-id", "0"}, scope...), "--row-id must be a positive integer"},
		{"empty cloud source", ignoreRuleVerb, []string{"--rule", "email_check", "--row-id", "1", "--cloud-source", ""}, "--cloud-source cannot be empty"},
		{"--signature", ignoreRuleVerb, append([]string{"--rule", "email_check", "--signature", "ab"}, scope...), cobraUnknownFlag(t, "signature")},
		{"--all", unignoreRuleVerb, append([]string{"--rule", "email_check", "--all"}, scope...), cobraUnknownFlag(t, "all")},
		{"--file-id", ignoreRuleVerb, []string{"--rule", "email_check", "--row-id", "1", "--file-id", "3"}, cobraUnknownFlag(t, "file-id")},
		{"--reason on unignore", unignoreRuleVerb, append([]string{"--rule", "email_check", "--row-id", "1", "--reason", "x"}, scope...), cobraUnknownFlag(t, "reason")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newIgnoreEnv(t)
			hits := countTableResolves(e.mock)
			_, _, err := runRows(t, append([]string{"rows", tc.verb, "orders"}, tc.args...)...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d (%v), want 2", code, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q lacks %q", err, tc.want)
			}
			if *hits != 0 || e.ruleLists != 0 || len(e.calls) != 0 {
				t.Errorf("a usage refusal cost %d resolve(s), %d rule list(s), %d ignore call(s)", *hits, e.ruleLists, len(e.calls))
			}
		})
	}
}

// The rule must be on this table and is matched strictly: a fragment of a name
// is not found (exit 3) rather than picked, and nothing is written.
func TestIgnoreVerbs_UnknownRuleIsNotFound(t *testing.T) {
	for _, verb := range []string{ignoreRuleVerb, unignoreRuleVerb} {
		for _, rule := range []string{"nope", "email", "cccc0000-0000-0000-0000-000000000009"} {
			t.Run(verb+"/"+rule, func(t *testing.T) {
				e := newIgnoreEnv(t)
				_, _, err := runRows(t, "rows", verb, "orders", "--cloud-source-id", "7",
					"--rule", rule, "--row-id", "1", "--yes")
				if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
					t.Fatalf("exit code = %d (%v), want 3", code, err)
				}
				if !strings.Contains(err.Error(), "--rule") {
					t.Errorf("error does not name --rule: %v", err)
				}
				if len(e.calls) != 0 {
					t.Errorf("ignore calls made: %q", e.calls)
				}
			})
		}
	}
}

// A row that is not a live quarantined row of the source is the route's 404,
// and the exit code survives the chain.
func TestIgnoreRule_UnknownRowIsNotFound(t *testing.T) {
	e := newIgnoreEnv(t)
	e.createSt = http.StatusNotFound
	e.createRsp = `{"detail":{"msg":"Row ids [99] are not bad rows of this data source."}}`
	_, _, err := runRows(t, "rows", ignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "99", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
}

func TestIgnoreRule_NonInteractiveNamesYes(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	e := newIgnoreEnv(t)
	_, stderr, err := runRows(t, "rows", ignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (%v), want 2", code, err)
	}
	want := "pass --yes to ignore rule email_check on 2 row(s) in cloud source s3-nightly (id: 7) of table orders"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want %q", err, want)
	}
	if len(e.methods("POST")) != 0 {
		t.Error("the refusal wrote anyway")
	}
	if !strings.Contains(stderr, ignoreReprocessNote) {
		t.Errorf("the consequences did not print before the refusal: %q", stderr)
	}
}

// --- unignore-rule ----------------------------------------------------------

// The ignore ids come from ONE scoped listing by rule, and only the ignores it
// returned for the named rows are deleted, each with its own record's source.
// A named row with no active ignore is warned about and skipped.
func TestUnignoreRule_DeletesOnlyWhatTheScopedListingFound(t *testing.T) {
	e := newIgnoreEnv(t)
	// The unmatched ignore is listed FIRST, so deleting "the first N listed"
	// instead of the matched ones would take it.
	e.listResp = "[" + ignoreRecord(3, 2000, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "," +
		ignoreRecord(1, 1042, emailRuleID) + "]"

	stdout, stderr, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--row-id", "55", "--yes")
	if err != nil {
		t.Fatalf("unignore-rule: %v\n%s", err, stderr)
	}
	gets := e.methods("GET")
	if len(gets) != 1 || !strings.Contains(gets[0], "data_source_model_id=7") ||
		!strings.Contains(gets[0], "dataset_rule_id="+emailRuleID.String()) || strings.Contains(gets[0], "row_id") {
		t.Errorf("listing = %q", gets)
	}
	dels := e.methods("DELETE")
	if want := []string{ignoreID(1), ignoreID(2)}; fmt.Sprint(e.deleted) != fmt.Sprint(want) {
		t.Fatalf("deleted ignores = %q, want %q (rows 1042 then 1043, as named)", e.deleted, want)
	}
	for _, d := range dels {
		if !strings.Contains(d, "data_source_model_id=7") {
			t.Errorf("DELETE without the record's source: %q", d)
		}
	}
	if !strings.Contains(stderr, "rule email_check is not ignored on row(s) 55; skipping them") {
		t.Errorf("stderr lacks the skipped-row warning:\n%s", stderr)
	}
	if !strings.Contains(stdout, "unignore-rule: rule email_check no longer ignored on 2 row(s) in cloud source s3-nightly (id: 7) of table orders") {
		t.Errorf("stdout:\n%s", stdout)
	}
	for _, note := range []string{unignoreReprocessNote, unignoreSkipNote} {
		if !strings.Contains(stderr, note) {
			t.Errorf("stderr lacks the consequence %q", note)
		}
	}
	if strings.Contains(stderr, ignoreReprocessNote) {
		t.Error("unignore-rule printed ignore-rule's reprocess note, which says the named rows are queued")
	}
}

func TestUnignoreRule_NothingIgnoredIsNotFoundWithNoWrite(t *testing.T) {
	e := newIgnoreEnv(t)
	e.listResp = "[" + ignoreRecord(1, 2000, emailRuleID) + "]"
	_, _, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	if !strings.Contains(err.Error(), "rule email_check is not ignored on row(s) 1042") {
		t.Errorf("error = %v", err)
	}
	if len(e.methods("DELETE")) != 0 {
		t.Error("deleted with nothing found")
	}
}

// A 404 between the listing and the DELETE is someone else reaching the same
// outcome — once a fresh listing confirms the ignore is gone: warned, not
// fatal, and not counted as removed by this run.
func TestUnignoreRule_RacedDeleteIsAWarning(t *testing.T) {
	e := newIgnoreEnv(t)
	e.listResp = "[" + ignoreRecord(1, 1042, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "]"
	e.relistResp = "[]"
	e.deleteSt[ignoreID(1)] = http.StatusNotFound
	stdout, stderr, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--yes")
	if err != nil {
		t.Fatalf("unignore-rule: %v", err)
	}
	if !strings.Contains(stderr, "the ignore on row(s) 1042 was already removed") {
		t.Errorf("stderr:\n%s", stderr)
	}
	if !strings.Contains(stdout, "no longer ignored on 1 row(s)") {
		t.Errorf("stdout:\n%s", stdout)
	}
	// The confirming listing is the same scope as the first: source and rule,
	// never row_id, which atlas would let win over the rule.
	gets := e.methods("GET")
	if len(gets) != 2 || gets[1] != gets[0] {
		t.Errorf("listings = %q, want the scoped listing twice", gets)
	}
}

// Any other failure stops the loop, keeps its exit code, and says how far the
// run got — the deletes are not atomic.
func TestUnignoreRule_FailureMidwayReportsProgress(t *testing.T) {
	e := newIgnoreEnv(t)
	e.listResp = "[" + ignoreRecord(1, 1042, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "," +
		ignoreRecord(3, 1044, emailRuleID) + "]"
	e.deleteSt[ignoreID(2)] = http.StatusForbidden
	_, _, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--row-id", "1044", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Fatalf("exit code = %d (%v), want 4", code, err)
	}
	if !strings.Contains(err.Error(), "1 of 3 ignore(s) were removed by this run") {
		t.Errorf("error = %v", err)
	}
	if n := len(e.methods("DELETE")); n != 2 {
		t.Errorf("DELETEs = %d, want 2 (stop at the failure)", n)
	}
}

// A 404 is not proof of a race: the route's dataset and cloud-source guards
// answer 404 too. When the row is still ignored afterwards, the 404 is the
// error it is — never "already removed" at exit 0 — and a row the listing did
// confirm gone is still warned about.
func TestUnignoreRule_NotFoundWithTheRowStillIgnoredFails(t *testing.T) {
	cases := map[string]string{
		"the same ignore": ignoreRecord(1, 1042, emailRuleID),
		// Removed and re-created between the listing and the DELETE: a new id,
		// the same row, still ignored.
		"a re-created ignore": ignoreRecord(3, 1042, emailRuleID),
	}
	for name, relisted := range cases {
		t.Run(name, func(t *testing.T) {
			e := newIgnoreEnv(t)
			e.listResp = "[" + ignoreRecord(1, 1042, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "]"
			e.relistResp = "[" + relisted + "]"
			e.deleteSt[ignoreID(1)] = http.StatusNotFound
			e.deleteSt[ignoreID(2)] = http.StatusNotFound
			_, stderr, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
				"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit code = %d (%v), want 3", code, err)
			}
			for _, want := range []string{"row 1042 is still ignored for this rule", "0 of 2 ignore(s) were removed by this run"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error lacks %q: %v", want, err)
				}
			}
			if !strings.Contains(stderr, "the ignore on row(s) 1043 was already removed") {
				t.Errorf("the row the listing confirmed gone was not warned about:\n%s", stderr)
			}
			if strings.Contains(stderr, "row(s) 1042") {
				t.Errorf("the still-ignored row was reported as removed:\n%s", stderr)
			}
		})
	}
}

// A table deleted mid-run 404s every DELETE AND the confirming listing: that is
// the listing's error, with nothing reported as removed by someone else.
func TestUnignoreRule_ConfirmingListingFailureIsTheError(t *testing.T) {
	e := newIgnoreEnv(t)
	e.listResp = "[" + ignoreRecord(1, 1042, emailRuleID) + "," + ignoreRecord(2, 1043, emailRuleID) + "]"
	e.relistStatus = http.StatusNotFound
	e.deleteSt[ignoreID(1)] = http.StatusNotFound
	e.deleteSt[ignoreID(2)] = http.StatusNotFound
	stdout, stderr, err := runRows(t, "rows", unignoreRuleVerb, "orders", "--cloud-source-id", "7",
		"--rule", "email_check", "--row-id", "1042", "--row-id", "1043", "--yes")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Fatalf("exit code = %d (%v), want 3", code, err)
	}
	if !strings.Contains(err.Error(), "0 of 2 ignore(s) were removed by this run") {
		t.Errorf("error = %v", err)
	}
	if strings.Contains(stderr, "already removed") || strings.Contains(stdout, "no longer ignored") {
		t.Errorf("a failed confirmation reported success:\nstdout: %s\nstderr: %s", stdout, stderr)
	}
}

// --rule accepts an r-* alias, resolved against THIS table's rules: an alias for
// a rule on another table is not found (exit 3) and nothing is written.
func TestIgnoreVerbs_RuleAlias(t *testing.T) {
	for _, verb := range []string{ignoreRuleVerb, unignoreRuleVerb} {
		t.Run(verb+"/on this table", func(t *testing.T) {
			e := newIgnoreEnv(t)
			allocateRuleAlias(t, e.mock, phoneRuleID.String())
			e.createRsp = "[" + ignoreRecord(1, 1042, phoneRuleID) + "]"
			e.listResp = "[" + ignoreRecord(1, 1042, phoneRuleID) + "]"
			if _, stderr, err := runRows(t, "rows", verb, "orders", "--cloud-source-id", "7",
				"--rule", "r-1", "--row-id", "1042", "--yes"); err != nil {
				t.Fatalf("%s: %v\n%s", verb, err, stderr)
			}
			if len(e.methods("POST"))+len(e.deleted) != 1 {
				t.Errorf("calls = %q", e.calls)
			}
			for _, c := range e.calls {
				if strings.Contains(c, emailRuleID.String()) {
					t.Errorf("the alias reached another rule: %q", c)
				}
			}
		})
		t.Run(verb+"/on another table", func(t *testing.T) {
			e := newIgnoreEnv(t)
			allocateRuleAlias(t, e.mock, "cccc0000-0000-0000-0000-000000000009")
			_, _, err := runRows(t, "rows", verb, "orders", "--cloud-source-id", "7",
				"--rule", "r-1", "--row-id", "1042", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit code = %d (%v), want 3", code, err)
			}
			if len(e.calls) != 0 {
				t.Errorf("ignore calls made: %q", e.calls)
			}
		})
		t.Run(verb+"/unknown alias", func(t *testing.T) {
			e := newIgnoreEnv(t)
			allocateRuleAlias(t, e.mock, phoneRuleID.String())
			_, _, err := runRows(t, "rows", verb, "orders", "--cloud-source-id", "7",
				"--rule", "r-99", "--row-id", "1042", "--yes")
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Fatalf("exit code = %d (%v), want 3", code, err)
			}
			if len(e.calls) != 0 {
				t.Errorf("ignore calls made: %q", e.calls)
			}
		})
	}
}

func allocateRuleAlias(t *testing.T, mock *testutil.MockAPIServer, id string) {
	t.Helper()
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	store, err := aliasstore.Open()
	if err != nil {
		t.Fatalf("open alias store: %v", err)
	}
	if _, err := store.Allocate(key, aliasstore.ResourceDatasetRule, id); err != nil {
		t.Fatalf("allocate: %v", err)
	}
	store.Close()
}
