package rows

import (
	"bytes"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// newConfirmProbe is newProbe plus --yes, i.e. exactly the flag surface a verb
// carries when confirmAction reads it.
func newConfirmProbe(t *testing.T, sup support, kinds []selectorKind, args ...string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	cmd := newProbe(sup, kinds...)
	addYesFlag(cmd)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	return cmd, stdout, stderr
}

// The consequence lines and the missing-count reason must reach the user
// BEFORE the prompt, so --yes is the only path that can be asserted without a
// terminal — and it is also the path a script takes, where those lines are the
// only warning there is.
//
// The reason is worded for the SKIPPED prompt here, not "No count is shown":
// reportResult's line comes next and may carry the server's own count, and the
// two read as a contradiction otherwise.
func TestConfirmAction_YesStillPrintsTheConsequences(t *testing.T) {
	// deleteQuarantinedSupport rather than a literal: the clause names a
	// population now, and a bare support{} would leave it undeclared and assert
	// the fallback wording instead of a verb's real one.
	sup := deleteQuarantinedSupport
	cmd, stdout, stderr := newConfirmProbe(t, sup, []selectorKind{selSignatures, selStatus, selAll}, "--all", "--yes")

	sel := selector{kind: selAll}
	act := action{verb: "delete-quarantined", gerund: "Delete", extra: []string{"--all excludes nothing."}}

	// nil ctx: --all is never sized, so no count request may be attempted.
	if err := confirmAction(cmd, nil, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sel, sup, act); err != nil {
		t.Fatalf("confirmAction with --yes: %v", err)
	}
	for _, want := range []string{
		"--all excludes nothing.",
		"The prompt --yes skipped had no count",
		// The CLAIM, not just a literal: --all is unsized because qctl does not
		// spend the request, and the user is told where the number lives. The
		// string it replaced ("no endpoint reports how many rows are in scope")
		// was false -- GET /datasets/{id} carries bad_rows_count and `describe
		// table` prints it.
		"qctl does not count these rows before acting.",
		"'qctl describe table orders' reports the table's quarantined-row count.",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr does not carry %q: %q", want, stderr.String())
		}
	}
	// The prompt-path lead must NOT appear here: it is what reads as a claim
	// about the result line that follows.
	if strings.Contains(stderr.String(), "No count is shown") {
		t.Errorf("the --yes path used the prompt wording: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("confirmation wrote to stdout: %q", stdout.String())
	}
}

// THE COUNT REQUEST IS FOR THE PROMPT. Under --yes no prompt is shown, so
// sizing one is a request spent for nothing — and when it fails it prints a
// "could not count the rows" warning about a count nobody was going to see.
//
// A nil context is the proof: sizedStatusCount dereferences ctx.ServerURL, so
// if confirmAction still reached it this would panic instead of returning.
// --status passed_validation at TABLE scope is the one selector that would.
func TestConfirmAction_YesDoesNotSpendTheCountRequest(t *testing.T) {
	sup := support{table: []selectorKind{selRowIDs, selStatus, selAll}}
	cmd, stdout, stderr := newConfirmProbe(t, sup, []selectorKind{selRowIDs, selStatus, selAll},
		"--status", "passed_validation", "--yes")

	sel := selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}
	act := action{verb: "hold", gerund: "Hold", extra: []string{"Held rows are exempt from reprocessing."}}

	if err := confirmAction(cmd, nil, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sel, sup, act); err != nil {
		t.Fatalf("confirmAction with --yes: %v", err)
	}
	if !strings.Contains(stderr.String(), "exempt from reprocessing") {
		t.Errorf("--yes dropped the consequence line: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "could not count") {
		t.Errorf("--yes attempted the count for a prompt it never shows: %q", stderr.String())
	}
	if stdout.Len() != 0 {
		t.Errorf("confirmation wrote to stdout: %q", stdout.String())
	}
}

// A prompt that cannot be shown is bad usage, not a server failure: the only
// fix is to change the command line.
func TestConfirmAction_NonInteractiveIsBadUsage(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the non-interactive path cannot be exercised here")
	}
	sup := support{table: []selectorKind{selRowIDs}}
	cmd, _, _ := newConfirmProbe(t, sup, []selectorKind{selRowIDs}, "--row-id", "7")

	err := confirmAction(cmd, nil, scope{kind: scopeTable, tableID: 42, tableName: "orders"},
		selector{kind: selRowIDs, rowIDs: []int{7}}, sup, action{verb: "hold", gerund: "Hold"})
	if err == nil {
		t.Fatal("expected a refusal when stdin cannot be prompted")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("message does not name the fix: %q", err.Error())
	}
}

