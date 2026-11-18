package tags

import "strings"

// TagPair pairs a label with a boolean indicating whether it is active.
type TagPair struct {
	Label  string
	Active bool
}

// Build constructs a comma-separated tag string from label/bool pairs.
// Only includes labels where Active is true.
// Returns "-" if no tags are active.
func Build(pairs ...TagPair) string {
	var parts []string
	for _, p := range pairs {
		if p.Active {
			parts = append(parts, p.Label)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
