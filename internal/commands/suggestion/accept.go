package suggestion

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

// actionVerb renders the accepted suggestion's effect on the rule.
func actionVerb(action api.SuggestionAction) string {
	switch action {
	case api.SuggestionActionCreate:
		return "created"
	case api.SuggestionActionDelete:
		return "deleted"
	default:
		return "updated"
	}
}

func newAcceptCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "accept <suggestion-id>... | --source <s> | --action <a>",
		Short: "Accept proposals to create, update or delete rules",
		Long: `Accept one or more rule proposals to create, update or delete rules.

The suggestion id is the full UUID printed by 'qctl get rule-suggestions'
or by the staged (202) output of 'qctl apply'.

Several ids may be given at once. Every id is checked before anything is
written: if any one of them is unknown, already resolved, or a revalidation
candidate, the whole command is refused and nothing is accepted.

Instead of ids you may select a set with --source and/or --action, which
accepts every PENDING suggestion matching. That form shows what it matched
and asks for confirmation; --yes skips the prompt.

-o json|yaml prints the accepted suggestion for one id (created_dataset_rule_id
names the rule a create made), or {processed, skipped, errors, missing} for
several ids or a selection, and moves the result lines to stderr.

The table the suggestion belongs to is required: pass it with
--table <name> or --table-id <id>.

Examples:
  qctl suggestion accept 550e8400-e29b-41d4-a716-446655440000 --table-id 42
  qctl suggestion accept 550e8400-... 6f1c2d3e-... --table orders
  qctl suggestion accept --source profiling --table orders --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-suggestion"), "table", "json", "yaml"),
		Args:        cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runResolve(cmd, args, resolveVerb{
				name: verbAccept,
				single: func(c *rule_suggestions.Client, token string, tableID int, id string) (*rule_suggestions.Suggestion, string, error) {
					result, err := c.Accept(token, tableID, id)
					if err != nil {
						return nil, "", fmt.Errorf("failed to accept suggestion: %w", err)
					}
					return result, fmt.Sprintf("suggestion %s accepted — tablerule %q %s\n",
						result.ID, result.InstanceName, actionVerb(result.Action)), nil
				},
				bulk: func(c *rule_suggestions.Client, token string, tableID int, sel rule_suggestions.BulkSelector) (*rule_suggestions.BulkResult, error) {
					return c.BulkAccept(token, tableID, sel)
				},
			})
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	addSelectorFlags(cmd)
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt shown when selecting by --source/--action")
	return cmd
}

// resolveVerb is the per-verb half of the shared accept/dismiss flow.
//
// single returns the suggestion the route answered with and the exact line the
// verb has always printed, so the one-id path stays byte-identical: it is the
// same call, and this is the same format string, not a reimplementation.
//
// name is read by every stage runResolve hands it to, so it is one of the verb
// constants in selector.go rather than a literal spelled here. selector.go's
// comment on those constants enumerates the consumers and says what a wrong
// spelling costs at each; this one deliberately does not repeat the list, since
// two copies of it drift. The short version: for accept it is not housekeeping,
// because preflight's revalidation-candidate refusal keys on verbAccept and on
// nothing else, so any other spelling would skip it and write.
type resolveVerb struct {
	name   string
	single func(c *rule_suggestions.Client, token string, tableID int, id string) (*rule_suggestions.Suggestion, string, error)
	bulk   func(c *rule_suggestions.Client, token string, tableID int, sel rule_suggestions.BulkSelector) (*rule_suggestions.BulkResult, error)
}

// runResolve is the whole flow for both accept and dismiss.
//
// The ORDER is parseSelection, local table-selector validation,
// cmdutil.Bootstrap, ResolveTableIDFromFlags, then the route switch. Both local
// stages are first because every refusal they make is decidable from the
// command line alone, so a malformed selection or table reference costs no
// request and never turns into an exit 1 from whatever config or credentials
// said.
//
// What runs between parseSelection and the route switch — cmdutil.Bootstrap and
// ResolveTableIDFromFlags among them — is covered by neither paragraph above:
// read those functions for their exit codes, and GetExitCode
// (internal/errors/errors.go) for how a code survives a wrap. Every attempt to
// inventory them in this comment has produced a list that was wrong or
// incomplete, so there is no list here.
//
// Exit 2 also grades refusals this flow REACHES only after asking the server —
// a fact about this arrangement, not about what is possible — and they are not
// rare: preflight's bad-id refusal (preflight.go, with one Get per id ahead
// of it, up to maxBulkIDs of them), every refusal the filter route makes once
// its preview walk is paid for (filter.go — read it rather than this line; an
// earlier version of this sentence named two of them and was overtaken by a
// third within one commit), and every 4xx the server itself returns other than
// 401/403/404/429 (FromHTTPStatusWithCause, internal/errors/errors.go).
// None of those is decidable from the command line alone and they exit 2
// regardless, because what grades a refusal here is whether a different command
// line is the only fix — not whether it was free to detect.
// internal/commands/rows/confirm.go's confirmAction spends a request ahead of
// the same kind of refusal for the same reason.
// NOTE ON SIZE: keep command-line parsing/validation in parseResolveSelection
// and filter approval in approveFilterWrite. Both are separate because adding
// either branch directly here pushes this route switch over the lint limit.
func runResolve(cmd *cobra.Command, args []string, v resolveVerb) error {
	sel, err := parseResolveSelection(cmd, args, v.name)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	// FromFlags: this command's arguments are suggestion UUIDs, not a table
	// reference.
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	token := ctx.Credential.AccessToken
	out := cmdutil.NewResultWriter(cmd)

	switch sel.kind {
	case routeSingle:
		result, line, err := v.single(client, token, tableID, sel.ids[0])
		if err != nil {
			return err
		}
		fmt.Fprint(out.Human, line)
		return out.Write(result)

	case routeBulkIDs:
		if err := preflight(client, token, tableID, sel.ids, v.name); err != nil {
			return err
		}
		res, err := v.bulk(client, token, tableID, sel.bulkSelector())
		if err != nil {
			return fmt.Errorf("failed to %s suggestions: %w", v.name, err)
		}
		// sel.ids, never nil here: renderBulkResult reads a nil id list as the
		// filter route and reports an empty result as "nothing matched" instead
		// of reconciling it against what we sent.
		return writeBulkResult(out, res, sel.ids, v.name)

	case routeBulkFilter:
		matches, err := previewFilterMatches(client, token, tableID, sel)
		if err != nil {
			return err
		}
		if len(matches) == 0 {
			// Deliberately NOT result.go renderBulkResult's "no pending
			// suggestions matched — nothing <verb>ed". That line is only
			// reachable AFTER a write, so it means the set the user approved
			// evaporated between the preview and the write; this one means the
			// listing came back empty and no write was ever attempted. Same exit
			// 0, different facts, so they are worded differently rather than
			// collapsed.
			fmt.Fprintf(out.Human,
				"nothing to %s: no pending suggestion matches that selection\n", v.name)
			return out.Write(newBulkOutcome(&rule_suggestions.BulkResult{}, nil))
		}
		// approveFilterWrite, not confirmFilterWrite: this route runs no
		// pre-flight — there is no caller id list to read back — so the
		// accounting for what the server will refuse happens there, beside the
		// prompt that has to state it.
		if err := approveFilterWrite(cmd, out.Human, matches, v.name); err != nil {
			return err
		}
		res, err := v.bulk(client, token, tableID, sel.bulkSelector())
		if err != nil {
			return fmt.Errorf("failed to %s suggestions: %w", v.name, err)
		}
		// nil sentIDs: the server chose this set, so there is no caller list to
		// reconcile against.
		return writeBulkResult(out, res, nil, v.name)

	default:
		// Unreachable: parseSelection returns exactly the three kinds above.
		// Falling through to `return nil` would be the worst answer a mutating
		// verb could give — exit 0, no output, nothing written — so a fourth
		// route kind that nobody wired here says so instead of passing silently.
		return qerrors.New(qerrors.ExitGenericError,
			fmt.Sprintf("internal error: unhandled suggestion selection route %d", sel.kind))
	}
}

// writeBulkResult prints a batch's outcome to out and returns renderBulkResult's
// exit decision; the document is written whatever that decision is.
func writeBulkResult(out cmdutil.ResultWriter, res *rule_suggestions.BulkResult, sentIDs []string, verb string) error {
	err := renderBulkResult(out.Human, res, sentIDs, verb)
	if writeErr := out.Write(newBulkOutcome(res, sentIDs)); writeErr != nil {
		return writeErr
	}
	return err
}

func parseResolveSelection(cmd *cobra.Command, args []string, verb string) (selection, error) {
	sel, err := parseSelection(cmd, args, verb)
	if err != nil {
		return selection{}, err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return selection{}, err
	}
	return sel, nil
}
