package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

const (
	testOrgID    = "b2c3d4e5-f6a7-8901-bcde-f23456789012"
	testTargetID = 42
	testSourceID = 12
	testMergeID  = 7
)

var (
	testOrgBase      = "/api/orgs/" + testOrgID
	testDatasetsPath = testOrgBase + "/datasets"
	testSourceRules  = fmt.Sprintf("%s/%d/dataset-rules", testDatasetsPath, testSourceID)
	testTargetRules  = fmt.Sprintf("%s/%d/dataset-rules", testDatasetsPath, testTargetID)
	testPreviewPath  = fmt.Sprintf("%s/%d/rule-merges", testDatasetsPath, testTargetID)
	testMergePath    = fmt.Sprintf("%s/%d", testPreviewPath, testMergeID)
	testApplyPath    = testMergePath + "/apply"
	testDescribeLine = "Next: qctl describe rule-merge 7 --table-id 42"
)

func testRuleID(table, name string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(table+"/"+name)).String()
}

// Both tables have a rule named price_range, so a reference resolves against
// the table it names a rule of.
var (
	srcPrice = testRuleID("src", "price_range")
	srcZip   = testRuleID("src", "zip_check")
	srcEmail = testRuleID("src", "email_check")
	srcPhone = testRuleID("src", "phone_check")
	tgtPrice = testRuleID("tgt", "price_range")
	tgtZipA  = testRuleID("tgt", "zip_a")
	tgtZipB  = testRuleID("tgt", "zip_b")
	tgtPhone = testRuleID("tgt", "phone_check")
)

var (
	testSourceRuleNames = []string{"price_range", "zip_check", "email_check", "phone_check"}
	testTargetRuleNames = []string{"price_range", "zip_a", "zip_b", "phone_check"}
)

// testItem is one merge item of a fixture body.
type testItem struct {
	id         int
	action     string
	source     string
	target     string // "" is null
	note       string // "" is null
	choice     string // "" is null
	candidates []string
}

func testJSONString(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func (it testItem) json() string {
	snapshot := `{"rule_revision_id":"rev-1","params":{"max":9007199254740993},"severity":"blocker",` +
		`"state":"enabled","column_mapping":{"value":"price"},"produced_fields":[]}`
	targetSnapshot := "null"
	if it.target != "" {
		targetSnapshot = strings.Replace(snapshot, "9007199254740993", "10", 1)
	}
	candidates, _ := json.Marshal(append([]string{}, it.candidates...))
	return fmt.Sprintf(`{"id":%d,"action":%q,"source_object_id":%q,"target_object_id":%s,"conflict_choice":%s,`+
		`"note":%s,"rule_snapshot":%s,"base_snapshot":null,"target_snapshot":%s,"source_changes":null,`+
		`"target_changes":null,"candidate_object_ids":%s,"added_field_names":[]}`,
		it.id, it.action, it.source, testJSONString(it.target), testJSONString(it.choice),
		testJSONString(it.note), snapshot, targetSnapshot, candidates)
}

// testPlanItems is the preview: a conflict paired with one target rule, an
// ambiguous conflict, a created rule and a kept one.
func testPlanItems() []testItem {
	return []testItem{
		{id: 101, action: "conflict", source: srcPrice, target: tgtPrice, note: "It changed in both tables."},
		{id: 102, action: "conflict", source: srcZip, candidates: []string{tgtZipA, tgtZipB},
			note: "Its match by rule family and columns is ambiguous; the candidates are zip_a, zip_b."},
		{id: 103, action: "created", source: srcEmail},
		{id: 104, action: "kept", source: srcPhone, target: tgtPhone},
	}
}

// testMergeBody renders a merge the way a server might send it: keys in an
// order no Go struct marshals, a key no schema declares, and an integer past
// 2^53, indented and ending in a newline. summary is a JSON literal.
func testMergeBody(state, summary string, items []testItem) string {
	parts := make([]string, len(items))
	for i, it := range items {
		parts[i] = it.json()
	}
	raw := fmt.Sprintf(`{"id":%d,"source_dataset_id":%d,"target_dataset_id":%d,"state":%q,"summary":%s,`+
		`"created_at":"2026-09-25T10:00:00Z","items":[%s],"x_unmodelled":{"b":1,"a":9007199254740993}}`,
		testMergeID, testSourceID, testTargetID, state, summary, strings.Join(parts, ","))
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		panic(err)
	}
	buf.WriteByte('\n')
	return buf.String()
}

