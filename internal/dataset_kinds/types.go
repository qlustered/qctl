package dataset_kinds

import (
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
)

// Type aliases for generated types
type (
	DatasetKindTiny             = api.DatasetKindTinySchema
	DatasetKindFull             = api.DatasetKindSchemaFull
	DatasetKindsPage            = api.DatasetKindsListSchema
	DatasetFieldKindTiny        = api.DatasetFieldKindTinySchema
	DatasetFieldKindFull        = api.DatasetFieldKindSchemaFull
	DatasetFieldKindsPage       = api.DatasetFieldKindsListSchema
	DatasetKindWithFieldKinds   = api.DatasetKindWithFieldKindsSchema
	DatasetKindImportRequest    = api.DatasetKindImportRequestSchema
	DatasetKindImportFormat     = api.DatasetKindImportFormat
	PaginationSchema            = api.PaginationSchema
)

// DatasetKindDisplay is the display struct for list output.
type DatasetKindDisplay struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	IsBuiltin bool   `json:"is_builtin"`
	UpdatedAt string `json:"updated_at"`
	ShortID   string `json:"short_id"`
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
			Slug:      k.Slug,
			Name:      k.Name,
			IsBuiltin: k.IsBuiltin,
			UpdatedAt: timeutil.FormatRelative(k.UpdatedAt),
			ShortID:   ShortID(k.ID.String()),
		}
	}
	return result
}
