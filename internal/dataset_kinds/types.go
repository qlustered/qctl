package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/tags"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// Type aliases for generated types.
// The backend renamed "dataset kind" to "problem domain"; the CLI keeps the
// "table kind" UX, so local names stay while pointing at the new API types.
type (
	DatasetKindTiny          = api.ProblemDomainTinySchema
	DatasetKindsPage         = api.ProblemDomainsListSchema
	DatasetConceptFull       = api.ConceptSchemaFull
	DatasetKindWithConcepts  = api.ProblemDomainWithConceptsSchema
	DatasetKindImportRequest = api.ProblemDomainImportRequestSchema
	DatasetKindImportFormat  = api.ProblemDomainImportFormat
	PaginationSchema         = api.PaginationSchema
)

// DatasetKindDisplay is the display struct for list output.
type DatasetKindDisplay struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Tags      string `json:"tags"`
	UpdatedAt string `json:"updated_at"`
	ShortID   string `json:"short_id"`
	ID        string `json:"id"`
}

// ShortID returns the first 8 hex characters of a UUID, stripping dashes.
func ShortID(uuid string) string {
	hex := strings.ReplaceAll(uuid, "-", "")
	if len(hex) > 8 {
		return hex[:8]
	}
	return hex
}

// ToDisplayList converts a slice of DatasetKindTiny to display structs.
func ToDisplayList(kinds []DatasetKindTiny) []DatasetKindDisplay {
	result := make([]DatasetKindDisplay, len(kinds))
	for i, k := range kinds {
		result[i] = DatasetKindDisplay{
			Slug: k.Slug,
			Name: k.Name,
			Tags: tags.Build(
				tags.TagPair{Label: "Built-in", Active: k.IsBuiltin},
				tags.TagPair{Label: "Customized", Active: k.IsBuiltin && k.IsCustomized},
			),
			UpdatedAt: timeutil.FormatRelative(k.UpdatedAt),
			ShortID:   ShortID(k.ID.String()),
			ID:        k.ID.String(),
		}
	}
	return result
}