// testWaitingItems is the plan as an applying merge shows it, with the
// created rule waiting.
func testWaitingItems() []testItem {
	items := testPlanItems()
	items[0].note, items[1].note = "", ""
	items[0].action, items[0].choice = "replaced", "take_source"
	items[1].action, items[1].choice, items[1].target = "kept", "keep_target", tgtZipB
	items[2].note = "Waiting for validation."
	return items
}

func testSettledItems() []testItem {
	items := testWaitingItems()
	items[2].note = ""
	return items
}

const testFullResolution = `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: price_range
      choice: take_source
    - rule: zip_check
      choice: keep_target
      target_rule: zip_b
`

// testFullResolutionWire is testFullResolution as the apply request carries it.
var testFullResolutionWire = `{"resolutions":[` +
	`{"item_id":101,"choice":"take_source","column_mapping":null,"params":null,"target_rule_id":null},` +
	`{"item_id":102,"choice":"keep_target","column_mapping":null,"params":null,"target_rule_id":"` + tgtZipB + `"}]}`

type mergeEnv struct {
	te   *testutil.TestEnv
	mock *testutil.MockAPIServer

	previewBody   string
	previewSent   []string
	applyStatus   int
	applyResponse string
	applySent     []string
	getBodies     []string
	targetStates  map[string]string
}