// The subject is the noun phrase the prompt acts on. Every case here is one a
// nil ctx proves costs no request.
func TestDescribeSubject(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sc        scope
		sel       selector
		sup       support
		want      string
		wantSized bool
	}{
		{"exact list", scope{kind: scopeTable}, selector{kind: selRowIDs, rowIDs: []int{1, 2}}, support{}, "2 row(s)", true},
		// The three --all subjects, one per declared population. The question
		// must not claim a wider population than the count recipe printed
		// directly above it, which is why the noun comes from support.all.
		{"all, undeclared population", scope{kind: scopeTable}, selector{kind: selAll}, support{}, "every row", false},
		{"all, quarantined population", scope{kind: scopeTable}, selector{kind: selAll}, deleteQuarantinedSupport, "every quarantined row", false},
		{"all, clean population", scope{kind: scopeTable}, selector{kind: selAll}, moveSupport, "every clean row", false},
		{
			"an unsizeable status at cloud scope",
			scope{kind: scopeCloudSource, cloudSourceID: 7},
			selector{kind: selStatus, status: api.QuarantineStatusFailedValidation},
			support{},
			"every row with status failed_validation", false,
		},
		{
			"status at file scope is never sized",
			scope{kind: scopeFile, fileID: 3},
			selector{kind: selStatus, status: api.QuarantineStatusPassedValidation},
			support{},
			"every row with status passed_validation", false,
		},
		{
			"an unsizeable status at table scope",
			scope{kind: scopeTable, tableID: 42},
			selector{kind: selStatus, status: api.QuarantineStatusFailedValidation},
			support{},
			"every row with status failed_validation", false,
		},
		{
			// A status the counter CAN answer, for a verb whose bulk path
			// filters awaiting_processing_ and so cannot use it. The nil ctx is
			// the proof the request is not even attempted: sizedStatusCount
			// dereferences ctx.ServerURL, so reaching it would panic here.
			"passed_validation for a verb that skips queued rows",
			scope{kind: scopeTable, tableID: 42},
			selector{kind: selStatus, status: api.QuarantineStatusPassedValidation},
			reprocessSupport,
			"every row with status passed_validation", false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd, _, _ := newConfirmProbe(t, support{table: allSelectorKinds}, allSelectorKinds)
			got, sized := describeSubject(cmd, nil, tc.sc, tc.sel, tc.sup)
			if got != tc.want {
				t.Errorf("describeSubject = %q, want %q", got, tc.want)
			}
			if sized != tc.wantSized {
				t.Errorf("describeSubject sized = %v, want %v", sized, tc.wantSized)
			}
		})
	}
}

// A count request that FAILS must leave the prompt with a reason. Before this,
// describeSubject degraded to the unsized wording while unsizedReason still
// returned "" for passed_validation at table scope, so the prompt carried
// neither a number nor an explanation — the one thing its contract forbids.
//
// The failure is produced by pointing the client at a closed port rather than a
// mock server: the assertion is about what qctl prints when the count is
// unavailable, not about any particular server behaviour.
func TestConfirmAction_AFailedCountStillExplainsTheMissingNumber(t *testing.T) {
	if cmdutil.CanPrompt() {
		t.Skip("stdin is a terminal; the prompt path cannot be exercised here")
	}
	sup := support{table: []selectorKind{selRowIDs, selStatus, selAll}}
	cmd, _, stderr := newConfirmProbe(t, sup, []selectorKind{selRowIDs, selStatus, selAll},
		"--status", "passed_validation")

	ctx := &cmdutil.CommandContext{
		ServerURL:  "http://127.0.0.1:1",
		Credential: &auth.Credential{AccessToken: "t"},
	}
	sel := selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}

	err := confirmAction(cmd, ctx, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sel, sup,
		action{verb: "hold", gerund: "Hold"})
	if err == nil {
		t.Fatal("expected the non-interactive refusal after the count failed")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	got := stderr.String()
	if !strings.Contains(got, "could not count the rows this will affect") {
		t.Errorf("the failed count was not reported: %q", got)
	}
	if !strings.Contains(got, "No count is shown") {
		t.Errorf("the prompt carried neither a number nor a reason: %q", got)
	}
}

