package cmdutil

import (
	"errors"
	"io"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// argsTree builds a small qctl tree typed by TypeArgsErrors. The leaves are
// named after real resources so catalog.ListCommand and catalog.ShowCommand
// resolve against them.
func argsTree(t *testing.T) map[string]*cobra.Command {
	t.Helper()
	cmds := map[string]*cobra.Command{
		"get rule":        {Use: "rule <name-or-id>", Args: cobra.ExactArgs(1)},
		"get rules":       {Use: "rules", Args: cobra.NoArgs},
		"get alerts":      {Use: "alerts", Args: cobra.NoArgs},
		"get table-rule":  {Use: "table-rule <name-or-id>", Args: cobra.ExactArgs(1)},
		"get table-rules": {Use: "table-rules [table-id-or-name]", Args: cobra.MaximumNArgs(1)},
		"get table-kinds": {Use: "table-kinds", Args: cobra.NoArgs},
		"describe alert":  {Use: "alert <id>", Args: cobra.ExactArgs(1)},
		"describe rule":   {Use: "rule <name-or-id>", Args: cobra.ExactArgs(1)},
		"explain":         {Use: "explain RESOURCE[.FIELD]", Args: cobra.ExactArgs(1)},

		"get data-review-policy":    {Use: "data-review-policy", Args: cobra.NoArgs},
		"delete data-review-policy": {Use: "data-review-policy", Args: cobra.NoArgs},
	}
	alerts := cmds["get alerts"]
	alerts.RunE = func(*cobra.Command, []string) error { return nil }
	alerts.Flags().Bool("resolved", false, "")
	alerts.Flags().Bool("reverse", false, "")

	root := &cobra.Command{Use: "qctl"}
	get := &cobra.Command{Use: "get"}
	describe := &cobra.Command{Use: "describe"}
	del := &cobra.Command{Use: "delete"}
	get.AddCommand(cmds["get rule"], cmds["get rules"], cmds["get alerts"],
		cmds["get table-rule"], cmds["get table-rules"], cmds["get table-kinds"],
		cmds["get data-review-policy"])
	describe.AddCommand(cmds["describe alert"], cmds["describe rule"])
	del.AddCommand(cmds["delete data-review-policy"])
	root.AddCommand(get, describe, del, cmds["explain"])
	TypeArgsErrors(root)
	return cmds
}

// rejectArgs runs cmd's Args and fails the test unless it rejects args with
// exit 2.
func rejectArgs(t *testing.T, cmd *cobra.Command, args ...string) error {
	t.Helper()
	err := cmd.Args(cmd, args)
	if err == nil {
		t.Fatalf("%s accepted %q", cmd.CommandPath(), args)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	return err
}

func TestTypeArgsErrorsMessage(t *testing.T) {
	cmds := argsTree(t)
	cases := []struct {
		name string
		cmd  string
		args []string
		want string
	}{
		{
			name: "missing argument names the list command",
			cmd:  "get rule",
			want: "qctl get rule: accepts 1 arg(s), received 0\n" +
				"Usage: qctl get rule <name-or-id>\n" +
				"To list them, run: qctl get rules",
		},
		{
			name: "missing argument reaches the list under another verb",
			cmd:  "describe alert",
			want: "qctl describe alert: accepts 1 arg(s), received 0\n" +
				"Usage: qctl describe alert <id>\n" +
				"To list them, run: qctl get alerts",
		},
		{
			name: "argument to a list command names the show command",
			cmd:  "get alerts",
			args: []string{"5", "6"},
			want: "qctl get alerts takes no arguments, got \"5\"\n" +
				"Usage: qctl get alerts\n" +
				"To show one, run: qctl describe alert 5",
		},
		{
			name: "argument to a list command prefers the get sibling",
			cmd:  "get rules",
			args: []string{"5"},
			want: "qctl get rules takes no arguments, got \"5\"\n" +
				"Usage: qctl get rules\n" +
				"To show one, run: qctl get rule 5",
		},
		{
			name: "no resource, no hint",
			cmd:  "explain",
			want: "qctl explain: accepts 1 arg(s), received 0\n" +
				"Usage: qctl explain RESOURCE[.FIELD]",
		},
		{
			name: "an optional argument is not an item, no hint",
			cmd:  "get table-rules",
			args: []string{"orders", "extra"},
			want: "qctl get table-rules: accepts at most 1 arg(s), received 2\n" +
				"Usage: qctl get table-rules [table-id-or-name]",
		},
		{
			name: "too many for a show command, no hint",
			cmd:  "get rule",
			args: []string{"a", "b"},
			want: "qctl get rule: accepts 1 arg(s), received 2\n" +
				"Usage: qctl get rule <name-or-id>",
		},
		{
			name: "not the list command, no hint",
			cmd:  "delete data-review-policy",
			args: []string{"5"},
			want: "qctl delete data-review-policy takes no arguments, got \"5\"\n" +
				"Usage: qctl delete data-review-policy",
		},
		{
			name: "no show sibling, no hint",
			cmd:  "get table-kinds",
			args: []string{"5"},
			want: "qctl get table-kinds takes no arguments, got \"5\"\n" +
				"Usage: qctl get table-kinds",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := rejectArgs(t, cmds[tc.cmd], tc.args...)
			if err.Error() != tc.want {
				t.Errorf("message =\n%s\nwant\n%s", err, tc.want)
			}
		})
	}
}

// pflag never takes a boolean flag's value from the next word, so
// `--resolved false` leaves "false" as a positional argument.
func TestTypeArgsErrorsBoolValueHint(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{
			name: "names the one boolean flag set",
			argv: []string{"get", "alerts", "--resolved", "false"},
			want: "qctl get alerts takes no arguments, got \"false\"\n" +
				"Usage: qctl get alerts\n" +
				"Boolean flags take a value only as --resolved=false",
		},
		{
			name: "generic when several are set",
			argv: []string{"get", "alerts", "--reverse", "--resolved", "true"},
			want: "qctl get alerts takes no arguments, got \"true\"\n" +
				"Usage: qctl get alerts\n" +
				"Boolean flags take a value only as --flag=true",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := argsTree(t)["get alerts"].Root()
			root.SetArgs(tc.argv)
			root.SetOut(io.Discard)
			root.SetErr(io.Discard)
			err := root.Execute()
			if err == nil {
				t.Fatalf("qctl %q exited 0", tc.argv)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
			if err.Error() != tc.want {
				t.Errorf("message =\n%s\nwant\n%s", err, tc.want)
			}
		})
	}
}

