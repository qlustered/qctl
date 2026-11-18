package get

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestEmptyHintFormatsAndGates(t *testing.T) {
	for _, tc := range []struct {
		name         string
		args         []string
		total        int
		wantHint     bool
		wantSkip     bool
		wantBadUsage bool
	}{
		{name: "table", wantHint: true, wantSkip: true},
		{name: "json", args: []string{"-o", "json"}, wantHint: true},
		{name: "yaml", args: []string{"-o", "yaml"}, wantHint: true},
		{name: "name", args: []string{"-o", "name"}, wantHint: true},
		{name: "table past end", args: []string{"--page", "9"}, total: 1, wantSkip: true},
		{name: "json past end", args: []string{"-o", "json", "--page", "9"}, total: 1},
		{name: "invalid table columns", args: []string{"--columns", "nonexistent"}, wantBadUsage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			calls := 0
			ctx := &cmdutil.CommandContext{OrganizationID: "Acme"}
			root := filterTestRoot(func(c *cobra.Command) {
				skip, err := printEmptyResultWithHint(c, ctx, "things", []alerts.AlertTiny{}, &tc.total, func() string {
					calls++
					return "access hint\n"
				})
				if skip != tc.wantSkip {
					t.Errorf("skip = %v, want %v", skip, tc.wantSkip)
				}
				if tc.wantBadUsage {
					if qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
						t.Errorf("err = %v, want bad usage", err)
					}
				} else if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			})
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(append([]string{"things"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if (calls == 1) != tc.wantHint || strings.Contains(stderr.String(), "access hint") != tc.wantHint {
				t.Errorf("hint calls = %d, stderr = %q", calls, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("hint leaked to stdout: %q", stdout.String())
			}
		})
	}
}

// filterTestRoot builds a stand-in list command carrying one flag of every
// shape activeFilters has to deal with: root globals it must ignore,
// pagination it must ignore, and filters of each value type.
func filterTestRoot(run func(*cobra.Command)) *cobra.Command {
	root := &cobra.Command{Use: "qctl"}
	root.PersistentFlags().StringP("output", "o", "table", "output format")
	root.PersistentFlags().String("columns", "", "columns")
	root.PersistentFlags().StringP("org", "O", "", "organization")

	cmd := &cobra.Command{
		Use: "things",
		// Mirrors table_rules.go / rule_suggestions.go, the two real commands
		// that actually consume a positional -- activeFilters only echoes one
		// when cmd.Args is non-nil, so this stand-in has to declare it too.
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, _ []string) error {
			run(c)
			return nil
		},
	}
	cmd.Flags().Int("limit", 100, "Maximum number of things to return")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("order-by", "id", "Order by field")
	cmd.Flags().String("search", "", "Search things")
	cmd.Flags().Bool("deleted", false, "List deleted things only")
	cmd.Flags().Bool("show-kind", false, "Show the kind column")
	cmd.Flags().Int("table-id", 0, "Filter by table ID")
	cmd.Flags().StringArray("state", nil, "Filter by state (repeatable)")
	root.AddCommand(cmd)

	return root
}

func runFilters(t *testing.T, args ...string) []string {
	t.Helper()
	var got []string
	root := filterTestRoot(func(c *cobra.Command) { got = activeFilters(c) })
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"things"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return got
}

func TestActiveFiltersExcludesGlobalsAndPagination(t *testing.T) {
	got := runFilters(t, "--limit", "5", "--page", "2", "--order-by", "id",
		"-o", "table", "--org", "Acme", "--show-kind", "--search", "foo")
	want := []string{`--search "foo"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

func TestActiveFiltersEmptyWhenNothingSet(t *testing.T) {
	if got := runFilters(t); len(got) != 0 {
		t.Errorf("activeFilters = %v, want empty", got)
	}
}

func TestActiveFiltersRendersBoolAsBareFlag(t *testing.T) {
	got := runFilters(t, "--deleted")
	want := []string{"--deleted"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

// TestActiveFiltersRendersExplicitFalseBool: --deleted=false asks for the
// opposite of --deleted, so it must not print as --deleted.
func TestActiveFiltersRendersExplicitFalseBool(t *testing.T) {
	got := runFilters(t, "--deleted=false")
	want := []string{"--deleted=false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

// TestActiveFiltersExcludesTheWatchTimeout: --timeout bounds how long a watch
// looks, like --watch-interval; it narrows nothing.
func TestActiveFiltersExcludesTheWatchTimeout(t *testing.T) {
	var got []string
	root := filterTestRoot(func(c *cobra.Command) { got = activeFilters(c) })
	things, _, err := root.Find([]string{"things"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	cmdutil.AddWatchTimeoutFlag(things, 0)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"things", "--timeout", "5m", "--search", "foo"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if want := []string{`--search "foo"`}; !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

// TestActiveFiltersRendersValueTypes: strings are quoted, numbers are not,
// repeated flags collapse to the comma list the user could paste back, and the
// order is alphabetical by flag name whatever order they were typed in.
func TestActiveFiltersRendersValueTypes(t *testing.T) {
	got := runFilters(t, "--table-id", "5", "--state", "pending", "--state", "applied", "--search", "foo")
	want := []string{`--search "foo"`, `--state "pending,applied"`, "--table-id 5"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

func emptyResultStderr(t *testing.T, total *int, args ...string) string {
	t.Helper()
	var stderr bytes.Buffer
	ctx := &cmdutil.CommandContext{
		Config:           &config.Config{CurrentContext: "dev"},
		OrganizationID:   "00000000-0000-0000-0000-000000000000",
		OrganizationName: "Acme",
	}
	root := filterTestRoot(func(c *cobra.Command) {
		if _, err := printEmptyResult(c, ctx, "things", []alerts.AlertTiny{}, total); err != nil {
			t.Fatalf("printEmptyResult() error = %v", err)
		}
	})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&stderr)
	root.SetArgs(append([]string{"things"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return stderr.String()
}

// TestPrintEmptyResultNoFiltersIsUnchanged pins the message every existing
// caller's tests already assert on.
func TestPrintEmptyResultNoFiltersIsUnchanged(t *testing.T) {
	got := emptyResultStderr(t, nil, "--limit", "5")
	want := "No things found in org \"Acme\" (context: \"dev\").\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func TestPrintEmptyResultMultipleFilters(t *testing.T) {
	got := emptyResultStderr(t, nil, "--state", "pending", "--search", "foo")
	want := "No things matched --search \"foo\", --state \"pending\" in org \"Acme\" (context: \"dev\").\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

func intPtr(n int) *int { return &n }

// TestPrintEmptyResultPastLastPage: the server said 60 rows match, the page holds
// 50, and the user asked for page 99 -- pagination emptied this list, not a filter,
// and the message has to say which.
func TestPrintEmptyResultPastLastPage(t *testing.T) {
	got := emptyResultStderr(t, intPtr(60), "--page", "99", "--limit", "50")
	want := "No things on page 99 in org \"Acme\" (context: \"dev\").\n" +
		"The last page is 2 (60 things, 50 per page).\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultPastLastPageWithFilters: both narrowed the result, so both
// are named -- the filters explain why the range is short, the page explains why
// this request came back empty.
func TestPrintEmptyResultPastLastPageWithFilters(t *testing.T) {
	got := emptyResultStderr(t, intPtr(3), "--page", "99", "--limit", "50", "--search", "foo")
	want := "No things matched --search \"foo\" on page 99 in org \"Acme\" (context: \"dev\").\n" +
		"--search \"foo\" matches 3 things — the last page is 1.\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultPastLastPageMultipleFilters: the verb agrees with the number
// of filters, not the number of rows.
func TestPrintEmptyResultPastLastPageMultipleFilters(t *testing.T) {
	got := emptyResultStderr(t, intPtr(3), "--page", "9", "--limit", "50",
		"--search", "foo", "--state", "pending")
	want := "No things matched --search \"foo\", --state \"pending\" on page 9 in org \"Acme\" (context: \"dev\").\n" +
		"--search \"foo\", --state \"pending\" match 3 things — the last page is 1.\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultPastLastPageSingular: one row is "1 thing", not "1 things".
func TestPrintEmptyResultPastLastPageSingular(t *testing.T) {
	got := emptyResultStderr(t, intPtr(1), "--page", "4", "--limit", "50")
	want := "No things on page 4 in org \"Acme\" (context: \"dev\").\n" +
		"The last page is 1 (1 thing, 50 per page).\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultZeroTotalIsNotAPageProblem: total 0 means the filters
// emptied it, whatever page was asked for. --page must not be echoed as a filter.
func TestPrintEmptyResultZeroTotalIsNotAPageProblem(t *testing.T) {
	got := emptyResultStderr(t, intPtr(0), "--page", "99", "--limit", "50", "--search", "foo")
	want := "No things matched --search \"foo\" in org \"Acme\" (context: \"dev\").\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultNilTotalIsUnchanged: a caller with no count from the server
// gets exactly the Task 2 message.
func TestPrintEmptyResultNilTotalIsUnchanged(t *testing.T) {
	got := emptyResultStderr(t, nil, "--page", "99", "--limit", "50")
	want := "No things found in org \"Acme\" (context: \"dev\").\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultPageWithinRange: page 2 of 2 is a page that exists. Empty is
// then a state we cannot explain, so we say no more than we know.
func TestPrintEmptyResultPageWithinRange(t *testing.T) {
	got := emptyResultStderr(t, intPtr(60), "--page", "2", "--limit", "50")
	want := "No things found in org \"Acme\" (context: \"dev\").\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestPrintEmptyResultPastLastPageWithPositional: a positional scope narrows
// the request exactly like a flag does (TestActiveFiltersIncludesPositionals),
// so it must combine with the page branch the same way a filter flag does --
// named in both lines, with the verb agreeing with "one filter" (a single
// positional) rather than the row count.
func TestPrintEmptyResultPastLastPageWithPositional(t *testing.T) {
	got := emptyResultStderr(t, intPtr(3), "orders", "--page", "9", "--limit", "50")
	want := "No things matched \"orders\" on page 9 in org \"Acme\" (context: \"dev\").\n" +
		"\"orders\" matches 3 things — the last page is 1.\n"
	if got != want {
		t.Errorf("stderr = %q, want %q", got, want)
	}
}

// TestActiveFiltersIncludesPositionals: `get table-rules orders` scopes the whole
// request to one table, and a message that omits it claims something about the org.
func TestActiveFiltersIncludesPositionals(t *testing.T) {
	got := runFilters(t, "orders")
	want := []string{`"orders"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

// TestActiveFiltersPositionalsComeBeforeFlags: scope first, then the flags that
// narrowed within it.
func TestActiveFiltersPositionalsComeBeforeFlags(t *testing.T) {
	got := runFilters(t, "orders", "--search", "foo")
	want := []string{`"orders"`, `--search "foo"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("activeFilters = %v, want %v", got, want)
	}
}

// TestPrintEmptyResultIgnoresStrayPositionalWithoutArgs: fifteen of the
// seventeen `get` list commands never declare Args, so cobra's legacyArgs
// silently accepts and discards a stray positional (cobra@v1.10.1/args.go,
// "subcommands will always accept arbitrary arguments") -- the server never
// saw it. `qctl get rules field_required` (a plausible mistyping of --search)
// against an empty catalog must not print `matched "field_required"`: the
// token narrowed nothing, and claiming otherwise is the exact false message
// this branch exists to delete. Only a command with cmd.Args set (like
// table_rules.go / rule_suggestions.go) may echo a positional as a filter.
func TestPrintEmptyResultIgnoresStrayPositionalWithoutArgs(t *testing.T) {
	var stderr bytes.Buffer
	ctx := &cmdutil.CommandContext{
		Config:           &config.Config{CurrentContext: "dev"},
		OrganizationID:   "00000000-0000-0000-0000-000000000000",
		OrganizationName: "Acme",
	}
	root := &cobra.Command{Use: "qctl"}
	root.PersistentFlags().StringP("output", "o", "table", "output format")
	root.PersistentFlags().String("columns", "", "columns")
	cmd := &cobra.Command{
		Use: "things",
		// Args deliberately left nil: this is the fifteen-command case.
		RunE: func(c *cobra.Command, _ []string) error {
			if _, err := printEmptyResult(c, ctx, "things", []alerts.AlertTiny{}, nil); err != nil {
				t.Fatalf("printEmptyResult() error = %v", err)
			}
			return nil
		},
	}
	root.AddCommand(cmd)
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&stderr)
	root.SetArgs([]string{"things", "field_required"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	want := "No things found in org \"Acme\" (context: \"dev\").\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}
