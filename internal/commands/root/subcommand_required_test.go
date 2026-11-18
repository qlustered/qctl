package root

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// The unknown-subcommand and bare-parent messages against the shipped tree,
// where the resource nouns are the real pages and aliases.
func TestSubcommandRequiredOnTheRealTree(t *testing.T) {
	cases := []struct {
		name string
		args []string
		// want is every substring the message must carry.
		want []string
		// unwanted is every substring it must not.
		unwanted []string
		// exact, when set, is the whole message.
		exact string
	}{
		{
			name: "unknown completion shell",
			args: []string{"completion", "bogus"},
			want: []string{`unknown command "bogus" for "qctl completion"`},
		},
		{
			name: "invalid completion format",
			args: []string{"completion", "-o", "bogus"},
			want: []string{`unknown output format "bogus"`},
		},
		{
			name: "unknown skill verb",
			args: []string{"skill", "bogus"},
			want: []string{`unknown command "bogus" for "qctl skill"`},
		},
		{
			name: "singular resource noun at root",
			args: []string{"table"},
			want: []string{
				"unknown command \"table\" for \"qctl\"\n\nDid you mean this?\n" +
					"  qctl get table <id-or-name>\n" +
					"  qctl get tables\n" +
					"  qctl describe table [id-or-name]\n",
			},
			// Levenshtein neighbours of "table" that the old message listed.
			unwanted: []string{"  qctl enable\n", "  qctl table-rule\n"},
		},
		{
			name: "plural resource noun at root",
			args: []string{"tables"},
			want: []string{
				"Did you mean this?\n" +
					"  qctl get table <id-or-name>\n" +
					"  qctl get tables\n" +
					"  qctl describe table [id-or-name]\n",
			},
		},
		{
			name: "resource with no get command",
			args: []string{"get", "rule-merge"},
			want: []string{
				"unknown command \"rule-merge\" for \"qctl get\"\n\nDid you mean this?\n" +
					"  qctl describe rule-merge <id>\n",
			},
		},
		{
			name:  "no suggestion",
			args:  []string{"get", "bogus"},
			exact: "unknown command \"bogus\" for \"qctl get\"\n\n" + cmdutil.APIResourcesHint,
		},
		{
			name: "typo of an alias",
			args: []string{"config", "get-contexs"},
			want: []string{"  qctl config list-contexts\n"},
		},
		{
			name: "near miss at root",
			args: []string{"gt"},
			want: []string{"Did you mean this?\n  qctl get\n"},
		},
		{
			name:  "bare parent with many subcommands",
			args:  []string{"get"},
			exact: "qctl get needs a subcommand.\n" + cmdutil.APIResourcesHint,
		},
		{
			name: "bare parent with few subcommands",
			args: []string{"config"},
			want: []string{"qctl config needs a subcommand: ", "list-contexts", "use-context"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runRoot(t, tc.args...)
			if err == nil {
				t.Fatalf("qctl %s exited 0", strings.Join(tc.args, " "))
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
			if out != "" {
				t.Errorf("wrote to stdout:\n%s", out)
			}
			msg := err.Error()
			if tc.exact != "" && msg != tc.exact {
				t.Errorf("message =\n%q\nwant\n%q", msg, tc.exact)
			}
			for _, w := range tc.want {
				if !strings.Contains(msg, w) {
					t.Errorf("message does not carry %q:\n%s", w, msg)
				}
			}
			for _, u := range tc.unwanted {
				if strings.Contains(msg, u) {
					t.Errorf("message carries %q:\n%s", u, msg)
				}
			}
			if n := strings.Count(msg, "Run '"); n > 1 {
				t.Errorf("message carries %d \"Run '\" trailers, want at most 1:\n%s", n, msg)
			}
			if strings.Contains(msg, "--help' for usage") {
				t.Errorf("message still carries the --help trailer:\n%s", msg)
			}
			if strings.HasPrefix(msg, "unknown command") && !strings.HasSuffix(msg, cmdutil.APIResourcesHint) {
				t.Errorf("unknown-command message does not end with the api-resources hint:\n%s", msg)
			}
		})
	}
}

func TestLocalCommandsRejectStrayArgs(t *testing.T) {
	for _, args := range [][]string{
		{"version", "foo"},
		{"info", "foo"},
		{"config", "current-context", "foo"},
		{"config", "list-contexts", "foo"},
		{"config", "get-contexts", "foo"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runRootWithoutContext(t, args...)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want %d; error: %v", code, qerrors.ExitBadUsage, err)
			}
		})
	}
}