// newMergeEnv serves tables orders (id 42, the target) and orders_eu (id 12,
// the source), their rules, and merge 7 into orders: previewBody for the
// preview, applyResponse with applyStatus for the apply, and getBodies in turn
// for each read, the last one repeating. The wait clock is stubbed.
func newMergeEnv(t *testing.T) *mergeEnv {
	t.Helper()
	te := testutil.NewTestEnv(t)
	t.Cleanup(te.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	key, _ := config.NormalizeEndpointKey(mock.Server.URL)
	te.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	te.SetupCredential(key, testOrgID, "test-token")

	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	rule_merges.SetWaitClock(func() time.Time { return now }, func(d time.Duration) { now = now.Add(d) })
	t.Cleanup(rule_merges.ResetWaitClock)

	e := &mergeEnv{
		te:            te,
		mock:          mock,
		previewBody:   testMergeBody("previewed", "null", testPlanItems()),
		applyStatus:   http.StatusAccepted,
		applyResponse: testMergeBody("applying", "null", testWaitingItems()),
		targetStates:  map[string]string{},
	}
	mock.RegisterHandler("GET", testDatasetsPath, func(w http.ResponseWriter, req *http.Request) {
		name := req.URL.Query().Get("name")
		id, ok := map[string]int{"orders": testTargetID, "orders_eu": testSourceID}[name]
		if !ok {
			testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"total_rows":0,"page":1,"results":[]}`))
			return
		}
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(fmt.Sprintf(
			`{"total_rows":1,"page":1,"results":[{"id":%d,"name":%q,"state":"active"}]}`, id, name)))
	})
	mock.RegisterHandler("GET", testSourceRules, testRuleListHandler("src", testSourceRuleNames, nil))
	mock.RegisterHandler("GET", testTargetRules, testRuleListHandler("tgt", testTargetRuleNames, e.targetStates))
	mock.RegisterHandler("POST", testPreviewPath, func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		e.previewSent = append(e.previewSent, strings.TrimSpace(string(b)))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, e.previewBody)
	})
	mock.RegisterHandler("POST", testApplyPath, func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		e.applySent = append(e.applySent, string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(e.applyStatus)
		_, _ = io.WriteString(w, e.applyResponse)
	})
	gets := 0
	mock.RegisterHandler("GET", testMergePath, func(w http.ResponseWriter, _ *http.Request) {
		body := e.getBodies[min(gets, len(e.getBodies)-1)]
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, body)
	})
	return e
}

func testRuleListHandler(table string, names []string, states map[string]string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		results := make([]map[string]any, len(names))
		for i, name := range names {
			state := states[name]
			if state == "" {
				state = "enabled"
			}
			results[i] = map[string]any{
				"id": testRuleID(table, name), "instance_name": name, "position": i + 1, "state": state,
				"severity": "as_declared", "release": "1.0.0", "family_name": name,
				"family_id": uuid.Nil, "rule_revision_id": uuid.Nil, "has_multiple_releases": false,
				"created_at": "2026-09-14T00:00:00Z", "updated_at": "2026-09-14T00:00:00Z",
			}
		}
		testutil.RespondJSON(w, http.StatusOK, map[string]any{"results": results, "total_rows": len(results), "page": 1})
	}
}

// count is how many requests reached method path.
func (e *mergeEnv) count(method, path string) int {
	n := 0
	for _, r := range e.mock.Requests() {
		if r == method+" "+path {
			n++
		}
	}
	return n
}

// file writes content to a file in the test home and returns its path.
func (e *mergeEnv) file(content string) string {
	return e.te.CreateFile("resolution.yaml", content)
}

// setupTestCommand builds the root the real one builds, minus what this
// package does not exercise: the FlagErrorFunc and TypeArgsErrors are the real
// root's, so a flag or arity error exits 2 here as it does in the binary.
func setupTestCommand() *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("server", "", "")
	root.PersistentFlags().String("user", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("allow-insecure-http", false, "")
	root.PersistentFlags().CountP("verbose", "v", "")
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "")
	})
	root.AddCommand(NewCommand())
	cmdutil.TypeArgsErrors(root)
	return root
}

func runMerge(t *testing.T, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := setupTestCommand()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(append([]string{"merge", "table-rules"}, args...))
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("want exit %d, got success", code)
	}
	if got := qerrors.GetExitCode(err); got != code {
		t.Fatalf("exit = %d, want %d: %v", got, code, err)
	}
}

func wantContains(t *testing.T, where, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Errorf("%s lacks %q:\n%s", where, want, text)
		}
	}
}

// hasRow reports whether text has a line whose fields are cells.
func hasRow(text string, cells ...string) bool {
	for _, line := range strings.Split(text, "\n") {
		if slices.Equal(strings.Fields(line), cells) {
			return true
		}
	}
	return false
}

func TestMergeTableRules_DryRunPreviewsOnceAndAppliesNothing(t *testing.T) {
	e := newMergeEnv(t)
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v\n%s%s", err, stdout, stderr)
	}
	for _, c := range []struct {
		method, path string
		want         int
	}{
		{"POST", testPreviewPath, 1},
		{"GET", testSourceRules, 1},
		{"GET", testTargetRules, 1},
		{"POST", testApplyPath, 0},
		{"GET", testMergePath, 0},
	} {
		if got := e.count(c.method, c.path); got != c.want {
			t.Errorf("%s %s: %d requests, want %d", c.method, c.path, got, c.want)
		}
	}
	if len(e.previewSent) != 1 || e.previewSent[0] != `{"source_dataset_id":12}` {
		t.Errorf("preview requests = %q", e.previewSent)
	}
	wantContains(t, "stdout", stdout,
		"Rule merge 7: orders_eu (id 12) -> orders (id 42)",
		"Plan:     1 created, 1 kept, 2 conflict",
		"UNRESOLVED CONFLICTS",
		"2 conflicts have no resolution: price_range (item 101), zip_check (item 102)",
		"kind: RuleMergeResolution",
	)
	if !hasRow(stdout, "103", "created", "email_check", "-", "-", "-") {
		t.Errorf("the plan has no row for the created item:\n%s", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

// The skeleton a dry run prints fails to parse only for its empty choices,
// and once they are filled in it drives an apply.
func TestMergeTableRules_DryRunSkeletonFailsOnlyForItsEmptyChoice(t *testing.T) {
	e := newMergeEnv(t)
	stdout, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "--dry-run")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	start := strings.Index(stdout, "# Set choice on every entry")
	if start < 0 {
		t.Fatalf("no skeleton in the dry run:\n%s", stdout)
	}
	skeleton := stdout[start:]

	_, err = rule_merges.ParseResolution([]byte(skeleton))
	wantExit(t, err, qerrors.ExitBadUsage)
	if !strings.Contains(err.Error(), "choice is required") {
		t.Errorf("the skeleton fails for another reason: %v", err)
	}
	var doc rule_merges.RuleMergeResolution
	if err := pkgmanifest.StrictUnmarshal([]byte(skeleton), &doc); err != nil {
		t.Fatalf("the skeleton does not decode: %v\n%s", err, skeleton)
	}
	var rules []string
	for _, entry := range doc.Spec.Resolutions {
		rules = append(rules, entry.Rule)
		if entry.Choice != "" {
			t.Errorf("rule %s arrives with choice %q", entry.Rule, entry.Choice)
		}
	}
	if want := []string{"price_range", "zip_check"}; !slices.Equal(rules, want) {
		t.Errorf("skeleton rules = %q, want %q", rules, want)
	}

	filled := strings.Replace(skeleton, "      choice:\n", "      choice: take_source\n", 1)
	filled = strings.Replace(filled, "      choice:\n", "      choice: keep_target\n", 1)
	filled = strings.Replace(filled, "      target_rule:\n", "      target_rule: zip_b\n", 1)
	if _, err := rule_merges.ParseResolution([]byte(filled)); err != nil {
		t.Fatalf("the filled skeleton does not parse: %v\n%s", err, filled)
	}
	if _, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(filled), "--yes"); err != nil {
		t.Fatalf("apply with the filled skeleton: %v", err)
	}
	if len(e.applySent) != 1 {
		t.Fatalf("apply requests = %d, want 1", len(e.applySent))
	}
	testutil.RequireSameJSONTree(t, json.RawMessage(e.applySent[0]), testFullResolutionWire)
}

func TestMergeTableRules_YesAppliesTheResolutionsByRuleName(t *testing.T) {
	e := newMergeEnv(t)
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution), "--yes")
	if err != nil {
		t.Fatalf("apply: %v\n%s%s", err, stdout, stderr)
	}
	if len(e.applySent) != 1 {
		t.Fatalf("apply requests = %d, want 1", len(e.applySent))
	}
	testutil.RequireSameJSONTree(t, json.RawMessage(e.applySent[0]), testFullResolutionWire)
	if got := e.count("GET", testMergePath); got != 0 {
		t.Errorf("read the merge %d times without --wait", got)
	}
	wantContains(t, "stdout", stdout, "RESOLUTIONS", "\nrule-merge/7 applying\n"+testDescribeLine+"\n")
	if !hasRow(stdout, "101", "price_range", "take_source", "price_range") ||
		!hasRow(stdout, "102", "zip_check", "keep_target", "zip_b") {
		t.Errorf("RESOLUTIONS rows are wrong:\n%s", stdout)
	}
	if strings.Contains(stdout, "UNRESOLVED") {
		t.Errorf("every conflict was resolved:\n%s", stdout)
	}
}

// An edit_source row_filter reaches the apply request as written -- an
// object, or null to remove the source rule's filter -- and RESOLUTIONS says
// which.
func TestMergeTableRules_EditSourceRowFilterIsSentAndShown(t *testing.T) {
	e := newMergeEnv(t)
	doc := `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: price_range
      choice: edit_source
      row_filter:
        description: Completed orders
        conditions:
          - {column_name: status, operator: eq, value: done}
    - rule: zip_check
      choice: edit_source
      target_rule: zip_b
      row_filter: null
`
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(doc), "--yes")
	if err != nil {
		t.Fatalf("apply: %v\n%s%s", err, stdout, stderr)
	}
	if len(e.applySent) != 1 {
		t.Fatalf("apply requests = %d, want 1", len(e.applySent))
	}
	testutil.RequireSameJSONTree(t, json.RawMessage(e.applySent[0]), `{"resolutions":[`+
		`{"item_id":101,"choice":"edit_source","column_mapping":null,"params":null,"target_rule_id":null,`+
		`"row_filter":{"description":"Completed orders","conditions":[`+
		`{"column_name":"status","operator":"eq","value":"done","value_high":null,"value_list":null,"case_sensitive":false}]}},`+
		`{"item_id":102,"choice":"edit_source","column_mapping":null,"params":null,"target_rule_id":"`+tgtZipB+`",`+
		`"row_filter":null}]}`)
	if !hasRow(stdout, "101", "price_range", "edit_source", "(row_filter)", "price_range") ||
		!hasRow(stdout, "102", "zip_check", "edit_source", "(row_filter", "removed)", "zip_b") {
		t.Errorf("RESOLUTIONS rows are wrong:\n%s", stdout)
	}
}

// -f - reads the document from stdin.
func TestMergeTableRules_ResolutionFromStdin(t *testing.T) {
	e := newMergeEnv(t)
	if _, _, err := runMerge(t, testFullResolution, "--table-id", "42", "--from-id", "12", "-f", "-", "--yes"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(e.applySent) != 1 {
		t.Fatalf("apply requests = %d, want 1", len(e.applySent))
	}
	testutil.RequireSameJSONTree(t, json.RawMessage(e.applySent[0]), testFullResolutionWire)
}

func TestMergeTableRules_UnresolvedConflictRefusesToApply(t *testing.T) {
	partial := strings.Join(strings.Split(testFullResolution, "\n")[:6], "\n") + "\n"
	cases := map[string]struct {
		file bool
		want []string
		deny string
	}{
		"no document": {want: []string{"2 conflicts have no resolution: price_range (item 101), zip_check (item 102)"}},
		"one missing": {file: true, want: []string{"1 conflict has no resolution: zip_check (item 102)"}, deny: "item 101"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newMergeEnv(t)
			args := []string{"orders", "--from", "orders_eu", "--yes"}
			if tc.file {
				args = append(args, "-f", e.file(partial))
			}
			stdout, _, err := runMerge(t, "", args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			wantContains(t, "error", err.Error(), append(tc.want, "document printed after the plan")...)
			if strings.Contains(err.Error(), "--dry-run") {
				t.Errorf("the hint names --dry-run, which --yes refuses: %v", err)
			}
			wantContains(t, "stdout", stdout, "UNRESOLVED CONFLICTS", "kind: RuleMergeResolution", "rule: zip_check")
			if tc.deny != "" && strings.Contains(err.Error(), tc.deny) {
				t.Errorf("error names the resolved %s: %v", tc.deny, err)
			}
			if got := e.count("POST", testApplyPath); got != 0 {
				t.Errorf("apply requests = %d, want 0", got)
			}
		})
	}
}

func TestMergeTableRules_NonInteractiveWithoutYesExitsTwo(t *testing.T) {
	cmdutil.ResetReader()
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the non-interactive path cannot be exercised here")
	}
	e := newMergeEnv(t)
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution))
	wantExit(t, err, qerrors.ExitBadUsage)
	wantContains(t, "error", err.Error(), "--yes", "--dry-run")
	// Each preview is a record the server keeps, so none is made.
	if got := e.count("POST", testPreviewPath) + e.count("POST", testApplyPath); got != 0 {
		t.Errorf("preview and apply requests = %d, want 0", got)
	}
}

func TestMergeTableRules_PromptNamesTheMergeAndItsCounts(t *testing.T) {
	cases := map[string]struct {
		answer  string
		applies int
		exit    int
	}{
		"confirmed": {answer: "y\n", applies: 1},
		"declined":  {answer: "n\n", applies: 0, exit: 1},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cmdutil.SetReader(strings.NewReader(tc.answer))
			t.Cleanup(cmdutil.ResetReader)
			e := newMergeEnv(t)
			_, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution))
			if tc.exit == 0 && err != nil {
				t.Fatalf("apply: %v", err)
			}
			if tc.exit != 0 {
				wantExit(t, err, tc.exit)
				if !errors.Is(err, cmdutil.ErrDeclined) {
					t.Errorf("error is not the decline: %v", err)
				}
			}
			wantContains(t, "stderr", stderr,
				"Apply rule merge 7 into orders (id 42): 1 rule created, 0 replaced, 2 conflicts resolved? [y/N]: ")
			if got := e.count("POST", testApplyPath); got != tc.applies {
				t.Errorf("apply requests = %d, want %d", got, tc.applies)
			}
		})
	}
}

// A keep_target entry on a created item declines it, and the prompt counts it
// as declined rather than created.
func TestMergeTableRules_DeclinedCreateIsCountedAndSent(t *testing.T) {
	cmdutil.SetReader(strings.NewReader("y\n"))
	t.Cleanup(cmdutil.ResetReader)
	e := newMergeEnv(t)
	doc := testFullResolution + "    - rule: email_check\n      choice: keep_target\n"
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(doc))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	wantContains(t, "stderr", stderr, ": 0 rules created, 0 replaced, 2 conflicts resolved, 1 declined? [y/N]: ")
	if !hasRow(stdout, "103", "email_check", "keep_target", "-") {
		t.Errorf("RESOLUTIONS lacks the declined item:\n%s", stdout)
	}
	if len(e.applySent) != 1 || !strings.Contains(e.applySent[0], `{"choice":"keep_target","column_mapping":null,"item_id":103`) {
		t.Errorf("apply requests = %q", e.applySent)
	}
}

func TestMergeTableRules_WaitUntilApplied(t *testing.T) {
	e := newMergeEnv(t)
	waiting := testMergeBody("applying", "null", testWaitingItems())
	e.getBodies = []string{waiting, waiting,
		testMergeBody("applied", `"Rules: 1 created, 1 replaced, 2 kept."`, testSettledItems())}
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution),
		"--yes", "--wait")
	if err != nil {
		t.Fatalf("wait: %v\n%s%s", err, stdout, stderr)
	}
	if got := e.count("GET", testMergePath); got != 3 {
		t.Errorf("merge reads = %d, want 3", got)
	}
	if got := strings.Count(stderr, "rule merge 7: applying\n  item 103: Waiting for validation.\n"); got != 1 {
		t.Errorf("progress printed %d times, want once:\n%s", got, stderr)
	}
	wantContains(t, "stdout", stdout, "\nrule-merge/7 applying\n", "State:    applied",
		"Summary:  Rules: 1 created, 1 replaced, 2 kept.")
	if strings.Contains(stdout+stderr, "Next:") {
		t.Errorf("an applied merge printed a Next line:\n%s%s", stdout, stderr)
	}
}

func TestMergeTableRules_WaitPartiallyAppliedExitsOne(t *testing.T) {
	e := newMergeEnv(t)
	items := testSettledItems()
	items[2].action, items[2].note = "failed", "Columns not found in dataset: email"
	e.getBodies = []string{testMergeBody("partially_applied", `"Rules: 1 replaced, 2 kept, 1 failed."`, items)}
	stdout, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution),
		"--yes", "--wait")
	wantExit(t, err, 1)
	wantContains(t, "error", err.Error(), "partially_applied", "Rules: 1 replaced, 2 kept, 1 failed.", testDescribeLine)
	wantContains(t, "stdout", stdout, "State:    partially_applied", "Columns not found in dataset: email")
}

func TestMergeTableRules_WaitTimeoutExits124(t *testing.T) {
	e := newMergeEnv(t)
	e.getBodies = []string{testMergeBody("applying", "null", testWaitingItems())}
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution),
		"--yes", "--wait", "--wait-timeout", "10", "-o", "name")
	wantExit(t, err, qerrors.ExitTimeout)
	if !errors.Is(err, rule_merges.ErrWaitTimeout) {
		t.Errorf("error is not the wait timeout: %v", err)
	}
	wantContains(t, "error", err.Error(), "still applying", testDescribeLine)
	if stdout != "rule-merge/7\n" {
		t.Errorf("stdout = %q, want the merge's name", stdout)
	}
	wantContains(t, "stderr", stderr, pwMergeBanner("10s"))
}

func TestMergeTableRules_JSONDryRunIsThePreviewAsReceived(t *testing.T) {
	e := newMergeEnv(t)
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "--dry-run", "-o", "json")
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if stdout != e.previewBody {
		t.Errorf("stdout is not the preview body as received\n got: %s\nwant: %s", stdout, e.previewBody)
	}
	wantContains(t, "stderr", stderr, "Rule merge 7: orders_eu (id 12) -> orders (id 42)", "kind: RuleMergeResolution")
}

func TestMergeTableRules_JSONWaitIsTheLastRead(t *testing.T) {
	e := newMergeEnv(t)
	last := testMergeBody("applied", `"Rules: 1 created, 1 replaced, 2 kept."`, testSettledItems())
	e.getBodies = []string{testMergeBody("applying", "null", testWaitingItems()), last}
	stdout, stderr, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution),
		"--yes", "--wait", "-o", "json")
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if stdout != last {
		t.Errorf("stdout is not the last read as received\n got: %s\nwant: %s", stdout, last)
	}
	wantContains(t, "stderr", stderr, "RESOLUTIONS", "rule merge 7: applying", "State:    applied")
	banner := pwMergeBanner("15m0s")
	if strings.Count(stderr, banner) != 1 || strings.Index(stderr, banner) > strings.Index(stderr, "rule merge 7: applying") {
		t.Errorf("stderr lacks the banner once, before the first progress line:\n%s", stderr)
	}
}

// pwMergeBanner is the line --wait prints to stderr before its first poll of
// merge 7, for a wait of budget.
func pwMergeBanner(budget string) string {
	return "waiting for rule merge 7 to finish applying, for up to " + budget + "; check with: qctl describe rule-merge 7 --table-id 42\n"
}

func TestMergeTableRules_StructuredOutputAfterApply(t *testing.T) {
	cases := map[string]struct {
		args  []string
		check func(t *testing.T, e *mergeEnv, stdout string)
	}{
		"json": {args: []string{"-o", "json"}, check: func(t *testing.T, e *mergeEnv, stdout string) {
			if stdout != e.applyResponse {
				t.Errorf("stdout is not the apply body as received\n got: %s\nwant: %s", stdout, e.applyResponse)
			}
		}},
		"yaml": {args: []string{"-o", "yaml"}, check: func(t *testing.T, e *mergeEnv, stdout string) {
			testutil.AssertYAMLReadsAsJSON(t, stdout, e.applyResponse)
		}},
		"name": {args: []string{"-o", "name"}, check: func(t *testing.T, _ *mergeEnv, stdout string) {
			if stdout != "rule-merge/7\n" {
				t.Errorf("stdout = %q", stdout)
			}
		}},
		"name dry run": {args: []string{"-o", "name", "--dry-run"}, check: func(t *testing.T, e *mergeEnv, stdout string) {
			if stdout != "rule-merge/7\n" || len(e.applySent) != 0 {
				t.Errorf("stdout = %q, applies = %d", stdout, len(e.applySent))
			}
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newMergeEnv(t)
			args := append([]string{"orders", "--from", "orders_eu", "-f", e.file(testFullResolution)}, tc.args...)
			if !slices.Contains(tc.args, "--dry-run") {
				args = append(args, "--yes")
			}
			stdout, stderr, err := runMerge(t, "", args...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			tc.check(t, e, stdout)
			wantContains(t, "stderr", stderr, "Rule merge 7:")
			if !slices.Contains(tc.args, "--dry-run") {
				wantContains(t, "stderr", stderr, "rule-merge/7 applying\n"+testDescribeLine)
			}
		})
	}
}

// Each is refused from the command line alone: exit 2 and no request.
func TestMergeTableRules_BadUsageSendsNothing(t *testing.T) {
	cases := map[string]struct {
		args []string
		file string
	}{
		"not a resolution document": {args: []string{"orders", "--from", "orders_eu"}, file: "kind: Nope\n"},
		"empty choice": {args: []string{"orders", "--from", "orders_eu"},
			file: strings.Replace(testFullResolution, "choice: take_source", "choice:", 1)},
		"no -f file":            {args: []string{"orders", "--from", "orders_eu", "-f", ""}},
		"--from and --from-id":  {args: []string{"orders", "--from", "orders_eu", "--from-id", "12"}},
		"no source":             {args: []string{"orders"}},
		"empty --from":          {args: []string{"orders", "--from", ""}},
		"zero --from-id":        {args: []string{"orders", "--from-id", "0"}},
		"negative --from-id":    {args: []string{"orders", "--from-id", "-3"}},
		"--dry-run with --yes":  {args: []string{"orders", "--from", "orders_eu", "--dry-run", "--yes"}},
		"--dry-run with --wait": {args: []string{"orders", "--from", "orders_eu", "--dry-run", "--wait"}},
		"--wait-timeout alone":  {args: []string{"orders", "--from", "orders_eu", "--wait-timeout", "60"}},
		"no target":             {args: []string{"--from", "orders_eu"}},
		"target twice":          {args: []string{"orders", "--table-id", "42", "--from", "orders_eu"}},
		"two positionals":       {args: []string{"orders", "more", "--from", "orders_eu"}},
		"unknown flag":          {args: []string{"orders", "--from", "orders_eu", "--bogus"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e := newMergeEnv(t)
			args := tc.args
			if tc.file != "" {
				args = append(args, "-f", e.file(tc.file))
			}
			_, _, err := runMerge(t, "", args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("requests = %q, want none", reqs)
			}
		})
	}
}

func TestMergeTableRules_SourceIsTheTargetExitsTwo(t *testing.T) {
	for name, args := range map[string][]string{
		"by name": {"orders", "--from", "orders"},
		"by id":   {"--table-id", "42", "--from-id", "42"},
	} {
		t.Run(name, func(t *testing.T) {
			e := newMergeEnv(t)
			_, _, err := runMerge(t, "", args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			wantContains(t, "error", err.Error(), "same table (id 42)")
			for _, r := range e.mock.Requests() {
				if strings.HasPrefix(r, "POST ") {
					t.Errorf("sent %s", r)
				}
			}
		})
	}
}

// --from is always a name: "12" is looked up as one, not taken as table 12.
func TestMergeTableRules_FromIsAlwaysAName(t *testing.T) {
	e := newMergeEnv(t)
	_, _, err := runMerge(t, "", "orders", "--from", "12", "--dry-run")
	if err == nil {
		t.Fatal("--from 12 resolved although no table is named 12")
	}
	wantContains(t, "error", err.Error(), "--from", `no table named "12"`, "use --from-id 12")
	wantExit(t, err, qerrors.ExitNotFound)
	if strings.Contains(err.Error(), "--table-id") {
		t.Errorf("the --from hint names --table-id:\n%v", err)
	}
	if got := e.count("POST", testPreviewPath); got != 0 {
		t.Errorf("preview requests = %d, want 0", got)
	}
}

func TestMergeTableRules_ApplyConflictExitsTwo(t *testing.T) {
	e := newMergeEnv(t)
	e.applyStatus = http.StatusConflict
	e.applyResponse = `{"detail":{"msg":"Rule merge 3 into this dataset is applying; apply this one once it finishes."}}`
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(testFullResolution), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	wantContains(t, "error", err.Error(), "is applying")
}

// A document naming a rule the merge has no item for is refused before the
// prompt.
func TestMergeTableRules_ResolutionForARuleWithNoItemExitsTwo(t *testing.T) {
	e := newMergeEnv(t)
	doc := testFullResolution + "    - rule: phone_check\n      choice: keep_target\n"
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(doc), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	wantContains(t, "error", err.Error(), "phone_check")
	if got := e.count("POST", testApplyPath); got != 0 {
		t.Errorf("apply requests = %d, want 0", got)
	}
}

func TestMergeTableRules_RefusesTwoItemsReplacingOneTarget(t *testing.T) {
	cases := map[string]struct {
		first testItem
		doc   string
	}{
		"two ambiguous conflicts": {
			first: testItem{id: 101, action: "conflict", source: srcPrice, candidates: []string{tgtPrice}},
			doc: `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: price_range
      choice: take_source
      target_rule: price_range
    - rule: zip_check
      choice: edit_source
      target_rule: price_range
`,
		},
		"replaced and conflict": {
			first: testItem{id: 101, action: "replaced", source: srcPrice, target: tgtPrice},
			doc: `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: zip_check
      choice: take_source
      target_rule: price_range
`,
		},
	}
	for name, tc := range cases {
		for _, dryRun := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/dry-run=%t", name, dryRun), func(t *testing.T) {
				cmdutil.SetReader(strings.NewReader("y\n"))
				t.Cleanup(cmdutil.ResetReader)
				e := newMergeEnv(t)
				items := testPlanItems()
				items[0] = tc.first
				items[1].candidates = []string{tgtPrice}
				e.previewBody = testMergeBody("previewed", "null", items)
				args := []string{"orders", "--from", "orders_eu", "-f", e.file(tc.doc)}
				if dryRun {
					args = append(args, "--dry-run")
				}
				_, stderr, err := runMerge(t, "", args...)
				wantExit(t, err, qerrors.ExitBadUsage)
				wantContains(t, "error", err.Error(), "more than one item replaces", "price_range", "101", "102")
				if got := e.count("POST", testApplyPath); got != 0 {
					t.Errorf("apply requests = %d, want 0", got)
				}
				if strings.Contains(stderr, "[y/N]") {
					t.Errorf("prompted before rejecting the plan: %s", stderr)
				}
			})
		}
	}
}

func TestMergeTableRules_RefusesPendingValidationTarget(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry-run=%t", dryRun), func(t *testing.T) {
			cmdutil.SetReader(strings.NewReader("y\n"))
			t.Cleanup(cmdutil.ResetReader)
			e := newMergeEnv(t)
			e.targetStates["zip_a"] = "pending_validation"
			args := []string{"orders", "--from", "orders_eu", "-f", e.file(`apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: price_range
      choice: keep_target
    - rule: zip_check
      choice: take_source
      target_rule: zip_a
`)}
			if dryRun {
				args = append(args, "--dry-run")
			}
			_, stderr, err := runMerge(t, "", args...)
			wantExit(t, err, qerrors.ExitBadUsage)
			wantContains(t, "error", err.Error(), "zip_a", "waits for validation", "cannot be replaced")
			if got := e.count("POST", testApplyPath); got != 0 {
				t.Errorf("apply requests = %d, want 0", got)
			}
			if strings.Contains(stderr, "[y/N]") {
				t.Errorf("prompted before rejecting the plan: %s", stderr)
			}
		})
	}
}

func TestMergeTableRules_PendingTargetCanBeKept(t *testing.T) {
	e := newMergeEnv(t)
	e.targetStates["zip_a"] = "pending_validation"
	doc := strings.Replace(testFullResolution, "target_rule: zip_b", "target_rule: zip_a", 1)
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(doc), "--yes")
	if err != nil {
		t.Fatalf("keep target: %v", err)
	}
	if got := e.count("POST", testApplyPath); got != 1 {
		t.Errorf("apply requests = %d, want 1", got)
	}
}

func TestMergeTableRules_AutomaticReplacementOfPendingTargetExitsTwo(t *testing.T) {
	e := newMergeEnv(t)
	e.targetStates["price_range"] = "pending_validation"
	items := testPlanItems()
	items[0].action = "replaced"
	e.previewBody = testMergeBody("previewed", "null", items)
	doc := `apiVersion: qluster.ai/v1
kind: RuleMergeResolution
spec:
  resolutions:
    - rule: zip_check
      choice: keep_target
      target_rule: zip_b
`
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "-f", e.file(doc), "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	wantContains(t, "error", err.Error(), "price_range", "waits for validation")
	if got := e.count("POST", testApplyPath); got != 0 {
		t.Errorf("apply requests = %d, want 0", got)
	}
}

func TestMergeTableRules_ReplacementWithoutResolutionChecksTargetState(t *testing.T) {
	e := newMergeEnv(t)
	e.targetStates["price_range"] = "pending_validation"
	items := []testItem{{id: 101, action: "replaced", source: srcPrice, target: tgtPrice}}
	e.previewBody = testMergeBody("previewed", "null", items)
	_, _, err := runMerge(t, "", "orders", "--from", "orders_eu", "--yes")
	wantExit(t, err, qerrors.ExitBadUsage)
	wantContains(t, "error", err.Error(), "price_range", "waits for validation")
	if got := e.count("POST", testApplyPath); got != 0 {
		t.Errorf("apply requests = %d, want 0", got)
	}
}
