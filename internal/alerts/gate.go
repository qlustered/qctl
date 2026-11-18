package alerts

import (
	"fmt"
	"slices"
	"strings"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
)

// GateEntry is one pending rule suggestion as the pre-flight reports it.
type GateEntry struct {
	ID             string
	Name           string
	Action         string
	IsCarried      bool
	MissingColumns []string
	// IsCandidate marks a revalidation candidate. The generic accept endpoint
	// refuses one with 409 (_raise_if_revalidation_candidate, atlas
	// capi/capi/views/suggestion_view.py, called from _apply_accept), so a
	// candidate blocker is cleared by dismissing it, never by accepting.
	// Dismiss does clear one: _apply_dismiss branches into
	// dismiss_revalidation_candidate and the dismiss view, dismiss_suggestion,
	// gates only on state == pending.
	IsCandidate bool
}

// SuggestionGate describes pending suggestions affected by apply_migration.
type SuggestionGate struct {
	// AutoAccept is the pending `create`s the backend accepts as a side
	// effect of applying the migration.
	AutoAccept []GateEntry
	// AutoDismiss holds carried suggestions whose columns do not fit the draft.
	AutoDismiss []GateEntry
	// Unchecked holds carried suggestions whose fit could not be read.
	Unchecked []GateEntry
	// Blockers is the pending `update`/`delete`s that would fail the call
	// with 409 PENDING_SUGGESTIONS_BLOCK_MIGRATION.
	Blockers []GateEntry
	// Exempt is the validated revalidation candidates: pending `update`s the
	// request neither blocks on nor accepts. The migration job attempts promotion.
	Exempt []GateEntry
	// JobApply is the pending user, profiling and carried suggestions outside the request
	// gate that the migration job attempts to apply.
	JobApply []GateEntry
	// Accept and Dismiss are the draft's pending suggestions the request
	// itself decides (spec.accept_suggestion_ids, spec.dismiss_suggestion_ids).
	Accept  []GateEntry
	Dismiss []GateEntry
}

// WithDecisions moves explicitly decided suggestions into Accept and Dismiss.
func (g SuggestionGate) WithDecisions(accept, dismiss []openapi_types.UUID) SuggestionGate {
	if len(accept) == 0 && len(dismiss) == 0 {
		return g
	}
	named := func(ids []openapi_types.UUID, id string) bool {
		return slices.ContainsFunc(ids, func(u openapi_types.UUID) bool { return u.String() == id })
	}
	out := SuggestionGate{JobApply: g.JobApply}
	route := func(entries []GateEntry, keep *[]GateEntry) {
		for _, e := range entries {
			switch {
			case named(dismiss, e.ID):
				out.Dismiss = append(out.Dismiss, e)
			case named(accept, e.ID):
				out.Accept = append(out.Accept, e)
			default:
				*keep = append(*keep, e)
			}
		}
	}
	route(g.AutoAccept, &out.AutoAccept)
	route(g.Blockers, &out.Blockers)
	route(g.Exempt, &out.Exempt)
	route(g.AutoDismiss, &out.AutoDismiss)
	route(g.Unchecked, &out.Unchecked)
	return out
}

// BuildSuggestionGate partitions a table's suggestions by what the request and
// migration job do with them. A nil migrationModelID yields an
// empty gate: with no draft migration the backend 404s before it reaches the
// suggestion query at all (needs_review_migration, migration_handlers.py).
func BuildSuggestionGate(pending []api.DatasetRuleSuggestionSchema, migrationModelID *int) SuggestionGate {
	var gate SuggestionGate
	if migrationModelID == nil {
		return gate
	}
	for _, s := range pending {
		if s.State != api.SuggestionStatePending {
			continue
		}
		entry := GateEntry{
			ID:          s.ID.String(),
			Name:        s.InstanceName,
			Action:      string(s.Action),
			IsCandidate: rule_suggestions.IsCandidate(&s),
			IsCarried:   s.Source == api.RecreatedPeriod,
		}
		onMigration := s.MigrationModelID != nil && *s.MigrationModelID == *migrationModelID
		if !onMigration {
			if (s.Source == api.User || s.Source == api.Profiling || s.Source == api.RecreatedPeriod) && !entry.IsCandidate {
				gate.JobApply = append(gate.JobApply, entry)
			}
			continue
		}
		switch s.Action {
		case api.SuggestionActionCreate:
			if entry.IsCarried {
				gate.Unchecked = append(gate.Unchecked, entry)
				continue
			}
			gate.AutoAccept = append(gate.AutoAccept, entry)
		case api.SuggestionActionUpdate, api.SuggestionActionDelete:
			if isValidatedRevalidationCandidate(s) {
				gate.Exempt = append(gate.Exempt, entry)
				continue
			}
			gate.Blockers = append(gate.Blockers, entry)
		}
	}
	return gate
}

