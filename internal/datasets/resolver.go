package datasets

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ResolvedDataset holds the result of name/ID resolution.
type ResolvedDataset struct {
	ID   int
	Name string
}

// ResolveDataset resolves a user-provided name or ID to exactly one dataset.
func ResolveDataset(datasets []DatasetTiny, input string) (*ResolvedDataset, error) {
	if input == "" {
		return nil, qerrors.NewBadUsage("table identifier cannot be empty")
	}

	// 1. Integer → treat as ID
	if id, err := strconv.Atoi(input); err == nil {
		return &ResolvedDataset{ID: id}, nil
	}

	ds, err := resolveDatasetName(datasets, input, "")
	if err != nil {
		return nil, err
	}
	if ds != nil {
		return &ResolvedDataset{ID: ds.ID, Name: ds.Name}, nil
	}
	return nil, formatDatasetNoMatchError(input, datasets, "", "")
}

func resolveDatasetName(datasets []DatasetTiny, input, idFlag string) (*DatasetTiny, error) {
	var match *DatasetTiny
	for i := range datasets {
		if datasets[i].Name != input {
			continue
		}
		if match != nil {
			return nil, qerrors.NewBadUsagef("table name %q is ambiguous (ids %d and %d); %s instead",
				input, match.ID, datasets[i].ID, tableIDHint(idFlag))
		}
		match = &datasets[i]
	}
	return match, nil
}

func formatDatasetNoMatchError(input string, datasets []DatasetTiny, orgID, idFlag string) error {
	return formatProviderDatasetNoMatchError(input, datasets, orgID, idFlag, false)
}

func formatProviderDatasetNoMatchError(input string, datasets []DatasetTiny, orgID, idFlag string, thirdParty bool) error {
	var msg strings.Builder
	fmt.Fprintf(&msg, "no table named %q", input)
	if thirdParty {
		msg.WriteString(" among shared tables")
	}
	if orgID != "" {
		fmt.Fprintf(&msg, " in org %q", orgID)
	}
	for _, ds := range suggestDatasetNames(input, datasets) {
		fmt.Fprintf(&msg, "\nDid you mean %q (id %d)?", ds.Name, ds.ID)
	}
	msg.WriteString("\n\nNames are exact and case-sensitive. ")
	if !thirdParty {
		msg.WriteString("Run 'qctl get tables' to list visible tables,\n")
	}
	if thirdParty {
		fmt.Fprintf(&msg, "Use %s to address a table by ID.", tableIDHint(idFlag))
	} else {
		fmt.Fprintf(&msg, "or %s to address a table by ID.", tableIDHint(idFlag))
	}
	if idFlag != "" && strings.IndexFunc(input, func(r rune) bool { return r < '0' || r > '9' }) == -1 {
		fmt.Fprintf(&msg, "\nIf you meant table ID %s, use %s %s.", input, idFlag, input)
	}
	return qerrors.NewNotFound(apierror.WithScopeNote(msg.String()))
}

func tableIDHint(idFlag string) string {
	if idFlag == "" {
		return "use the table ID"
	}
	return "use " + idFlag + " <id>"
}

func suggestDatasetNames(input string, datasets []DatasetTiny) []DatasetTiny {
	var suggestions []DatasetTiny
	query := strings.ToLower(input)
	for _, ds := range datasets {
		if strings.EqualFold(ds.Name, input) || strings.Contains(strings.ToLower(ds.Name), query) {
			suggestions = append(suggestions, ds)
		}
	}
	sort.SliceStable(suggestions, func(i, j int) bool {
		a, b := suggestions[i], suggestions[j]
		if exactA, exactB := strings.EqualFold(a.Name, input), strings.EqualFold(b.Name, input); exactA != exactB {
			return exactA
		}
		return a.Name < b.Name
	})
	if len(suggestions) > 3 {
		suggestions = suggestions[:3]
	}
	return suggestions
}
