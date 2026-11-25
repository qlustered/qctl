package delete

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// underRoot mounts the delete command under a minimal root, because cobra's
// legacyArgs short-circuits a parentless command that has subcommands: it
// returns `unknown command ... for "delete"` before RunE is ever reached
// (cobra@v1.10.2/args.go), so a parentless harness can never observe the
// refusal or the near-miss suggestion this file asserts on.
func underRoot(t *testing.T, args ...string) error {
	t.Helper()

	root := &cobra.Command{Use: "qctl", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(NewCommand())
	root.SetArgs(append([]string{"delete"}, args...))
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)

	return root.Execute()
}

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "delete" {
		t.Errorf("Expected Use to be 'delete', got '%s'", cmd.Use)
	}

	if cmd.Short != "Delete resources" {
		t.Errorf("Expected Short to be 'Delete resources', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) == 0 {
		t.Error("Expected delete command to have subcommands")
	}

	// Check for file subcommand
	found := false
	for _, sub := range subcommands {
		if sub.Name() == "file" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected delete command to have file subcommand")
	}
}

// Table deletion is deliberately absent (see
// the coverage decision): the permission is
// ops/admin-only, recovery is lossy, and the blast radius includes every
// alert, cloud source and running job on the table. A bare "unknown command"
// teaches an agent nothing and invites it to retry variants, so the parent
// answers with the reason -- the same mechanism `qctl create table` uses
// (internal/commands/create/create.go).
//
// The plural and capitalized spellings are covered because the exact match this
// replaced left them, and only them, with no answer at all: cobra's levenshtein
// finds nothing within SuggestionsMinimumDistance for "tables" (the nearest
// subcommand, "rules", is distance 3), so they hit a bare "unknown command"
// with nothing to try next. The singular spellings have no fallback either --
// "table" is distance 3 from both "file" and "rule" -- but the refusal already
// answered those.
//
// " Tables " is the padded plural, and it is the one spelling that makes
// namesTheTable's TrimSpace load-bearing -- the "one deliberate difference"
// from createRedirect that its comment claims. catalog.Lookup trims its own
// input, so the direct hit needs no help; the plural retry does, because
// TrimSuffix(" tables ", "s") is a no-op on a string that ends in a space.
// Drop the TrimSpace and this subtest, alone, goes red.
func TestDeleteTableIsRefusedWithAReason(t *testing.T) {
	for _, arg := range []string{"table", "dataset", "tables", "datasets", "Table", " Tables "} {
		t.Run(arg, func(t *testing.T) {
			err := underRoot(t, arg, "orders")
			if err == nil {
				t.Fatalf("qctl delete %s must not succeed", arg)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			msg := err.Error()
			for _, want := range []string{"web UI", "undelete table", cmdutil.APIResourcesHint} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal should mention %q, got:\n%s", want, msg)
				}
			}
		})
	}
}

// The wrapper must not swallow the ordinary near-miss path.
func TestDeleteUnknownSubcommandStillSuggests(t *testing.T) {
	err := underRoot(t, "fil")
	if err == nil {
		t.Fatal("expected an error for an unknown subcommand")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "\n  qctl delete file <id>\n") {
		t.Errorf("expected a near-miss suggestion running 'qctl delete file', got:\n%s", err)
	}
	// Both endings are asserted, on both paths, because the refusal's comment
	// claims it behaves like `qctl create table` -- and until this round the
	// refusal ended at the undelete pointer while every other rejected guess
	// ended here. A refusal that names no alternative is the dead end the
	// whole wrapper exists to remove.
	if !strings.Contains(err.Error(), cmdutil.APIResourcesHint) {
		t.Errorf("near-miss should end with the api-resources hint, got:\n%s", err)
	}
}

