package datasets

import (
	"fmt"
	"strconv"
)

// ResolvedDataset holds the result of name/ID resolution.
type ResolvedDataset struct {
	ID   int
	Name string
}

// ResolveDataset resolves a user-provided name or ID to exactly one dataset.
//
// Resolution order:
//  1. Integer → treat as dataset ID (fast path, no list API call needed)
//  2. Otherwise → exact name match against the provided list
func ResolveDataset(datasets []DatasetTiny, input string) (*ResolvedDataset, error) {
	if input == "" {
		return nil, fmt.Errorf("table identifier cannot be empty")
	}

	// 1. Integer → treat as ID
	if id, err := strconv.Atoi(input); err == nil {
		return &ResolvedDataset{ID: id}, nil
	}

	// 2. Exact name match (case-sensitive)
	if len(datasets) == 0 {
		return nil, fmt.Errorf("no tables found in the current organization")
	}

	for _, ds := range datasets {
		if ds.Name == input {
			return &ResolvedDataset{ID: ds.ID, Name: ds.Name}, nil
		}
	}

	return nil, formatDatasetNoMatchError(input, datasets)
}

// --- error formatters ---

func formatDatasetNoMatchError(input string, datasets []DatasetTiny) error {
	var msg string
	msg = fmt.Sprintf("no table found matching '%s'", input)

	if len(datasets) > 0 {
		msg += "\n\nAvailable tables:\n"
		limit := 10
		for i, ds := range datasets {
			if i >= limit {
				msg += fmt.Sprintf("  ... and %d more\n", len(datasets)-limit)
				break
			}
			msg += fmt.Sprintf("  - [%d] %s\n", ds.ID, ds.Name)
		}
	}

	msg += "\nHint: Use 'qctl get tables' to see all available tables."
	return fmt.Errorf("%s", msg)
}
