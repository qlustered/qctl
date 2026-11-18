package ingestion

import (
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// JobStates returns every value of the IngestionJobState enum, sorted.
func JobStates() []string {
	states := api.EnumValues[api.IngestionJobState]()
	slices.Sort(states)
	return states
}

// ValidateJobState returns a bad-usage error when state is not an
// IngestionJobState value. label names the flag or filter key the value came
// from.
func ValidateJobState(label, state string) error {
	states := JobStates()
	if slices.Contains(states, state) {
		return nil
	}
	return qerrors.NewBadUsagef("invalid %s: %q (valid: %s)", label, state, strings.Join(states, ", "))
}
