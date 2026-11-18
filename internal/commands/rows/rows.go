// Package rows implements the `qctl rows` command group — the verbs that act on
// a table's rows. Listings of those rows live under `qctl get data` and
// `qctl describe table`, and the one other read about them — the values the
// recommender proposes for a cell — is a listing too and lives under
// `qctl get value-recommendations`. `qctl guide edit-data` walks the loop the
// edit verbs and that read make together.
//
// Eleven of the twelve verbs write. The one that does not is `rows check-unique`,
// which asks whether some columns are a key over the clean rows, and it is here
// rather than under `qctl get` because it answers a question ABOUT the rows
// this group edits and answers it with a verdict and an exit code: `get` is
// where listings live, and a yes/no is not one.
//
// The group exists rather than twelve root verbs for the same reason `alert`,
// `warning`, `suggestion`, `review`, `comment-thread` and `notification` do:
// universal read verbs stay at root, resource-specific write verbs live under
// the noun. It is also what keeps the data plane affordable: `search-replace`,
// `set-cell` and `check-unique` landed on the eight lifecycle verbs' scope
// resolver, selector vocabulary and confirmation prompt rather than each
// minting its own, and whatever the sibling gap tickets still add lands here
// the same way instead of as single-child root verbs. (The export the old plan
// called `rows export` shipped as `qctl download data`; `rows dedupe` is
// deliberately not built, see qctl-ftf.)
//
// POPULATION IS IN THE VERB NAME, NEVER IN A DEFAULTED FLAG. Clean rows cannot
// be deleted at all, so the one delete verb is `delete-quarantined`. `hold`, `release`, `reset-hold`, `search-replace` and `set-cell`
// are unqualified because they are quarantine-only by construction: a clean row
// is not held, and the search-and-replace and modify-clean-cell routes both read
// the bad-rows table and have no clean-table form. `check-unique` is unqualified
// the other way round — its route reads the clean table and nothing else — so
// the population is still decided by the route, never by a flag.
package rows

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// NewCommand creates the `rows` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rows",
		Short: "Delete, requeue, hold, release, edit or un-flag a table's rows",
		Long: `Act on the rows of a table: the remediation half of the quarantine loop.

  qctl rows delete-quarantined orders --status failed_validation --reason "bad export"
  qctl rows release orders --row-id 1042 --row-id 1043
  qctl rows reprocess-quarantined orders --all
  qctl rows search-replace orders --search "N/A" --replace "" --dry-run
  qctl rows set-cell orders --row-id 1042 --field state --value CA
  qctl rows check-unique orders --column order_id --column region
  qctl rows ignore-rule orders --cloud-source-id 7 --rule email_check --row-id 1042

Each verb takes the table as a positional (or --table/--table-id) and exactly
one row selector. --row-id and --signature name rows; --status and --all name a
set.

Two verbs take no selector. 'search-replace' has none because its --search
term is what names the rows; it previews with the route's own dry run before it
writes. 'check-unique' has none either, writes nothing at all, and answers
through its exit code as well as its output: 0 unique, 1 not unique.

The two edit verbs and the read each address ONE table by construction.
'search-replace' and 'set-cell' write the QUARANTINED rows and have no
clean-table form, so the row ids 'set-cell' takes come from the --is-bad-data
listing; 'check-unique' reads the CLEAN rows and nothing else.

Only quarantined rows can be deleted; clean rows are immutable. To delete a
clean row, send it to quarantine first (it keeps its id), then delete it:
  qctl rows send-to-quarantine <table> --row-id 1042
  qctl rows delete-quarantined <table> --signature <hex> --reason <reason>

'delete-quarantined' takes SIGNATURES, not row ids: the route identifies rows by
signature, and an id can be turned into a signature only one row at a time.
Read them from the initial_signature_ column of the quarantine listing:
  qctl get data <table> --is-bad-data -o json
  qctl get data <table> --is-bad-data --filter-by-field-name id --filter-by-field-value 1042 -o json

'ignore-rule' and 'unignore-rule' address ONE cloud source: --cloud-source or
--cloud-source-id is required, because an ignore of a rule on a row belongs to
one source and there is no table-wide form.

A --signature delete also keeps each deleted line from loading again: it
writes an exclusion entry scoped to the line's source. --status and --all
exclude nothing, so those rows load again if the sender re-sends them.

Reads live under:
  qctl get data <table>                  # the rows, clean or --is-bad-data
  qctl describe table <table>            # N clean / M quarantined, rules, jobs
  qctl get value-recommendations <table> --field <field> --value <value>
  qctl get rule-ignores <table> --rule <rule>   # what ignore-rule suppressed

Walkthrough: qctl guide edit-data`,
		Annotations: catalog.Parent(),
		RunE:        runRowsSubcommand,
	}
	cmd.SetHelpFunc(helpForRows(cmd))
	cmd.SetFlagErrorFunc(flagErrorForRows(cmd))
	cmd.AddCommand(newDeleteQuarantinedCommand())
	cmd.AddCommand(newSendToQuarantineCommand())
	cmd.AddCommand(newReprocessCleanCommand())
	cmd.AddCommand(newReprocessQuarantinedCommand())
	cmd.AddCommand(newHoldCommand())
	cmd.AddCommand(newReleaseCommand())
	cmd.AddCommand(newResetHoldCommand())
	cmd.AddCommand(newSearchReplaceCommand())
	cmd.AddCommand(newSetCellCommand())
	cmd.AddCommand(newCheckUniqueCommand())
	cmd.AddCommand(newIgnoreRuleCommand())
	cmd.AddCommand(newUnignoreRuleCommand())
	return cmd
}

