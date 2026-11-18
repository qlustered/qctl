package dataview

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
)

// ValidateSelfExclusion checks the alignment of sent_or_changed_by_me with results.
func ValidateSelfExclusion(schema *api.DataWithCellHistorySchema) error {
	if flags := schema.SentOrChangedByMe; flags != nil && len(*flags) != len(schema.Results) {
		return fmt.Errorf("data response has %d sent_or_changed_by_me entries for %d result rows",
			len(*flags), len(schema.Results))
	}
	return nil
}
