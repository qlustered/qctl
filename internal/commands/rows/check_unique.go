package rows

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// checkUniqueVerb is the name cobra registers, the name every refusal spells
// and the name the verdict line prints, declared once so those cannot drift —
// the precedent resetHoldVerb sets.
const checkUniqueVerb = "check-unique"

// errNotUnique is the exit-1 half of this verb's kubectl diff contract.
//
// SILENT, and that is the whole point of the sentinel: the verdict is already
// on stdout, so a message here would print the same answer twice and — worse —
// put it on stderr, where a script's error handler reads it as a failure. A
// real failure (a 504, a 5xx, a 4xx) still exits with its own typed error and
// its own message, so stderr stays the channel that means "this did not run",
// exactly as `qctl diff` splits the two with ErrDiffDifferences.
//
// A distinct sentinel rather than ErrDiffDifferences itself: they exit the same
// way and mean different things, and a test that asserted the diff sentinel
// here would pass if the two were ever wired together by accident.
var errNotUnique = qerrors.NewSilent(qerrors.ExitGenericError)

// checkUniqueSupport is `rows check-unique`'s row of the group's matrix, and it
// is the emptiest row in it: table scope, no selector, no narrowing.
//
// noSelectors rather than nil — the distinction checkScopeFlags reads — says
// "this scope is supported and no flag names rows within it": the COLUMNS are
// the whole question, and the route's body is `{columns}` and nothing else
// (atlas CheckColumnUniquenessRequestSchema, capi/capi/schemas/data_schema.py).
//
// cloud and file stay nil, so addScopeFlags registers neither
// --cloud-source/--cloud-source-id nor --file-id and cobra's own unknown-flag
// error refuses all three at exit 2 before any request. That is a fact about
// the route, not a gap: check_column_uniqueness_core takes the dataset and the
// body, resolves the clean table off the dataset, and has no
// data_source_model_id to narrow with (data_view_core.py).
//
// rowIDs, all and isBadData stay at their zero values because no flag reads
// them here. isBadData in particular means "not applicable": this route reads
// the CLEAN table by construction and has no is_bad_data parameter to choose
// with, which is also why nothing in this file calls scope.getDataLine — a
// follow-up listing of "the rows that broke the key" would need a filter per
// duplicate group, and the groups are the output.
var checkUniqueSupport = support{table: noSelectors}

func newCheckUniqueCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   checkUniqueVerb + " [table-id-or-name]",
		Short: "Check whether columns are a unique key over a table's clean rows",
		Long: `Answer whether a column, or a combination of columns, is a unique key over the
CLEAN rows of a table.

  qctl rows check-unique orders --column order_id
  qctl rows check-unique orders --column order_id --column region
  qctl rows check-unique 42 --column email -o json

This is the natural-key question: can these columns identify a row? Repeat
--column for a composite key — the columns are checked TOGETHER, as one key,
never one at a time.

CLEAN rows only, and deleted rows are excluded. The quarantined rows are not
looked at, so a key that is unique here can still be broken by a row that is
still in quarantine and by one that has not been ingested yet.

A NULL is a failure, not a skip. If any requested column is NULL in any row the
answer is "no" with that row count, and NO duplicate groups are computed — an
empty group list under that verdict says nothing about whether the remaining
values repeat. Fix the NULLs and ask again.

When values do repeat, the server returns at most five worst-offender groups,
largest first, printed as a table of the requested columns plus the number of
rows sharing each combination. It is a sample, never the full duplicate list.

Read-only: nothing is written, nothing is asked, and there is no row selector.

Exit codes (the kubectl diff contract):
  0  unique
  1  NOT unique — duplicates, or a NULL in a requested column
  2  bad usage, or a column this table does not have
  3  table not found
  4  unauthorized

Exit 1 for a verdict is silent: the reason is on stdout and stderr stays empty.
A real failure also exits 1 — the server's 30-second statement timeout answers
504, and any other 5xx exits 1 too — but always WITH a message on stderr, which
is how a script tells "these columns are not a key" from "the check did not
run".`,
		// The bespoke verdict renderer honors table, json and yaml and has no
		// name view: the answer is a yes/no plus counts, and there is no id or
		// name in the response to print. `-o name` is therefore exit 2 through
		// root's PersistentPreRunE rather than an empty line at exit 0, which a
		// script would read as "unique".
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheckUnique(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, checkUniqueSupport)
	cmd.Flags().StringArray("column", nil, "Column in the key; repeat for a composite key (required)")
	return cmd
}

