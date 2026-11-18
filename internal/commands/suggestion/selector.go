package suggestion

import (
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

// verbAccept and verbDismiss are the verb strings runResolve carries through
// the whole flow. Both are the subcommand's own name because most sites render
// one into user-visible prose: parseSelection quotes it in the usage and cap
// refusals below, preflight in "cannot be <verb>ed", approveFilterWrite in
// "nothing here can be <verb>ed", confirmFilterWrite in the prompt,
// renderBulkResult in the past tense it prints (result.go), and runResolve
// in its own status and error lines. At those sites a wrong spelling costs
// garbled prose and nothing else.
//
// DO NOT re-read that list as a complete inventory of the value's consumers —
// it was one when it was written and stopped being one in the same package.
// Grep `verbAccept` before relying on it.
//
// The two are NOT symmetric, and only verbAccept is load-bearing. TWO sites
// gate behaviour on the value, and both test for verbAccept specifically, so
// that ONE string opts INTO them and every other spelling — "dismiss",
// "Dismiss", "revert", "" alike — skips both identically. Nothing about the
// value "dismiss" tells either site anything, and a third verb inherits the
// skip by default: to be treated like accept it must be added to those
// conditions, not merely named here.
//
//  1. preflight's revalidation-candidate refusal (preflight.go), on the
//     multi-id route. An accept that passed any other spelling would skip the
//     one check whose whole job is to stop a write, and would then write. What
//     that costs is ATOMICITY and wording parity, not visibility: the candidate
//     still enters the bulk result set, where _apply_accept's 409 lands in
//     `errors` and prints as a `refused …` line at exit 1 — but the other ids in
//     the batch are committed on the way there, and the two routes stop wording
//     it the same way, which is the reason preflight (preflight.go) gives for
//     making the refusal there at all.
//  2. splitRevalidationCandidates (filter.go, testing `verb != verbAccept`), on the
//     --source/--action route, which has no pre-flight to fall back on. Any
//     other spelling makes
//     the split empty, so the confirmation reverts to promising "N will be
//     accepted" about a set the server half-refuses, and approveFilterWrite's
//     refusal of an all-candidate selection never fires. That is the exact
//     defect that function was added to fix.
//
// A misspelled verbDismiss can only garble a message.
const (
	verbAccept  = "accept"
	verbDismiss = "dismiss"
)

// maxBulkIDs caps the id list. This is QCTL's cap, not the server's: the
// backend declares `suggestion_ids: list[UUID] | None` with no bound. It exists
// because the pre-flight costs one request per id, so an unbounded argument
// list is an unbounded fan-out. Every message that reports it says whose cap it
// is, so nobody goes looking for it in the API docs.
const maxBulkIDs = 100

// routeKind is which of the three call shapes a command line resolved to.
type routeKind int

const (
	// routeSingle is exactly one id and no filter. It calls the SAME
	// single-item route the command has always called, so its stdout bytes and
	// its 404/409 handling are unchanged by construction rather than by test.
	routeSingle routeKind = iota
	routeBulkIDs
	routeBulkFilter
)

type selection struct {
	kind   routeKind
	ids    []string
	source *api.SuggestionSource
	action *api.SuggestionAction
}

func (s selection) bulkSelector() rule_suggestions.BulkSelector {
	return rule_suggestions.BulkSelector{IDs: s.ids, Source: s.source, Action: s.action}
}

// validSources and validActions are derived from the generated enums so a spec
// regen that adds a value does not leave a hand-typed list behind. They are the
// ONLY spelling of either vocabulary in this package: --help and the refusal
// message both render from here, in this order, so the two can never disagree.
// The order is the enum's own — for actions that is the lifecycle
// create/update/delete, which reads better than alphabetical.
var validSources = []string{string(api.Periodic), string(api.Profiling), string(api.RecreatedPeriod), string(api.User)}

var validActions = []string{
	string(api.SuggestionActionCreate),
	string(api.SuggestionActionUpdate),
	string(api.SuggestionActionDelete),
}

// addSelectorFlags registers the two filter flags. The positional ids need no
// registration; the verb declares cobra.ArbitraryArgs and parseSelection
// enforces the real rule, which cobra's Args validators cannot express
// ("at least one positional OR at least one of these flags").
func addSelectorFlags(cmd *cobra.Command) {
	cmd.Flags().String("source", "",
		"Select every pending suggestion from this source ("+strings.Join(validSources, "|")+")")
	cmd.Flags().String("action", "",
		"Select every pending suggestion proposing this change ("+strings.Join(validActions, "|")+")")
}

// parseSelection resolves the command line into one of the three routes, or
// refuses it. Every refusal here is decidable from the command line alone and
// exits 2, so a wrong invocation costs no request.
func parseSelection(cmd *cobra.Command, args []string, verb string) (selection, error) {
	sourceRaw, _ := cmd.Flags().GetString("source")
	actionRaw, _ := cmd.Flags().GetString("action")
	hasFilter := sourceRaw != "" || actionRaw != ""

	switch {
	case len(args) == 0 && !hasFilter:
		return selection{}, qerrors.NewBadUsagef(
			"name at least one suggestion id, or select a set with --source/--action\n"+
				"  qctl suggestion %s <id> [<id>...] --table-id <id>\n"+
				"  qctl suggestion %s --source profiling --table-id <id>%s", verb, verb, prompt.YesFlag(false))
	case len(args) > 0 && hasFilter:
		// The backend ANDs ids and filters, so this would mean "these ids, but
		// only the profiling ones" — which nobody types on purpose.
		return selection{}, qerrors.NewBadUsage(
			"cannot combine suggestion ids with --source/--action: " +
				"the server intersects them, so the filter can only narrow the ids you already named")
	}

	sel := selection{}
	if sourceRaw != "" {
		v, err := oneOf("--source", sourceRaw, validSources)
		if err != nil {
			return selection{}, err
		}
		s := api.SuggestionSource(v)
		sel.source = &s
	}
	if actionRaw != "" {
		v, err := oneOf("--action", actionRaw, validActions)
		if err != nil {
			return selection{}, err
		}
		a := api.SuggestionAction(v)
		sel.action = &a
	}

	if hasFilter {
		sel.kind = routeBulkFilter
		return sel, nil
	}

	if len(args) > maxBulkIDs {
		return selection{}, qerrors.NewBadUsagef(
			"qctl caps a bulk %s at %d suggestion ids (got %d); split the list. "+
				"The cap is qctl's own — it checks every id before writing, one request each — not a server limit",
			verb, maxBulkIDs, len(args))
	}

	ids, err := parseIDs(args)
	if err != nil {
		return selection{}, err
	}
	sel.ids = ids

	sel.kind = routeSingle
	if len(sel.ids) > 1 {
		sel.kind = routeBulkIDs
	}
	return sel, nil
}

// parseIDs validates every positional as a full UUID — on the single-id route
// too, which is what makes a malformed id exit 2 whichever route it took — and
// returns them de-duplicated, each in the spelling first given.
func parseIDs(args []string) ([]string, error) {
	ids := make([]string, 0, len(args))
	seen := make(map[string]bool, len(args))
	for _, raw := range args {
		u, err := uuid.Parse(raw)
		if err != nil {
			return nil, qerrors.NewBadUsagef(
				"invalid suggestion id %q: expected a full UUID, as printed by 'qctl get rule-suggestions'", raw)
		}
		// Keyed on the canonical form, not on `raw`: uuid.Parse also accepts
		// the braced, urn:uuid: and unhyphenated spellings, so two arguments can
		// name one suggestion. The server collapses a repeat either way
		// (`id.in_(...)`), which would make the post-call reconcile report a
		// phantom shortfall — and the pre-flight would read the same row twice.
		if seen[u.String()] {
			continue
		}
		seen[u.String()] = true
		ids = append(ids, raw)
	}
	return ids, nil
}

// oneOf validates a flag value against a fixed vocabulary, naming the whole
// vocabulary in the refusal — in the caller's declared order, which is the same
// order the flag's own --help prints, so a reader comparing the two sees one
// list rather than two orderings of one list.
func oneOf(flag, got string, valid []string) (string, error) {
	for _, v := range valid {
		if got == v {
			return got, nil
		}
	}
	return "", qerrors.NewBadUsagef("invalid %s %q: expected one of %s", flag, got, strings.Join(valid, ", "))
}
