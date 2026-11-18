package get

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"
)

// printContextBanner writes "Context: X | Org: Y" to stderr when format is
// table AND stdout is a TTY. No-op otherwise (piped output stays clean).
func printContextBanner(cmd *cobra.Command, ctx *cmdutil.CommandContext) {
	if !isTableFormat(cmd) || !stdoutIsTTY(cmd) {
		return
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "Context: %s | Org: %s\n\n",
		contextLabel(ctx), orgLabel(ctx))
}

// printEmptyResult is the single choke point for every `get` list command's
// empty-result path: validate --columns first, and only once that passes,
// write "No X found..." to stderr when format is table.
//
// Before this, every list command short-circuited straight to "No X found"
// on an empty result set, without ever constructing the printer that would
// have caught an unknown --columns name — the printer that does that
// checking (output.printTable / tableui.PrintFromCmd / pkg/printer.printTable)
// was simply never reached. "The list was empty so we could not check" is
// the same wrong answer with a better excuse (see output.AvailableColumns'
// doc comment) — so this validates first, in exactly one place, rather than
// trusting ~18 call sites to each remember to.
//
// sample must be the same value (or at least same type) the caller's normal,
// non-empty print path eventually hands its printer as `data` — typically a
// zero-length slice of the display type the printer receives, e.g.
// []orgs.OrgDisplay{}. AvailableColumns reflects the type, not the slice's
// contents, so an empty sample validates identically to a full one.
//
// The message names the filters that produced the empty set (see
// activeFilters), because "No rules found in org X" is a claim about the
// organization and was false every time a filter was what emptied the list.
// With no filters set the wording is unchanged, so the claim stays true and
// existing assertions keep passing.
//
// Returns (skip, err):
//   - err != nil: --columns named a column sample's type doesn't have.
//     Callers must return err as-is (wrapped with %w if wrapped at all) and
//     must not print "No X found" in this case.
//   - err == nil, skip == true: format is table and columns validated —
//     the message was written; the caller should return nil.
//   - err == nil, skip == false: not table format (json/yaml/name) — the
//     caller should fall through to its normal printer, which prints the
//     empty result in that format (e.g. `[]`).
//
// total is the server's count of rows matching this request's filters across
// ALL pages, or nil when the caller has no such count. It is what lets the
// message tell "your filters matched nothing" apart from "you paged past the
// end": the backend counts before it slices (atlas
// get_paginated_results_order_by, pagination.py), so a positive total on an
// empty page can only mean the page is past the end.
func printEmptyResult(cmd *cobra.Command, ctx *cmdutil.CommandContext, resource string, sample interface{}, total *int) (bool, error) {
	return printEmptyResultWithHint(cmd, ctx, resource, sample, total, nil)
}

