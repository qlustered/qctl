package warnings

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// flagFields maps each request field a `qctl warning resolve` flag supplies to
// that flag.
var flagFields = map[string]string{
	"ids":                 "--row-id",
	"field_name_to_value": "--set",
	"reason":              "--reason",
}

// FlagFor returns the `qctl warning resolve` flag that supplies field, "" when
// no flag does.
func FlagFor(field string) string {
	return flagFields[field]
}

// FlagsCanSupply reports whether every field action requires has a flag and
// action has no one-of group, judged from api.WarningActionFields.
func FlagsCanSupply(action string) bool {
	fields := api.WarningActionFields[action]
	if len(fields.OneOf) > 0 {
		return false
	}
	for _, name := range fields.Required {
		if flagFields[name] == "" {
			return false
		}
	}
	return true
}

// FlagForm is the `qctl warning resolve` command line that runs action on
// warning id with flags, placeholders standing in for the values: its required
// flags bare and its optional ones in brackets.
func FlagForm(id int, action string) string {
	fields := api.WarningActionFields[action]
	var b strings.Builder
	fmt.Fprintf(&b, "qctl warning resolve %d --action %s", id, action)
	for _, name := range fields.Required {
		b.WriteString(" " + flagPlaceholder(name))
	}
	for _, name := range fields.Optional {
		if flag := flagFields[name]; flag != "" {
			b.WriteString(" [" + flagPlaceholder(name) + "]")
		}
	}
	return b.String()
}

// flagPlaceholder is field's flag followed by a placeholder for its value.
func flagPlaceholder(field string) string {
	switch field {
	case "ids":
		return "--row-id <id>"
	case "field_name_to_value":
		return "--set <column>=<value>"
	case "reason":
		return `--reason "<text>"`
	}
	return flagFields[field]
}

// CheckRuleOutputs refuses, with exit 2, a field_name_to_value key that is not
// one of w's rule_output_columns when action is overwrite_rule_outputs. It
// passes every other action, and a warning whose rule_output_columns is null.
func CheckRuleOutputs(w *WarningFull, action string, fieldNameToValue map[string]string) error {
	if action != string(api.OverwriteRuleOutputs) || w == nil || w.RuleOutputColumns == nil {
		return nil
	}
	allowed := *w.RuleOutputColumns
	var unknown []string
	for name := range fieldNameToValue {
		if !slices.Contains(allowed, name) {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	if len(allowed) == 0 {
		return qerrors.NewBadUsagef(
			"warning %d's rule writes no column, so overwrite_rule_outputs has nothing to write; %s refused",
			w.ID, quoteList(unknown))
	}
	return qerrors.NewBadUsagef(
		"%s not an output column of warning %d's rule; overwrite_rule_outputs writes only: %s",
		quoteList(unknown), w.ID, strings.Join(allowed, ", "))
}

// quoteList renders names quoted and comma-separated, followed by "is" or
// "are" to agree with how many there are.
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	verb := "is"
	if len(names) > 1 {
		verb = "are"
	}
	return strings.Join(quoted, ", ") + " " + verb
}
