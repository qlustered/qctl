package catalog_test

import (
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// tableFlag looks up one of the selector flags on a single command.
//
// LocalFlags, not Flags: cobra's Flags() returns c.flags without merging the
// command's own PersistentFlags (command.go's mergePersistentFlags runs from
// LocalFlags/InheritedFlags/ParseFlags, not from Flags), so a hand-rolled
// PersistentFlags().String("table", ...) would be invisible to every guard
// below until execution time. AddTableFlags uses cmd.Flags(), so a
// correctly-registered pair is seen either way — the hole opens only for the
// hand-rolled registration, which is precisely what a guard exists to catch.
// LocalFlags excludes flags inherited from parents, so a parent's persistent
// --table is reported once, on the parent that declares it, not on every
// descendant.
func tableFlag(cmd *cobra.Command, name string) *pflag.Flag {
	return cmd.LocalFlags().Lookup(name)
}

// TestTableFlagIsAlwaysAName pins the selector convention the way
// TestEveryCommandIsAnnotated pins annotations: mechanically, on the real
// tree, so the next command cannot quietly reintroduce the ambiguity.
//
// --table used to mean three different things across the tree — an Int ID on
// seven commands, a "name or ID" string on five, and a name paired with an Int
// --table-id on four. A table name can be a number and datasets.ResolveID
// tries strconv.Atoi first, so on the first two groups a table literally named
// "42" was unreachable.
func TestTableFlagIsAlwaysAName(t *testing.T) {
	var offenders []string
	for _, n := range catalog.Walk(realRoot(t)) {
		flag := tableFlag(n.Cmd, "table")
		if flag == nil {
			continue
		}
		if flag.Value.Type() != "string" {
			offenders = append(offenders, n.Path+" (--table is "+flag.Value.Type()+")")
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("--table must be a table NAME on every command; these declare it otherwise:\n  %s\n"+
			"Register the pair with cmdutil.AddTableFlags and read it with "+
			"cmdutil.ResolveTableID / cmdutil.OptionalTableID.",
			strings.Join(offenders, "\n  "))
	}
}

// TestTableIDFlagIsAlwaysAnInt is the other half: --table-id must never be a
// string, or "the pair" would carry the same type twice.
func TestTableIDFlagIsAlwaysAnInt(t *testing.T) {
	var offenders []string
	for _, n := range catalog.Walk(realRoot(t)) {
		flag := tableFlag(n.Cmd, "table-id")
		if flag == nil {
			continue
		}
		if flag.Value.Type() != "int" {
			offenders = append(offenders, n.Path+" (--table-id is "+flag.Value.Type()+")")
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("--table-id must be an int on every command:\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestTableSelectorFlagsComeInPairs is the guard the two type checks above
// cannot be: both of them pass a lone String --table that still means "name or
// id" and still resolves through datasets.ResolveID's strconv.Atoi fast path —
// exactly the bug this convention removes. Only the pairing catches it.
//
// A lone --table is the "name or ID" shape; a lone --table-id is the ID-only
// shape. Either one is a command a user cannot address a table named "42" on,
// so the pair is the convention, not the types alone.
func TestTableSelectorFlagsComeInPairs(t *testing.T) {
	var offenders []string
	for _, n := range catalog.Walk(realRoot(t)) {
		haveName := tableFlag(n.Cmd, "table") != nil
		haveID := tableFlag(n.Cmd, "table-id") != nil
		switch {
		case haveName == haveID:
			// both or neither: nothing to say
		case haveName:
			offenders = append(offenders, n.Path+" (--table without --table-id)")
		default:
			offenders = append(offenders, n.Path+" (--table-id without --table)")
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("--table and --table-id are one selector and must be declared together; these declare half of it:\n  %s\n"+
			"Register both with cmdutil.AddTableFlags(cmd, purpose) and read them with "+
			"cmdutil.ResolveTableID / cmdutil.ResolveTableIDFromFlags / cmdutil.OptionalTableID.\n"+
			"A lone --table is the \"name or id\" shape (unreachable table named \"42\"); "+
			"a lone --table-id has no way to name a table at all.",
			strings.Join(offenders, "\n  "))
	}
}

// tableSelectorMode is how one command lets a user name the table it acts on.
type tableSelectorMode int

const (
	// selectorPositional: the table is the command's own positional argument,
	// and the flag pair is the unambiguous alternative. All three forms work.
	selectorPositional tableSelectorMode = iota
	// selectorFlagsOnly: the positional slot holds something else (a rule
	// name, a suggestion UUID), the command takes no positional at all, or the
	// table is a filter rather than the object being named. The pair is the
	// only way in, and cmdutil.ResolveTableIDFromFlags says so.
	selectorFlagsOnly
	// selectorPositionalOnly: the table arrives as an id-or-name positional
	// and there is no flag pair at all, so the argument resolves through an
	// integer-first fast path -- datasets.Client.ResolveID's strconv.Atoi on
	// most of them, its own on undelete table -- and a table literally named
	// "42" cannot be addressed on that command. This is the shape the
	// convention exists to replace; every entry is a recorded exception, not a
	// pattern to copy, and the resource pages name them as exceptions too.
	selectorPositionalOnly
)

// tableScopedCommands is every command a table scopes, and the selector shape
// it is allowed to have. Adding a line is a claim about addressing, so the
// diff is the review — the same contract as declaredOutputFormats.
//
// The ticket that produced this test asked for "positional, --table and
// --table-id on every table-scoped command". That cannot hold: get table-rule
// and describe rule-suggestion already spend their positional slot on the rule
// name and the suggestion UUID, and binding a table there would be a silent
// wrong lookup rather than a usage error. The workable contract, and the one
// this map encodes, is: one shared resolver everywhere, and the positional
// form wherever the positional slot is free.
//
// "Every command a table scopes" is a claim TestTableSelectorInventoryIsComplete
// checks in both directions, and it can only check what it can see: a command
// counts as table-scoped when it declares the flag pair, or when its own
// positional argument is a table (spelled with one of tablePositionalTokens,
// or <id-or-name> on a command annotated for the table resource). A command
// that takes a table by some other route — an undeclared flag, a positional
// spelled a new way — is invisible to it, which is why the spelling guard
// below refuses unrecognized "table"-ish placeholders instead of skipping them.
var tableScopedCommands = map[string]tableSelectorMode{
	"qctl get error-incidents": selectorFlagsOnly,
	// The table is the object being named and the slot is free.
	"qctl get table-members":                 selectorPositional,
	"qctl table-member leave":                selectorPositional,
	"qctl table-member remove":               selectorFlagsOnly,
	"qctl table-member set-permission":       selectorFlagsOnly,
	"qctl get pii-access-requests":           selectorPositional,
	"qctl pii-access-request request":        selectorFlagsOnly,
	"qctl pii-access-request withdraw":       selectorFlagsOnly,
	"qctl pii-access-request approve":        selectorFlagsOnly, // <request id>
	"qctl pii-access-request deny":           selectorFlagsOnly, // <request id>
	"qctl duplicate-group allow":             selectorFlagsOnly, // <group id>
	"qctl duplicate-group keep-one":          selectorFlagsOnly, // <group id>
	"qctl duplicate-group not-duplicates":    selectorFlagsOnly, // <group id>
	"qctl duplicate-group approve":           selectorFlagsOnly, // <group id>
	"qctl duplicate-group withdraw":          selectorFlagsOnly, // <group id>
	"qctl duplicate-group disagree":          selectorFlagsOnly, // <group id>
	"qctl duplicate-group dismiss":           selectorFlagsOnly, // <group id>
	"qctl duplicate-group stop-series":       selectorFlagsOnly, // <group id>
	"qctl duplicate-group comment":           selectorFlagsOnly, // <group id>
	"qctl duplicate-group share":             selectorFlagsOnly, // <group id>
	"qctl duplicate-group respond":           selectorFlagsOnly, // <group id>
	"qctl duplicate-group withdraw-response": selectorFlagsOnly, // <group id> <response id>
	"qctl duplicate-group retract":           selectorFlagsOnly, // <group id>
	"qctl duplicate-group mark":              selectorFlagsOnly, // the noun's verbs all take the table by flag
	"qctl duplicate-group allow-all":         selectorFlagsOnly, // the noun's verbs all take the table by flag
	"qctl duplicate-group reapply":           selectorFlagsOnly, // the noun's verbs all take the table by flag
	"qctl get duplicate-groups":              selectorFlagsOnly, // the noun's verbs all take the table by flag
	"qctl get pii-grants":                    selectorPositional,
	"qctl pii-grant create":                  selectorFlagsOnly, // no positional at all
	"qctl pii-grant revoke":                  selectorFlagsOnly, // <grant id>
	"qctl reveal cell":                       selectorPositional,
	"qctl reveal alert":                      selectorFlagsOnly, // <alert id>; optional, read from the alert
	"qctl reveal warning":                    selectorFlagsOnly, // <warning id>; optional, read from the warning
	"qctl get table-rules":                   selectorPositional,
	"qctl get rule-suggestions":              selectorPositional,
	"qctl get standard-field-suggestions":    selectorPositional,
	"qctl get table-kind-suggestions":        selectorPositional,
	"qctl get table-schema":                  selectorPositional,
	"qctl describe table-schema":             selectorPositional,
	"qctl get table-config":                  selectorPositional,
	"qctl get dry-run-jobs":                  selectorPositional,
	"qctl get latest-alert":                  selectorPositional,

	// The table is the object being named and the slot is free, and here the
	// flag pair earns its keep twice over: the table is one of three things the
	// request body names, so a table named "42" being unreachable would cost
	// the whole answer rather than a narrowing.
	"qctl get value-recommendations": selectorPositional,
	"qctl get rule-ignores":          selectorPositional,
	"qctl get deleted-rows":          selectorPositional,
	"qctl get data":                  selectorPositional,
	"qctl describe table":            selectorPositional,

	// The table is the object being named and the slot is free. Every verb
	// of the group -- the ones that write and `check-unique`, which reads:
	// the flag pair is what reaches a table literally named "42", which
	// matters most on the ones that change it.
	"qctl rows check-unique":          selectorPositional,
	"qctl rows delete-quarantined":    selectorPositional,
	"qctl rows send-to-quarantine":    selectorPositional,
	"qctl rows reprocess-clean":       selectorPositional,
	"qctl rows reprocess-quarantined": selectorPositional,
	"qctl rows hold":                  selectorPositional,
	"qctl rows release":               selectorPositional,
	"qctl rows reset-hold":            selectorPositional,
	"qctl rows search-replace":        selectorPositional,
	"qctl rows set-cell":              selectorPositional,
	"qctl rows ignore-rule":           selectorPositional,
	"qctl rows unignore-rule":         selectorPositional,

	// Same shape, same reason: the table is the object and the slot is free.
	// Kept out of the rows block above so that block stays exactly the
	// `qctl rows` group.
	"qctl undo table": selectorPositional,
	"qctl redo table": selectorPositional,

	// The table is what the report is of, and the slot is free.
	"qctl download alert-report": selectorPositional,

	// The table the command writes, and the slot is free: the table a copy
	// is made FROM, and the table a rule merge writes INTO. The merge's other
	// table is its own --from/--from-id pair.
	"qctl copy table":        selectorPositional,
	"qctl merge table-rules": selectorPositional,

	// The linked table whose composite spec revision the dry run previews,
	// whose spec revision is enabled or shown, or whose rebuild is queued.
	"qctl create composite-dry-run-job": selectorPositional,
	"qctl enable linked-table":          selectorPositional,
	"qctl rekey linked-table":           selectorPositional,
	"qctl get linked-table":             selectorPositional,
	"qctl run composite-run-job":        selectorPositional,

	// The positional slot already holds something else.
	"qctl comment-thread create":           selectorFlagsOnly, // no positional at all
	"qctl comment-thread reply":            selectorFlagsOnly, // <thread uuid>
	"qctl comment-thread edit-comment":     selectorFlagsOnly, // <thread uuid> <comment uuid>
	"qctl comment-thread delete-comment":   selectorFlagsOnly, // <thread uuid> <comment uuid>
	"qctl comment-thread resolve":          selectorFlagsOnly, // <thread uuid>
	"qctl comment-thread unresolve":        selectorFlagsOnly, // <thread uuid>
	"qctl get comment-threads":             selectorFlagsOnly, // <thread uuid>...
	"qctl describe comment-thread":         selectorFlagsOnly, // <thread uuid>
	"qctl describe duplicate-group":        selectorFlagsOnly, // <group uuid>
	"qctl describe row":                    selectorFlagsOnly, // <row id>
	"qctl describe rule-merge":             selectorFlagsOnly, // <merge id>
	"qctl get table-rule":                  selectorFlagsOnly, // <rule name or r-* alias>
	"qctl describe table-rule":             selectorFlagsOnly, // <rule name or r-* alias>
	"qctl table-rule recheck-clean-rows":   selectorFlagsOnly,
	"qctl table-rule move":                 selectorFlagsOnly, // <rule name or r-* alias>
	"qctl table-rule test-filter":          selectorFlagsOnly, // <rule name or r-* alias>, or -f manifest
	"qctl table-rule propose":              selectorFlagsOnly, // <rule name or r-* alias>
	"qctl deliverable test-filter":         selectorFlagsOnly, // <deliverable ref>, or -f manifest with an optional table override
	"qctl deliverable-run waive":           selectorFlagsOnly, // <run id>
	"qctl deliverable-run override-source": selectorFlagsOnly, // <run id> [<source id>]
	"qctl describe rule-suggestion":        selectorFlagsOnly, // <suggestion uuid>
	"qctl suggestion accept":               selectorFlagsOnly, // <suggestion uuid>
	"qctl suggestion dismiss":              selectorFlagsOnly, // <suggestion uuid>
	"qctl suggestion edit":                 selectorFlagsOnly, // <suggestion uuid>
	"qctl review request":                  selectorFlagsOnly, // <rule name>
	"qctl upload file":                     selectorFlagsOnly, // <path>
	"qctl create dry-run-job":              selectorFlagsOnly, // -f manifest
	"qctl alert submit":                    selectorFlagsOnly, // no positional at all
	"qctl warning submit":                  selectorFlagsOnly, // no positional at all
	"qctl get data-reviews":                selectorFlagsOnly, // no positional; the contract requires an explicit selector flag
	"qctl get data-review-groups":          selectorFlagsOnly, // no positional; the contract requires an explicit selector flag
	"qctl get data-review-changes":         selectorFlagsOnly, // no positional; the contract requires an explicit selector flag
	"qctl data-review progress":            selectorFlagsOnly, // no positional; the contract requires an explicit selector flag
	"qctl data-review request":             selectorFlagsOnly, // no positional; the contract requires an explicit selector flag

	// The table is an optional filter, not the object. A positional here would
	// read as the resource's own id on half of them.
	"qctl get alerts":                  selectorFlagsOnly,
	"qctl get warnings":                selectorFlagsOnly,
	"qctl get files":                   selectorFlagsOnly,
	"qctl get history":                 selectorFlagsOnly,
	"qctl get deliverables":            selectorFlagsOnly,
	"qctl get cloud-sources":           selectorFlagsOnly,
	"qctl get ingestion-jobs":          selectorFlagsOnly,
	"qctl get queue-messages":          selectorFlagsOnly,
	"qctl get profiling-jobs":          selectorFlagsOnly,
	"qctl get composite-run-jobs":      selectorFlagsOnly, // the table is the CHANGED table, not the composite being rebuilt
	"qctl get composite-dry-run-jobs":  selectorFlagsOnly, // the table is the linked (downstream) table the dry run previews
	"qctl get rule-reviews":            selectorFlagsOnly,
	"qctl get change-proposals":        selectorFlagsOnly,
	"qctl get rule-review-policy":      selectorFlagsOnly,
	"qctl get data-review-policy":      selectorFlagsOnly,
	"qctl delete rule-review-policy":   selectorFlagsOnly,
	"qctl delete data-review-policy":   selectorFlagsOnly,
	"qctl get change-review-policy":    selectorFlagsOnly,
	"qctl delete change-review-policy": selectorFlagsOnly,
	"qctl run profiling-job":           selectorFlagsOnly,

	// The table is the resource itself and its positional argument is the only
	// way to name it. No flag pair, so "42" is always read as an id first:
	// these four are where the unreachable-table-named-"42" hole survives.
	"qctl get table":      selectorPositionalOnly, // <id-or-name>, --watch/--job-activity own the flag surface
	"qctl download data":  selectorPositionalOnly, // <table-id-or-name>; unlike get data, the export remains positional-only
	"qctl undelete table": selectorPositionalOnly, // <id-or-name>; its own strconv.Atoi, not ResolveID -- a deleted table resolves through the states=deleted list, which the shared resolver cannot reach
	"qctl share table":    selectorPositionalOnly, // <id-or-name>...; the tables being granted are the resource, and the recipient owns the flag surface (--to/--to-id)
}

// tablePositionalTokens are the spellings the tree uses for "this positional
// argument is the table". They are a closed set on purpose: the inventory can
// only see a table-scoped command if it recognizes how that command spells its
// table argument, so an unrecognized "table"-ish placeholder is an error
// rather than a silent skip.
var tablePositionalTokens = map[string]bool{
	"table":            true, // get table-schema, get table-config, get dry-run-jobs, get latest-alert
	"table-id-or-name": true, // get table-rules, get rule-suggestions, get data
	"table-name":       true, // unused today; reserved so a name-only argument is still seen
	"table-id":         true, // unused today; reserved so an id-only argument is still seen
}

// positionalTokens returns the argument placeholders in a Use string, brackets
// stripped: "table-config [table]" → ["table"].
func positionalTokens(use string) []string {
	fields := strings.Fields(use)
	if len(fields) < 2 {
		return nil
	}
	var out []string
	for _, f := range fields[1:] {
		if strings.HasPrefix(f, "-") {
			continue
		}
		tok := strings.TrimSuffix(strings.Trim(f, "[]<>"), "...")
		if tok == "" || tok == "flags" {
			continue
		}
		out = append(out, tok)
	}
	return out
}

// tablePositionalOf reports whether a command's own positional slot holds a
// table, plus any placeholder that looks like one but is not a recognized
// spelling. The second return is what stops the inventory from being blind to
// a command that invents a new way to say "table".
func tablePositionalOf(n catalog.Node) (isTable bool, unrecognized string) {
	tokens := positionalTokens(n.Cmd.Use)
	for _, tok := range tokens {
		switch {
		case tablePositionalTokens[tok]:
			isTable = true
		case strings.Contains(tok, "table"):
			unrecognized = tok
		}
	}
	// The table resource's own commands name their argument <id-or-name>:
	// nothing in the placeholder says "table", the annotation does.
	if n.Resource == "table" && len(tokens) > 0 {
		isTable = true
	}
	return isTable, unrecognized
}

// TestTableSelectorInventoryIsComplete is the both-directions check: a new
// table-scoped command cannot appear without a decision being recorded here.
//
// It deliberately does not start from "declares the flag pair". That is the
// shape the convention produces, so keying on it made the guard blind to the
// one class of command that most needs recording — a table-scoped command with
// a bare positional and no flags, which is what get table, describe table and
// get data are, and what a new `qctl get table-lineage [table]` would be. A
// command written that way used to pass every test on this branch. It now has
// to be classified.
func TestTableSelectorInventoryIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		hasFlags := tableFlag(n.Cmd, "table") != nil || tableFlag(n.Cmd, "table-id") != nil
		hasPositional, unrecognized := tablePositionalOf(n)

		if unrecognized != "" {
			t.Errorf("%s spells a positional argument %q, which this inventory does not "+
				"recognize as a table. If it is a table, use one of: %s. If it is not "+
				"(a table kind, a table rule), rename the placeholder so it does not read "+
				"as one — an unrecognized spelling is a table-scoped command the inventory "+
				"cannot see.", n.Path, unrecognized, strings.Join(sortedKeys(tablePositionalTokens), ", "))
		}
		if !hasFlags && !hasPositional {
			continue
		}
		seen[n.Path] = true

		mode, ok := tableScopedCommands[n.Path]
		if !ok {
			t.Errorf("%s is scoped by a table but is not in tableScopedCommands; add it as "+
				"selectorPositional (the table is the object, the positional slot is free, "+
				"and cmdutil.AddTableFlags registers the pair), selectorFlagsOnly (the "+
				"positional slot holds something else — put the reason in a trailing "+
				"comment), or selectorPositionalOnly (a recorded exception: positional and "+
				"nothing else, so a table named \"42\" is unreachable)", n.Path)
			continue
		}

		// The recorded mode must match the selector the command actually has,
		// or the map documents an addressing contract the tree does not have —
		// which is the drift these tests exist to catch.
		switch mode {
		case selectorPositional, selectorFlagsOnly:
			if !hasFlags {
				t.Errorf("%s is recorded as taking --table/--table-id but declares neither; "+
					"register them with cmdutil.AddTableFlags, or record it as "+
					"selectorPositionalOnly and say so wherever the addressing contract is "+
					"documented (internal/resources/pages, docs/commands.md)", n.Path)
			}
		case selectorPositionalOnly:
			if hasFlags {
				t.Errorf("%s now declares --table/--table-id, so it is no longer a "+
					"positional-only exception; move it to selectorPositional and drop it "+
					"from the exception lists in internal/resources/pages and "+
					"docs/commands.md", n.Path)
			}
			if !hasPositional {
				t.Errorf("%s is recorded as positional-only but takes no table positional", n.Path)
			}
		}
	}
	for path := range tableScopedCommands {
		if !seen[path] {
			t.Errorf("tableScopedCommands lists %s, which no table scopes any more "+
				"(renamed, removed, or the selector was dropped)", path)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestPositionalTableCommandsAcceptOne: a selectorPositional command must
// actually take the argument. Args is the only mechanical witness — Use text
// can say [table] while Args is cobra.NoArgs.
func TestPositionalTableCommandsAcceptOne(t *testing.T) {
	byPath := map[string]*cobra.Command{}
	for _, n := range catalog.Walk(realRoot(t)) {
		byPath[n.Path] = n.Cmd
	}
	for path, mode := range tableScopedCommands {
		if mode != selectorPositional {
			continue
		}
		cmd, ok := byPath[path]
		if !ok {
			continue // reported by the inventory test above
		}
		if cmd.Args == nil {
			continue // cobra's default accepts arbitrary args
		}
		if err := cmd.Args(cmd, []string{"orders"}); err != nil {
			t.Errorf("%s is selectorPositional but rejects a positional table: %v", path, err)
		}
	}
}

// TestEveryTableSelectorRejectsLocalErrorsBeforeCredentials exercises the
// actual RunE attached to every command that declares the selector pair. The
// inventory tests above can prove the flags exist and have the right types,
// but they cannot prove a runner calls the local validator before Bootstrap.
// An empty HOME makes that ordering observable: a missed integration answers
// "not logged in" (exit 1) instead of the command-line error (exit 2).
func TestEveryTableSelectorRejectsLocalErrorsBeforeCredentials(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("QCTL_ALLOW_PLAINTEXT_TOKENS", "1")

	probeFile := t.TempDir() + "/rows.csv"
	if err := os.WriteFile(probeFile, []byte("id\n1\n"), 0o600); err != nil {
		t.Fatalf("write upload probe: %v", err)
	}
	manifestFile := t.TempDir() + "/dry-run.yaml"
	manifest := `apiVersion: qluster.ai/v1
kind: DryRunJob
metadata:
  cloud_source_id: 1
spec:
  rule_run_specs:
    - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440000"
`
	if err := os.WriteFile(manifestFile, []byte(manifest), 0o600); err != nil {
		t.Fatalf("write dry-run probe: %v", err)
	}

	for _, n := range catalog.Walk(realRoot(t)) {
		if tableFlag(n.Cmd, "table-id") == nil {
			continue
		}
		t.Run(strings.TrimPrefix(n.Path, "qctl "), func(t *testing.T) {
			tests := []struct {
				name string
				set  map[string]string
				want string
			}{
				{
					name: "non-positive ID",
					set:  map[string]string{"table-id": "0"},
					want: "--table-id must be a positive integer, got 0",
				},
				{
					name: "conflicting flags",
					set:  map[string]string{"table": "orders", "table-id": "42"},
					want: "table specified more than once (--table, --table-id)",
				},
				{
					name: "empty name",
					set:  map[string]string{"table": ""},
					want: "--table cannot be empty",
				},
			}
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					args := prepareTableSelectorProbe(t, n, probeFile, manifestFile)
					if tableScopedCommands[n.Path] == selectorPositional {
						args = nil
					}
					for name, value := range tt.set {
						setTableProbeFlag(t, n.Cmd, name, value)
					}

					err := n.Cmd.RunE(n.Cmd, args)
					if err == nil {
						t.Fatalf("RunE returned nil; want %q", tt.want)
					}
					if !strings.Contains(err.Error(), tt.want) {
						t.Errorf("RunE returned %q; want %q", err, tt.want)
					}
					if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
						t.Errorf("RunE exits %d, want %d (ExitBadUsage): %v",
							got, qerrors.ExitBadUsage, err)
					}
				})
			}
		})
	}
}

const tableProbeUUID = "550e8400-e29b-41d4-a716-446655440000"

// prepareTableSelectorProbe supplies only the inputs validated before the
// table selector. The command set itself still comes from catalog.Walk: these
// exceptions do not enumerate which commands have table flags, so the test
// reaches a newly-added selector command automatically.
func prepareTableSelectorProbe(t *testing.T, n catalog.Node, probeFile, manifestFile string) []string {
	t.Helper()

	mode := tableScopedCommands[n.Path]
	args := []string(nil)
	if mode == selectorPositional {
		args = []string{"orders"}
	}

	switch n.Path {
	case "qctl table-member remove", "qctl table-member set-permission":
		args = []string{tableProbeUUID}
	case "qctl comment-thread create":
		setTableProbeFlag(t, n.Cmd, "row", "1")
		setTableProbeFlag(t, n.Cmd, "body", "probe")
	case "qctl comment-thread reply":
		args = []string{tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "body", "probe")
	case "qctl comment-thread edit-comment":
		args = []string{tableProbeUUID, tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "body", "probe")
	case "qctl comment-thread delete-comment":
		args = []string{tableProbeUUID, tableProbeUUID}
	case "qctl comment-thread resolve", "qctl comment-thread unresolve",
		"qctl describe comment-thread", "qctl get comment-threads":
		args = []string{tableProbeUUID}
	case "qctl describe row", "qctl describe rule-merge", "qctl pii-grant revoke",
		"qctl reveal alert", "qctl reveal warning", "qctl pii-access-request withdraw",
		"qctl pii-access-request approve":
		args = []string{"1"}
	case "qctl pii-access-request deny":
		args = []string{"1"}
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl duplicate-group allow", "qctl duplicate-group keep-one", "qctl duplicate-group approve",
		"qctl duplicate-group withdraw", "qctl duplicate-group share", "qctl describe duplicate-group",
		"qctl duplicate-group retract", "qctl duplicate-group stop-series":
		args = []string{tableProbeUUID}
	case "qctl duplicate-group mark":
		setTableProbeFlag(t, n.Cmd, "row-id", "1,2")
		setTableProbeFlag(t, n.Cmd, "mode", "flag")
		setTableProbeFlag(t, n.Cmd, "comment", "probe")
	case "qctl duplicate-group not-duplicates", "qctl duplicate-group disagree", "qctl duplicate-group dismiss":
		args = []string{tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl duplicate-group comment":
		args = []string{tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "body", "probe")
	case "qctl duplicate-group allow-all":
		setTableProbeFlag(t, n.Cmd, "file-id", "1")
	case "qctl duplicate-group respond":
		args = []string{tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "kind", "separate-rows")
		setTableProbeFlag(t, n.Cmd, "comment", "probe")
	case "qctl duplicate-group withdraw-response":
		args = []string{tableProbeUUID, tableProbeUUID}
	case "qctl reveal cell":
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
		setTableProbeFlag(t, n.Cmd, "field", "probe")
	case "qctl pii-access-request request":
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl pii-grant create":
		setTableProbeFlag(t, n.Cmd, "to-id", tableProbeUUID)
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl get table-rule", "qctl describe table-rule", "qctl table-rule recheck-clean-rows",
		"qctl table-rule test-filter":
		args = []string{"rule-name"}
	case "qctl table-rule move":
		args = []string{"rule-name"}
		setTableProbeFlag(t, n.Cmd, "position", "1")
	case "qctl table-rule propose":
		args = []string{"rule-name"}
		setTableProbeFlag(t, n.Cmd, "set-param", "probe=1")
	case "qctl describe rule-suggestion", "qctl suggestion accept", "qctl suggestion dismiss":
		args = []string{tableProbeUUID}
	case "qctl suggestion edit":
		args = []string{tableProbeUUID}
		setTableProbeFlag(t, n.Cmd, "severity", "warning")
	case "qctl review request":
		args = []string{"rule-name"}
	case "qctl upload file":
		args = []string{probeFile}
	case "qctl create dry-run-job":
		setTableProbeFlag(t, n.Cmd, "file", manifestFile)
	case "qctl deliverable test-filter":
		setTableProbeFlag(t, n.Cmd, "filename", manifestFile)
		setTableProbeFlag(t, n.Cmd, "sheet", "Data")
	case "qctl deliverable-run waive":
		args = []string{"1"}
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl deliverable-run override-source":
		args = []string{"1"}
		setTableProbeFlag(t, n.Cmd, "sheet", "Data")
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl alert submit", "qctl warning submit":
		setTableProbeFlag(t, n.Cmd, "row", "1")
		setTableProbeFlag(t, n.Cmd, "field", "amount")
		setTableProbeFlag(t, n.Cmd, "comment", "probe")
	case "qctl get rule-reviews":
		setTableProbeFlag(t, n.Cmd, "rule", "rule-name")
	case "qctl get value-recommendations":
		setTableProbeFlag(t, n.Cmd, "field", "amount")
		setTableProbeFlag(t, n.Cmd, "value", "1")
	case "qctl rows check-unique":
		setTableProbeFlag(t, n.Cmd, "column", "id")
	case "qctl rows delete-quarantined":
		setTableProbeFlag(t, n.Cmd, "all", "true")
		setTableProbeFlag(t, n.Cmd, "reason", "probe")
	case "qctl rows reprocess-quarantined":
		setTableProbeFlag(t, n.Cmd, "all", "true")
	case "qctl rows send-to-quarantine", "qctl rows reprocess-clean",
		"qctl rows hold", "qctl rows release",
		"qctl rows reset-hold":
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
	case "qctl rows search-replace":
		setTableProbeFlag(t, n.Cmd, "search", "old")
		setTableProbeFlag(t, n.Cmd, "replace", "new")
		setTableProbeFlag(t, n.Cmd, "dry-run", "true")
	case "qctl get rule-ignores":
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
	case "qctl rows ignore-rule", "qctl rows unignore-rule":
		setTableProbeFlag(t, n.Cmd, "cloud-source-id", "7")
		setTableProbeFlag(t, n.Cmd, "rule", "rule-name")
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
	case "qctl rows set-cell":
		setTableProbeFlag(t, n.Cmd, "row-id", "1")
		setTableProbeFlag(t, n.Cmd, "field", "amount")
		setTableProbeFlag(t, n.Cmd, "value", "1")
	}
	return args
}

func setTableProbeFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("set --%s on %s: flag does not exist", name, cmd.CommandPath())
	}
	wasChanged := flag.Changed
	if slice, ok := flag.Value.(pflag.SliceValue); ok {
		original := slice.GetSlice()
		original = append([]string(nil), original...)
		t.Cleanup(func() {
			if err := slice.Replace(original); err != nil {
				t.Errorf("restore --%s on %s: %v", name, cmd.CommandPath(), err)
			}
			flag.Changed = wasChanged
		})
	} else {
		original := flag.Value.String()
		t.Cleanup(func() {
			if err := flag.Value.Set(original); err != nil {
				t.Errorf("restore --%s on %s: %v", name, cmd.CommandPath(), err)
			}
			flag.Changed = wasChanged
		})
	}
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set --%s on %s: %v", name, cmd.CommandPath(), err)
	}
}