// printEmptyResultWithHint writes an optional newline-terminated hint to stderr
// in every format. Table output validates columns first. A page past the end
// skips the hint. Non-table output returns false so the caller prints its data.
func printEmptyResultWithHint(cmd *cobra.Command, ctx *cmdutil.CommandContext, resource string, sample interface{}, total *int, hint func() string) (bool, error) {
	if !isTableFormat(cmd) {
		if _, _, _, pastEnd := pastLastPage(cmd, total); !pastEnd && hint != nil {
			fmt.Fprint(cmd.ErrOrStderr(), hint())
		}
		return false, nil
	}
	if err := validateColumnsFlag(cmd, sample); err != nil {
		return false, err
	}

	out := cmd.ErrOrStderr()
	filters := activeFilters(cmd)

	// A positive total on an empty page means the filters DID match rows and
	// the page is past the end. Pagination emptied this list, so say where
	// the end is -- and skip the hint, because the search was not the
	// problem.
	if page, last, size, ok := pastLastPage(cmd, total); ok {
		if len(filters) > 0 {
			joined := strings.Join(filters, ", ")
			verb := "matches"
			if len(filters) > 1 {
				verb = "match"
			}
			fmt.Fprintf(out, "No %s matched %s on page %d in org %q (context: %q).\n",
				resource, joined, page, orgLabel(ctx), contextLabel(ctx))
			fmt.Fprintf(out, "%s %s %s — the last page is %d.\n",
				joined, verb, countLabel(*total, resource), last)
		} else {
			fmt.Fprintf(out, "No %s on page %d in org %q (context: %q).\n",
				resource, page, orgLabel(ctx), contextLabel(ctx))
			fmt.Fprintf(out, "The last page is %d (%s, %d per page).\n",
				last, countLabel(*total, resource), size)
		}
		return true, nil
	}

	if len(filters) > 0 {
		fmt.Fprintf(out, "No %s matched %s in org %q (context: %q).\n",
			resource, strings.Join(filters, ", "), orgLabel(ctx), contextLabel(ctx))
	} else {
		fmt.Fprintf(out, "No %s found in org %q (context: %q).\n",
			resource, orgLabel(ctx), contextLabel(ctx))
	}
	if hint != nil {
		if extra := hint(); extra != "" {
			fmt.Fprint(out, extra)
		}
	}
	return true, nil
}

// pastLastPage reports whether the page the user asked for lies beyond the
// last one that holds rows, and where that last page is.
//
// It answers false whenever it cannot be certain, and every one of those
// cases is a real caller: no count from the server, a count of zero (the
// filters emptied the list, not the page), a command with no --page/--limit,
// page 1, or a page that does exist.
// Saying nothing is always better than guessing wrong about why a list is
// empty -- that is the bug this whole branch exists to fix.
//
// When it does answer true, the last-page math assumes --limit is the page
// size the server actually used to compute total: that holds today because
// atlas never substitutes a different limit than the one sent -- some
// endpoints (rule families, the rule-reviews inbox, rule suggestions)
// validate it and 422 on an out-of-range value, the rest accept any positive
// int unbounded through the shared pagination dispatcher (atlas
// pagination.py) -- but a future endpoint that clamped instead of rejecting
// or passing it through would make this function report a wrong last page,
// and no test in this suite would notice.
func pastLastPage(cmd *cobra.Command, total *int) (page, last, size int, ok bool) {
	if total == nil || *total <= 0 {
		return 0, 0, 0, false
	}
	if cmd.Flags().Lookup("page") == nil || cmd.Flags().Lookup("limit") == nil {
		return 0, 0, 0, false
	}
	page, err := cmd.Flags().GetInt("page")
	if err != nil || page <= 1 {
		return 0, 0, 0, false
	}
	size, err = cmd.Flags().GetInt("limit")
	if err != nil || size <= 0 {
		return 0, 0, 0, false
	}
	last = (*total + size - 1) / size
	if page <= last {
		return 0, 0, 0, false
	}
	return page, last, size, true
}

// countLabel renders "60 rules" or "1 rule". Resource names printEmptyResult
// is called with pluralize by a trailing "s" -- "rules", "tables", "files",
// "table kinds", "dry-run jobs" -- except "privilege audit entries", whose
// "ies" becomes "y". A future resource that pluralizes differently needs a
// case here.
func countLabel(n int, resource string) string {
	if n != 1 {
		return fmt.Sprintf("%d %s", n, resource)
	}
	if stem, ok := strings.CutSuffix(resource, "ies"); ok {
		return "1 " + stem + "y"
	}
	return "1 " + strings.TrimSuffix(resource, "s")
}

// validateColumnsFlag parses --columns the same way every print path does
// (split on comma, trim, drop empties) and validates the result against
// sample's display type, so a name valid for the real printer is valid here
// and vice versa.
func validateColumnsFlag(cmd *cobra.Command, sample interface{}) error {
	columnsStr, _ := cmd.Flags().GetString("columns")
	return output.ValidateColumns(output.ParseColumns(columnsStr), output.AvailableColumns(sample, nil))
}

