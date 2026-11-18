package suggestion

import (
	"fmt"
	"io"

	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// maxPreviewPages bounds the preview walk. Reaching it means more pending
// suggestions match than any confirmation prompt could usefully show, and
// acting on a partial view is exactly what the exhaustive walk exists to
// prevent.
const maxPreviewPages = 50

const previewPageSize = 100

// previewFilterMatches lists what a --source/--action selection will write to.
//
// TWO asymmetries drive this function:
//
//  1. The list route has `state`, `source`, `page` and `limit` but NO `action`
//     parameter (api.ListSuggestionsParams; atlas list_suggestions,
//     capi/capi/views/suggestion_view.py), while the bulk routes DO
//     filter on action (_resolve_pending_suggestions, same file). So
//     source goes on the wire and action is applied here, to the rows that
//     came back.
//  2. Because of (1) the preview is only sound if every page is read. The
//     WRITE is correct either way — the server applies both filters itself —
//     but a preview assembled from a partial listing would under-report what
//     the user is being asked to confirm.
func previewFilterMatches(c *rule_suggestions.Client, token string, tableID int, sel selection) ([]api.DatasetRuleSuggestionTinySchema, error) {
	opts := rule_suggestions.ListOptions{
		States: []string{string(api.SuggestionStatePending)},
		Limit:  previewPageSize,
	}
	if sel.source != nil {
		opts.Source = string(*sel.source)
	}

	var matches []api.DatasetRuleSuggestionTinySchema
	for page := 1; ; page++ {
		opts.Page = page
		list, err := c.List(token, tableID, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list pending suggestions: %w", err)
		}
		for _, row := range list.Results {
			if sel.action != nil && row.Action != *sel.action {
				continue
			}
			matches = append(matches, row)
		}
		// Next is the same "there is another page" signal `get
		// rule-suggestions` reads to print its --page hint; atlas sets it to
		// page+1 while page < total_pages and to null on the last page
		// (suggestion_view.py list_suggestions), so this walk terminates.
		if list.Next == nil {
			return matches, nil
		}
		if page == maxPreviewPages {
			// Bad usage (exit 2), like selector.go's maxBulkIDs cap and
			// preflight.go's preflight — and for preflight's reason, not
			// maxBulkIDs': this refusal is not decidable from the command line
			// alone either, it costs one request per page to reach. What grades a
			// refusal here is whether a different command line is the only
			// fix, which it is; exit 1 would tell a script "server or
			// transient" and send a retry loop at a condition that cannot
			// clear.
			return nil, qerrors.NewBadUsagef(
				"listing this table's pending suggestions passed %d rows; qctl will not act on a "+
					"partial preview. Name the ids explicitly, or resolve some of them first. The cap "+
					"is on that listing rather than on what you selected: --source narrows it "+
					"server-side, but the list route has no action parameter, so --action is applied "+
					"to the rows that come back and adding it cannot shorten the walk",
				maxPreviewPages*previewPageSize)
		}
	}
}

// confirmQuestion keeps "all" out of the question when part of the matched set
// will be refused, because there "accept all 4?" would name a write the command
// has just said it cannot perform.
func confirmQuestion(verb string, willLand, matched int) string {
	if willLand == matched {
		return fmt.Sprintf("%s all %d?", verb, matched)
	}
	return fmt.Sprintf("%s the other %d?", verb, willLand)
}

// splitRevalidationCandidates separates a preview into what the verb will
// actually write and what the server will refuse.
//
// It exists because the --source/--action route runs NO pre-flight: the id
// route reads every id back and refuses the whole command (preflight.go), but
// here the set is the server's, so there is nothing to read back that the
// listing has not already returned. The listing HAS returned it —
// `is_revalidation_candidate` is on the compact row
// (api.DatasetRuleSuggestionTinySchema) — so the refusal is free to predict,
// and predicting it is the only way the prompt can be honest: accept's
// generic door 409s a candidate (_raise_if_revalidation_candidate,
// ../atlas capi/capi/views/suggestion_view.py), and on the bulk route that
// 409 lands in `errors` and exits 1 AFTER the user approved a write that named
// it. dismiss is the verb that retires a candidate, so nothing is refused there
// and the split is empty by construction.
//
// A nil pointer counts as NOT a candidate, matching the default on the schema
// this actually receives — `is_revalidation_candidate: bool = False` on
// DatasetRuleSuggestionTinySchema, ../atlas capi/capi/schemas/suggestion_schema.py
// (the detail schema, DatasetRuleSuggestionSchema, declares the same default,
// which is NOT the one on this path). The field is populated on every row
// today, and the page's GOTCHAS says so, but nothing verifies that per row —
// gotchassweep hashes the schema's SHAPE, not its population — so the default is
// load-bearing rather than decorative. Reading a missing optional as "candidate"
// would refuse writes the server would have accepted, which is the worse
// direction to be wrong in.
func splitRevalidationCandidates(
	matches []api.DatasetRuleSuggestionTinySchema, verb string,
) (writable, refused []api.DatasetRuleSuggestionTinySchema) {
	if verb != verbAccept {
		return matches, nil
	}
	for _, m := range matches {
		if m.IsRevalidationCandidate != nil && *m.IsRevalidationCandidate {
			refused = append(refused, m)
			continue
		}
		writable = append(writable, m)
	}
	return writable, refused
}

// approveFilterWrite is the gate between a filter preview and the write.
//
// It refuses outright when nothing the selection matched can be written —
// prompting there would ask the user to approve a write of zero — and
// otherwise hands the prompt the arithmetic. That refusal is exit 2 for the
// same reason as the preview cap above: only a different command line clears
// it, and exit 1 would tell a script "server or transient" and send a retry
// loop at a condition that cannot change on its own. The two command lines
// that DO clear it are dismiss and a rule patch; "resolve them first" is not
// one of them, because a candidate is resolved by its own dry run and there is
// no verb a caller can reach for.
//
// The listing goes to out, before the refusal, not instead of it. A
// selection that matched three rows and can write none still has to say WHICH
// three, or the user is told their command is impossible and given nothing to
// act on — and unlike the zero-match case (which exits 0 upstream, because
// selecting nothing is not a usage error) there is something here to show.
func approveFilterWrite(cmd *cobra.Command, out io.Writer, matches []api.DatasetRuleSuggestionTinySchema, verb string) error {
	writable, refused := splitRevalidationCandidates(matches, verb)
	if len(writable) == 0 {
		subject := fmt.Sprintf("all %d matching pending suggestions are revalidation candidates", len(refused))
		if len(refused) == 1 {
			subject = "the only matching pending suggestion is a revalidation candidate"
		}
		fmt.Fprintf(out, "%d pending suggestion%s matched, none of which can be %sed:\n",
			len(matches), plural(len(matches)), verb)
		listMatches(out, matches, refused)
		fmt.Fprintln(out)
		return qerrors.NewBadUsagef(
			"nothing here can be %sed: %s, and a candidate's outcome is decided by the validation "+
				"run its rule edit launched, not by this verb. Use 'qctl suggestion dismiss' to "+
				"retire them, or patch the rule again to replace them",
			verb, subject)
	}
	return confirmFilterWrite(cmd, out, matches, refused, verb)
}

// listMatches prints one line per matched row, marking the ones the verb will
// not be able to write. Shared by the confirmation and the refusal above so the
// two cannot drift into showing the same set differently.
func listMatches(w io.Writer, matches, refused []api.DatasetRuleSuggestionTinySchema) {
	isRefused := make(map[openapi_types.UUID]bool, len(refused))
	for _, m := range refused {
		isRefused[m.ID] = true
	}
	for _, m := range matches {
		mark := ""
		if isRefused[m.ID] {
			mark = "  (revalidation candidate — will be refused)"
		}
		// Every column but the UUID and our own mark is the server's text, and
		// this listing is what the user reads before authorising the write.
		fmt.Fprintf(w, "  %s  %-28s %-7s %s%s\n", m.ID,
			terminal.Sanitize(m.InstanceName), terminal.Sanitize(string(m.Action)),
			terminal.Sanitize(string(m.Source)), mark)
	}
}

// confirmFilterWrite shows what matched and asks. The set was chosen by the
// server rather than named by the caller, so this listing is the only place the
// user sees what they are authorising.
//
// `refused` is what splitRevalidationCandidates predicted the server will turn
// down; the rows are still listed, marked, and still sent, because the write is
// the server's own re-selection and qctl cannot subtract from it without
// switching to an id list and inheriting the race that would open. What changes
// is only the arithmetic the prompt states, which without this said "N will be
// accepted" about a number the command could not deliver.
//
// Only the listing belongs here; the prompting is cmdutil.Confirm's, which is
// what gives this site the repo's two settled exits — exit 2 when stdin cannot
// answer (only the command line can fix that, so the message names --yes) and
// exit 1 when the user declines, which is a choice rather than a usage error.
//
// That the exit-2 case arrives only after previewFilterMatches has already
// spent its requests is deliberate, and it is NOT free: a non-interactive run
// with no --yes pays the whole preview walk before being told to pass --yes.
// The early gate that would avoid it belongs in runResolve's routeBulkFilter
// arm, ahead of the previewFilterMatches call — by the time THIS function runs
// the walk is already paid for — and it is rejected because CanPrompt is the
// condition cmdutil.Confirm exists to own, and a copy of it at a call site is
// the clone that drifts when the shared version changes. Nothing is written on
// either arrangement, and the request cost is bounded by the same cap that
// bounds the preview itself.
func confirmFilterWrite(
	cmd *cobra.Command,
	out io.Writer,
	matches, refused []api.DatasetRuleSuggestionTinySchema,
	verb string,
) error {
	willLand := len(matches) - len(refused)
	if len(refused) == 0 {
		fmt.Fprintf(out, "%d pending suggestion%s will be %sed:\n", len(matches), plural(len(matches)), verb)
	} else {
		clause := fmt.Sprintf("%d are revalidation candidates the server will refuse", len(refused))
		if len(refused) == 1 {
			clause = "1 is a revalidation candidate the server will refuse"
		}
		fmt.Fprintf(out, "%d pending suggestion%s matched; %s, so %d will be %sed:\n",
			len(matches), plural(len(matches)), clause, willLand, verb)
	}
	listMatches(out, matches, refused)
	fmt.Fprintln(out)

	yes, _ := cmd.Flags().GetBool("yes")
	return cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		fmt.Sprintf(
			"confirmation required but stdin is not an interactive terminal — pass --yes to %s these %d suggestion%s",
			verb, willLand, plural(willLand)),
		confirmQuestion(verb, willLand, len(matches)))
}
