package get

import (
	"slices"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// stateFlags reads the --state and --states flags, refusing any value not in
// valid. --states members are trimmed, and --states wins when both are set.
// It returns nil when neither flag is set.
func stateFlags(cmd *cobra.Command, valid []string) ([]string, error) {
	var states []string
	if state, _ := cmd.Flags().GetString("state"); state != "" {
		if err := validateStateValue("--state", state, valid); err != nil {
			return nil, err
		}
		states = []string{state}
	}
	if list, _ := cmd.Flags().GetString("states"); list != "" {
		states = strings.Split(list, ",")
		for i := range states {
			states[i] = strings.TrimSpace(states[i])
			if err := validateStateValue("--states", states[i], valid); err != nil {
				return nil, err
			}
		}
	}
	return states, nil
}

// validateStateValue returns a bad-usage error when state is not in valid.
// label names the flag the value came from.
func validateStateValue(label, state string, valid []string) error {
	if slices.Contains(valid, state) {
		return nil
	}
	return qerrors.NewBadUsagef("invalid %s: %q (valid: %s)", label, state, strings.Join(valid, ", "))
}