// Every verb that reaches sizedStatusPhrase filters its --status rows exactly
// as the counter does, tombstones included, so the count is stated as a
// figure with no hedge.
func TestSizedStatusPhraseStatesTheCount(t *testing.T) {
	got := sizedStatusPhrase(9, api.QuarantineStatusPassedValidation)
	if want := "9 row(s) with status passed_validation"; got != want {
		t.Errorf("sizedStatusPhrase = %q, want %q", got, want)
	}
}

// The count is sized at table and cloud-source scope only. Only on_hold,
// approved, waiting_for_approval, held_for_approval and passed_validation use
// the same predicate as the bulk selector, so every other (scope, status)
// pair must return before it can spend a request.
// A nil ctx is the proof that it does.
func TestSizedStatusCount_OnlyUsableStatusesAtCountableScopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sc     scope
		status api.QuarantineStatus
		sup    support
	}{
		{"file scope", scope{kind: scopeFile, fileID: 3}, api.QuarantineStatusPassedValidation, support{}},
		{"failed_validation at cloud scope", scope{kind: scopeCloudSource, cloudSourceID: 7}, api.QuarantineStatusFailedValidation, support{}},
		{"failed_validation", scope{kind: scopeTable, tableID: 42}, api.QuarantineStatusFailedValidation, support{}},
		// The second gate: the right status at the right scope, but for a verb
		// whose bulk path filters a column the counter does not.
		{"passed_validation for a verb that skips queued rows", scope{kind: scopeTable, tableID: 42}, api.QuarantineStatusPassedValidation, reprocessSupport},
		{"on_hold for a verb that skips queued rows", scope{kind: scopeTable, tableID: 42}, api.QuarantineStatusOnHold, reprocessSupport},
		{"approved for a verb that skips queued rows", scope{kind: scopeTable, tableID: 42}, api.QuarantineStatusApproved, reprocessSupport},
		{"held_for_approval for a verb that skips queued rows", scope{kind: scopeTable, tableID: 42}, api.QuarantineStatusHeldForApproval, reprocessSupport},
		{"passed_validation at cloud scope for a verb that skips queued rows", scope{kind: scopeCloudSource, cloudSourceID: 7}, api.QuarantineStatusPassedValidation, reprocessSupport},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var warn bytes.Buffer
			if n := sizedStatusCount(&warn, nil, tc.sc, tc.status, tc.sup); n != nil {
				t.Errorf("sizedStatusCount = %d, want nil", *n)
			}
			if warn.Len() != 0 {
				t.Errorf("wrote a warning for a case it never attempted: %q", warn.String())
			}
		})
	}
}

// unsizedClause's default arm is failed_validation's reason, worded for it
// alone ("also matches rows whose column is NULL"). A status added to
// validStatuses without a statusBuckets entry would fall into that arm and be
// told something false, so every status is either sizeable or that one.
func TestEveryStatusIsSizeableOrFailedValidation(t *testing.T) {
	sized := 0
	for _, st := range validStatuses {
		switch {
		case sizeableStatus(st):
			sized++
		case st == api.QuarantineStatusFailedValidation:
		default:
			t.Errorf("--status %s has no count bucket and is not failed_validation: "+
				"decide whether statusBuckets gets it, and what unsizedClause says if not", st)
		}
	}
	if sized != len(statusBuckets) {
		t.Errorf("%d of validStatuses are sizeable, statusBuckets has %d entries: "+
			"a bucket names a status --status does not accept", sized, len(statusBuckets))
	}
}

