package stored_items

import (
	"time"

	"github.com/qlustered/qctl/internal/pkg/tags"
)

// StoredItemDisplay wraps StoredItemTiny with a computed Tags field for table output.
type StoredItemDisplay struct {
	ID                  int       `json:"id"`
	FileName            string    `json:"file_name"`
	DatasetName         string    `json:"dataset_name"`
	DataSourceModelName string    `json:"data_source_model_name"`
	BadRowsCount        *int      `json:"bad_rows_count"`
	CleanRowsCount      *int      `json:"clean_rows_count"`
	Tags                string    `json:"tags"`
	CreatedAt           time.Time `json:"created_at"`
}

// ToDisplayList converts API results to display structs with computed Tags.
func ToDisplayList(items []StoredItemTiny) []StoredItemDisplay {
	result := make([]StoredItemDisplay, len(items))
	for i, item := range items {
		result[i] = StoredItemDisplay{
			ID:                  item.ID,
			FileName:            item.FileName,
			DatasetName:         item.DatasetName,
			DataSourceModelName: item.DataSourceModelName,
			BadRowsCount:        item.BadRowsCount,
			CleanRowsCount:      item.CleanRowsCount,
			Tags: tags.Build(
				tags.TagPair{Label: "Deleted", Active: item.IgnoreFile != nil && *item.IgnoreFile},
			),
			CreatedAt: item.CreatedAt,
		}
	}
	return result
}
