package root

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

// cobra raises these itself and hands them straight to main, where GetExitCode
// falls through to 1. `guide output-and-scripting` promises 2 for every input
// qctl rejects as malformed, and CLAUDE.md's error contract says the same.
func TestCobraFlagRejectionsExitBadUsage(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "unknown flag",
			args: []string{"api-resources", "--bogus"},
			want: "unknown flag: --bogus",
		},
		// A flag a sibling registers is still unknown here: approve carries
		// --comment, withdraw does not.
		{
			name: "flag only a sibling verb registers",
			args: []string{"data-review", "withdraw", "0190aaaa-1111-4222-8333-444444444444",
				"--comment", "x"},
			want: "unknown flag: --comment",
		},
		{
			name: "invalid flag value",
			args: []string{"api-resources", "--max-column-width=abc"},
			want: `invalid argument "abc" for "--max-column-width" flag`,
		},
		// The server's cursor ceiling is 2^63-1, the int64 flag's own.
		{
			name: "delta cursor past int64",
			args: []string{"upload", "file", "delta.csv", "--delta", "--delta-cursor", "9223372036854775808"},
			want: `invalid argument "9223372036854775808" for "--delta-cursor" flag`,
		},
		{
			name: "required flag not set",
			args: []string{"apply", "table"},
			want: `required flag(s) "filename" not set`,
		},
		// Pins root.go's ordering claim: ValidateRequiredFlags runs last in
		// the hook, so a command missing -f AND carrying a bad -o still
		// reports the output format. Both exit 2, so the message is what
		// tells the two errors apart.
		{
			name: "output format beats the missing required flag",
			args: []string{"apply", "table", "-o", "bogus"},
			want: `unknown output format "bogus"`,
		},
		// cobra's execute (command.go) calls ValidateArgs before the
		// PersistentPreRunE loop over parents, so root's hook cannot pre-empt
		// it. cmdutil.TypeArgsErrors decorates the validators instead.
		{
			name: "too few positional arguments",
			args: []string{"explain"},
			want: "qctl explain: accepts 1 arg(s), received 0",
		},
		{
			name: "too many positional arguments",
			args: []string{"explain", "table", "extra"},
			want: "qctl explain: accepts 1 arg(s), received 2",
		},
		{
			name: "missing id names the list command",
			args: []string{"get", "rule"},
			want: "To list them, run: qctl get rules",
		},
		// What cmdutil.FlagError and RequiredFlagsError add after cobra's line.
		{
			name: "unknown flag ends at the command's help",
			args: []string{"api-resources", "--bogus"},
			want: "unknown flag: --bogus\n\nRun 'qctl api-resources --help' for its flags.",
		},
		{
			name: "flag of the list sibling",
			args: []string{"get", "table", "--limit", "5"},
			want: "unknown flag: --limit\n\n--limit is a flag of 'qctl get tables', which lists them.",
		},
		{
			name: "typo of an inherited flag",
			args: []string{"get", "tables", "--colums", "x"},
			want: "unknown flag: --colums\n\nDid you mean --columns?",
		},
		{
			name: "required flag shows usage",
			args: []string{"apply", "table"},
			want: `required flag(s) "filename" not set` + "\nUsage: qctl apply table -f <file.yaml>",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := runRoot(t, tc.args...)
			if err == nil {
				t.Fatalf("qctl %s exited 0", strings.Join(tc.args, " "))
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The negative control: the hooks must not turn a command that runs fine into
// a failure, and must not fire when nothing is wrong.
func TestValidInvocationStillSucceeds(t *testing.T) {
	if _, err := runRoot(t, "api-resources", "-o", "json"); err != nil {
		t.Fatalf("qctl api-resources -o json failed: %v", err)
	}
}

// Root is the last command in the tree that rejected an unknown name through
// cobra's legacyArgs (args.go), which Find returns from inside Execute
// before execute() runs at all. Giving root an Args makes Find skip that
// branch, and giving it a RunE gets execute() past the !Runnable() bail in
// command.go -- together they route `qctl bogustop` into the same
// SubcommandRequired every other parent uses.
func TestUnknownRootCommandExitsBadUsage(t *testing.T) {
	_, err := runRoot(t, "bogustop")
	if err == nil {
		t.Fatal("qctl bogustop exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `unknown command "bogustop" for "qctl"`) {
		t.Errorf("message does not name the unknown command; got: %v", err)
	}
	// Root now points at api-resources like the other 23 nodes routing
	// through SubcommandRequired do (22 assign it as their RunE; `qctl
	// create` calls it from a wrapper). Before this change it was the only
	// one of them that did not.
	if !strings.Contains(err.Error(), "qctl api-resources") {
		t.Errorf("message does not redirect to api-resources; got: %v", err)
	}
}

// Root's suggestions came from cobra's findSuggestions, which defaults
// SuggestionsMinimumDistance to 2. SubcommandRequired had to learn that
// default (Task 2) or routing root through it would have silently dropped
// them. This is that regression guard.
func TestUnknownRootCommandStillSuggests(t *testing.T) {
	_, err := runRoot(t, "gt")
	if err == nil {
		t.Fatal("qctl gt exited 0")
	}
	if !strings.Contains(err.Error(), "Did you mean this?") {
		t.Errorf("near miss produced no suggestion block; got: %v", err)
	}
	if !strings.Contains(err.Error(), "  qctl get\n") {
		t.Errorf("suggestion block does not name \"qctl get\"; got: %v", err)
	}
}

// The negative control: bare `qctl` printed help and exited 0 through cobra's
// flag.ErrHelp branch. Root's RunE must reproduce that, not turn the bare
// invocation into an error.
func TestBareRootStillPrintsHelp(t *testing.T) {
	// []string{}... not a zero-arg call: a zero-arg variadic passes a nil
	// slice, and cobra reads a nil SetArgs as "no args were set" and
	// substitutes os.Args[1:] (ExecuteC, cobra@v1.10.2/command.go) -- i.e.
	// this test binary's own -test.* flags. A non-nil empty slice pins the
	// intent.
	out, err := runRoot(t, []string{}...)
	if err != nil {
		t.Fatalf("qctl exited non-zero: %v", err)
	}
	if !strings.Contains(out, "Available Commands:") {
		t.Errorf("bare qctl did not print help; got %q", out)
	}
}

// cobra answers the help flag with HelpFunc before RunE, so a dispatch parent
// handed a name it does not have printed its own help and exited 0 -- and an
// agent read that as "the command exists". Root's HelpFunc rejects it the way
// RunE would have: exit 2, nothing on stdout. Root under both spellings of the
// flag, two ordinary parents, and delete (whose own HelpFunc falls through to
// root's) are each covered.
func TestUnknownSubcommandWithHelpFlagExitsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"tables", "--help"},
		{"tables", "-h"},
		{"bogustop", "--help"},
		{"get", "bogus", "--help"},
		{"config", "bogus", "--help"},
		{"config", "bogus", "-h"},
		{"delete", "bogus", "--help"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			// The stuck help flag this would otherwise leave on the shared
			// tree is runRoot's problem, not this test's -- see the cleanup
			// there.
			out, err := runRoot(t, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d (ExitBadUsage); err = %v", got, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), "unknown command") {
				t.Errorf("not the unknown-command error; got: %v", err)
			}
			if out != "" {
				t.Errorf("printed on stdout as well as failing:\n%s", out)
			}
		})
	}
}