// WithCarriedRules classifies carried suggestions using the draft's fit results.
func (g SuggestionGate) WithCarriedRules(rules []api.RecreatedPeriodRuleSchema) SuggestionGate {
	unchecked := g.Unchecked
	g.Unchecked = nil
	for _, entry := range unchecked {
		i := slices.IndexFunc(rules, func(r api.RecreatedPeriodRuleSchema) bool { return r.SuggestionID.String() == entry.ID })
		switch {
		case i < 0:
			g.Unchecked = append(g.Unchecked, entry)
		case rules[i].Fits:
			g.AutoAccept = append(g.AutoAccept, entry)
		default:
			entry.MissingColumns = rules[i].MissingColumns
			g.AutoDismiss = append(g.AutoDismiss, entry)
		}
	}
	return g
}

// CheckAccepts refuses an explicit accept of a carried suggestion that does not fit.
func (g SuggestionGate) CheckAccepts(ids []openapi_types.UUID) error {
	for _, entry := range g.AutoDismiss {
		if slices.ContainsFunc(ids, func(id openapi_types.UUID) bool { return id.String() == entry.ID }) {
			return qerrors.NewBadUsagef("carried rule suggestion %s (%s) cannot be accepted: the draft migration lacks columns %s; remove it from spec.accept_suggestion_ids (it will be dismissed when the migration is applied)",
				entry.ID, entry.Name, strings.Join(entry.MissingColumns, ", "))
		}
	}
	return nil
}

// isValidatedRevalidationCandidate ports _is_validated_revalidation_candidate
// (atlas capi/capi/controllers/alert_handlers/migration_handlers.py). All
// three fields are required: the flag marks the row a candidate, the job id is
// stamped when its dry run launches (rule_revalidation.py
// stage_revalidation_dry_run) and validated_at only when that run passes
// (complete_revalidation_dry_run).
//
// The predicate itself lives in internal/rule_suggestions so the gate and
// `describe rule-suggestion` cannot drift apart.
func isValidatedRevalidationCandidate(s api.DatasetRuleSuggestionSchema) bool {
	return rule_suggestions.IsValidatedCandidate(&s)
}

// Empty reports whether the gate has nothing to say.
func (g SuggestionGate) Empty() bool {
	return len(g.AutoAccept) == 0 && len(g.Blockers) == 0 && len(g.Exempt) == 0 && len(g.JobApply) == 0 &&
		len(g.Accept) == 0 && len(g.Dismiss) == 0 && len(g.AutoDismiss) == 0 && len(g.Unchecked) == 0
}

func (g SuggestionGate) hasCarriedRules() bool {
	for _, entries := range [][]GateEntry{g.AutoAccept, g.AutoDismiss, g.Unchecked, g.Accept, g.Dismiss} {
		if slices.ContainsFunc(entries, func(e GateEntry) bool { return e.IsCarried }) {
			return true
		}
	}
	return false
}