// checkUniqueColumns is every refusal this verb decides from its own flags, and
// it stands where resolveSelector stands for the eleven verbs that have a
// selector: after the scope refusals, before Bootstrap. Every one is exit 2
// through qerrors.NewBadUsage and none costs a request.
//
// The three checks MIRROR the route's own pydantic validator — non-empty list,
// no blank name, no duplicate (atlas
// CheckColumnUniquenessRequestSchema.validate_columns,
// capi/capi/schemas/data_schema.py) — and that is the whole reason they exist. A
// pydantic field_validator failure is the object-shaped 422
// (`detail: {msg, error_code, context}`) that the generated client cannot decode
// into its list-shaped HTTPValidationError, so letting one of these reach the
// server spends a request to produce a worse message than the one here.
//
// Blank means whitespace-only, matching `not col.strip()` — but the value sent
// is the string the user typed, never a trimmed one, because the server appends
// the ORIGINAL (`cleaned.append(col)`) and then matches it against
// dataset.data_field_names. Trimming here would turn a 422 naming the column
// into a silently different question.
//
// Duplicates are exact-string, matching `len(set(cleaned)) != len(cleaned)`:
// `--column a --column A` is two columns to both sides, and Postgres would read
// the second as a different identifier only if it were quoted, which is not
// this route's business to guess.
func checkUniqueColumns(cmd *cobra.Command) ([]string, error) {
	if !cmd.Flags().Changed("column") {
		return nil, qerrors.NewBadUsagef(
			"'qctl rows %s' needs --column <name>: the columns are the whole question", checkUniqueVerb)
	}
	columns, _ := cmd.Flags().GetStringArray("column")
	if len(columns) == 0 {
		// Changed is true and the list is EMPTY, which pflag alone produces:
		// GetStringArray reconstructs the values by parsing the flag's String()
		// form as CSV, and a lone empty value renders as "[]" and comes back as
		// no values at all (a second --column makes it survive as ""). So this
		// is `--column ""` — the flag named nothing — and it has to be refused
		// here, or it reaches the wire as columns:[] and earns the object-shaped
		// 422 this function exists to pre-empt.
		return nil, qerrors.NewBadUsage(
			"--column cannot be empty: name the column, and repeat --column for a composite key")
	}
	seen := make(map[string]bool, len(columns))
	for _, c := range columns {
		if strings.TrimSpace(c) == "" {
			return nil, qerrors.NewBadUsage(
				"--column cannot be empty: name the column, and repeat --column for a composite key")
		}
		if seen[c] {
			return nil, qerrors.NewBadUsagef(
				"--column %s is repeated: a key names each column once", c)
		}
		seen[c] = true
	}
	return columns, nil
}