// The negative control: the help flag still prints help wherever no unknown
// name is involved -- a parent with no tail, a leaf with or without its own
// positional, delete's redirect, and the help command.
func TestHelpFlagWithoutAnUnknownSubcommandStillPrintsHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"--help"}, want: "Available Commands:"},
		{args: []string{"get", "--help"}, want: "qctl get [command]"},
		{args: []string{"get", "table", "--help"}, want: "qctl get table"},
		{args: []string{"get", "table", "foo", "--help"}, want: "qctl get table"},
		{args: []string{"delete", "rows", "--help"}, want: "qctl rows delete-quarantined"},
		{args: []string{"help", "get"}, want: "qctl get [command]"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := runRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("qctl %s: %v", strings.Join(tc.args, " "), err)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output does not contain %q; got %q", tc.want, out)
			}
		})
	}
}

// The rejection reads state cobra leaves on the shared tree, and runRoot only
// resets it when the test ends. So within one test, `get bogus --help` leaves
// get's help flag set and its tail ["bogus"] behind for `help get`, which
// renders get's help through the same HelpFunc. It must print, not reject.
func TestHelpTopicAfterAHelpFlagRejectionStillPrints(t *testing.T) {
	if _, err := runRoot(t, "get", "bogus", "--help"); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Fatalf("qctl get bogus --help: want exit %d, got err = %v", qerrors.ExitBadUsage, err)
	}
	get, _, err := GetRootCmd().Find([]string{"get"})
	if err != nil {
		t.Fatalf("Find(get): %v", err)
	}
	// Without the leftover state this proves nothing.
	if !get.Flags().Changed("help") || len(get.Flags().Args()) == 0 {
		t.Fatalf("get's help flag or tail did not survive; changed=%v tail=%q",
			get.Flags().Changed("help"), get.Flags().Args())
	}

	out, err := runRoot(t, "help", "get")
	if err != nil {
		t.Fatalf("qctl help get after get bogus --help: %v", err)
	}
	if !strings.Contains(out, "qctl get [command]") {
		t.Errorf("qctl help get did not print get's help; got %q", out)
	}
}

