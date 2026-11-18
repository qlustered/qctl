package cmdutil

import (
	"errors"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// flagTree builds qctl -> get -> {table, tables} and qctl -> apply -> table.
// The names are real resource nouns, so catalog.ListCommand resolves `get
// table` to `get tables`. It returns get table, get tables and apply table.
func flagTree() (getTable, getTables, applyTable *cobra.Command) {
	run := func(*cobra.Command, []string) error { return nil }
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("columns", "", "")
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().String("org", "", "")
	root.PersistentFlags().Bool("legacy", false, "")
	_ = root.PersistentFlags().MarkHidden("legacy")
	root.SetFlagErrorFunc(FlagError)

	get := &cobra.Command{Use: "get"}
	getTable = &cobra.Command{Use: "table <name-or-id>", RunE: run}
	getTable.Flags().String("release", "", "")
	getTables = &cobra.Command{Use: "tables", RunE: run}
	getTables.Flags().IntP("limit", "l", 0, "")
	getTables.Flags().Bool("reverse", false, "")
	get.AddCommand(getTable, getTables)

	apply := &cobra.Command{Use: "apply"}
	applyTable = &cobra.Command{Use: "table", RunE: run}
	applyTable.Flags().StringP("filename", "f", "", "")
	_ = applyTable.MarkFlagRequired("filename")
	apply.AddCommand(applyTable)

	root.AddCommand(get, apply)
	return getTable, getTables, applyTable
}

// parseFailure parses args with cmd.ParseFlags, as cobra's execute does, and
// hands the failure to FlagError, returning both.
func parseFailure(t *testing.T, cmd *cobra.Command, args ...string) (orig, got error) {
	t.Helper()
	orig = cmd.ParseFlags(args)
	if orig == nil {
		t.Fatalf("%s %s parsed without error", cmd.CommandPath(), strings.Join(args, " "))
	}
	return orig, FlagError(cmd, orig)
}

func TestFlagErrorHints(t *testing.T) {
	cases := []struct {
		name string
		cmd  func() *cobra.Command
		args []string
		want string // the line between cobra's text and the trailer; "" for none
	}{
		{
			name: "typo of an inherited flag",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--colums", "x"},
			want: "Did you mean --columns?",
		},
		{
			name: "case is ignored",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--Columns", "x"},
			want: "Did you mean --columns?",
		},
		{
			name: "prefix matches rank before edit distance",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--out", "json"},
			want: "Did you mean --output or --org?",
		},
		{
			name: "flag of the list sibling",
			cmd:  func() *cobra.Command { c, _, _ := flagTree(); return c },
			args: []string{"--limit", "5"},
			want: "--limit is a flag of 'qctl get tables', which lists them.",
		},
		// --reverse is two edits from get table's --release; the exact flag
		// on the sibling is the better answer.
		{
			name: "list sibling beats a near miss",
			cmd:  func() *cobra.Command { c, _, _ := flagTree(); return c },
			args: []string{"--reverse"},
			want: "--reverse is a flag of 'qctl get tables', which lists them.",
		},
		{
			name: "shorthand of the list sibling",
			cmd:  func() *cobra.Command { c, _, _ := flagTree(); return c },
			args: []string{"-l", "5"},
			want: "-l is a flag of 'qctl get tables', which lists them.",
		},
		// -c is not --columns' shorthand; a shorthand gets no near misses.
		{
			name: "unknown shorthand gets no did-you-mean",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"-c"},
		},
		// pflag names a non-ASCII shorthand by its first byte, which
		// ShorthandLookup would panic on.
		{
			name: "non-ASCII shorthand",
			cmd:  func() *cobra.Command { c, _, _ := flagTree(); return c },
			args: []string{"-é"},
		},
		{
			name: "hidden flag is never suggested",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--legacyy"},
		},
		{
			name: "nothing near",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--bogus"},
		},
		{
			name: "invalid value",
			cmd:  func() *cobra.Command { _, c, _ := flagTree(); return c },
			args: []string{"--limit", "abc"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := tc.cmd()
			orig, err := parseFailure(t, cmd, tc.args...)
			trailer := "Run '" + cmd.CommandPath() + " --help' for its flags."
			want := orig.Error() + "\n\n" + trailer
			if tc.want != "" {
				want = orig.Error() + "\n\n" + tc.want + "\n" + trailer
			}
			if err.Error() != want {
				t.Errorf("message =\n%s\nwant\n%s", err.Error(), want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	}
}

func TestFlagErrorKeepsTheOriginalInTheChain(t *testing.T) {
	_, getTables, _ := flagTree()
	orig, err := parseFailure(t, getTables, "--colums", "x")

	if !errors.Is(err, orig) {
		t.Errorf("errors.Is does not find cobra's error in %v", err)
	}
	reached := false
	for e := err; e != nil; e = errors.Unwrap(e) {
		if e == orig {
			reached = true
		}
	}
	if !reached {
		t.Error("errors.Unwrap never reaches cobra's error")
	}
	if first, _, _ := strings.Cut(err.Error(), "\n"); first != "unknown flag: --colums" {
		t.Errorf("first line = %q, want cobra's text unchanged", first)
	}
}

// Through Execute, so the flags FlagError sees are the ones cobra merged.
func TestFlagErrorTrailerAppearsOnce(t *testing.T) {
	getTable, _, _ := flagTree()
	root := getTable.Root()
	root.SetArgs([]string{"get", "table", "--limit", "5"})
	err := root.Execute()
	if err == nil {
		t.Fatal("get table --limit 5 succeeded")
	}
	msg := err.Error()
	trailer := "Run 'qctl get table --help' for its flags."
	if n := strings.Count(msg, "--help' for"); n != 1 {
		t.Errorf("trailer appears %d times, want 1; got:\n%s", n, msg)
	}
	if !strings.HasSuffix(msg, trailer) {
		t.Errorf("message does not end with %q; got:\n%s", trailer, msg)
	}
	if !strings.Contains(msg, "--limit is a flag of 'qctl get tables'") {
		t.Errorf("no list-sibling hint; got:\n%s", msg)
	}
}

func TestRequiredFlagsErrorShowsUsage(t *testing.T) {
	_, _, applyTable := flagTree()
	orig := applyTable.ValidateRequiredFlags()
	if orig == nil {
		t.Fatal("ValidateRequiredFlags passed with -f unset")
	}
	err := RequiredFlagsError(applyTable, orig)

	want := `required flag(s) "filename" not set` + "\nUsage: qctl apply table -f <file.yaml>"
	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
	if !errors.Is(err, orig) {
		t.Errorf("errors.Is does not find cobra's error in %v", err)
	}
}