// RenderPreflight returns a sanitized preview of the pending suggestions.
func (g SuggestionGate) RenderPreflight(datasetID int) string {
	if g.Empty() {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pending rule suggestions on table %d:\n", datasetID)
	if len(g.Dismiss) > 0 {
		fmt.Fprintf(&b, "  %d will be dismissed by this request:\n", len(g.Dismiss))
		for _, e := range g.Dismiss {
			fmt.Fprintf(&b, "    - %s  %s (%s)\n", e.ID, e.Name, e.Action)
		}
	}
	if len(g.Accept) > 0 {
		fmt.Fprintf(&b, "  %d will be accepted by this request:\n", len(g.Accept))
		for _, e := range g.Accept {
			fmt.Fprintf(&b, "    - %s  %s (%s)\n", e.ID, e.Name, e.Action)
		}
	}
	if len(g.Blockers) > 0 {
		fmt.Fprintf(&b, "  %d block this migration until you decide them:\n", len(g.Blockers))
		b.WriteString(g.RenderBlockers(datasetID))
	}
	if len(g.AutoAccept) > 0 {
		fmt.Fprintf(&b, "  %d create suggestion(s) will be accepted when it is confirmed:\n", len(g.AutoAccept))
		for _, e := range g.AutoAccept {
			fmt.Fprintf(&b, "    - %s  %s", e.ID, e.Name)
			if e.IsCarried {
				b.WriteString(" (carried from recreated period)")
			}
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "  Decide any of them yourself first: qctl describe rule-suggestion <id> --table-id %d\n", datasetID)
	}
	g.writeCarriedPreflight(&b)

	if len(g.Exempt) > 0 {
		fmt.Fprintf(&b, "  %d validated revalidation candidate(s) do not block the request;\n", len(g.Exempt))
		b.WriteString("  the migration job will attempt to promote them:\n")
		for _, e := range g.Exempt {
			fmt.Fprintf(&b, "    - %s  %s (%s)\n", e.ID, e.Name, e.Action)
		}
		b.WriteString("  Promotion can fail or leave a candidate pending.\n")
	}
	if len(g.JobApply) > 0 {
		fmt.Fprintf(&b, "  The migration job will attempt to apply %d other pending suggestion(s):\n", len(g.JobApply))
		for _, e := range g.JobApply {
			fmt.Fprintf(&b, "    - %s  %s (%s)\n", e.ID, e.Name, e.Action)
		}
		b.WriteString("  A suggestion the job cannot apply may remain pending.\n")
	}
	return terminal.Sanitize(b.String())
}

func (g SuggestionGate) writeCarriedPreflight(b *strings.Builder) {
	if len(g.AutoDismiss) > 0 {
		fmt.Fprintf(b, "  %d carried suggestion(s) do not fit and will be dismissed when it is confirmed:\n", len(g.AutoDismiss))
		for _, e := range g.AutoDismiss {
			fmt.Fprintf(b, "    - %s  %s (missing columns: %s)\n", e.ID, e.Name, strings.Join(e.MissingColumns, ", "))
		}
	}
	if len(g.Unchecked) > 0 {
		fmt.Fprintf(b, "  Could not check the fit of %d carried suggestion(s):\n", len(g.Unchecked))
		for _, e := range g.Unchecked {
			fmt.Fprintf(b, "    - %s  %s\n", e.ID, e.Name)
		}
		b.WriteString("  The server accepts those that fit and dismisses those that do not.\n")
	}
	if len(g.Dismiss) > 0 && g.hasCarriedRules() {
		b.WriteString("  Carried suggestion fits are previewed before these dismissals; the server rechecks them without dismissed suggestions' outputs.\n")
	}
}

// RenderBlockers lists each blocking suggestion with the commands that clear
// it. A revalidation candidate is cleared by dismissing it only — accept is
// refused for one — so it gets different advice. This is the text the 409
// handler reuses.
//
// Unsanitized on purpose; see RenderPreflight. Neither of its two callers
// writes this string to a terminal without a pass of its own.
func (g SuggestionGate) RenderBlockers(datasetID int) string {
	var b strings.Builder
	for _, e := range g.Blockers {
		fmt.Fprintf(&b, "    - %s  %s (%s)\n", e.ID, e.Name, e.Action)
		if e.IsCandidate {
			fmt.Fprintf(&b, "        this is a revalidation candidate whose dry run has not passed;\n")
			fmt.Fprintf(&b, "        it is cleared by dismissing it, not by accepting it:\n")
			fmt.Fprintf(&b, "        discard it: qctl suggestion dismiss %s --table-id %d\n", e.ID, datasetID)
			continue
		}
		fmt.Fprintf(&b, "        apply it:   qctl suggestion accept %s --table-id %d\n", e.ID, datasetID)
		fmt.Fprintf(&b, "        discard it: qctl suggestion dismiss %s --table-id %d\n", e.ID, datasetID)
	}
	return b.String()
}