// Each sizeable status reads its OWN bucket. Every value in the fixture is
// distinct, so a status wired to a sibling's field reads a wrong number.
func TestStatusBucketsReadTheirOwnField(t *testing.T) {
	counts := &datasets.QuarantineCounts{
		OnHold: ptrTo(6), Approved: ptrTo(3), WaitingForApproval: ptrTo(9), HeldForApproval: ptrTo(5),
		PassedValidation: ptrTo(17), FailedValidation: ptrTo(4),
	}
	for st, want := range map[api.QuarantineStatus]int{
		api.QuarantineStatusOnHold: 6, api.QuarantineStatusApproved: 3, api.QuarantineStatusWaitingForApproval: 9,
		api.QuarantineStatusHeldForApproval: 5, api.QuarantineStatusPassedValidation: 17,
	} {
		bucket, ok := statusBuckets[st]
		if !ok {
			t.Errorf("statusBuckets has no %s", st)
			continue
		}
		if got := bucket(counts); got == nil || *got != want {
			t.Errorf("statusBuckets[%s] = %v, want %d", st, got, want)
		}
	}
}

// A missing number must be explained, and the explanation must be the right
// one — narrower scope, a verb that skips queued rows, and failed_validation's
// NULL-inclusive selector are three different reasons.
func TestUnsizedReason(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   scope
		sel  selector
		sup  support
		want string // "" means: the prompt has a number, say nothing
	}{
		{"a sized list says nothing", scope{kind: scopeTable}, selector{kind: selRowIDs, rowIDs: []int{1}}, support{}, ""},
		{"passed_validation at table scope is sized", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, support{}, ""},
		{"on_hold at table scope is sized", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusOnHold}, support{}, ""},
		{"approved at table scope is sized", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusApproved}, support{}, ""},
		// The counter and the bulk filters build this bucket from the same
		// QuarantineMixin._quarantine_status_sql (atlas quarantine.py), so no
		// NULL clause belongs here.
		{"held_for_approval at table scope is sized", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusHeldForApproval}, support{}, ""},
		// The same (scope, status) for a verb whose bulk path skips rows already
		// queued: the counter's number is then only a ceiling, so it must be
		// suppressed AND explained.
		{"passed_validation for a verb that skips queued rows", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, reprocessSupport, "only a ceiling"},
		{"on_hold for a verb that skips queued rows", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusOnHold}, reprocessSupport, "only a ceiling"},
		{"approved for a verb that skips queued rows", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusApproved}, reprocessSupport, "only a ceiling"},
		{"held_for_approval for a verb that skips queued rows", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusHeldForApproval}, reprocessSupport, "only a ceiling"},
		// The three --all arms at table scope, one per declared population. The
		// quarantined and clean cases are the whole point of support.all: the
		// same (scope, selector) must name a DIFFERENT number for a verb whose
		// route reads the other table, and before support.all existed this
		// clause said "quarantined" to both.
		{"all, quarantined population", scope{kind: scopeTable}, selector{kind: selAll}, deleteQuarantinedSupport, "'qctl describe table <table>' reports the table's quarantined-row count."},
		{"all, clean population", scope{kind: scopeTable}, selector{kind: selAll}, moveSupport, "'qctl describe table <table>' reports the table's clean-row count."},
		{"all, numeric table name", scope{kind: scopeTable, tableID: 7, tableName: "42"}, selector{kind: selAll}, moveSupport, "'qctl describe table --table 42' reports the table's clean-row count."},
		// An undeclared population names BOTH figures. That is true of
		// `describe table`'s Rows line whatever the verb does, where naming one
		// half would be a claim no support row made — and the tripwire in
		// selector_test.go keeps a shipped verb from reaching it.
		{"all, undeclared population", scope{kind: scopeTable}, selector{kind: selAll}, support{}, "'qctl describe table <table>' reports the table's clean and quarantined row counts."},
		// The unsized --all clause is cased by SCOPE, for the same reason the
		// passed_validation one below is: pointing a cloud-source run at the
		// table's number would be a new imprecision in place of the old false
		// universal. DataSourceModel.bad_rows_count is a per-source column
		// (atlas move_rows_to_quarantine,
		// cettings/cettings/controllers/data_controller/quarantine.py), which
		// `describe cloud-source` prints on its Rows line.
		{"all at cloud scope names the source's own count", scope{kind: scopeCloudSource, cloudSourceID: 7}, selector{kind: selAll}, holdReleaseSupport, "'qctl describe cloud-source 7' reports this source's quarantined-row count."},
		// No verb allows --all at file scope today. The clause must still not
		// send a file-scoped run to the TABLE's number, so it names no command
		// at all there.
		{"all at file scope names no command", scope{kind: scopeFile, fileID: 3}, selector{kind: selAll}, support{}, "qctl does not count these rows before acting."},
		{"passed_validation at cloud scope", scope{kind: scopeCloudSource}, selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, support{}, ""},
		{"failed_validation at cloud scope", scope{kind: scopeCloudSource}, selector{kind: selStatus, status: api.QuarantineStatusFailedValidation}, support{}, "rows whose column is NULL"},
		// Unreachable today (no verb allows --status at file scope) but one
		// support{} row edit away. The wording must follow the actual scope.
		{"passed_validation at file scope", scope{kind: scopeFile}, selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, support{}, "qctl does not count --status rows at file scope."},
		{"failed_validation", scope{kind: scopeTable}, selector{kind: selStatus, status: api.QuarantineStatusFailedValidation}, support{}, "rows whose column is NULL"},
		// Not reachable from resolveSelector, but the merged branch must not
		// render "--status  also matches rows whose column is NULL".
		{"an unset selector", scope{kind: scopeTable}, selector{}, support{}, "qctl does not count these rows before acting."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := unsizedReason(tc.sc, tc.sel, tc.sup)
			if tc.want == "" {
				if got != "" {
					t.Errorf("unsizedReason = %q, want no explanation", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("unsizedReason = %q, want it to carry %q", got, tc.want)
			}
		})
	}
}

