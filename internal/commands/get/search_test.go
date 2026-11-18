package get

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

// searchFlagCmd builds a bare command carrying just --search, so the parser is
// exercised without a config file, a credential, or a server.
func searchFlagCmd(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "things"}
	cmd.Flags().String("search", "", "Search things")
	if err := cmd.Flags().Parse(args); err != nil {
		t.Fatalf("Parse(%v) error = %v", args, err)
	}
	return cmd
}

// The 3-character floor is the backend's: atlas capi/views/pagination.py
// get_search_filters drops a shorter search_query and answers with the
// unfiltered set. len() there is Python's, over a str -- code points, which is
// why the CJK rows below are not decoration: "日本" is 6 bytes and 2 characters,
// and a byte-length check would forward it to a server that ignores it.
func TestParseSearchFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "flag absent", args: nil, want: ""},
		{name: "explicit empty value", args: []string{"--search", ""}, want: ""},
		{name: "one character", args: []string{"--search", "a"}, wantErr: true},
		{name: "two characters", args: []string{"--search", "ab"}, wantErr: true},
		{name: "two spaces", args: []string{"--search", "  "}, wantErr: true},
		{name: "two multi-byte characters", args: []string{"--search", "日本"}, wantErr: true},
		{name: "three characters", args: []string{"--search", "abc"}, want: "abc"},
		{name: "three multi-byte characters", args: []string{"--search", "日本語"}, want: "日本語"},
		{name: "longer", args: []string{"--search", "email_check"}, want: "email_check"},
		{name: "not trimmed", args: []string{"--search", " a "}, want: " a "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSearchFlag(searchFlagCmd(t, tt.args...))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseSearchFlag(%v) error = nil, want an error", tt.args)
				}
				if got != "" {
					t.Errorf("parseSearchFlag(%v) = %q on error, want %q", tt.args, got, "")
				}
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", code, qerrors.ExitBadUsage, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseSearchFlag(%v) error = %v, want nil", tt.args, err)
			}
			if got != tt.want {
				t.Errorf("parseSearchFlag(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}

// The message is asserted whole, here and at every command call site: it is the
// only thing that tells a user their filter was refused rather than applied.
func TestParseSearchFlagMessage(t *testing.T) {
	_, err := parseSearchFlag(searchFlagCmd(t, "--search", "ab"))
	if err == nil {
		t.Fatal("parseSearchFlag(--search ab) error = nil, want an error")
	}
	want := `--search "ab" is too short (minimum 3 characters); the server ignores shorter queries ` +
		`and would return unfiltered results. Widen the search or drop --search.`
	if err.Error() != want {
		t.Errorf("error = %q,\nwant %q", err.Error(), want)
	}
}

// searchCommand is one `get` subcommand that offers --search, plus whether it
// also offers --table (which costs a name-resolution request when set).
type searchCommand struct {
	name     string
	hasTable bool
}

// searchCommands walks the real get tree rather than listing names, so a new
// list command with a --search flag is covered the day it is added -- the
// backend's floor is not per-command, and neither is this test.
func searchCommands(t *testing.T) []searchCommand {
	t.Helper()
	var out []searchCommand
	for _, sub := range NewCommand().Commands() {
		if sub.Flags().Lookup("search") == nil {
			continue
		}
		out = append(out, searchCommand{name: sub.Name(), hasTable: sub.Flags().Lookup("table") != nil})
	}
	// A walk that silently found nothing would pass every assertion below.
	if len(out) < 12 {
		t.Fatalf("found %d get commands with --search, want at least 12: %+v", len(out), out)
	}
	return out
}

// runGetSubcommand executes one get subcommand under a root carrying the global
// flags cmdutil.Bootstrap reads.
func runGetSubcommand(t *testing.T, args ...string) error {
	t.Helper()
	root := &cobra.Command{Use: "qctl"}
	root.PersistentFlags().String("server", "", "API server URL")
	root.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	root.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	root.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	root.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	root.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	root.PersistentFlags().StringP("org", "O", "", "Organization")
	root.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	root.AddCommand(NewCommand())

	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get"}, args...))
	return root.Execute()
}

// TestEverySearchCommandRejectsShortSearch is the anti-drift guard the shared
// parser exists for: twelve commands inlined this flag read, and a rule spelled
// out twelve times is a rule that eventually differs in one of them.
//
// Commands that take --table get one, so the datasets trap proves the rejection
// beats the name lookup; get table-rules additionally *requires* a table, and
// would otherwise fail with "table is required" before reaching the filters.
func TestEverySearchCommandRejectsShortSearch(t *testing.T) {
	want := `--search "ab" is too short (minimum 3 characters); the server ignores shorter queries ` +
		`and would return unfiltered results. Widen the search or drop --search.`

	for _, sc := range searchCommands(t) {
		t.Run(sc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			env.SetupCredential(endpointKey, testOrgID, "test-token")
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets",
				func(_ http.ResponseWriter, _ *http.Request) {
					t.Error("a short --search must fail before --table is resolved")
				})

			args := []string{sc.name, "--search", "ab"}
			if sc.hasTable {
				args = append(args, "--table", "orders")
			}

			err := runGetSubcommand(t, args...)
			if err == nil {
				t.Fatalf("get %s --search ab: error = nil, want the short-search error", sc.name)
			}
			if err.Error() != want {
				t.Errorf("get %s --search ab: error = %q,\nwant %q", sc.name, err.Error(), want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("get %s --search ab: exit code = %d, want %d (ExitBadUsage)",
					sc.name, got, qerrors.ExitBadUsage)
			}
		})
	}
}