// With no word to redirect, bare `qctl delete` reaches SubcommandRequired.
func TestDeleteBareNamesItsSubcommands(t *testing.T) {
	out, err := underRootCapturing(t)
	if err == nil {
		t.Fatal("bare qctl delete exited 0")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.HasPrefix(err.Error(), "qctl delete needs a subcommand: ") {
		t.Errorf("message = %q", err.Error())
	}
	if out != "" {
		t.Errorf("bare qctl delete printed:\n%s", out)
	}
}

// The row deletes live under `qctl rows`, so `qctl delete rows` has to say so.
// The spellings are the five a user actually reaches for, and each is here for
// a measured reason rather than for symmetry:
//
//	rows  -- the plural alias data.md declares. Neither arm of cobra's
//	         SuggestionsFor fires on it: `rule` and `rules` are both
//	         levenshtein distance 3, past SuggestionsMinimumDistance, and no
//	         subcommand name has "rows" as a prefix. So it dead-ends today.
//	row   -- the singular guess. It is NOT an alias, and it only resolves
//	         because namesTheRows retries the PLURALISED spelling; drop that
//	         retry and this subtest is the one that goes red.
//	datas -- the plural of the canonical name, and the mirror case: it
//	         resolves only through the SINGULARISING retry. Before that retry
//	         existed it was the worst outcome of the five -- not a
//	         misdirection but a bare "unknown command" with no suggestion at
//	         all, since nothing in the delete tree is within cobra's
//	         levenshtein distance of it either.
//	data  -- the canonical name, and the spelling that most needs this: it
//	         does not merely dead-end today, it MISDIRECTS. SuggestionsFor's
//	         prefix arm matches `data-review-policy`, so a user who wanted to
//	         delete rows is pointed at deleting a review policy.
//	" Rows " -- case and surrounding space. catalog.Lookup folds both itself,
//	         so this one passes even without namesTheRows' own fold.
//	" Row "  -- case, surrounding space AND the pluralising retry at once, and
//	         the spelling that holds namesTheRows' own TrimSpace: the retry
//	         appends "s" to the trimmed name, so without that trim " Row "
//	         would be looked up as " Row s" and resolve to nothing.
//	" DATAS " -- what holds the sibling ToLower, which used to be held by
//	         nothing. While the only retry was the pluralising one that fold
//	         genuinely could not matter -- Lookup folds its own input and the
//	         appended "s" is already lowercase. The SINGULARISING retry is
//	         different: TrimSuffix is case-SENSITIVE, so it does not strip a
//	         capital "S". Without the ToLower, " DATAS " misses all three
//	         lookups ("DATAS", "DATASs", and "DATAS" again unchanged) and the
//	         redirect is lost.
func TestDeleteRowsIsRedirectedToTheRowsGroup(t *testing.T) {
	for _, arg := range []string{"rows", "row", "datas", "data", " Rows ", " Row ", " DATAS "} {
		t.Run(arg, func(t *testing.T) {
			err := underRoot(t, arg, "orders")
			if err == nil {
				t.Fatalf("qctl delete %q must not succeed", arg)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			// The exit code alone cannot identify this refusal -- cobra's own
			// unknown-command and unknown-flag errors are exit 2 too -- so the
			// message is asserted as well: the delete verb, and the move that
			// a clean row needs first.
			msg := err.Error()
			for _, want := range []string{
				"qctl rows delete-quarantined",
				"qctl rows send-to-quarantine",
				cmdutil.APIResourcesHint,
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("redirect should mention %q, got:\n%s", want, msg)
				}
			}
		})
	}
}

// namesTheRows ends in `r.Name == dataResource`, and until this test nothing
// held it: replace it with a bare `return ok` and every catalog-resolvable
// spelling gets the ROWS redirect, with the whole package still green.
// namesTheTable's identical filter is held only by accident -- the table check
// runs first, so a bare `return ok` there hands the TABLE refusal to the three
// subtests above that resolve on ITS direct hit (`rows`, `data`, `" Rows "`),
// and they go red. Not `row` or `" Row "`, which miss both of that helper's
// lookups. The rows check runs second, so no table spelling ever reaches its
// own filter, and it had no cover at all.
//
// So this test comes at it from the other side: a resource that is neither
// `table` nor `data` must fall through BOTH redirects to the ordinary
// near-miss path. The first three are ordinary direct hits, listed in the
// plural so that an alias or page change to any one resource cannot silently
// retire the guard. `alia` is not padding: it is the ONLY spelling in the
// whole catalog that reaches the filter through the pluralising RETRY without
// being `row`. `alias` is a resource whose name already ends in "s", so the
// direct lookup of "alia" misses and the retry lands it on `alias` -- exactly
// the "can land on any resource" case the filter exists for, and the arm the
// three direct hits never touch.
func TestDeleteAnotherResourceNameIsNotRedirected(t *testing.T) {
	for _, arg := range []string{"alert", "warning", "org", "alia"} {
		t.Run(arg, func(t *testing.T) {
			err := underRoot(t, arg, "orders")
			if err == nil {
				t.Fatalf("qctl delete %s must not succeed", arg)
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
			}
			// The exit code cannot tell the three paths apart -- all of them
			// are exit 2 -- so the message decides which one ran. It must be
			// the generic one, carrying neither refusal's distinctive line.
			msg := err.Error()
			if !strings.Contains(msg, "unknown command") {
				t.Errorf("expected the generic unknown-command path, got:\n%s", msg)
			}
			for _, unwanted := range []string{"qctl rows delete-quarantined", "undelete table"} {
				if strings.Contains(msg, unwanted) {
					t.Errorf("%s is neither a table nor a rows spelling, but the refusal mentioning %q fired:\n%s",
						arg, unwanted, msg)
				}
			}
		})
	}
}

// underRootCapturing is underRoot with the output kept, for the paths that
// print rather than return: cobra answers --help by calling HelpFunc and
// returning nil, so the assertions there are on stdout and on a nil error.
func underRootCapturing(t *testing.T, args ...string) (string, error) {
	t.Helper()

	root := &cobra.Command{Use: "qctl", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(NewCommand())
	root.SetArgs(append([]string{"delete"}, args...))
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)

	err := root.Execute()
	return out.String(), err
}

// --help must reach the redirects too.
//
// It does not reach RunE: cobra's execute() returns flag.ErrHelp as soon as the
// help flag is set (command.go), before the Run hooks, and ExecuteC
// answers it by calling HelpFunc. So `qctl delete rows` was
// redirected while `qctl delete rows --help` printed the delete group's own
// help -- seven subcommands, not one word about where row deletes live -- and
// --help is the likelier of the two spellings from an agent, which reaches for
// it before it guesses.
//
// Exit 0 and stdout, as any --help is. `qctl delete rows` without the flag is
// still exit 2: there the user asked to DO something, which is a different
// question with a different answer, and TestDeleteRowsIsRedirectedToTheRows
// Group pins that half.
func TestDeleteHelpIsRedirectedToo(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{"rows", "qctl rows delete-quarantined"},
		{"data", "qctl rows delete-quarantined"},
		{"table", "qctl undelete table"},
		{"tables", "qctl undelete table"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			out, err := underRootCapturing(t, tc.arg, "--help")
			if err != nil {
				t.Fatalf("--help must not error: %v", err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("`delete %s --help` does not carry the redirect (want %q):\n%s", tc.arg, tc.want, out)
			}
			// The group help is what it REPLACED, so its presence means the
			// interception did not fire and the assertion above passed on a
			// coincidence.
			if strings.Contains(out, "Available Commands:") {
				t.Errorf("`delete %s --help` printed the group help instead:\n%s", tc.arg, out)
			}
		})
	}
}

