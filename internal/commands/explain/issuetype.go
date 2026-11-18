package explain

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
)

// fieldDecoration is the offline text that `qctl explain` adds to an alert or
// warning field. The zero value adds nothing.
type fieldDecoration struct {
	// valueActions maps an issue_type value to the actions it offers.
	valueActions map[string][]string
	// issueTypes maps an issue_type value to its catalog entry, for its
	// business label and fix owner.
	issueTypes map[string]api.IssueTypeCatalog
	// actions maps an action to its catalog entry, printed as an ACTIONS
	// section.
	actions map[string]api.ActionCatalogEntry
}

// decorationFor returns the decoration for field of resource: issue_type gets
// each value's actions, business label and fix owner, and
// alert_actions_list / warning_actions_list get the action catalog.
func decorationFor(resource, field string) fieldDecoration {
	var offered map[string][]string
	var cat api.ActionCatalog
	var listField string
	switch resource {
	case "alert":
		offered, cat, listField = api.IssueTypeAlertActions, api.AlertActionCatalog, "alert_actions_list"
	case "warning":
		offered, cat, listField = api.IssueTypeWarningActions, api.WarningActionCatalog, "warning_actions_list"
	default:
		return fieldDecoration{}
	}
	switch field {
	case "issue_type":
		return fieldDecoration{valueActions: offered, issueTypes: cat.IssueTypes}
	case listField:
		return fieldDecoration{actions: cat.Actions}
	}
	return fieldDecoration{}
}

// printAllowedValue prints one ALLOWED VALUES line for v, then its business
// label and fix owner when d has them.
func (d fieldDecoration) printAllowedValue(w io.Writer, prefix, v string) {
	if names := d.valueActions[v]; len(names) > 0 {
		fmt.Fprintf(w, "%s  - %s → %s\n", prefix, v, strings.Join(names, ", "))
	} else {
		fmt.Fprintf(w, "%s  - %s\n", prefix, v)
	}
	it, ok := d.issueTypes[v]
	if !ok || it.BusinessLabel == "" && it.FixOwner == "" {
		return
	}
	var parts []string
	if it.BusinessLabel != "" {
		parts = append(parts, it.BusinessLabel)
	}
	if it.FixOwner != "" {
		parts = append(parts, "fix owner: "+it.FixOwner)
	}
	fmt.Fprintf(w, "%s      %s\n", prefix, strings.Join(parts, "; "))
}

// printActions prints d's action catalog as an ACTIONS section, one action
// per entry in name order, after a blank line when separate is set; nothing
// when d has no catalog.
func (d fieldDecoration) printActions(w io.Writer, separate bool) {
	if len(d.actions) == 0 {
		return
	}
	names := make([]string, 0, len(d.actions))
	for name := range d.actions {
		names = append(names, name)
	}
	sort.Strings(names)
	if separate {
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "ACTIONS:")
	for _, name := range names {
		fmt.Fprintf(w, "  %s\n", name)
		fmt.Fprint(w, alerts.CatalogNote(d.actions[name]))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "An issue type can change what an action does; 'qctl describe alert' and")
	fmt.Fprintln(w, "'qctl describe warning' print the entry for the issue at hand.")
}
