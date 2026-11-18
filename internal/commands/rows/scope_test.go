package rows

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// --cloud-source and --file-id are mutually exclusive scopes; naming both is
// bad usage, not a silent precedence rule.
func TestLocalScope_CloudSourceAndFileConflict(t *testing.T) {
	sup := support{
		table: []selectorKind{selSignatures},
		cloud: []selectorKind{selSignatures},
		file:  []selectorKind{selSignatures},
	}
	cmd := newProbe(sup, selSignatures)
	if err := cmd.ParseFlags([]string{"--cloud-source-id", "7", "--file-id", "3"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	// localScope takes no context at all: this refusal cannot reach a server
	// even by accident, which is a stronger statement than the nil ctx that
	// stood here while the resolve sat in the same function.
	_, _, err := localScope(cmd, "orders", sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected naming two scopes to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// A verb whose support{} has a nil `file` list must refuse --file-id.
//
// Note the flag is only REGISTERED when the verb supports the scope, so for a
// table-only verb cobra rejects it first. This test uses a verb that registers
// the flag (file support present) against a support value that does not, which
// is the shape localScope has to defend: the two must never disagree.
func TestLocalScope_UnsupportedScopeIsBadUsage(t *testing.T) {
	registering := support{
		table: []selectorKind{selRowIDs},
		file:  []selectorKind{selSignatures},
	}
	cmd := newProbe(registering, selRowIDs, selSignatures)
	if err := cmd.ParseFlags([]string{"--file-id", "3"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	enforcing := support{table: []selectorKind{selRowIDs}} // file: nil
	_, _, err := localScope(cmd, "orders", enforcing, "send-to-quarantine")
	if err == nil {
		t.Fatal("expected file scope to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// A table-only verb does not register the narrowing flags at all, so the
// refusal is cobra's. The table selector pair, in contrast, is registered on
// every verb: the table is the object being acted on, not a narrowing.
func TestLocalScope_TableOnlyVerbHasNoScopeFlags(t *testing.T) {
	cmd := newProbe(support{table: []selectorKind{selRowIDs}}, selRowIDs)
	for _, name := range []string{"cloud-source", "cloud-source-id", "file-id"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("--%s is registered on a table-only verb", name)
		}
	}
	for _, name := range []string{"table", "table-id"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("--%s must be registered on every rows verb", name)
		}
	}
}

// tableNameFrom is what every message and follow-up command line says, so its
// rule is pinned: --table is authoritative, a positional NAME is kept, and a
// positional that parses as an integer is an id and yields no name.
func TestTableNameFrom(t *testing.T) {
	for _, tc := range []struct {
		name     string
		flags    []string
		tableArg string
		want     string
	}{
		{"positional name", nil, "orders", "orders"},
		{"positional id", nil, "42", ""},
		{"no table at all", nil, "", ""},
		{"table flag", []string{"--table", "orders"}, "", "orders"},
		{"table-id flag only", []string{"--table-id", "42"}, "", ""},
		{"flag wins over positional", []string{"--table", "orders"}, "7", "orders"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newProbe(support{table: []selectorKind{selRowIDs}}, selRowIDs)
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			if got := tableNameFrom(cmd, tc.tableArg); got != tc.want {
				t.Errorf("tableNameFrom(%q) = %q, want %q", tc.tableArg, got, tc.want)
			}
		})
	}
}

// Local file scope retains the table; table scope needs no remote lookup for an ID.
func TestLocalScope_TableAndFileScopes(t *testing.T) {
	ctx := &cmdutil.CommandContext{}
	sup := support{
		table: []selectorKind{selSignatures},
		cloud: []selectorKind{selSignatures},
		file:  []selectorKind{selSignatures},
	}

	t.Run("no narrowing flag is table scope", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--table-id", "42"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sc, f, err := localScope(cmd, "", sup, "delete-quarantined")
		if err != nil {
			t.Fatalf("localScope: %v", err)
		}
		if sc.kind != scopeTable || sc.tableID != 42 {
			t.Errorf("scope = %+v, want table scope on 42", sc)
		}
		if sc.tableName != "" {
			t.Errorf("tableName = %q, want empty: --table-id carries no name", sc.tableName)
		}
		// And hydrating it costs nothing either: cmdutil.OptionalTableID
		// short-circuits on --table-id, so the empty ctx is never dialled.
		if sc, err = hydrateScope(cmd, ctx, sc, "", f); err != nil {
			t.Fatalf("hydrateScope: %v", err)
		}
		if sc.tableID != 42 {
			t.Errorf("tableID = %d after hydrating, want 42", sc.tableID)
		}
	})

	t.Run("--file-id narrows but keeps the table", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--table-id", "42", "--file-id", "3"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sc, _, err := localScope(cmd, "", sup, "delete-quarantined")
		if err != nil {
			t.Fatalf("localScope: %v", err)
		}
		if sc.kind != scopeFile || sc.fileID != 3 {
			t.Errorf("scope = %+v, want file scope on 3", sc)
		}
		if sc.tableID != 42 {
			t.Errorf("tableID = %d, want the table to survive the narrowing", sc.tableID)
		}
	})

	// A table NAME, which is the case that would cost a resolve: this refusal
	// is flags-only too, and localScope has no way to make that call.
	t.Run("--file-id 0 is bad usage, not a dropped narrowing", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--file-id", "0"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		_, _, err := localScope(cmd, "orders", sup, "delete-quarantined")
		if err == nil {
			t.Fatal("expected --file-id 0 to be refused")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})
}

// The two cloud-source flags are one selector, and neither may become a way
// into cmdutil.ResolveCloudSource's auto-detection: on a read, silently
// picking a table's only cloud source is a convenience; on a delete it is the
// wrong rows.
func TestLocalScope_CloudSourceNeverAutoDetects(t *testing.T) {
	sup := support{
		table: []selectorKind{selSignatures},
		cloud: []selectorKind{selSignatures},
	}

	t.Run("naming it twice is refused", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--cloud-source", "s3-drop", "--cloud-source-id", "7"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		// No ctx: decided from flags alone, before any resolve.
		_, _, err := localScope(cmd, "orders", sup, "delete-quarantined")
		if err == nil {
			t.Fatal("expected --cloud-source and --cloud-source-id together to be refused")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})

	// Both of these pass the table as a NAME, the case that would cost an API
	// call (--table-id short-circuits the resolve). localScope cannot make that
	// call at all, so reaching the refusal is the proof that these are decided
	// from flags alone, as checkScopeFlags claims. With --table-id they passed
	// even while the refusal sat after the resolve.
	t.Run("an empty name is refused before the table is resolved", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--cloud-source", ""}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		_, _, err := localScope(cmd, "orders", sup, "delete-quarantined")
		if err == nil {
			t.Fatal("expected an empty --cloud-source to be refused")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
		if !strings.Contains(err.Error(), "--cloud-source-id") {
			t.Errorf("message does not name the unambiguous alternative: %q", err.Error())
		}
	})

	t.Run("--cloud-source-id 0 is refused before the table is resolved", func(t *testing.T) {
		cmd := newProbe(sup, selSignatures)
		if err := cmd.ParseFlags([]string{"--cloud-source-id", "0"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		_, _, err := localScope(cmd, "orders", sup, "delete-quarantined")
		if err == nil {
			t.Fatal("expected --cloud-source-id 0 to be refused")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})
}

// getDataLine is the ONE builder behind both the "See the current state with"
// follow-up and the --row-id refusal's signature recipe. They were separate
// once and drifted — the follow-up narrowed to the acted-on cloud source or
// file while the recipe named the whole table. On a CONTENT signature that
// mismatch can hand the user a signature belonging to a row in a different
// file, whose deletion then writes an exclusion entry, so the whole
// matrix is pinned here rather than sampled through one caller.
func TestScopeGetDataLine(t *testing.T) {
	table := scope{kind: scopeTable, tableID: 42, tableName: "orders"}
	numericName := scope{kind: scopeTable, tableID: 7, tableName: "42"}
	cloud := scope{kind: scopeCloudSource, tableID: 42, tableName: "orders", cloudSourceID: 7, cloudSourceName: "s3-drop"}
	file := scope{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3}
	byID := scope{kind: scopeTable, tableID: 42}
	spaced := scope{kind: scopeTable, tableID: 42, tableName: "my table"}
	quoted := scope{kind: scopeTable, tableID: 42, tableName: "O'Brien"}

	for _, tc := range []struct {
		name      string
		sc        scope
		isBadData bool
		want      string
	}{
		{"table, clean", table, false, "qctl get data orders"},
		{"table, quarantined", table, true, "qctl get data orders --is-bad-data"},
		{"numeric table name", numericName, false, "qctl get data --table 42"},
		{"cloud source, clean", cloud, false, "qctl get data orders --data-source-model-id 7"},
		{"cloud source, quarantined", cloud, true, "qctl get data orders --data-source-model-id 7 --is-bad-data"},
		{"file, clean", file, false, "qctl get data orders --stored-item-id 3"},
		{"file, quarantined", file, true, "qctl get data orders --stored-item-id 3 --is-bad-data"},
		// No name was supplied, so the line names the table by id — which
		// 'get data' resolves id-first, exactly like the positional here.
		{"table named by id", byID, true, "qctl get data 42 --is-bad-data"},
		// --table's usage says a name with spaces is legal if quoted, so the
		// line that reads such a table back has to quote it too: unquoted it
		// is two positionals and cobra refuses the paste at exit 2.
		{"table name with a space", spaced, true, "qctl get data 'my table' --is-bad-data"},
		{"table name with a quote", quoted, false, `qctl get data 'O'\''Brien'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.getDataLine(tc.isBadData); got != tc.want {
				t.Errorf("getDataLine = %q, want %q", got, tc.want)
			}
		})
	}
}

// tableCommandRef is the identity boundary for every runnable command this
// package prints. A positional that strconv.Atoi accepts is an ID everywhere
// these commands resolve one, so only a numeric-looking NAME needs the
// explicit name-only flag. IDs and ordinary names keep the compact positional
// form users already see.
func TestScopeTableCommandRefPreservesNumericNameIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		sc   scope
		want string
	}{
		{"ordinary name", scope{tableID: 42, tableName: "orders"}, "orders"},
		{"numeric name", scope{tableID: 7, tableName: "42"}, "--table 42"},
		{"numeric name with leading zeros", scope{tableID: 7, tableName: "0042"}, "--table 0042"},
		{"explicit id", scope{tableID: 42}, "42"},
		{"quoted ordinary name", scope{tableID: 42, tableName: "my table"}, "'my table'"},
		{"unknown", scope{}, "<table>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.sc.tableCommandRef(); got != tc.want {
				t.Errorf("tableCommandRef = %q, want %q", got, tc.want)
			}
		})
	}
}

// A clean population contributes NO argument rather than an empty one: the line
// is meant to be pasted, and "qctl get data orders " with a dangling space is a
// line nobody typed.
func TestScopeGetDataLineHasNoTrailingSpace(t *testing.T) {
	for _, sc := range []scope{
		{kind: scopeTable, tableName: "orders"},
		{kind: scopeCloudSource, tableName: "orders", cloudSourceID: 7},
		{kind: scopeFile, tableName: "orders", fileID: 3},
	} {
		for _, bad := range []bool{false, true} {
			if got := sc.getDataLine(bad); got != strings.TrimSpace(got) {
				t.Errorf("getDataLine(%v) = %q, want no surrounding whitespace", bad, got)
			}
		}
	}
}

// Every verb in the group takes the table positional OPTIONALLY, because
// --table/--table-id name the table too. This is the group-wide half of the
// claim TestReprocessClean_TableIDFlagWithoutPositional used to make on its
// own: it exercised one verb through a full run, so a revert to ExactArgs(1)
// on any OTHER verb was caught by nothing.
//
// Both directions are asserted. Zero args must be ACCEPTED, or --table-id stops
// working; two args must be REFUSED, or the check would also pass against
// ArbitraryArgs (or a nil Args, which cobra treats as arbitrary), which accepts
// zero for the wrong reason.
func TestEveryVerbTakesTheTablePositionalOptionally(t *testing.T) {
	subs := NewCommand().Commands()
	if len(subs) != 12 {
		t.Fatalf("the group has %d verbs, want 12; update this test deliberately", len(subs))
	}
	for _, sub := range subs {
		if sub.Args == nil {
			t.Errorf("'qctl rows %s' has no Args validator, so it accepts any number of "+
				"positionals; want MaximumNArgs(1)", sub.Name())
			continue
		}
		if err := sub.Args(sub, nil); err != nil {
			t.Errorf("'qctl rows %s' rejects a missing table positional (%v); "+
				"--table/--table-id name the table too", sub.Name(), err)
		}
		if err := sub.Args(sub, []string{"orders", "extra"}); err == nil {
			t.Errorf("'qctl rows %s' accepts two positionals; want at most one", sub.Name())
		}
	}
}

// countTableResolves replaces the fixture's table lookup with a counting one.
//
// The name-only resolve is the ONE request every run path could make before the
// selector was checked — cmdutil.ResolveTableID is what turns "orders" into 42,
// and the cloud-source lookups it feeds cannot run before it — so counting it
// is counting every request the old ordering spent on a doomed command line.
func countTableResolves(mock *testutil.MockAPIServer) *int {
	hits := 0
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		hits++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	return &hits
}

// THE ORDERING CONTRACT, on all four run paths.
//
// A selector refusal is decided from the command line alone, so CLAUDE.md's
// rule applies: it exits 2, and it costs nothing. Before the reorder the table
// was resolved first, so the SAME typo answered differently depending on how
// the table was named — `qctl rows hold 42` exited 2 while `qctl rows hold
// orders` spent a GET /datasets and then exited 1 on whatever the network said.
//
// Every run path is listed because the ordering was written out four times, and
// three passing copies say nothing about the fourth. The verbs chosen are one
// per path, not one per verb: runDelete, runKeepInQuarantine, runMove and
// runReprocessQuarantined.
func TestSelectorRefusalCostsNoRequestOnANamedTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"runDelete", []string{"rows", "delete-quarantined", "orders"}},
		{"runKeepInQuarantine", []string{"rows", "hold", "orders"}},
		{"runMove", []string{"rows", "send-to-quarantine", "orders"}},
		{"runReprocessQuarantined", []string{"rows", "reprocess-quarantined", "orders"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newRowsTestEnv(t)
			resolves := countTableResolves(mock)

			_, _, err := runRows(t, append([]string{}, tc.args...)...)
			if err == nil {
				t.Fatal("expected a missing selector to be refused")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2 — a client-side usage failure", code)
			}
			if !strings.Contains(err.Error(), "needs a row selector") {
				t.Errorf("error is not the selector refusal: %v", err)
			}
			if *resolves != 0 {
				t.Errorf("the table was resolved %d time(s) for a command line that was "+
					"already refusable; a client-side refusal must cost no request", *resolves)
			}
		})
	}
}

// The same ordering, seen from the other end: a scope refusal must still be
// reported BEFORE a selector refusal when both apply, because the scope is what
// decides which selectors are even legal. checkScopeFlags runs first inside
// localScope, and resolveTarget calls localScope first.
func TestScopeRefusalIsReportedBeforeTheSelectorRefusal(t *testing.T) {
	mock := newRowsTestEnv(t)
	resolves := countTableResolves(mock)

	// Two scopes AND no selector. The scope error is the one that must surface.
	_, _, err := runRows(t, "rows", "delete-quarantined", "--reason", testReason, "orders",
		"--cloud-source-id", "7", "--file-id", "3")
	if err == nil {
		t.Fatal("expected naming two scopes to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	if !strings.Contains(err.Error(), "a row action has one scope") {
		t.Errorf("the selector refusal won over the scope refusal: %v", err)
	}
	if *resolves != 0 {
		t.Errorf("the table was resolved %d time(s)", *resolves)
	}
}

// kindName's default used to be "table", so a refusal for an unknown scopeKind
// named the wrong scope — in unroutableSelector's message, which exists to be
// read during the debugging session that bug caused, and which is the message a
// fourth scopeKind is most likely to produce.
//
// The three real kinds keep their names; the fourth says it does not have one.
func TestScopeKindName_DoesNotCallAnUnknownKindTheTable(t *testing.T) {
	// One literal, reused: goconst counts every occurrence in the package and
	// reports a position in the non-test file, where the _test.go exclusion
	// cannot reach it.
	const tableName = "table"
	for _, tc := range []struct {
		kind scopeKind
		want string
	}{
		{scopeTable, tableName},
		{scopeCloudSource, "cloud-source"},
		{scopeFile, "file"},
	} {
		if got := (scope{kind: tc.kind}).kindName(); got != tc.want {
			t.Errorf("kindName(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
	// The zero value is scopeTable, so "table" must still be what an
	// unset-but-real scope renders — several messages are built from scope{}.
	if got := (scope{}).kindName(); got != tableName {
		t.Errorf("the zero scope renders %q, want %q", got, tableName)
	}

	unknown := scope{kind: scopeFile + 1}
	if got := unknown.kindName(); got == tableName {
		t.Fatalf("an unknown scopeKind still renders %q", got)
	}
	// Named so a bug report can start from the number.
	if got := unknown.kindName(); !strings.Contains(got, "unknown") || !strings.Contains(got, "3") {
		t.Errorf("kindName() = %q, want it to name the unknown kind and its value", got)
	}
	// And it must reach the two messages that matter. unroutableSelector is the
	// bug report; label() is the CONFIRMATION PROMPT, which renders before any
	// route dispatch and so is what an unknown kind reaches first.
	err := unroutableSelector("hold", selector{kind: selRowIDs}, unknown)
	if strings.Contains(err.Error(), "at table scope") {
		t.Errorf("unroutableSelector calls an unknown scope the table: %v", err)
	}
	labelled := scope{kind: scopeFile + 1, tableID: 42, tableName: "orders"}.label()
	if strings.HasPrefix(labelled, tableName+" ") {
		t.Errorf("the prompt would say the action lands on the table: %q", labelled)
	}
	if !strings.Contains(labelled, "unknown") {
		t.Errorf("label() = %q, want it to name the unknown kind", labelled)
	}
	// The real table label is unchanged: the zero value and scopeTable both
	// still read "table orders (id: 42)".
	if got := (scope{tableID: 42, tableName: "orders"}).label(); got != tableName+" orders (id: 42)" {
		t.Errorf("the zero scope's label changed: %q", got)
	}

	// The unknown-kind arm names the table through tableLabel, like the three
	// real arms, so it cannot reintroduce the doubled id on a table named by
	// id. Asserted here rather than in TestScopeLabel's table because that
	// table also checks kindName is readable inside the label, and an unknown
	// kind's name is not written as prose.
	if got := (scope{kind: scopeFile + 1, tableID: 42}).label(); strings.Contains(got, "42 (id: 42)") {
		t.Errorf("label() = %q, want the id named once", got)
	}
}

// shellQuote must escape exactly as shellArg does, and an apostrophe is the
// only character where a hand-written second copy can differ while every other
// value agrees -- which is how one shipped. A splice missing its backslash
// renders O'Brien as a run a shell reads back as OBrien, so the prompt that is
// the last chance to catch a wrong value would name a value the write never
// used: the same defect as formatting it with %q, which is what these two
// functions replaced.
func TestShellQuoteEscapesLikeShellArg(t *testing.T) {
	if got, want := shellQuote("O'Brien"), `'O'\''Brien'`; got != want {
		t.Errorf("shellQuote(O'Brien) = %s, want %s", got, want)
	}

	// The tripwire rather than the example: every value shellArg decides to
	// quote must come back byte-identical from shellQuote, so a second copy of
	// the splice cannot appear again without failing here.
	for _, in := range []string{"", " ", "it's", "a'b'c", "'", "''", "N/A", "$(rm -rf )", `^\s+`, "my table"} {
		// shellQuote is the always-quoting twin, so for a value shellArg leaves
		// bare the expected form is that bare word in quotes -- a bare word
		// cannot hold an apostrophe, since the allowlist excludes one.
		want := shellArg(in)
		if !strings.HasPrefix(want, "'") {
			want = "'" + want + "'"
		}
		if q := shellQuote(in); q != want {
			t.Errorf("shellQuote(%q) = %s, want %s -- the two escapers have drifted", in, q, want)
		}
	}
}