// And the group's own help is untouched. The interception reads the positional
// tail, so `qctl delete --help` and a spelling that names no redirected
// resource must both reach the inherited HelpFunc -- here the minimal root's
// cobra default. Under the real root, that HelpFunc rejects `delete alert
// --help` with exit 2; root's tests pin that half.
func TestDeleteHelpIsOtherwiseUnchanged(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"alert", "--help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			out, err := underRootCapturing(t, args...)
			if err != nil {
				t.Fatalf("--help must not error: %v", err)
			}
			if !strings.Contains(out, "Available Commands:") {
				t.Errorf("the delete group's own help was replaced:\n%s", out)
			}
			for _, unwanted := range []string{"qctl rows delete-quarantined", "qctl undelete table"} {
				if strings.Contains(out, unwanted) {
					t.Errorf("a redirect fired on %v:\n%s", args, out)
				}
			}
		})
	}
}

// The fall-through is the parent's HelpFunc when --help runs, not when
// NewCommand ran: delete is built before it has a parent, so a HelpFunc the
// root installs afterwards must still see every call the redirects leave.
// That includes a subcommand's, which inherits helpForDelete: its positional
// is its own argument, so `delete rule table --help` is help for `delete
// rule`, not the table refusal.
func TestDeleteHelpFallsThroughToTheParentsCurrentHelpFunc(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--help"}, "qctl delete"},
		{[]string{"alert", "--help"}, "qctl delete"},
		{[]string{"rule", "--help"}, "qctl delete rule"},
		{[]string{"rule", "table", "--help"}, "qctl delete rule"},
		{[]string{"rule", "rows", "--help"}, "qctl delete rule"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			root := &cobra.Command{Use: "qctl", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(NewCommand())
			var helped []string
			root.SetHelpFunc(func(c *cobra.Command, _ []string) {
				helped = append(helped, c.CommandPath())
			})
			root.SetArgs(append([]string{"delete"}, tc.args...))
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)

			if err := root.Execute(); err != nil {
				t.Fatalf("--help must not error: %v", err)
			}
			if len(helped) != 1 || helped[0] != tc.want {
				t.Errorf("parent's HelpFunc saw %q, want exactly [%q]", helped, tc.want)
			}
			if out.Len() != 0 {
				t.Errorf("something other than the parent's HelpFunc printed:\n%s", out.String())
			}
		})
	}
}

func TestDeleteRedirectsConfirmationSuffix(t *testing.T) {
	for _, interactive := range []bool{false, true} {
		cmdutil.ResetReader()
		if interactive {
			cmdutil.SetReader(strings.NewReader("y\n"))
		}
		t.Cleanup(cmdutil.ResetReader)
		suffix := " --yes"
		if interactive {
			suffix = ""
		}
		for _, tc := range []struct{ arg, command string }{
			{"table", "qctl undelete table <id-or-name>" + suffix + "   #"},
			{"rows", "qctl rows delete-quarantined <table-id-or-name> --status failed_validation --reason <reason>" + suffix + "\n"},
			{"rows", "qctl rows send-to-quarantine <table-id-or-name> --row-id <id>" + suffix + "\n"},
			{"rows", "qctl rows delete-quarantined <table-id-or-name> --signature <hex> --exclude-from-future-uploads=<true|false> --reason <reason>" + suffix + "\n"},
		} {
			out, err := underRootCapturing(t, tc.arg, "--help")
			if err != nil || !strings.Contains(out, tc.command) {
				t.Errorf("interactive=%t: want %q: %v\n%s", interactive, tc.command, err, out)
			}
		}
	}
}
