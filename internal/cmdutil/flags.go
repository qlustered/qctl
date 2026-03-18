package cmdutil

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// ValidateConflictingFlags checks if multiple flags from a conflicting set are specified.
// Each set is a slice of flag names that cannot be used together.
// Returns an error if more than one flag from any set is changed.
//
// Example:
//
//	ValidateConflictingFlags(cmd,
//	    []string{"table-id", "table"},        // can't use both
//	    []string{"cloud-source-id", "cloud-source"}, // can't use both
//	)
func ValidateConflictingFlags(cmd *cobra.Command, flagSets ...[]string) error {
	for _, set := range flagSets {
		var changed []string
		for _, flag := range set {
			if cmd.Flags().Changed(flag) {
				changed = append(changed, "--"+flag)
			}
		}
		if len(changed) > 1 {
			return fmt.Errorf("cannot use both %s flags", strings.Join(changed, " and "))
		}
	}
	return nil
}

// RequireOneOfFlags checks that at least one flag from the required set is specified.
// Returns an error if none of the flags are changed.
//
// Example:
//
//	RequireOneOfFlags(cmd, []string{"table-id", "table"})
func RequireOneOfFlags(cmd *cobra.Command, flags []string) error {
	for _, flag := range flags {
		if cmd.Flags().Changed(flag) {
			return nil
		}
	}

	formatted := make([]string, len(flags))
	for i, f := range flags {
		formatted[i] = "--" + f
	}
	return fmt.Errorf("must specify one of: %s", strings.Join(formatted, " or "))
}

// ParseIntArgs parses command arguments as integers.
// Returns a descriptive error if any argument is not a valid integer.
func ParseIntArgs(args []string, itemName string) ([]int, error) {
	ids := make([]int, 0, len(args))
	for _, arg := range args {
		id, err := strconv.Atoi(arg)
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %s", itemName, arg)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