func runCheckUnique(cmd *cobra.Command, tableArg string) error {
	// The group's refusal order, with checkUniqueColumns standing where
	// resolveSelector stands for the verbs that have a selector: scope
	// refusals, then the verb's own flag refusals, then Bootstrap, then the
	// lookups that cost requests. See resolveTarget for why the order is the
	// contract.
	sc, f, err := resolveScopeOnly(cmd, tableArg, checkUniqueSupport, checkUniqueVerb)
	if err != nil {
		return err
	}
	columns, err := checkUniqueColumns(cmd)
	if err != nil {
		return err
	}
	ctx, sc, err := remoteScope(cmd, sc, tableArg, f)
	if err != nil {
		return err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	res, err := client.CheckColumnUniqueness(ctx.Credential.AccessToken, sc.tableID, columns)
	if err != nil {
		return err
	}
	if err := reportUniqueness(cmd, columns, res); err != nil {
		return err
	}
	if res.IsUnique {
		return nil
	}
	// AFTER the render, on every format: the exit code is the verdict's machine
	// half and the rendered answer is its human half, so a `-o json` consumer
	// gets both. Returning early would make the exit code the only output.
	return errNotUnique
}

// reportUniqueness writes the verdict. -o json and -o yaml pass the response
// through untouched; everything else gets the text view.
//
// Changed("output") rather than a bare read, the shape describe row uses: the
// flag's default is "table", and cmdutil.ApplyContextOutputFormat sets it from
// the current context when the user did not, so a configured `output: json`
// reaches this switch exactly as a typed -o json does.
func reportUniqueness(cmd *cobra.Command, columns []string, res *datasets.ColumnUniqueness) error {
	if cmd.Flags().Changed("output") {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case "json", "yaml":
			return output.WriteRaw(cmd.OutOrStdout(), format, res)
		}
	}
	return renderUniqueness(cmd, columns, res)
}

// renderUniqueness writes the two verdict lines, then whichever third thing the
// answer earns: the duplicate groups, or the line explaining why there are
// none.
//
// The REQUEST columns name the key here, not res.Columns, even though the route
// echoes them: `columns=body.columns` is the echo (data_view_core.py
// check_column_uniqueness_core) so the two cannot differ, and the request is the
// list that is always present — a response missing its echo would otherwise
// print "unique on : yes".
func renderUniqueness(cmd *cobra.Command, columns []string, res *datasets.ColumnUniqueness) error {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "unique on %s: %s\n", strings.Join(columns, ", "), uniquenessVerdict(res))
	fmt.Fprintf(out, "%d clean row(s) checked (deleted rows excluded); %d with a NULL in any of the columns\n",
		res.TotalRows, res.NullCount)

	// Branch on the DATA, not on reason: the three answers the handler can
	// produce are unique (no groups), contains_nulls (no groups, computed
	// nowhere) and has_duplicates (groups), so "there are groups" and "the
	// reason is has_duplicates" select the same responses — and if a future
	// server ever sent groups under some other reason, printing them is the
	// honest move and hiding them behind a reason test is not.
	if examples := res.DuplicateExamples; examples != nil && len(*examples) > 0 {
		fmt.Fprintln(out)
		if err := renderDuplicateGroups(cmd, columns, *examples); err != nil {
			return err
		}
		fmt.Fprintln(out, "The server returns at most five duplicate groups, largest first; there may be more.")
		if res.MaskedFields != nil && len(*res.MaskedFields) > 0 {
			fmt.Fprintf(out, "Masked fields: %s. Each value is •••••, or ••••• and its last 4 characters, "+
				"so two groups can print alike.\n", terminal.Sanitize(strings.Join(*res.MaskedFields, ", ")))
		}
		return nil
	}
	if reasonIs(res, api.ContainsNulls) {
		// The single most load-bearing fact about this route: the NULL check
		// short-circuits BEFORE the duplicate-group query runs, so an empty
		// group list under this verdict is "not computed", not "not found".
		// Without this line the two renderings of "no groups" are identical on
		// screen and mean opposite things.
		fmt.Fprintln(out,
			"Duplicate groups are not computed when a column contains NULLs, so this says nothing about whether the non-NULL values repeat.")
	}
	return nil
}

// uniquenessVerdict is the tail of line one.
//
// IsUnique is read first and alone, never inferred from Reason: both are always
// on the wire, and the route sends `reason: null` for the unique case
// (data_view_core.py check_column_uniqueness_core). A reason qctl does not
// recognise is printed verbatim rather than mapped to one of the two it knows —
// the honest default this group applies to every enum it renders (see
// scope.kindName).
func uniquenessVerdict(res *datasets.ColumnUniqueness) string {
	if res.IsUnique {
		return "yes"
	}
	switch {
	case res.Reason == nil:
		return "no (the server gave no reason)"
	case reasonIs(res, api.ContainsNulls):
		return "no (contains NULLs)"
	case reasonIs(res, api.HasDuplicates):
		return "no (has duplicates)"
	default:
		return fmt.Sprintf("no (%s)", string(*res.Reason))
	}
}

