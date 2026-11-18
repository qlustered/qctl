package stored_items

import (
	"time"

	"github.com/qlustered/qctl/internal/pkg/tags"
)

// StoredItemDisplay is the table view of a stored file.
type StoredItemDisplay struct {
	ID                             int       `json:"id"`
	FileName                       string    `json:"file_name"`
	DatasetName                    string    `json:"dataset_name"`
	DataSourceModelName            string    `json:"data_source_model_name"`
	BadRowsCount                   *int      `json:"bad_rows_count"`
	CleanRowsCount                 *int      `json:"clean_rows_count"`
	Tags                           string    `json:"tags"`
	CreatedAt                      time.Time `json:"created_at"`
	DeltaCursor                    *int      `json:"delta_cursor,omitempty"`
	Routing                        string    `json:"routing"`
	RoutingPending                 bool      `json:"routing_pending"`
	RoutingPeriodKey               *string   `json:"routing_period_key"`
	ContradictedExternalFixesCount *int      `json:"contradicted_external_fixes_count"`
}

// ToDisplayList converts file API results into table rows.
func ToDisplayList(items []StoredItemTiny) []StoredItemDisplay {
	result := make([]StoredItemDisplay, len(items))
	for i, item := range items {
		routingPending := item.RoutingPending != nil && *item.RoutingPending
		var routing string
		switch {
		case routingPending:
			routing = "pending"
		case item.RoutingPeriodKey != nil:
			routing = *item.RoutingPeriodKey
		}
		result[i] = StoredItemDisplay{
			ID:                  item.ID,
			FileName:            item.FileName,
			DatasetName:         item.DatasetName,
			DataSourceModelName: item.DataSourceModelName,
			BadRowsCount:        item.BadRowsCount,
			CleanRowsCount:      item.CleanRowsCount,
			Tags: tags.Build(
				tags.TagPair{Label: "Deleted", Active: item.IsTrashed != nil && *item.IsTrashed},
				tags.TagPair{Label: "Delta", Active: item.IsDelta != nil && *item.IsDelta},
			),
			CreatedAt:                      item.CreatedAt,
			DeltaCursor:                    item.DeltaCursor,
			Routing:                        routing,
			RoutingPending:                 routingPending,
			RoutingPeriodKey:               item.RoutingPeriodKey,
			ContradictedExternalFixesCount: item.ContradictedExternalFixesCount,
		}
	}
	return result
}