// A clean-population --all at cloud-source scope names the source's CLEAN-row
// count, never its quarantined one.
//
// It is its own test rather than a row in the table above, because the
// load-bearing assertion is the negative one: the source's quarantined-row
// figure must not be offered to a verb that does not touch it.
//
// Unreachable from a command line today: move-all-rows-to-quarantine is the
// only clean --all route and it exists at dataset scope alone. It is driven
// here for the reason unroutableSelector's defaults are kept — a support{} edit
// giving a clean verb cloud scope would otherwise print the wrong number and
// nothing would fail.
func TestUnsizedClause_CleanAllAtCloudScopeNamesTheCleanCount(t *testing.T) {
	got := unsizedReason(
		scope{kind: scopeCloudSource, cloudSourceID: 7},
		selector{kind: selAll},
		support{cloud: []selectorKind{selAll}, all: allClean})

	if !strings.Contains(got, "'qctl describe cloud-source 7' reports this source's clean-row count.") {
		t.Errorf("unsizedReason = %q, want the source's clean-row count", got)
	}
	if strings.Contains(got, "quarantined") {
		t.Errorf("unsizedReason = %q offers the quarantined-row figure to a clean-population verb", got)
	}
}

// The two leads share one clause, so a reason can never be right in a prompt
// and wrong (or missing) under --yes. Only the lead may differ, and the --yes
// one must not read as a statement about the result line that follows it.
func TestSkippedPromptReasonSharesTheClauseButNotTheLead(t *testing.T) {
	sc := scope{kind: scopeTable}
	for _, tc := range []struct {
		sel selector
		sup support
	}{
		{selector{kind: selAll}, support{}},
		{selector{kind: selStatus, status: api.QuarantineStatusFailedValidation}, support{}},
		{selector{kind: selStatus, status: api.QuarantineStatusOnHold}, reprocessSupport},
		// The suppression I-1 adds must reach BOTH leads: under --yes this is
		// the only warning a script gets that the number printed next will not
		// match the one a count endpoint would report.
		{selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, reprocessSupport},
	} {
		sel, sup := tc.sel, tc.sup
		prompt := unsizedReason(sc, sel, sup)
		skipped := skippedPromptReason(sc, sel, sup)
		clause := unsizedClause(sc, sel, sup)
		if clause == "" {
			t.Fatalf("unsizedClause(%v) is empty; the case is no longer unsized", sel)
		}
		if !strings.HasSuffix(prompt, clause) || !strings.HasSuffix(skipped, clause) {
			t.Errorf("the two leads do not share one clause:\n prompt=%q\nskipped=%q\n clause=%q",
				prompt, skipped, clause)
		}
		if !strings.HasPrefix(prompt, "No count is shown: ") {
			t.Errorf("prompt reason = %q, want the prompt lead", prompt)
		}
		if strings.Contains(skipped, "No count is shown") {
			t.Errorf("skipped-prompt reason = %q, want it anchored to the skipped prompt", skipped)
		}
	}
	// A sized selector has nothing to explain on either path.
	sized := selector{kind: selRowIDs, rowIDs: []int{1}}
	if unsizedReason(sc, sized, support{}) != "" || skippedPromptReason(sc, sized, support{}) != "" {
		t.Error("a sized selector must produce no explanation on either path")
	}
}

