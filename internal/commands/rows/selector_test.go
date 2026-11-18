package rows

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func intPtr(v int) *int { return &v }

// validStatuses is spelled out, so a status the spec RENAMES stops compiling
// and one it ADDS or DROPS goes unnoticed — except here. api.StringEnumValues
// is regenerated from the raw spec by the same `make generate-client` that
// regenerates the constants, so any change to the enum turns this red.
//
// Order is pinned as well as membership: the invalid-status refusal lists the
// values in this order, and statusUsage spells the same list.
func TestValidStatusesAreTheSpecEnum(t *testing.T) {
	spec, ok := api.StringEnumValues["QuarantineStatus"]
	if !ok || len(spec) == 0 {
		t.Fatal("api.StringEnumValues has no QuarantineStatus entry; this check cannot fail as written")
	}
	got := make([]string, len(validStatuses))
	for i, v := range validStatuses {
		got[i] = string(v)
	}
	if strings.Join(got, "|") != strings.Join(spec, "|") {
		t.Errorf("validStatuses = %v, the spec declares %v\n"+
			"Update validStatuses, statusUsage, statusBuckets (confirm.go) and every "+
			"help text naming a status.", got, spec)
	}
	if want := "(" + strings.Join(spec, "|") + ")"; !strings.HasSuffix(statusUsage, want) {
		t.Errorf("statusUsage = %q, want it to end with the spec's list %q", statusUsage, want)
	}
}

// The group's help names statuses in examples and prose, and a renamed status
// left there is a line that fails with "invalid --status" when pasted. Every
// status the group's Short and Long text names must be one resolveSelector
// accepts.
//
// A status is recognized in two shapes: any value on an example line, and a
// snake_case word after --status anywhere. Prose such as "--status skips rows"
// is neither, and a one-word status in prose ("--status approved names ...")
// is not checked.
func TestRowsHelpNamesOnlyAcceptedStatuses(t *testing.T) {
	accepted := map[string]bool{}
	for _, v := range validStatuses {
		accepted[string(v)] = true
	}
	root := NewCommand()
	refs := 0
	for _, c := range append([]*cobra.Command{root}, root.Commands()...) {
		for _, text := range []string{c.Short, c.Long} {
			named := map[string]bool{}
			for _, re := range []*regexp.Regexp{statusInExample, snakeCaseStatus} {
				for _, m := range re.FindAllStringSubmatch(text, -1) {
					named[m[1]] = true
				}
			}
			for word := range named {
				refs++
				if !accepted[word] {
					t.Errorf("'%s' help names --status %s, which resolveSelector refuses", c.CommandPath(), word)
				}
			}
		}
	}
	if refs < 5 {
		t.Fatalf("found %d status names in the group's help, want at least 5 — "+
			"the patterns stopped matching and this test is proving nothing", refs)
	}
}

var (
	// statusInExample: the value of --status on an indented `qctl ...` line.
	statusInExample = regexp.MustCompile(`(?m)^\s+qctl .*--status ([a-z][a-z_]*)`)
	// snakeCaseStatus: a word with an underscore after --status, which prose
	// never is.
	snakeCaseStatus = regexp.MustCompile(`--status ([a-z]+(?:_[a-z]+)+)`)
)

// newProbe builds a bare command carrying the flags a verb registers, so the
// validators can be exercised without a server.
func newProbe(sup support, kinds ...selectorKind) *cobra.Command {
	cmd := &cobra.Command{Use: "probe"}
	addScopeFlags(cmd, sup)
	addSelectorFlags(cmd, sup, kinds...)
	return cmd
}

func TestResolveSelector_NoSelectorIsBadUsage(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures, selStatus, selAll}}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	_ = cmd.ParseFlags(nil)

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected an error when no selector is given")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

func TestResolveSelector_TwoSelectorsIsBadUsage(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures, selStatus, selAll}}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--signature", "deadbeef", "--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected an error when two selectors are given")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// --row-id on a delete verb is the one refusal that must teach the recipe: the
// delete route identifies rows by signature, and an id turns into a signature
// only ONE ROW AT A TIME. The per-id lookup exists -- `qctl get data
// --filter-by-field-name id --filter-by-field-value <row-id>` -- and what does
// not is a BULK one: GetDataParams has no ids parameter and FilterOperator has
// no `in`. Do not restate that evidence as "no id->signature lookup"; the
// narrow claim is the only one it supports.
func TestResolveSelector_RowIDOnDeleteNamesTheRecipe(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures, selStatus, selAll}}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--row-id", "7"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected --row-id to be refused on a delete verb")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	// The recipe is the point of this message, so it IS asserted — but the exit
	// code above is what the contract test is.
	if !strings.Contains(err.Error(), "initial_signature_") {
		t.Errorf("message does not name the column to read: %q", err.Error())
	}
	// Unit-level guard on the recipe: it must name the same rows the refused
	// verb would have deleted, in BOTH axes. The population comes from
	// support.isBadData — the one place a verb declares it, and the same value
	// runDelete puts on the wire — and the scope narrows the listing, because
	// initial_signature_ is a content hash and an un-narrowed one can name a
	// row in a different file. The command-level pair lives in delete_test.go.
	sc := scope{kind: scopeTable, tableID: 42, tableName: "orders"}
	cleanSup := support{table: []selectorKind{selSignatures}}
	badSup := support{table: []selectorKind{selSignatures}, isBadData: true}

	cleanErr := unsupportedSelector(selRowIDs, sc, cleanSup, "clean-verb")
	if !strings.Contains(cleanErr.Error(), "qctl get data orders -o json") {
		t.Errorf("clean-verb recipe = %q, want the clean population", cleanErr.Error())
	}
	if strings.Contains(cleanErr.Error(), "--is-bad-data") {
		t.Errorf("clean-verb recipe reads the quarantine table: %q", cleanErr.Error())
	}
	badErr := unsupportedSelector(selRowIDs, sc, badSup, "delete-quarantined")
	if !strings.Contains(badErr.Error(), "qctl get data orders --is-bad-data -o json") {
		t.Errorf("delete-quarantined recipe = %q, want the quarantine population", badErr.Error())
	}
	// Narrowed scopes: an un-narrowed listing is the finding this guard exists
	// for, so both narrowings are asserted, not just the table case above.
	fileErr := unsupportedSelector(selRowIDs,
		scope{kind: scopeFile, tableID: 42, tableName: "orders", fileID: 3}, badSup, "delete-quarantined")
	if !strings.Contains(fileErr.Error(), "qctl get data orders --stored-item-id 3 --is-bad-data -o json") {
		t.Errorf("file-scope recipe = %q, want it narrowed to the file", fileErr.Error())
	}
	cloudErr := unsupportedSelector(selRowIDs,
		scope{kind: scopeCloudSource, tableID: 42, tableName: "orders", cloudSourceID: 7}, cleanSup, "clean-verb")
	if !strings.Contains(cloudErr.Error(), "qctl get data orders --data-source-model-id 7 -o json") {
		t.Errorf("cloud-scope recipe = %q, want it narrowed to the cloud source", cloudErr.Error())
	}
	if !strings.Contains(err.Error(), "qctl get data") {
		t.Errorf("message does not name the command that produces signatures: %q", err.Error())
	}
}