// reasonIs compares against the GENERATED constants rather than string
// literals, so a spec that renames or drops a member reaches this file as a
// compile error instead of a verdict line that quietly stops recognising the
// value it is rendering.
func reasonIs(res *datasets.ColumnUniqueness, want datasets.UniquenessReason) bool {
	return res.Reason != nil && *res.Reason == want
}

// renderDuplicateGroups prints the sample groups as a table: the requested
// columns in request order, then the number of rows sharing that combination.
//
// Rendered through tableui.Render like every other table qctl prints, which is
// also what makes --no-headers and --max-column-width work here for free.
// Render uppercases each header and turns `_` into `-`, so the column named
// order_id heads a column reading ORDER-ID; line one above prints the names
// exactly as typed, which is the spelling --column takes.
//
// Server ORDER is preserved — the groups arrive `ORDER BY dup_count DESC LIMIT
// 5` (_run_duplicate_examples_query, data_view_core.py) and sorting them here
// would throw away the only ranking there is.
func renderDuplicateGroups(cmd *cobra.Command, columns []string, examples []datasets.DuplicateExample) error {
	countCol := countColumn(columns)
	rows := make([]map[string]string, 0, len(examples))
	for _, ex := range examples {
		row := make(map[string]string, len(columns)+1)
		for _, c := range columns {
			row[c] = uniquenessCell(ex.Values[c])
		}
		row[countCol] = fmt.Sprintf("%d", ex.Count)
		rows = append(rows, row)
	}

	noHeaders, _ := cmd.Flags().GetBool("no-headers")
	return tableui.Render(cmd.OutOrStdout(), append(append([]string{}, columns...), countCol), rows, noHeaders, output.MaxColumnWidth(cmd))
}

// countColumn names the count column, and it is CHOSEN against the requested
// columns rather than fixed: tableui.Render reads each row as a map keyed by
// column name, so a table with a column actually called dup_count would have
// its values overwritten by the counts and nothing on screen would say so.
//
// The base name is the server's own SQL alias for it (grep `AS dup_count`).
//
// The fallback CHANGES THE WORD rather than appending a character. A trailing
// underscore collided visually with the thing it was distinguishing itself
// from -- `DUP-COUNT` beside `DUP-COUNT-` is close to the "nothing on screen
// would say so" this function exists to prevent -- so the first fallback is
// `duplicate_rows`, and only a table that has taken that name too falls back
// to appending.
func countColumn(columns []string) string {
	taken := make(map[string]bool, len(columns))
	for _, c := range columns {
		taken[c] = true
	}
	for _, name := range []string{"dup_count", "duplicate_rows"} {
		if !taken[name] {
			return name
		}
	}
	name := "duplicate_rows_"
	for taken[name] {
		name += "_"
	}
	return name
}

// uniquenessCell renders one value of a duplicate group.
//
// A value other than a string or nil prints with %v after jsonnum.Whole, so a
// whole float64 below 2^53 prints as an integer.
//
// nil is <null> rather than an empty cell, and it is not dead code even though
// the handler cannot normally produce it: the NULL count and the duplicate
// groups are two statements under READ COMMITTED, so a row inserted between
// them can carry a NULL into a group the first query certified NULL-free. An
// empty cell there would be indistinguishable from the empty string, which on
// this route is an ordinary value and a perfectly good duplicate.
func uniquenessCell(v interface{}) string {
	switch t := v.(type) {
	case nil:
		return "<null>"
	case string:
		return t
	default:
		return fmt.Sprintf("%v", jsonnum.Whole(t))
	}
}