// The validator's own error stays reachable through Unwrap.
func TestTypeArgsErrorsKeepsTheCause(t *testing.T) {
	cmd := argsTree(t)["get rule"]
	err := rejectArgs(t, cmd)
	want := cobra.ExactArgs(1)(cmd, nil).Error()
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e.Error() == want {
			return
		}
	}
	t.Errorf("no error in the Unwrap chain of %q reads %q", err, want)
}

func TestTypeArgsErrorsReachesDescendants(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	parent := &cobra.Command{Use: "get"}
	leaf := &cobra.Command{Use: "rule", Args: cobra.ExactArgs(2)}
	parent.AddCommand(leaf)
	root.AddCommand(parent)
	TypeArgsErrors(root)

	rejectArgs(t, leaf, "only-one")
}

// A nil Args must stay nil. cobra's Find only reaches legacyArgs when Args is
// nil (command.go), so installing one changes dispatch.
func TestTypeArgsErrorsLeavesNilArgsNil(t *testing.T) {
	cmd := &cobra.Command{Use: "get"}
	TypeArgsErrors(cmd)
	if cmd.Args != nil {
		t.Error("TypeArgsErrors installed an Args validator on a command that had none")
	}
}

// Wrapping twice must not double the message or change the code.
func TestTypeArgsErrorsIsIdempotent(t *testing.T) {
	cmds := argsTree(t)
	for _, tc := range []struct {
		cmd  string
		args []string
	}{
		{"get rule", nil},
		{"get alerts", []string{"5"}},
	} {
		cmd := cmds[tc.cmd]
		once := rejectArgs(t, cmd, tc.args...).Error()
		TypeArgsErrors(cmd.Root())
		if twice := rejectArgs(t, cmd, tc.args...).Error(); twice != once {
			t.Errorf("%s: second pass changed the message\nonce:\n%s\ntwice:\n%s", tc.cmd, once, twice)
		}
	}
}

// A validator that already chose an exit code keeps it and its message.
func TestTypeArgsErrorsLeavesTypedErrorsAlone(t *testing.T) {
	typed := qerrors.NewNotFound("no such target")
	cmd := &cobra.Command{Use: "install", Args: func(*cobra.Command, []string) error { return typed }}
	TypeArgsErrors(cmd)
	if err := cmd.Args(cmd, []string{"x"}); err != typed {
		t.Errorf("Args returned %v, want the validator's own error unchanged", err)
	}
}

// The negative control: a validator that accepts the input must still return
// nil, not a typed nil-wrapping error.
func TestTypeArgsErrorsPassesValidInput(t *testing.T) {
	cmd := &cobra.Command{Use: "explain", Args: cobra.ExactArgs(1)}
	TypeArgsErrors(cmd)
	if err := cmd.Args(cmd, []string{"table"}); err != nil {
		t.Errorf("ExactArgs(1) rejected one argument: %v", err)
	}
}

func TestTypeArgsErrorsRejectsEmptyArguments(t *testing.T) {
	for _, tc := range []struct {
		name string
		args cobra.PositionalArgs
		argv []string
		want string
	}{
		{"required", cobra.ExactArgs(1), []string{""}, "argument 1 cannot be empty"},
		{"optional", cobra.MaximumNArgs(1), []string{""}, "argument 1 cannot be empty"},
		{"second of several", cobra.MinimumNArgs(1), []string{"orders", ""}, "argument 2 cannot be empty"},
		{"arbitrary", cobra.ArbitraryArgs, []string{""}, "argument 1 cannot be empty"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "target <ref>", Args: tc.args}
			TypeArgsErrors(cmd)
			want := "target: " + tc.want + "\nUsage: target <ref>"
			for range 2 {
				if err := rejectArgs(t, cmd, tc.argv...); err.Error() != want {
					t.Errorf("error = %q, want %q", err, want)
				}
				TypeArgsErrors(cmd)
			}
		})
	}
}

func TestTypeArgsErrorsPreservesArgumentValidation(t *testing.T) {
	cmd := &cobra.Command{Use: "target <ref>", Args: cobra.ExactArgs(1)}
	TypeArgsErrors(cmd)
	if err := rejectArgs(t, cmd, "", "extra"); err.Error() !=
		"target: accepts 1 arg(s), received 2\nUsage: target <ref>" {
		t.Errorf("argument count did not win: %v", err)
	}
	for _, arg := range []string{"orders", " orders ", " "} {
		if err := cmd.Args(cmd, []string{arg}); err != nil {
			t.Errorf("nonempty argument %q was rejected: %v", arg, err)
		}
	}
}