// reportResult must never invent a number: a response without affected_rows
// has no count, and len(selector) is not that number.
func TestReportResult(t *testing.T) {
	sc := scope{kind: scopeCloudSource, cloudSourceID: 7, cloudSourceName: "s3-drop", tableID: 42, tableName: "orders"}
	sel := selector{kind: selSignatures, signatures: []string{"aa", "bb"}}
	act := action{verb: "delete-quarantined", gerund: "Delete"}
	// The verb the act names, so the --all subject below is the population
	// delete-quarantined really acts on rather than a bare "every row".
	sup := deleteQuarantinedSupport

	t.Run("a response with a count states it", func(t *testing.T) {
		cmd, stdout, _ := newConfirmProbe(t, sup, allSelectorKinds)
		reportResult(cmd, sc, sel, sup, act, &bulkResult{AffectedRows: intPtr(9)}, "qctl get data orders --is-bad-data")
		got := stdout.String()
		if !strings.Contains(got, "9 row(s) affected in cloud source s3-drop (id: 7) of table orders") {
			t.Errorf("stdout = %q", got)
		}
		if !strings.Contains(got, "qctl get data orders --is-bad-data") {
			t.Errorf("stdout does not carry the follow-up command: %q", got)
		}
	})

	t.Run("a response without one states none", func(t *testing.T) {
		cmd, stdout, _ := newConfirmProbe(t, sup, allSelectorKinds)
		reportResult(cmd, sc, sel, sup, act, &bulkResult{}, "")
		got := stdout.String()
		if strings.Contains(got, "row(s) affected") {
			t.Errorf("stdout claims an affected-row count the response did not carry: %q", got)
		}
		// The selector's own size is reported as what was REQUESTED, which is
		// true, and never as what was affected, which is not known.
		if !strings.Contains(got, "requested for 2 row(s)") {
			t.Errorf("stdout = %q", got)
		}
	})

	t.Run("a response without one names the route", func(t *testing.T) {
		cmd, stdout, _ := newConfirmProbe(t, sup, allSelectorKinds)
		reportResult(cmd, sc, sel, sup, act, &bulkResult{}, "")
		if got := stdout.String(); !strings.Contains(got, "this route returns no affected-row count") {
			t.Errorf("stdout = %q", got)
		}
	})

	// A nil result is not evidence about the ROUTE — the response may simply
	// not have been decoded — so the no-count wording must not claim it is.
	t.Run("a nil result claims nothing about the route", func(t *testing.T) {
		cmd, stdout, _ := newConfirmProbe(t, sup, allSelectorKinds)
		reportResult(cmd, sc, selector{kind: selAll}, sup, act, nil, "")
		got := stdout.String()
		// The verb's declared population, not a bare "every row":
		// delete-quarantined --all never touches the clean side, and the
		// result line must not imply it did. See allPopulation.subjectNoun.
		if !strings.Contains(got, "requested for every quarantined row") {
			t.Errorf("stdout = %q", got)
		}
		if strings.Contains(got, "route returns no affected-row count") {
			t.Errorf("stdout makes a claim about the route from a nil response: %q", got)
		}
		if strings.Contains(got, "row(s) affected") {
			t.Errorf("stdout invents a count: %q", got)
		}
	})
}

