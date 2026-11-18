package api

import "strings"

// ActionFields describes the request fields the backend expects alongside an
// alert or warning resolve action. It is the Go form of the spec's
// x-alert-action-fields / x-warning-action-fields extensions.
//
// Treat it as the backend describes it: an upper bound. A fetched alert's own
// spec.actions stays authoritative for what *this* alert offers *you* — the
// server filters that list by role and by runtime checks — which is what
// alerts.AllowedActions already relies on and must keep relying on.
type ActionFields struct {
	// Required fields the action cannot run without.
	Required []string
	// OneOf groups: at least one member of each group must be supplied.
	OneOf [][]string
	// Optional fields the action accepts but does not need.
	Optional []string
	// Note is the backend's own caveat, with its source citation.
	Note string
}

// NeedsInput reports whether the action needs a request field that
// `qctl alert resolve --action <name>` has no way to supply.
//
// Optional deliberately does not count: an action with only optional fields
// runs fine from a bare --action.
func (f ActionFields) NeedsInput() bool {
	return len(f.Required) > 0 || len(f.OneOf) > 0
}

// InputNames lists the fields that make NeedsInput true, for a message that
// tells the reader what is missing: every required field by name, plus each
// one-of group rendered as "a|b".
func (f ActionFields) InputNames() []string {
	names := make([]string, 0, len(f.Required)+len(f.OneOf))
	names = append(names, f.Required...)
	for _, group := range f.OneOf {
		names = append(names, strings.Join(group, "|"))
	}
	return names
}

// Accepts reports whether the action reads field at all — as a required field,
// a one-of member, or an optional one.
//
// The backend silently ignores a field an action does not read (nothing in
// AlertResolveSchema is per-action on the wire; it is a flat union), so this
// is what lets qctl refuse it client-side instead of sending a body half of
// which does nothing.
func (f ActionFields) Accepts(field string) bool {
	for _, name := range f.Required {
		if name == field {
			return true
		}
	}
	for _, group := range f.OneOf {
		for _, name := range group {
			if name == field {
				return true
			}
		}
	}
	for _, name := range f.Optional {
		if name == field {
			return true
		}
	}
	return false
}

// MissingRequired returns the Required fields absent from present, in the
// order the spec declares them.
func (f ActionFields) MissingRequired(present map[string]bool) []string {
	var missing []string
	for _, name := range f.Required {
		if !present[name] {
			missing = append(missing, name)
		}
	}
	return missing
}

// UnsatisfiedOneOf returns the OneOf groups with no member in present.
func (f ActionFields) UnsatisfiedOneOf(present map[string]bool) [][]string {
	var unsatisfied [][]string
	for _, group := range f.OneOf {
		satisfied := false
		for _, name := range group {
			if present[name] {
				satisfied = true
				break
			}
		}
		if !satisfied {
			unsatisfied = append(unsatisfied, group)
		}
	}
	return unsatisfied
}

// AllNames lists every field the action reads, annotated with why, for the
// "it reads: ..." half of a refusal. InputNames stays as it is — it answers a
// narrower question (what makes NeedsInput true) and has its own callers.
func (f ActionFields) AllNames() []string {
	names := make([]string, 0, len(f.Required)+len(f.OneOf)+len(f.Optional))
	for _, name := range f.Required {
		names = append(names, name+" (required)")
	}
	for _, group := range f.OneOf {
		names = append(names, strings.Join(group, "|")+" (one of)")
	}
	names = append(names, f.Optional...)
	return names
}

// ActionCatalog is the Go form of the spec's x-alert-action-catalog /
// x-warning-action-catalog extensions: what resolving an issue with each
// action does.
type ActionCatalog struct {
	// Actions holds each action's own entry.
	Actions map[string]ActionCatalogEntry
	// IssueTypes holds each issue type's catalog.
	IssueTypes map[string]IssueTypeCatalog
}

// IssueTypeCatalog is one issue type's entry in an ActionCatalog.
type IssueTypeCatalog struct {
	BusinessLabel         string
	BusinessSummary       string
	BusinessNamesByReason map[string]BusinessNames
	// FixOwner uses the FixOwner enum's values; "" when the backend sets none.
	FixOwner         string
	FixOwnerByReason map[string]string
	// UserResolvable reports whether a non-ops caller can resolve the issue
	// without an action, or is offered a held-file action.
	UserResolvable bool
	// Actions holds only the entries that differ from ActionCatalog.Actions
	// for the same action; use ActionCatalog.Entry.
	Actions map[string]ActionCatalogEntry
	// NoAction is what resolving without an action does; nil when the
	// resolve requires an action.
	NoAction *ActionCatalogEntry
	// FieldNamesResolution is what resolving with column mappings does; nil
	// for an issue type that takes none.
	FieldNamesResolution *ActionCatalogEntry
}

// BusinessNames is a label and summary for one reason of an issue type.
type BusinessNames struct {
	Label   string
	Summary string
}

// ActionCatalogEntry describes what resolving an issue with an action does.
type ActionCatalogEntry struct {
	Description string
	// Scope uses the values file, source, table, rows and none.
	Scope        []string
	ModifiesData bool
	// FollowOn names the work the resolve triggers beyond the issue.
	FollowOn   []string
	Reversible bool
	// Proposable is proposable, direct_only or client_side.
	Proposable   string
	SameEffectAs []ActionEffect
}

// ActionEffect is a separately governed operation key sharing an effect with
// an action.
type ActionEffect struct {
	Operation string
	Key       []string
}

// Entry returns the entry for resolving an issue of issueType with action,
// and false when the catalog has none.
func (c ActionCatalog) Entry(issueType, action string) (ActionCatalogEntry, bool) {
	if e, ok := c.IssueTypes[issueType].Actions[action]; ok {
		return e, true
	}
	if _, ok := c.IssueTypes[issueType]; !ok {
		return ActionCatalogEntry{}, false
	}
	e, ok := c.Actions[action]
	return e, ok
}
