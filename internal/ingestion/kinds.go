package ingestion

import (
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// JobKinds returns every value of the IngestionJobKind enum, sorted.
func JobKinds() []string {
	kinds := api.EnumValues[api.IngestionJobKind]()
	slices.Sort(kinds)
	return kinds
}

// ParseJobKinds returns values as IngestionJobKind, without repeats, or a
// bad-usage error naming label for the first value that is not one.
func ParseJobKinds(label string, values []string) ([]IngestionJobKind, error) {
	kinds := JobKinds()
	out := make([]IngestionJobKind, 0, len(values))
	for _, v := range values {
		if !slices.Contains(kinds, v) {
			return nil, qerrors.NewBadUsagef("invalid %s: %q (valid: %s)", label, v, strings.Join(kinds, ", "))
		}
		if k := IngestionJobKind(v); !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out, nil
}

// unactionableKinds are the kinds RunnableKinds leaves out.
var unactionableKinds = []IngestionJobKind{
	api.IngestionJobKindQuarantineReprocess,
	api.IngestionJobKindCompositeCarrier,
}

// RunnableKinds returns every IngestionJobKind except quarantine_reprocess
// and composite_carrier, sorted.
func RunnableKinds() []IngestionJobKind {
	var out []IngestionJobKind
	for _, k := range JobKinds() {
		if !slices.Contains(unactionableKinds, IngestionJobKind(k)) {
			out = append(out, IngestionJobKind(k))
		}
	}
	return out
}