func TestEmptyStringArgumentExitsBadUsage(t *testing.T) {
	_, err := runRoot(t, "")
	if err == nil {
		t.Fatal(`qctl "" exited 0`)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "qctl: argument 1 cannot be empty") {
		t.Errorf("message does not name the empty argument; got: %v", err)
	}
}

// A Runnable root reaches PersistentPreRunE, which it never did before: cobra
// bailed at !Runnable() (command.go execute) first. `qctl -o bogus` printed help
// and exited 0; now it is rejected like `qctl get -o bogus` always was.
func TestBareRootValidatesOutputFormat(t *testing.T) {
	_, err := runRoot(t, "-o", "bogus")
	if err == nil {
		t.Fatal("qctl -o bogus exited 0")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `unknown output format "bogus"`) {
		t.Errorf("message = %q", err.Error())
	}
}

// Giving root an Args (above) also made cobra's stock help command stop
// reporting an unknown topic: its Run trusts the error from
// c.Root().Find(args), and Find only reaches legacyArgs when Args is nil
// (cobra@v1.10.2/command.go Find), so `qctl help bogustopic` resolved to
// root and printed root's help with no diagnostic at all. root.go installs a
// help command that resolves the topic itself. Exit stays 0, as it was
// before this branch -- this is a restored diagnostic, not a new rejection.
func TestUnknownHelpTopicIsReported(t *testing.T) {
	out, err := runRoot(t, "help", "bogustopic")
	if err != nil {
		t.Fatalf("qctl help bogustopic exited non-zero: %v", err)
	}
	if !strings.Contains(out, "Unknown help topic [`bogustopic`]") {
		t.Errorf("no unknown-topic diagnostic; got %q", out)
	}
}

// The negative control, and the reason the check is legacyArgs' condition
// rather than "Find had anything left over": a topic that does resolve must
// still print that command's help and nothing else. `help get rule` covers a
// two-level path, where the found command is a grandchild rather than root.
func TestResolvedHelpTopicsPrintTheirHelp(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{args: []string{"help"}, want: "Available Commands:"},
		{args: []string{"help", "get"}, want: "qctl get [command]"},
		{args: []string{"help", "get", "rule"}, want: "qctl get rule"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			out, err := runRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("qctl %s exited non-zero: %v", strings.Join(tc.args, " "), err)
			}
			if strings.Contains(out, "Unknown help topic") {
				t.Errorf("resolved topic reported as unknown; got %q", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Errorf("output does not contain %q; got %q", tc.want, out)
			}
		})
	}
}

// The other negative control, and the reason the help command wraps Find
// rather than re-deriving what Find would have concluded. legacyArgs is not
// handed Find's leftover slice, it is handed stripFlags of it
// (cobra@v1.10.2/command.go Find), and stripFlags drops empty tokens, drops
// `-`-prefixed ones, stops at `--`, and eats the value of a `-f arg` pair.
// A predicate that tests the raw leftovers instead calls these
// three unknown, where every qctl before this branch printed root's help.
func TestHelpTopicsThatAreOnlyFlagsResolveToRoot(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "empty string", args: []string{"help", ""}},
		{name: "flag after --", args: []string{"help", "--", "--bogus"}},
		{name: "flag value after --", args: []string{"help", "--", "-o", "bogus"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runRoot(t, tc.args...)
			if err != nil {
				t.Fatalf("qctl %s exited non-zero: %v", strings.Join(tc.args, " "), err)
			}
			if strings.Contains(out, "Unknown help topic") {
				t.Errorf("reported as an unknown topic; got %q", out)
			}
			if !strings.Contains(out, "Available Commands:") {
				t.Errorf("did not print root's help; got %q", out)
			}
		})
	}
}

// Same divergence on the completion path, where it is not cosmetic: an empty
// word is exactly what a shell sends for `qctl help <TAB>` at the second
// position, and the raw-leftovers predicate answered it with nothing.
func TestHelpCompletionAfterAnEmptyWordStillCompletes(t *testing.T) {
	out, err := runRoot(t, "__complete", "help", "", "")
	if err != nil {
		t.Fatalf("__complete help '' '' failed: %v", err)
	}
	if !strings.Contains(out, "get") {
		t.Errorf("no completions offered; got %q", out)
	}
}

// findTopic clears root's Args and restores it. If the restore ever stops
// happening, Find starts returning legacyArgs' error again and `qctl bogustop`
// leaves via ExecuteC's error path (cobra@v1.10.2/command.go)
// without ever reaching SubcommandRequired -- exit 1, the whole point of the
// branch, undone by an invocation that ran earlier in the same process.
func TestHelpDoesNotLeaveRootWithoutItsArgs(t *testing.T) {
	if _, err := runRoot(t, "help", "bogustopic"); err != nil {
		t.Fatalf("qctl help bogustopic exited non-zero: %v", err)
	}
	_, err := runRoot(t, "bogustop")
	if err == nil {
		t.Fatal("qctl bogustop exited 0 after a help invocation")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}