// --status at cloud-source scope is refused because
// DeleteRowsInDataSourceParams has no QuarantineStatus field at all. The message
// must name what the scope DOES support so the user's next command works.
func TestResolveSelector_StatusUnsupportedAtCloudSourceScope(t *testing.T) {
	sup := support{
		table: []selectorKind{selSignatures, selStatus, selAll},
		cloud: []selectorKind{selSignatures, selAll},
	}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--status", "failed_validation"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeCloudSource, cloudSourceID: 7, cloudSourceName: "s3-drop"}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected --status to be refused at cloud-source scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	for _, want := range []string{"--signature", "--all"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %s as supported: %q", want, err.Error())
		}
	}
}

func TestResolveSelector_UnknownStatusIsBadUsage(t *testing.T) {
	sup := support{table: []selectorKind{selStatus}}
	cmd := newProbe(sup, selStatus)
	if err := cmd.ParseFlags([]string{"--status", "pending"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "hold")
	if err == nil {
		t.Fatal("expected a bad status to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// A malformed signature is caught before any request. The backend 422s on the
// whole list anyway (_hex_signatures_to_bytes, data_view_core.py), but a
// destructive call is worth refusing locally and for free.
func TestResolveSelector_NonHexSignatureIsBadUsage(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures}}
	cmd := newProbe(sup, selSignatures)
	if err := cmd.ParseFlags([]string{"--signature", "zzzz"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected a non-hex signature to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// --all for hold/release exists ONLY at cloud-source scope: at table scope an
// empty selector is a 422 from the backend (update_keep_in_quarantine_core,
// data_view_core.py), so qctl must not offer it there.
func TestResolveSelector_AllRefusedAtTableScopeForHold(t *testing.T) {
	sup := support{
		table: []selectorKind{selRowIDs, selStatus},
		cloud: []selectorKind{selRowIDs, selStatus, selAll},
	}
	cmd := newProbe(sup, selRowIDs, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sup, "hold")
	if err == nil {
		t.Fatal("expected --all to be refused at table scope for hold")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// The refusals above are only half the contract: every accepted selector must
// also land in the right field, since that field is what the request body is
// built from. A --status that parsed into rowIDs would act on nothing and
// report success.
func TestResolveSelector_AcceptedSelectorsParse(t *testing.T) {
	sup := support{table: allSelectorKinds}

	t.Run("row ids", func(t *testing.T) {
		cmd := newProbe(sup, allSelectorKinds...)
		if err := cmd.ParseFlags([]string{"--row-id", "7", "--row-id", "9"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "hold")
		if err != nil {
			t.Fatalf("resolveSelector: %v", err)
		}
		if sel.kind != selRowIDs || len(sel.rowIDs) != 2 || sel.rowIDs[0] != 7 || sel.rowIDs[1] != 9 {
			t.Errorf("selector = %+v, want row ids [7 9]", sel)
		}
	})

	t.Run("a non-positive row id is refused", func(t *testing.T) {
		cmd := newProbe(sup, allSelectorKinds...)
		if err := cmd.ParseFlags([]string{"--row-id", "0"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "hold")
		if err == nil {
			t.Fatal("expected --row-id 0 to be refused")
		}
		if code := qerrors.GetExitCode(err); code != 2 {
			t.Errorf("GetExitCode = %d, want 2", code)
		}
	})

	t.Run("signatures", func(t *testing.T) {
		cmd := newProbe(sup, allSelectorKinds...)
		if err := cmd.ParseFlags([]string{"--signature", "deadbeef"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
		if err != nil {
			t.Fatalf("resolveSelector: %v", err)
		}
		if sel.kind != selSignatures || len(sel.signatures) != 1 || sel.signatures[0] != "deadbeef" {
			t.Errorf("selector = %+v, want signatures [deadbeef]", sel)
		}
	})

	// The five words spelled out, not ranged over validStatuses: ranging over
	// the implementation's own list would pass if that list lost a status.
	t.Run("every status the user can type", func(t *testing.T) {
		for raw, want := range map[string]api.QuarantineStatus{
			"on_hold":           api.QuarantineStatusOnHold,
			"approved":          api.QuarantineStatusApproved,
			"held_for_approval": api.QuarantineStatusHeldForApproval,
			"passed_validation": api.QuarantineStatusPassedValidation,
			"failed_validation": api.QuarantineStatusFailedValidation,
		} {
			cmd := newProbe(sup, allSelectorKinds...)
			if err := cmd.ParseFlags([]string{"--status", raw}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "hold")
			if err != nil {
				t.Fatalf("resolveSelector(--status %s): %v", raw, err)
			}
			if sel.kind != selStatus || sel.status != want {
				t.Errorf("--status %s parsed to %+v, want status %s", raw, sel, want)
			}
		}
	})

	t.Run("all", func(t *testing.T) {
		cmd := newProbe(sup, allSelectorKinds...)
		if err := cmd.ParseFlags([]string{"--all"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
		if err != nil {
			t.Fatalf("resolveSelector: %v", err)
		}
		if sel.kind != selAll {
			t.Errorf("selector = %+v, want --all", sel)
		}
	})
}

// THE CRITICAL ONE. --all is the only selector whose value is a bool, and
// deciding it was "given" from Changed alone turned an explicit negation into
// the most destructive action this group has: `--all=$WANT_ALL` with
// WANT_ALL=false deleted every quarantined row.
func TestResolveSelector_NegatedAllIsRefusedNotObeyed(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures, selStatus, selAll}}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--all=false"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}

	sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42, tableName: "orders"}, sup, "delete-quarantined")
	if err == nil {
		t.Fatalf("--all=false was accepted as selector %q — it must never mean 'act on every row'", sel.kind)
	}
	if sel.kind == selAll {
		t.Errorf("selector kind is %q on the error path", sel.kind)
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	// The refusal has to say how to get either outcome, or a script author has
	// no way to tell which half they got wrong.
	for _, want := range []string{"--all", "--signature", "--status"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message does not name %s: %q", want, err.Error())
		}
	}
}

// --all=false must not become a way to smuggle a second selector past the
// arity check either.
func TestResolveSelector_NegatedAllIsRefusedBesideARealSelector(t *testing.T) {
	sup := support{table: []selectorKind{selSignatures, selStatus, selAll}}
	cmd := newProbe(sup, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--all=false", "--status", "failed_validation"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
	if err == nil {
		t.Fatal("expected --all=false beside --status to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
}

// A refusal must not send the user to a flag that will then be refused. --all
// is REGISTERED whenever any scope of the verb accepts it, so at a scope that
// does not, "to act on every row in scope pass --all" is advice that fails.
func TestResolveSelector_NegatedAllDoesNotSuggestAnUnsupportedAll(t *testing.T) {
	for _, tc := range []struct {
		name      string
		sup       support
		kinds     []selectorKind
		sc        scope
		verb      string
		wantScope string   // the scope --all IS supported at
		wantNamed []string // what this scope accepts instead
	}{
		{
			// hold: --all is cloud-source-only (an empty selector at table
			// scope is a backend 422).
			"hold at table scope", holdReleaseSupport,
			[]selectorKind{selRowIDs, selStatus, selAll},
			scope{kind: scopeTable, tableID: 42, tableName: "orders"}, "hold",
			"cloud-source", []string{"--row-id", "--status"},
		},
		{
			// delete-quarantined: file scope takes signatures only.
			"delete-quarantined at file scope", deleteQuarantinedSupport,
			[]selectorKind{selRowIDs, selSignatures, selStatus, selAll},
			scope{kind: scopeFile, fileID: 3, tableID: 42, tableName: "orders"}, "delete-quarantined",
			"cloud-source", []string{"--signature"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newProbe(tc.sup, tc.kinds...)
			if err := cmd.ParseFlags([]string{"--all=false"}); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			_, err := resolveSelector(cmd, tc.sc, tc.sup, tc.verb)
			if err == nil {
				t.Fatal("expected --all=false to be refused")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
			msg := err.Error()
			// The bug: telling the user to pass a flag this scope refuses.
			if strings.Contains(msg, "pass --all") {
				t.Errorf("message tells the user to pass --all at a scope that does not support it: %q", msg)
			}
			if !strings.Contains(msg, tc.wantScope) {
				t.Errorf("message does not say where --all works (%s): %q", tc.wantScope, msg)
			}
			for _, want := range tc.wantNamed {
				if !strings.Contains(msg, want) {
					t.Errorf("message does not name %s as usable here: %q", want, msg)
				}
			}
		})
	}
}

// At a scope whose only selector IS --all, there is nothing to fall back to,
// and the generic wording renders "name the rows with (none)". Found by reading
// the messages rather than by a test — reprocess-quarantined at cloud-source
// scope has only the reprocess-all-bad-rows route.
func TestResolveSelector_NegatedAllWhereAllIsTheOnlySelector(t *testing.T) {
	sup := reprocessSupport
	cmd := newProbe(sup, selRowIDs, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--all=false"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := resolveSelector(cmd, scope{kind: scopeCloudSource, cloudSourceID: 7, tableID: 42}, sup, "reprocess-quarantined")
	if err == nil {
		t.Fatal("expected --all=false to be refused")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	msg := err.Error()
	if strings.Contains(msg, "(none)") {
		t.Errorf("message offers the user an empty list of alternatives: %q", msg)
	}
	if !strings.Contains(msg, "only selector") {
		t.Errorf("message does not say --all is the only selector at this scope: %q", msg)
	}
}

// The same class as --all=false, found by grepping for it: pflag collapses a
// lone `--signature ""` to an EMPTY slice rather than to [""], so the
// per-element emptiness guard never sees it and the flag became a selector
// naming no rows that still reported success.
func TestResolveSelector_ASelectorMustNameAtLeastOneRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		kind selectorKind
		args []string
		// whichever layer refuses is fine; recording WHICH one keeps the test
		// honest, because a case pflag rejects never reaches the guard in
		// parseSelector and so proves nothing about it.
		refusedByPflag bool
	}{
		{"empty signature", selSignatures, []string{"--signature", ""}, false},
		{"empty signature, = form", selSignatures, []string{"--signature="}, false},
		// pflag's intSliceValue.Set splits on "," and Atoi's every field, so ""
		// fails there. This is what makes the len(ids)==0 guard in
		// parseSelector unreachable today; the case is here so that a change to
		// pflag or to the flag's type shows up as a failure rather than as a
		// silently accepted selector naming no rows.
		{"empty row-id list", selRowIDs, []string{"--row-id", ""}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sup := support{table: []selectorKind{tc.kind}}
			cmd := newProbe(sup, tc.kind)
			err := cmd.ParseFlags(tc.args)
			if tc.refusedByPflag {
				if err == nil {
					t.Fatalf("%v is no longer refused by pflag; parseSelector's empty-list "+
						"guard is now the only thing standing between it and a selector "+
						"naming no rows, so assert that guard here", tc.args)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			sel, err := resolveSelector(cmd, scope{kind: scopeTable, tableID: 42}, sup, "delete-quarantined")
			if err == nil {
				t.Fatalf("%v was accepted as selector %q naming no rows", tc.args, sel.kind)
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("GetExitCode = %d, want 2", code)
			}
		})
	}
}

// A refusal that names the wrong scope sends the user somewhere that does not
// work either. --all on delete-quarantined is supported at table AND
// cloud-source scope, so a message saying "only supported at table scope" is
// false at file scope.
func TestResolveSelector_UnsupportedScopeNamesEveryScopeThatWorks(t *testing.T) {
	cmd := newProbe(deleteQuarantinedSupport, selRowIDs, selSignatures, selStatus, selAll)
	if err := cmd.ParseFlags([]string{"--all"}); err != nil {
		t.Fatalf("ParseFlags: %v", err)
	}
	_, err := resolveSelector(cmd, scope{kind: scopeFile, fileID: 3, tableID: 42}, deleteQuarantinedSupport, "delete-quarantined")
	if err == nil {
		t.Fatal("expected --all to be refused at file scope")
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("GetExitCode = %d, want 2", code)
	}
	msg := err.Error()
	for _, want := range []string{"table", "cloud-source"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not name %s as a scope that supports --all: %q", want, msg)
		}
	}
	if strings.Contains(msg, "only supported at table scope") {
		t.Errorf("message claims table-only for a selector two scopes support: %q", msg)
	}
	// And it must still say what DOES work at the scope the user chose.
	if !strings.Contains(msg, "--signature") {
		t.Errorf("message does not name what file scope supports: %q", msg)
	}
}

// count() drives the confirmation prompt. Only the two list selectors and
// --status passed_validation can be sized; see C1.
func TestSelectorCount(t *testing.T) {
	for _, tc := range []struct {
		name string
		sel  selector
		want *int
	}{
		{"row ids", selector{kind: selRowIDs, rowIDs: []int{1, 2, 3}}, intPtr(3)},
		{"signatures", selector{kind: selSignatures, signatures: []string{"aa", "bb"}}, intPtr(2)},
		{"status", selector{kind: selStatus, status: api.QuarantineStatusPassedValidation}, nil},
		{"all", selector{kind: selAll}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.sel.count()
			switch {
			case tc.want == nil && got != nil:
				t.Errorf("count() = %d, want nil", *got)
			case tc.want != nil && got == nil:
				t.Errorf("count() = nil, want %d", *tc.want)
			case tc.want != nil && *got != *tc.want:
				t.Errorf("count() = %d, want %d", *got, *tc.want)
			}
		})
	}
}

// --row-id's usage string is a RECIPE, and the two row populations it can point
// at are not interchangeable: move-rows-to-quarantine takes CLEAN ids while
// reprocess-bad-rows and update-keep-in-quarantine take BAD-ROWS ids. Pasting
// ids from the wrong listing matches nothing, the route still answers
// {"result":"ok"}, and qctl reports success — so a wrong recipe here is a
// silent, total no-op, not a cosmetic slip.
//
// The map is exhaustive on purpose: the next verb that registers --row-id must
// be added here, which is the moment its author has to decide the answer.
func TestRowIDUsageNamesTheVerbsOwnPopulation(t *testing.T) {
	want := map[string]string{
		// Registered only so the refusal can teach the signature recipe; the
		// scope-aware recipe itself lives in unsupportedSelector's message.
		"delete-quarantined": rowIDUsageRefused,
		// move-rows-to-quarantine: SELECT ... FROM {dataset_table} WHERE id IN.
		"send-to-quarantine": rowIDUsageClean,
		"reprocess-clean":    rowIDUsageClean,
		// UPDATE {bad_rows_table}: reprocess_bad_rows_core via
		// nullify_alert_id_and_mark_bad_rows_for_processing, and
		// update_keep_in_quarantine_core via _update_keep_in_quarantine_returning.
		"reprocess-quarantined": rowIDUsageQuarantined,
		"hold":                  rowIDUsageQuarantined,
		"release":               rowIDUsageQuarantined,
		// The same route as hold and release, and the population is a fact
		// about the route rather than about the quarantine_hold value it
		// writes: `UPDATE {bad_rows_table}` is the statement for all three.
		"reset-hold": rowIDUsageQuarantined,
		// modify-clean-cell's overwrite_value action, whose ordinary branch is
		// `FROM {bad_rows_table} WHERE id in %s AND ingest_job_id_ IS null AND
		// awaiting_processing_ IS NOT TRUE` (overwrite_bad_rows_any_value,
		// row_overwrite.py). The COMPOSITE branch is the one place in this map
		// where a wrong id is not silent — validate_live_row_ids probes both
		// tables and 404s an id that is live in neither — but the recipe still
		// names the quarantine listing, because that is the branch every ordinary
		// table takes.
		"set-cell": rowIDUsageQuarantined,
		// create splits the ids with live_row_ids_for_data_source, `FROM` the
		// clean and the bad-rows table `WHERE id = ANY AND data_source_model_id
		// = %s AND is_deleted_ IS DISTINCT FROM TRUE`
		// (cettings/.../data_controller/quarantine.py), and 404s the rest.
		ignoreRuleVerb: rowIDUsageLive,
		// unignore-rule matches ids against the ignores themselves, and an
		// ignore stays on a row the rule then let out of quarantine, under the
		// same id: pointing at the quarantine listing hides exactly that row.
		unignoreRuleVerb: rowIDUsageIgnored,
	}

	seen := map[string]bool{}
	for _, sub := range NewCommand().Commands() {
		f := sub.Flags().Lookup("row-id")
		if f == nil {
			continue
		}
		seen[sub.Name()] = true
		exp, ok := want[sub.Name()]
		if !ok {
			t.Errorf("'qctl rows %s' registers --row-id but this test does not say which "+
				"row listing its ids come from; read the route and add it", sub.Name())
			continue
		}
		if f.Usage != exp {
			t.Errorf("'qctl rows %s' --row-id usage =\n  %q\nwant\n  %q", sub.Name(), f.Usage, exp)
		}
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("'qctl rows %s' no longer registers --row-id; drop its row from this test", name)
		}
	}
}

// The twin of TestEveryRegisteredRowIDFlagDeclaresAPopulation, and it exists
// for a sharper reason: an undeclared --row-id merely names no listing in a
// usage string, while an undeclared --all makes confirmAction print the vaguer
// of two sentences to a user who is about to act on every row in a table.
//
// The refused population counts as declared. set-cell registers --all only to
// refuse it, exactly as the delete verbs register --row-id.
func TestEveryRegisteredAllFlagDeclaresAPopulation(t *testing.T) {
	if allUndeclared.usage() != allUsageUndeclared {
		t.Fatalf("the undeclared usage moved: %q", allUndeclared.usage())
	}
	// The undeclared usage must not name a population, for the same reason the
	// undeclared --row-id usage must not name a listing: a wrong word here is
	// read as a fact about which rows the flag touches.
	for _, word := range []string{"clean", "quarantined"} {
		if strings.Contains(allUsageUndeclared, word) {
			t.Errorf("the undeclared usage names a population (%q); an unset population must name none",
				allUsageUndeclared)
		}
	}
	if allUndeclared.rowsLineCount() == allQuarantined.rowsLineCount() ||
		allUndeclared.rowsLineCount() == allClean.rowsLineCount() {
		t.Errorf("rowsLineCount's default (%q) is one of the declared answers; an unset population must guess neither",
			allUndeclared.rowsLineCount())
	}
	for _, sub := range NewCommand().Commands() {
		f := sub.Flags().Lookup("all")
		if f == nil {
			continue
		}
		if f.Usage == allUsageUndeclared {
			t.Errorf("'qctl rows %s' registers --all without declaring which population its "+
				"route acts on: set all on its support{}", sub.Name())
		}
	}
}

// registeredSupports is each registered verb's support row, keyed by the name
// cobra registers it under. It is the one way a test reaches a verb's
// DECLARATION from the tree: the renderer on a support row is a func, and
// nothing on a *cobra.Command can carry it back.
//
// It is a lookup, not a second matrix — every value is the verb's own var, so
// no selector list is restated here — and TestRegisteredSupportsCoverTheTree
// fails on a registered verb it is missing, so a verb added later cannot slip
// past the invariants that read it.
var registeredSupports = map[string]support{
	"delete-quarantined":    deleteQuarantinedSupport,
	sendToQuarantineVerb:    moveSupport,
	"reprocess-clean":       moveSupport,
	"reprocess-quarantined": reprocessSupport,
	"hold":                  holdReleaseSupport,
	"release":               holdReleaseSupport,
	resetHoldVerb:           holdReleaseSupport,
	"search-replace":        searchReplaceSupport,
	setCellVerb:             setCellSupport,
	"check-unique":          checkUniqueSupport,
	ignoreRuleVerb:          ignoreRuleSupport,
	unignoreRuleVerb:        unignoreRuleSupport,
}

// Coverage for every test that reads registeredSupports, in both directions:
// a registered verb it lacks, and an entry naming a verb that no longer exists.
//
// The map could still name the WRONG var for a verb, so each entry is tied to
// its command by the one declaration a flag does expose: --all's usage string
// is rendered from support.all, so a mismatch there means the entry is not
// that verb's row.
func TestRegisteredSupportsCoverTheTree(t *testing.T) {
	seen := map[string]bool{}
	for _, sub := range NewCommand().Commands() {
		seen[sub.Name()] = true
		sup, ok := registeredSupports[sub.Name()]
		if !ok {
			t.Errorf("'qctl rows %s' is registered but registeredSupports has no entry for it; "+
				"add its support var so the declaration invariants read it", sub.Name())
			continue
		}
		f := sub.Flags().Lookup("all")
		switch {
		case f == nil && sup.all != allUndeclared:
			t.Errorf("'qctl rows %s' declares an --all population but registers no --all flag; "+
				"is registeredSupports naming the wrong var?", sub.Name())
		case f != nil && f.Usage != sup.all.usage():
			t.Errorf("'qctl rows %s' --all usage = %q, but its registeredSupports entry renders %q; "+
				"is registeredSupports naming the wrong var?", sub.Name(), f.Usage, sup.all.usage())
		}
	}
	for name := range registeredSupports {
		if !seen[name] {
			t.Errorf("registeredSupports names 'qctl rows %s', which is not registered; drop it", name)
		}
	}
}

// refusedAllDeclarationDefects is the declaration invariant for a refused
// --all, stated over one support row so it can be pointed at a synthetic row
// as well as at every registered one.
//
// For a row declaring allRefused:
//
//   - a renderer is declared — nil is a defect, never a request for a default;
//   - no scope accepts --all, so the refusal is not contradicted by a route;
//   - at every scope the verb supports, the renderer says something;
//   - that scope accepts --row-id, because allUsageRefused, the --all usage
//     every such verb shows in --help, tells the user to use it.
//
// And for every other row, no renderer: one there is never called, and reads as
// though --all were refused when the flag in fact works.
func refusedAllDeclarationDefects(sup support) []string {
	var defects []string
	if sup.all != allRefused {
		if sup.refuseAll != nil {
			defects = append(defects, "declares a refuseAll renderer, but its --all is not allRefused, so nothing ever calls it")
		}
		return defects
	}
	if sup.refuseAll == nil {
		defects = append(defects, "declares allRefused without a refuseAll renderer; "+
			"set one explicitly (genericAllRefusal, when there is nothing verb-specific to say)")
	}
	rendered := 0
	for _, sk := range []scopeKind{scopeTable, scopeCloudSource, scopeFile} {
		sc := scope{kind: sk, tableName: "orders"}
		allowed := sup.at(sk)
		if contains(allowed, selAll) {
			defects = append(defects, fmt.Sprintf("declares allRefused but accepts --all at %s scope", sc.kindName()))
		}
		if len(allowed) == 0 {
			continue
		}
		if !contains(allowed, selRowIDs) {
			defects = append(defects, fmt.Sprintf(
				"declares allRefused, whose --all usage says %q, but does not accept --row-id at %s scope",
				allUsageRefused, sc.kindName()))
		}
		rendered++
		if sup.refuseAll != nil {
			if msg := sup.refuseAll(refusedAll{verb: "probe", sc: sc, alternatives: withoutAll(allowed)}); msg == "" {
				defects = append(defects, fmt.Sprintf("its refuseAll renderer says nothing at %s scope", sc.kindName()))
			}
		}
	}
	if rendered == 0 {
		defects = append(defects, "declares allRefused but supports no scope with a selector, so the refusal is never printed")
	}
	return defects
}

// Every registered verb's --all declaration is complete: refused ones name
// their renderer, and nothing else carries one. Driven off the registered tree
// through registeredSupports, whose own coverage test fails on a verb it does
// not know, so there is no list of refused verbs here to fall behind.
//
// This replaces a test that pinned reprocess-clean as the ONLY verb allowed a
// refused --all. That pin existed because the refusal's prose was shared and
// reprocess-specific; the prose is now each row's own, so the invariant is about
// the declaration rather than about who holds it.
func TestEveryRefusedAllDeclaresARenderer(t *testing.T) {
	refused := 0
	for _, sub := range NewCommand().Commands() {
		sup, ok := registeredSupports[sub.Name()]
		if !ok {
			continue // TestRegisteredSupportsCoverTheTree reports it
		}
		if sup.all == allRefused {
			refused++
		}
		for _, d := range refusedAllDeclarationDefects(sup) {
			t.Errorf("'qctl rows %s' %s", sub.Name(), d)
		}
	}
	// The harness-alive control: a walk that found no refused --all proves
	// nothing about the declarations it was written to check.
	if refused == 0 {
		t.Fatal("no registered verb declares allRefused; this test walked nothing")
	}
}

// The invariant above has to be able to fail, and the failure it exists for is
// the quiet one: a row declaring allRefused with no renderer. Pointed at
// synthetic rows, each defect is reported on its own.
func TestRefusedAllDeclarationDefects_CatchesEachIncompleteDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name string
		sup  support
		want string
	}{
		{"missing renderer",
			support{table: []selectorKind{selRowIDs}, all: allRefused},
			"without a refuseAll renderer"},
		{"accepts the --all it refuses",
			support{table: []selectorKind{selRowIDs, selAll}, all: allRefused, refuseAll: genericAllRefusal},
			"accepts --all at table scope"},
		{"renderer says nothing",
			support{table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: func(refusedAll) string { return "" }},
			"says nothing at table scope"},
		{"usage names a selector the verb refuses",
			support{table: []selectorKind{selSignatures}, all: allRefused, refuseAll: genericAllRefusal},
			"does not accept --row-id at table scope"},
		{"no supported scope",
			support{all: allRefused, refuseAll: genericAllRefusal},
			"supports no scope"},
		{"renderer on a working --all",
			support{table: []selectorKind{selRowIDs, selAll}, all: allClean, refuseAll: genericAllRefusal},
			"nothing ever calls it"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defects := refusedAllDeclarationDefects(tc.sup)
			if !strings.Contains(strings.Join(defects, "\n"), tc.want) {
				t.Errorf("defects = %q, want one containing %q", defects, tc.want)
			}
		})
	}

	// And the complete declarations are clean, or the cases above would pass
	// against a checker that reports everything.
	for name, sup := range map[string]support{
		"generic":  {table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: genericAllRefusal},
		"specific": {table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: specificAllRefusal},
		"working":  {table: []selectorKind{selRowIDs, selAll}, all: allClean},
	} {
		if d := refusedAllDeclarationDefects(sup); len(d) != 0 {
			t.Errorf("%s: complete declaration reported %q", name, d)
		}
	}
}

// A user who reaches an undeclared renderer anyway — the invariant is a test,
// not a compile error — gets --all refused at exit 2, and NOT the generic
// sentence: that sentence is a choice a verb makes, and handing it to a verb
// that made no choice is how a backend fact goes missing from its refusal.
func TestUnsupportedSelector_MissingRendererIsNotTheGenericMessage(t *testing.T) {
	sup := support{table: []selectorKind{selRowIDs}, all: allRefused}
	sc := scope{kind: scopeTable, tableName: "orders"}

	err := unsupportedSelector(selAll, sc, sup, "probe")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want 2: %v", code, err)
	}
	generic := genericAllRefusal(refusedAll{verb: "probe", sc: sc, alternatives: []selectorKind{selRowIDs}})
	if err.Error() == generic || strings.Contains(err.Error(), "use --row-id") {
		t.Errorf("error = %q, the generic advice stood in for a missing declaration", err.Error())
	}
	if !strings.Contains(err.Error(), "qctl bug") {
		t.Errorf("error = %q, want it to own the missing declaration as a qctl bug", err.Error())
	}

	// An empty rendering is the same incompleteness in a different spelling.
	sup.refuseAll = func(refusedAll) string { return "" }
	if err := unsupportedSelector(selAll, sc, sup, "probe"); !strings.Contains(err.Error(), "qctl bug") {
		t.Errorf("empty rendering: error = %q, want the declaration-bug refusal", err.Error())
	}
}

// specificAllRefusal stands in for a verb-specific renderer: one whose text
// names its own verb and something no generic sentence says.
func specificAllRefusal(r refusedAll) string {
	return fmt.Sprintf("--all is not supported by 'qctl rows %s': verb-specific guidance.", r.verb)
}

// What a refused --all says is the support row's declaration, never the verb's
// name: a specific renderer under another name keeps its guidance, and the
// generic renderer under set-cell's name does not grow anything specific.
func TestRefusedAll_MessageFollowsTheDeclaredRendererNotTheVerbName(t *testing.T) {
	sc := scope{kind: scopeTable, tableName: "orders"}

	renamed := unsupportedSelector(selAll, sc,
		support{table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: specificAllRefusal}, "renamed-verb")
	if want := "--all is not supported by 'qctl rows renamed-verb': verb-specific guidance."; renamed.Error() != want {
		t.Errorf("renamed verb: error = %q, want %q", renamed.Error(), want)
	}

	generic := unsupportedSelector(selAll, sc,
		support{table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: genericAllRefusal}, setCellVerb)
	if want := "--all is not supported by 'qctl rows set-cell' at table scope; use --row-id."; generic.Error() != want {
		t.Errorf("generic renderer under set-cell's name: error = %q, want %q", generic.Error(), want)
	}
}

// The renderer's alternatives come from the matrix at the ACTIVE scope, and a
// scope the verb does not support is refused as a scope before any renderer
// runs — a renderer there would be handed no alternatives and end "use (none)."
func TestRefusedAll_UnsupportedScopeIsRefusedBeforeTheRenderer(t *testing.T) {
	called := false
	sup := support{
		table: []selectorKind{selRowIDs},
		cloud: []selectorKind{selRowIDs, selStatus},
		all:   allRefused,
		refuseAll: func(r refusedAll) string {
			called = true
			return genericAllRefusal(r)
		},
	}

	err := unsupportedSelector(selAll, scope{kind: scopeFile, tableName: "orders", fileID: 3}, sup, "probe")
	if want := "'qctl rows probe' does not support file scope"; err.Error() != want {
		t.Errorf("unsupported scope: error = %q, want %q", err.Error(), want)
	}
	if called {
		t.Error("the renderer ran at a scope the verb does not support")
	}

	err = unsupportedSelector(selAll, scope{kind: scopeCloudSource, tableName: "orders", cloudSourceID: 7}, sup, "probe")
	if want := "--all is not supported by 'qctl rows probe' at cloud-source scope; use --row-id or --status."; err.Error() != want {
		t.Errorf("cloud-source scope: error = %q, want %q", err.Error(), want)
	}
}

// A renderer is handed a scope that cannot make a request. scope.forMessage's
// hydrate hook is the one lookup a scope carries, and a refused --all has no
// value missing that would justify paying for it.
func TestRefusedAll_RendererCannotHydrateTheScope(t *testing.T) {
	hydrated := false
	sc := scope{kind: scopeTable, tableName: "orders", hydrate: func(s scope) (scope, bool) {
		hydrated = true
		return s, true
	}}
	var got scope
	sup := support{table: []selectorKind{selRowIDs}, all: allRefused, refuseAll: func(r refusedAll) string {
		got = r.sc.forMessage()
		return genericAllRefusal(r)
	}}

	if err := unsupportedSelector(selAll, sc, sup, "probe"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("GetExitCode = %d, want 2: %v", qerrors.GetExitCode(err), err)
	}
	if hydrated {
		t.Error("a renderer reached the scope's hydrate hook")
	}
	if got.tableName != "orders" {
		t.Errorf("renderer scope = %+v, want the local scope's table name", got)
	}
}

// Every verb that declares a refused --all keeps the group's command-line
// contract around it, driven off the registered tree so a verb that joins them
// is covered without being named here:
//
//   - --all is answered by the verb's OWN declared renderer, at exit 2, with no
//     request — --yes included, since skipping the prompt must not skip
//     validation;
//   - --all=false is an invalid selector, and does not tell the user to pass
//     the --all this verb always refuses;
//   - --all beside another selector is the exactly-one-selector refusal, not
//     the --all one.
func TestRefusedAllVerbs_CommandLineContract(t *testing.T) {
	checked := 0
	for _, sub := range NewCommand().Commands() {
		verb := sub.Name()
		sup, ok := registeredSupports[verb]
		if !ok || sup.all != allRefused {
			continue
		}
		checked++
		t.Run(verb, func(t *testing.T) {
			if sup.refuseAll == nil {
				t.Fatal("no renderer declared; TestEveryRefusedAllDeclaresARenderer reports it")
			}
			rendered := sup.refuseAll(refusedAll{
				verb:         verb,
				sc:           scope{kind: scopeTable, tableName: "orders"},
				alternatives: withoutAll(sup.table),
			})
			for _, tc := range []struct {
				name   string
				args   []string
				want   string
				unwant string
			}{
				{"--all", []string{"--all", "--yes"}, rendered, ""},
				{"--all=false", []string{"--all=false", "--yes"},
					"--all=false is not a row selector: name the rows with " + orList(sup.table), "pass --all"},
				{"--all beside --row-id", []string{"--all", "--row-id", "7", "--yes"},
					"'qctl rows " + verb + "' takes exactly one row selector, got --all and --row-id", ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					mock := newRowsTestEnv(t)
					stdout, stderr, err := runRows(t, append([]string{"rows", verb, "orders"}, tc.args...)...)
					if err == nil {
						t.Fatal("expected a refusal")
					}
					if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
						t.Errorf("GetExitCode = %d, want 2: %v", code, err)
					}
					if err.Error() != tc.want {
						t.Errorf("error =\n%s\nwant\n%s", err.Error(), tc.want)
					}
					if tc.unwant != "" && strings.Contains(err.Error(), tc.unwant) {
						t.Errorf("error = %q, offers %q on a verb that always refuses it", err.Error(), tc.unwant)
					}
					if reqs := mock.Requests(); len(reqs) != 0 {
						t.Errorf("the refusal made %d request(s): %v", len(reqs), reqs)
					}
					if stdout != "" || stderr != "" {
						t.Errorf("the refusal printed a prompt or a result: stdout=%q stderr=%q", stdout, stderr)
					}
				})
			}
		})
	}
	if checked == 0 {
		t.Fatal("no registered verb declares allRefused; this test walked nothing")
	}
}

// The zero value of rowIDPopulation is not an answer, and its usage string names
// no listing rather than guessing one. This is what makes forgetting it loud: a
// verb wired later that never sets rowIDs fails here instead of shipping the
// wrong recipe.
func TestEveryRegisteredRowIDFlagDeclaresAPopulation(t *testing.T) {
	if rowIDsUndeclared.usage() != rowIDUsageUndeclared {
		t.Fatalf("the undeclared usage moved: %q", rowIDsUndeclared.usage())
	}
	if strings.Contains(rowIDUsageUndeclared, "get data") {
		t.Errorf("the undeclared usage names a listing (%q); an unset population must name none",
			rowIDUsageUndeclared)
	}
	for _, sub := range NewCommand().Commands() {
		f := sub.Flags().Lookup("row-id")
		if f == nil {
			continue
		}
		if f.Usage == rowIDUsageUndeclared {
			t.Errorf("'qctl rows %s' registers --row-id without declaring which listing its ids "+
				"come from: set rowIDs on its support{}", sub.Name())
		}
	}
}

// A selector flag registered IN ORDER TO BE REFUSED must not describe itself as
// though it worked, and the map is exhaustive so the next verb that borrows the
// trick has to decide the answer here.
//
// The failure it prevents is a quiet one: `qctl rows set-cell --help` listed
// "--signature ... Hex initial_signature_ of a row to act on (repeatable)", an
// affirmative promise, next to a --row-id that is the only selector the verb
// has. The flag list is what a user scans before any Long text, so the list has
// to agree with the refusal -- the rule rowIDUsageRefused already applies to
// --row-id on the delete verbs.
func TestRefusedSelectorFlagsSayTheyAreRefused(t *testing.T) {
	want := map[string]map[string]string{
		"signature": {
			"delete-quarantined": signatureUsage,
			// Registered only so unsupportedSelector can name --row-id.
			"set-cell": signatureUsageRefused,
		},
		"status": {
			"delete-quarantined":    statusUsage,
			"send-to-quarantine":    statusUsage,
			"reprocess-clean":       statusUsage,
			"reprocess-quarantined": statusUsage,
			"hold":                  statusUsage,
			"release":               statusUsage,
			"reset-hold":            statusUsage,
			"set-cell":              statusUsageRefused,
		},
	}

	checked := 0
	for _, sub := range NewCommand().Commands() {
		for flagName, byVerb := range want {
			f := sub.Flags().Lookup(flagName)
			if f == nil {
				continue
			}
			checked++
			exp, ok := byVerb[sub.Name()]
			if !ok {
				t.Errorf("'qctl rows %s' registers --%s but this test does not say whether "+
					"the verb accepts it; read its support row and add it", sub.Name(), flagName)
				continue
			}
			if f.Usage != exp {
				t.Errorf("'qctl rows %s' --%s usage =\n  %q\nwant\n  %q",
					sub.Name(), flagName, f.Usage, exp)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no verb registers --signature or --status; this test walked nothing")
	}
}

// A repeated --row-id or --signature names one row, not two.
//
// The backend already treated them as one — both selectors compile to an IN
// list — so the defect was entirely in what qctl said: the prompt asked to act
// on "2 row(s)" and the result line reported 2, for a single row. A
// confirmation prompt that overstates the blast radius of a delete is the
// failure this guards.
//
// Driven through resolveSelector rather than parseSelector so the flag parsing
// is the real one: pflag's IntSlice and StringArray accumulate differently, and
// a dedupe applied to only one of them would still pass a direct call.
func TestResolveSelector_RepeatedValuesNameOneRow(t *testing.T) {
	sup := support{table: []selectorKind{selRowIDs, selSignatures}, rowIDs: rowIDsQuarantined}
	sc := scope{kind: scopeTable, tableID: 42, tableName: "orders"}

	t.Run("--row-id", func(t *testing.T) {
		cmd := newProbe(sup, selRowIDs, selSignatures)
		if err := cmd.ParseFlags([]string{"--row-id", "7", "--row-id", "9", "--row-id", "7"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sel, err := resolveSelector(cmd, sc, sup, "hold")
		if err != nil {
			t.Fatalf("resolveSelector: %v", err)
		}
		// Order is the user's, first occurrence kept: the list is read back in
		// a message, so re-ordering it would be a second surprise.
		if got := sel.rowIDs; len(got) != 2 || got[0] != 7 || got[1] != 9 {
			t.Errorf("rowIDs = %v, want [7 9]", got)
		}
		if n := sel.count(); n == nil || *n != 2 {
			t.Errorf("count() = %v, want 2 — the prompt would name a row twice", n)
		}
	})

	t.Run("--signature", func(t *testing.T) {
		cmd := newProbe(sup, selRowIDs, selSignatures)
		if err := cmd.ParseFlags([]string{"--signature", "deadbeef", "--signature", "deadbeef"}); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		sel, err := resolveSelector(cmd, sc, sup, "delete-quarantined")
		if err != nil {
			t.Fatalf("resolveSelector: %v", err)
		}
		if got := sel.signatures; len(got) != 1 || got[0] != "deadbeef" {
			t.Errorf("signatures = %v, want [deadbeef]", got)
		}
	})
}

// "(none)" must never reach a user-facing sentence.
//
// Every refusal in selector.go names the selectors that would work, built from
// orList over sup.at(sc.kind); joinWords renders an empty list as the
// placeholder "(none)", which inside a sentence reads as advice. The three
// refusals that can be reached with that list empty all route through
// unsupportedScope instead, and this drives each of them with a support row
// whose scope list is nil -- the one edit that is between here and a shipped
// "name the rows with (none)."
//
// Driven against a REGISTERING support row, as TestLocalScope_UnsupportedScope
// IsBadUsage is: the flags have to exist for the refusal to be reachable at
// all, so the probe registers them and the enforcing row does not list them.
func TestSelectorRefusals_NeverSayNone(t *testing.T) {
	registering := support{
		table:  []selectorKind{selRowIDs, selSignatures, selAll},
		rowIDs: rowIDsQuarantined,
		all:    allQuarantined,
	}
	// file: nil — the scope the refusals below are driven at.
	enforcing := support{table: []selectorKind{selRowIDs}, rowIDs: rowIDsQuarantined}
	sc := scope{kind: scopeFile, fileID: 3, tableID: 42, tableName: "orders"}

	for _, tc := range []struct {
		name  string
		flags []string
	}{
		{"no selector", nil},
		{"--all=false", []string{"--all=false"}},
		{"an unsupported selector", []string{"--row-id", "7"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := newProbe(registering, selRowIDs, selSignatures, selAll)
			if err := cmd.ParseFlags(tc.flags); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}
			_, err := resolveSelector(cmd, sc, enforcing, "delete-quarantined")
			if err == nil {
				t.Fatal("expected the refusal")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("GetExitCode = %d, want 2: %v", code, err)
			}
			if strings.Contains(err.Error(), "(none)") {
				t.Errorf("the placeholder reached a user-facing sentence: %q", err.Error())
			}
			if !strings.Contains(err.Error(), "does not support file scope") {
				t.Errorf("error = %q, want the scope refusal", err.Error())
			}
		})
	}
}