// firstArg is the optional table positional, shared by every verb in the group:
// all twelve take MaximumNArgs(1) because --table/--table-id name the table too,
// and an empty string is what tableNameFrom and cmdutil.ResolveTableID both
// read as "it came from a flag".
func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

// deleteCleanRefusal answers `qctl rows delete-clean`, a verb qctl no longer
// has: the server refuses every clean-table delete with CLEAN_ROWS_IMMUTABLE.
const deleteCleanRefusal = `Clean rows cannot be deleted; only quarantined rows can. Send the rows to
quarantine first (each keeps its id), then delete them by signature:

  qctl rows send-to-quarantine <table-id-or-name> --row-id <id>
  qctl get data <table-id-or-name> --is-bad-data --filter-by-field-name id --filter-by-field-value <id> -o json
  qctl rows delete-quarantined <table-id-or-name> --signature <hex> --reason <reason>`

// namesDeleteClean reports whether args, the group's positionals, start with
// the removed verb.
func namesDeleteClean(args []string) bool {
	return len(args) > 0 && args[0] == "delete-clean"
}

func deleteCleanError(cmd *cobra.Command) error {
	return qerrors.NewBadUsagef("unknown command %q for %q\n\n%s",
		"delete-clean", cmd.CommandPath(), deleteCleanRefusal)
}

// runRowsSubcommand is cmdutil.SubcommandRequired with the delete-clean
// redirect in front of it.
func runRowsSubcommand(cmd *cobra.Command, args []string) error {
	if namesDeleteClean(args) {
		return deleteCleanError(cmd)
	}
	return cmdutil.SubcommandRequired(cmd, args)
}

// flagErrorForRows gives `qctl rows delete-clean ... --signature <hex>` the
// redirect: the group has none of the removed verb's flags, so the parse fails
// before RunE. The positionals read before the failing flag are in
// cmd.Flags().Args(). Every other flag error goes to cmdutil.FlagError.
func flagErrorForRows(group *cobra.Command) func(*cobra.Command, error) error {
	return func(cmd *cobra.Command, err error) error {
		if cmd == group && namesDeleteClean(cmd.Flags().Args()) {
			return deleteCleanError(cmd)
		}
		return cmdutil.FlagError(cmd, err)
	}
}

// helpForRows answers `qctl rows delete-clean --help` with the redirect on
// stdout at exit 0. Every other call goes to cmdutil.InheritedHelp.
func helpForRows(group *cobra.Command) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if cmd == group && namesDeleteClean(cmd.Flags().Args()) {
			fmt.Fprintln(cmd.OutOrStdout(), deleteCleanRefusal)
			return
		}
		cmdutil.InheritedHelp(group)(cmd, args)
	}
}
