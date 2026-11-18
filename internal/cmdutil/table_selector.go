package cmdutil

import (
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// AddTableFlags registers the standard table selector: --table is a NAME,
// --table-id is a numeric ID. Never one flag carrying both.
//
// A table name can be a number, and datasets.ResolveID tries strconv.Atoi
// first (internal/datasets/client.go), so a table literally named "42" is
// unreachable through every id-or-name path in the CLI. One flag cannot carry
// both types without an ambiguity that resolves silently in the wrong
// direction — which is why this pair, not a smarter parser, is the fix.
//
// purpose is the leading clause of both usage strings ("Filter by table",
// "Table (dataset)"), so a command reads naturally in --help.
func AddTableFlags(cmd *cobra.Command, purpose string) {
	RefInt(cmd.Flags(), "table-id", "table", purpose+" ID")
	cmd.Flags().String("table", "", purpose+" name (exact, case-sensitive; quote names containing spaces)")
}

// tableSelector is the part of a table reference that can be settled from the
// command line alone. Resolving a name or positional reference is deliberately
// separate because those paths can require credentials and an API request.
type tableSelector struct {
	id         int
	name       string
	positional string
}

func (s tableSelector) given() bool {
	return s.id != 0 || s.name != "" || s.positional != ""
}

// ValidateOptionalTableSelector rejects malformed or contradictory table
// selectors without loading config, credentials, or organization context. An
// omitted selector is valid for commands where the table is only a filter or
// selects an optional table-level override.
func ValidateOptionalTableSelector(cmd *cobra.Command, positional string) error {
	_, err := optionalTableSelector(cmd, positional)
	return err
}

// ValidateTableSelector is ValidateOptionalTableSelector for commands that
// require a table and accept it as their own positional argument.
func ValidateTableSelector(cmd *cobra.Command, positional string) error {
	_, err := requiredTableSelector(cmd, positional, true)
	return err
}

// ValidateTableSelectorFromFlags is ValidateTableSelector for commands whose
// positional slot names another resource, so the table must arrive through
// --table or --table-id.
func ValidateTableSelectorFromFlags(cmd *cobra.Command) error {
	_, err := requiredTableSelector(cmd, "", false)
	return err
}

func optionalTableSelector(cmd *cobra.Command, positional string) (tableSelector, error) {
	tableID, haveID, err := PositiveIDFlag(cmd, "table-id")
	if err != nil {
		return tableSelector{}, err
	}
	tableName, _ := cmd.Flags().GetString("table")
	haveName := cmd.Flags().Changed("table")

	var given []string
	if positional != "" {
		given = append(given, "the positional argument")
	}
	if haveName {
		given = append(given, "--table")
	}
	if haveID {
		given = append(given, "--table-id")
	}

	if len(given) > 1 {
		// Bad usage, not a generic error: a script needs exit 2 to tell "you
		// typed conflicting flags" from a retryable server failure. See the
		// header comment on flags.go's validators for the same rule.
		return tableSelector{}, qerrors.NewBadUsagef("table specified more than once (%s): use exactly one",
			strings.Join(given, ", "))
	}
	if haveName && tableName == "" {
		return tableSelector{}, qerrors.NewBadUsage(
			"--table cannot be empty: name the table, or use --table-id <id>")
	}

	return tableSelector{id: tableID, name: tableName, positional: positional}, nil
}

func requiredTableSelector(cmd *cobra.Command, positional string, takesPositional bool) (tableSelector, error) {
	selector, err := optionalTableSelector(cmd, positional)
	if err != nil {
		return tableSelector{}, err
	}
	if selector.given() {
		return selector, nil
	}

	// Bad usage, not a generic error — see the note in optionalTableSelector.
	if takesPositional {
		return tableSelector{}, qerrors.NewBadUsage("table is required: pass it as the positional argument, " +
			"or with --table <name> or --table-id <id>")
	}
	return tableSelector{}, qerrors.NewBadUsage("table is required: pass --table <name> or --table-id <id>")
}

func (s tableSelector) resolve(ctx *CommandContext) (int, error) {
	if s.id != 0 {
		return s.id, nil
	}
	if s.name != "" {
		id, _, err := ResolveTable(ctx, 0, s.name)
		return id, err
	}
	dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	return dsClient.ResolveID(ctx.Credential.AccessToken, s.positional)
}

// OptionalTableID resolves the table a command was pointed at, for commands
// where the table is optional — a filter, or an org-level default when it is
// absent. Returns (0, false, nil) when nothing was given.
//
// positional is the command's own table argument, or "" for commands whose
// positional argument is something else entirely (a suggestion UUID, a rule
// name). Passing args[0] blindly would read those as table references.
//
// --table takes the name-only lookup path. Routing it through
// datasets.ResolveID would reinstate the exact ambiguity AddTableFlags exists
// to remove. The positional argument keeps id-first, name-second resolution:
// it is the convenience path, and the flags are the unambiguous one.
func OptionalTableID(ctx *CommandContext, cmd *cobra.Command, positional string) (int, bool, error) {
	selector, err := optionalTableSelector(cmd, positional)
	if err != nil {
		return 0, false, err
	}
	if !selector.given() {
		return 0, false, nil
	}
	id, err := selector.resolve(ctx)
	if err != nil {
		return 0, false, err
	}
	return id, true, nil
}

// ResolveTableID is OptionalTableID for commands that cannot run without a
// table, and that accept the table as their own positional argument.
func ResolveTableID(ctx *CommandContext, cmd *cobra.Command, positional string) (int, error) {
	return requireTableID(ctx, cmd, positional, true)
}

// TableLabel is the table as the command line named it: the positional, else
// --table, else tableID.
func TableLabel(cmd *cobra.Command, positional string, tableID int) string {
	if positional != "" {
		return positional
	}
	if name, _ := cmd.Flags().GetString("table"); name != "" {
		return name
	}
	return strconv.Itoa(tableID)
}

// ResolveTableIDFromFlags is ResolveTableID for commands whose positional slot
// holds something else entirely — a rule name, a suggestion UUID — so the
// table can only arrive through --table/--table-id.
//
// It exists for the error message, not the resolution: an empty positional is
// how "the user omitted the table argument" and "this command has no table
// argument" both reach OptionalTableID, and telling the second group to "pass
// it as the positional argument" sends them to bind the table into
// <name-or-id> or <suggestion-id>. That misfire is silent — a wrong lookup,
// not a usage error — which is worse than the missing table it replaced.
func ResolveTableIDFromFlags(ctx *CommandContext, cmd *cobra.Command) (int, error) {
	return requireTableID(ctx, cmd, "", false)
}

// requireTableID is the shared body of both: one resolution path, two
// messages. Only the guidance differs, so only the guidance is branched.
func requireTableID(ctx *CommandContext, cmd *cobra.Command, positional string, takesPositional bool) (int, error) {
	selector, err := requiredTableSelector(cmd, positional, takesPositional)
	if err != nil {
		return 0, err
	}
	return selector.resolve(ctx)
}