// nonFilterFlags names every flag a list command can carry that does not
// narrow the result set: the root's globals, pagination and sorting, and the
// display, streaming or scope controls. Everything the user explicitly set and
// that is not named here is reported as a filter.
//
// A denylist rather than an allowlist, so a new filter flag is honest the day
// it is added and only a new *non*-filter flag needs an entry. Getting that
// wrong costs a slightly odd sentence on an empty result, never a wrong result.
var nonFilterFlags = map[string]bool{
	// root persistent flags -- internal/commands/root/root.go init
	"config": true, "server": true, "user": true, "output": true,
	"no-headers": true, "columns": true, "max-column-width": true,
	"allow-plaintext-secrets": true, "allow-insecure-http": true,
	"verbose": true, "request-timeout": true, "retries": true, "org": true,
	// pagination and sorting
	"limit": true, "page": true, "order-by": true, "reverse": true,
	// display, streaming and scope: --watch and friends change how long we
	// look, --context and --all change where we look, neither narrows a page
	// of results.
	"watch": true, "watch-interval": true, "interval": true, "timeout": true,
	"chunk-size": true, "context": true, "all": true,
	"show-params": true, "show-kind": true,
}

// activeFilters renders everything that narrowed this request: the
// positional scope the command was given, then the filter flags the user
// explicitly set, sorted by flag name so the sentence is stable across runs
// and across pflag versions.
//
// Positionals come first and render as a bare quoted value, because they are
// not flags: on `get table-rules orders` the table is the scope of the whole
// request, and leaving it out produced "No table rules found in org X" for a
// result that was never about the org.
//
// Only commands that declare Args consume a positional; the rest let
// cobra's legacyArgs silently discard it, and echoing a token the server
// never saw would make the same false claim this message exists to delete.
func activeFilters(cmd *cobra.Command) []string {
	var out []string
	if cmd.Args != nil {
		for _, arg := range cmd.Flags().Args() {
			if arg != "" {
				out = append(out, fmt.Sprintf("%q", arg))
			}
		}
	}

	var flags []*pflag.Flag
	cmd.Flags().Visit(func(f *pflag.Flag) {
		if !nonFilterFlags[f.Name] {
			flags = append(flags, f)
		}
	})
	sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })

	for _, f := range flags {
		out = append(out, renderFilterFlag(f))
	}
	return out
}

// renderFilterFlag prints a flag the way the user could type it back: a bare
// name for a true boolean, --flag=false for one explicitly turned off (which
// asks for the opposite, and must not read as the bare flag), the value quoted
// for a string, and a repeatable flag collapsed to one comma list.
func renderFilterFlag(f *pflag.Flag) string {
	switch f.Value.Type() {
	case "bool":
		if v, err := strconv.ParseBool(f.Value.String()); err == nil && v {
			return "--" + f.Name
		}
		return "--" + f.Name + "=false"
	case "string":
		return fmt.Sprintf("--%s %q", f.Name, f.Value.String())
	}
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		return fmt.Sprintf("--%s %q", f.Name, strings.Join(sv.GetSlice(), ","))
	}
	return fmt.Sprintf("--%s %s", f.Name, f.Value.String())
}

func isTableFormat(cmd *cobra.Command) bool {
	f, _ := cmd.Flags().GetString("output")
	return f == "" || f == "table" //nolint:goconst // Keep output format handling local to this command.
}

func stdoutIsTTY(cmd *cobra.Command) bool {
	f, ok := cmd.OutOrStdout().(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func contextLabel(ctx *cmdutil.CommandContext) string {
	if ctx.Config != nil && ctx.Config.CurrentContext != "" {
		return ctx.Config.CurrentContext
	}
	return "(none)"
}

func orgLabel(ctx *cmdutil.CommandContext) string {
	if ctx.OrganizationName != "" {
		return ctx.OrganizationName
	}
	return ctx.OrganizationID
}