// label is what a prompt and a result line say the action lands on, so a wrong
// one is a user acting on the wrong belief.
func TestScopeLabel(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   scope
		want string
	}{
		{"table by name", scope{kind: scopeTable, tableID: 42, tableName: "orders"}, "table orders (id: 42)"},
		// "table 42", never "table 42 (id: 42)". The parenthesis is there to
		// tell a user who typed a NAME which dataset it resolved to; a user who
		// typed the id is being shown their own number twice. The pin is
		// deliberate and coupled to scope.tableLabel — if that rendering
		// changes, this is the line that must change with it.
		{"table by id", scope{kind: scopeTable, tableID: 42}, "table 42"},
		{"named cloud source", scope{kind: scopeCloudSource, cloudSourceID: 7, cloudSourceName: "s3-drop", tableID: 42, tableName: "orders"}, "cloud source s3-drop (id: 7) of table orders"},
		{"unnamed cloud source", scope{kind: scopeCloudSource, cloudSourceID: 7, tableID: 42}, "cloud source 7 of table 42"},
		{"file", scope{kind: scopeFile, fileID: 3, tableID: 42, tableName: "orders"}, "file 3 of table orders"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.label(); got != tc.want {
				t.Errorf("label() = %q, want %q", got, tc.want)
			}
			if got := tc.sc.kindName(); !strings.Contains(tc.want, strings.ReplaceAll(got, "-", " ")) {
				t.Errorf("kindName() = %q, which the label %q does not name", got, tc.want)
			}
		})
	}
}

// The unsized-count clause is cased by scope, and the FILE arm names no command
// at all. Asserted as an absence because the table's substring is a substring of
// every arm: a table-scope fall-through would satisfy any positive assertion
// here while sending a file-scoped run to the whole table's number.
//
// --all is unreachable at file scope for every verb today, which is exactly why
// this is worth pinning — the wrong default would ship unnoticed.
func TestUnsizedClause_FileScopeNamesNoCount(t *testing.T) {
	got := unsizedClause(scope{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3},
		selector{kind: selAll}, support{})
	if got != "qctl does not count these rows before acting." {
		t.Errorf("unsizedClause = %q, want the bare reason with no recipe", got)
	}
	if strings.Contains(got, "describe") {
		t.Errorf("a file-scoped run was pointed at another scope's count: %q", got)
	}
}

// --- lowerLead -------------------------------------------------------------

// The six words the eight selector verbs ship as gerunds — Delete twice, Move,
// Reprocess twice, Hold, Approve, Reset — must come out byte-identical to what
// strings.ToLower produces. The two after them are why it was replaced: a
// gerund carrying USER TEXT is what ToLower corrupts, and set-cell's is the
// first one.
func TestLowerLead(t *testing.T) {
	cases := map[string]string{
		"Delete":                "delete",
		"Hold":                  "hold",
		"Approve":               "approve",
		"Reset":                 "reset",
		"Move":                  "move",
		"Reprocess":             "reprocess",
		`Set State to "CA" on`:  `set State to "CA" on`,
		`Set state to "N/A" on`: `set state to "N/A" on`,
		"Éclair":                "éclair",
		"":                      "",
	}
	for in, want := range cases {
		if got := lowerLead(in); got != want {
			t.Errorf("lowerLead(%q) = %q, want %q", in, got, want)
		}
	}
}
