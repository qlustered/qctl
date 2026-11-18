package delete

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// tableDeleteRefusal explains why `qctl delete table` does not exist, rather
// than letting it fall through to a bare "unknown command".
//
// The coverage decision: qctl's
// callers are mostly AI agents, DATASET_DELETE is held only by the ops and
// admin roles, and recovery is lossy -- the table returns but its cloud
// sources come back disabled, the alerts stay resolved and the killed jobs
// stay killed. So deletion stays with the human UI.
//
// This lives on the parent's RunE and not in a hidden `delete table` stub on
// purpose: catalog.PageNodes does not filter hidden commands
// (internal/catalog/signature.go), so a stub would be hashed into
// table.md's signature AND emitted into its generated USAGE block --
// advertising, on the resource page, the exact command being refused. Same
// mechanism, same reason, as `qctl create table`.
//
// It ends with cmdutil.APIResourcesHint for the same reason createRedirect
// does: a refusal that rejects what the user typed and then names nothing they
// could type instead is a dead end, and for an agent-first CLI
// `qctl api-resources` is the one command that lists the alternatives. Sharing
// the constant with cmdutil is what keeps this ending byte-identical to the
// one SubcommandRequired appends on the near-miss path a few lines below.
const tableDeleteRefusal = `Table deletion is deliberately not available in qctl. Deleting a table
resolves every unresolved alert, deletes every cloud source, and kills
every ingestion job and nonterminal profiling job; recovery leaves those jobs
killed, the cloud sources disabled and the alerts resolved. Delete tables
from the web UI, where a person confirms it.

  qctl undelete table <id-or-name>%s   # recover one that was deleted there

` + cmdutil.APIResourcesHint

// tableResource is the catalog name of the resource whose deletion is refused.
const tableResource = "table"

// namesTheTable reports whether arg is a way of saying "table". It resolves
// through the catalog rather than comparing against a hardcoded pair, so the
// alias set comes from internal/resources/pages/table.md and cannot drift, and
// so case is folded -- the same resolution `qctl create` does for its redirect
// (createRedirect, internal/commands/create/create.go), which this refusal's
// comment cites as the same mechanism.
//
// "The same mechanism" is about the RESOLUTION -- catalog.Lookup plus one
// fallback transform -- and not about what the two then do with the result.
// They differ on both halves of that, deliberately and for different reasons:
//
//   - the FALLBACK: this and createRedirect both retry once, by
//     singularising, while namesTheRows below retries both directions. See
//     its comment for why the set of transforms is per-helper.
//   - the FILTER: this accepts one resource by name (r.Name == tableResource),
//     because exactly one table is refused; createRedirect accepts a CLASS
//     (r.Class == catalog.ClassApply), because every apply-managed resource
//     gets the same redirect. Neither can be written as the other.
//
// The plural retry is what earns the catalog route here: `qctl get tables` is
// real, so `qctl delete tables` is the likely guess, and cobra's levenshtein
// suggests nothing for it (the nearest subcommand, `rules`, is distance 3 --
// past SuggestionsMinimumDistance), so an exact match would leave the likeliest
// spelling at the dead end this refusal exists to remove.
func namesTheTable(root *cobra.Command, arg string) bool {
	name := strings.ToLower(strings.TrimSpace(arg))
	r, ok := catalog.Lookup(root, name)
	if !ok {
		// Tolerate a plural guess (tables, datasets), like createRedirect.
		r, ok = catalog.Lookup(root, strings.TrimSuffix(name, "s"))
	}
	return ok && r.Name == tableResource
}

// rowsDeleteRefusal points `qctl delete rows` at the group that actually owns
// the row deletes, rather than letting it fall through.
//
// Unlike the table refusal above, the delete exists, it just lives under
// `qctl rows` as `rows delete-quarantined`: only quarantined rows can be
// deleted, and the verb names that population.
//
// The pointer is written out because cobra's SuggestionsFor cannot produce it.
// That function has THREE arms (cobra@v1.10.2, Command.SuggestionsFor): a
// levenshtein distance within SuggestionsMinimumDistance, a case-insensitive
// prefix match, and an explicit cmd.SuggestFor entry matched with
// strings.EqualFold. The third arm is inert in this repo -- nothing under
// internal/ or cmd/ sets SuggestFor on any command -- so only the first two
// can fire, and a delete child that ever sets it would invalidate the rest of
// this paragraph. From "rows" neither of the two fires: the nearest
// subcommands, `rule` and `rules`, are both distance 3, and no subcommand name
// starts with "rows". From "data" the PREFIX arm fires and produces the WRONG
// thing -- `data-review-policy` starts with "data", so someone who wanted to
// delete rows is pointed at deleting a review policy instead. That
// misdirection is why the canonical spelling is redirected here and not just
// the alias.
//
// Same no-hidden-stub reasoning as tableDeleteRefusal, and the same ending:
// catalog.PageNodes does not filter hidden commands, so a `delete rows` stub
// would be hashed into data.md's signature and emitted into its generated
// USAGE block, advertising the command this text exists to redirect.
const rowsDeleteRefusal = `Rows are deleted under 'qctl rows'. Only quarantined rows can be deleted:

  qctl rows delete-quarantined <table-id-or-name> --status failed_validation --reason <reason>%s

Clean rows cannot be deleted. Send them to quarantine first, then delete them
by signature:

  qctl rows send-to-quarantine <table-id-or-name> --row-id <id>%s
  qctl rows delete-quarantined <table-id-or-name> --signature <hex> --reason <reason>%s

  qctl rows --help                   # the full set of row verbs

` + cmdutil.APIResourcesHint

// dataResource is the catalog name of the resource whose deletes are redirected.
// Its page declares the alias `rows` (internal/resources/pages/data.md), which
// is what lets namesTheRows resolve both spellings without naming either.
const dataResource = "data"

// namesTheRows reports whether arg is a way of saying "the rows of a table". It
// resolves through the catalog for the same reason namesTheTable does: the
// alias set is owned by data.md and must not be re-declared here.
//
// It retries BOTH transforms, where namesTheTable retries only the singular.
// That is not symmetry for its own sake: this resource has one name of each
// number -- the canonical `data` and the declared alias `rows` -- so a guess
// can be wrong in either direction. `row` needs pluralising (TrimSuffix is a
// no-op on it) and `datas` needs singularising, and until it did both, `datas`
// fell all the way through to a bare "unknown command" with no suggestion at
// all, because nothing in the delete tree is within cobra's levenshtein
// distance of it either. The filter on r.Name is what keeps the wider retry
// honest: either transform can land on any resource, but only `data` is
// accepted.
//
// It does NOT match prefixes, so `qctl delete dat` still reaches cobra and is
// still answered with `data-review-policy` -- cobra's prefix arm, firing on a
// real sibling subcommand. That is left alone deliberately. The redirects here
// exist to remove DEAD ENDS, which is what `rows` and `datas` were: no
// suggestion, no pointer, nothing to try next. `dat` is a typo that already
// gets a suggestion, the api-resources pointer and exit 2, and the only way to
// outrank cobra on it would be to claim every prefix of `data` -- including
// ones that are equally prefixes of `data-review-policy`, which would swap one
// misdirection for another.
func namesTheRows(root *cobra.Command, arg string) bool {
	name := strings.ToLower(strings.TrimSpace(arg))
	for _, guess := range []string{name, name + "s", strings.TrimSuffix(name, "s")} {
		if r, ok := catalog.Lookup(root, guess); ok && r.Name == dataResource {
			return true
		}
	}
	return false
}

// deleteRefusal is the redirect for a guess, or "" when there is none.
//
// The order between the two checks does not matter, though catalog.Lookup
// being single-valued is not by itself the reason: the two helpers apply
// DIFFERENT fallback transforms, so one spelling could in principle reach
// `table` through one and `data` through the other. It cannot, because the two
// accepted sets are disjoint by enumeration. namesTheTable accepts
// {table, dataset} directly plus whatever singularises onto them, i.e.
// {table, dataset, tables, datasets}; namesTheRows accepts {data, rows}
// directly plus whatever pluralises OR singularises onto them, i.e.
// {data, rows, row, datas, rowss}. Up to the case and space folds both apply
// first, those four and those five share no member.
//
// It is split out of runDeleteSubcommand because the RunE path is not the only
// one that needs it: `qctl delete rows --help` never reaches RunE at all. See
// helpForDelete.
func deleteRefusal(root *cobra.Command, args []string) string {
	if len(args) == 0 {
		return ""
	}
	switch {
	case namesTheTable(root, args[0]):
		return fmt.Sprintf(tableDeleteRefusal, prompt.YesFlag(false))
	case namesTheRows(root, args[0]):
		return fmt.Sprintf(rowsDeleteRefusal, prompt.YesFlag(false), prompt.YesFlag(false), prompt.YesFlag(false))
	}
	return ""
}

// runDeleteSubcommand is cmdutil.SubcommandRequired with two redirects in
// front of it, both for guesses cobra's levenshtein answers badly or not at
// all. SubcommandRequired must stay last, so an ordinary typo still gets its
// near-miss suggestion.
func runDeleteSubcommand(cmd *cobra.Command, args []string) error {
	if refusal := deleteRefusal(cmd.Root(), args); refusal != "" {
		return qerrors.NewBadUsagef("unknown command %q for %q\n\n%s",
			args[0], cmd.CommandPath(), refusal)
	}
	return cmdutil.SubcommandRequired(cmd, args)
}

// helpForDelete answers `qctl delete rows --help` with the pointer at
// `qctl rows`, and `qctl delete table --help` with the refusal, instead of the
// delete group's own help.
//
// It exists because --help does not reach RunE. cobra's execute() returns
// flag.ErrHelp as soon as the help flag is set (command.go), BEFORE
// the Run hooks, and ExecuteC then calls HelpFunc -- so the two
// redirects above were reachable by `qctl delete rows` and invisible to
// `qctl delete rows --help`, which printed a help screen listing seven
// subcommands and not one word about where row deletes live. That is the
// likelier of the two spellings from an agent, which reaches for --help before
// it guesses.
//
// The positionals come from cmd.Flags().Args() and not from the args
// parameter, which is the WHOLE command line ("delete", "rows", "--help"):
// execute() has already run ParseFlags by this point, so Args() is the
// positional tail with the flags removed, which is what the RunE path sees.
//
// Exit 0 and stdout, as any --help is: the user asked how to delete rows and
// this answers that question. `qctl delete rows` without the flag is still a
// usage error at exit 2 -- there the user asked to DO something qctl will not
// do, which is a different question with a different answer.
//
// Only group itself is intercepted. Its subcommands inherit this HelpFunc, and
// their positionals are their own arguments: `qctl delete rule table --help`
// is help for `delete rule`. Every other call goes to cmdutil.InheritedHelp.
func helpForDelete(group *cobra.Command) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if cmd == group {
			if refusal := deleteRefusal(cmd.Root(), cmd.Flags().Args()); refusal != "" {
				fmt.Fprintln(cmd.OutOrStdout(), refusal)
				return
			}
		}
		cmdutil.InheritedHelp(group)(cmd, args)
	}
}

// NewCommand creates the delete command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "delete",
		Short:       "Delete resources",
		Long:        `Commands for deleting resources such as files, cloud sources, error incidents, rules, and table-level review policy overrides.`,
		Annotations: catalog.Parent(),
		RunE:        runDeleteSubcommand,
	}
	// Wrapping the inherited HelpFunc rather than writing one keeps whatever
	// help the rest of the CLI renders and only intercepts the two redirected
	// words.
	cmd.SetHelpFunc(helpForDelete(cmd))

	// Add subcommands
	cmd.AddCommand(NewFileCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewTableKindCommand())
	cmd.AddCommand(NewTableSeriesCommand())
	cmd.AddCommand(NewDeliverableCommand())
	cmd.AddCommand(NewDeliverableRevisionCommand())
	cmd.AddCommand(NewStandardFieldCommand())
	cmd.AddCommand(NewErrorIncidentCommand())
	cmd.AddCommand(NewRuleCommand())
	cmd.AddCommand(NewRulesCommand())
	cmd.AddCommand(NewRuleReviewPolicyCommand())
	cmd.AddCommand(NewDataReviewPolicyCommand())
	cmd.AddCommand(NewChangeReviewPolicyCommand())

	return cmd
}
