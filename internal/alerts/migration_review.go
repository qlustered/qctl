package alerts

import "github.com/qlustered/qctl/internal/api"

// migrationAlertLimit is the page size of FindMigrationReviewAlert's listing.
const migrationAlertLimit = 100

// FindMigrationReviewAlert returns the id of the unresolved
// needs_review_migration alert on datasetID whose migration_model_id is
// migrationID, and false when no alert on the listing's first page matches or
// the listing fails.
func (c *Client) FindMigrationReviewAlert(accessToken string, datasetID, migrationID int) (int, bool) {
	resolved := false
	issueType := api.AlertTypeNeedsReviewMigration
	page, err := c.GetAlerts(accessToken, GetAlertsParams{
		DatasetID: &datasetID,
		IssueType: &issueType,
		Resolved:  &resolved,
		Limit:     migrationAlertLimit,
	})
	if err != nil {
		return 0, false
	}
	for _, tiny := range page.Results {
		alert, err := c.GetAlert(accessToken, tiny.ID)
		if err != nil {
			continue
		}
		if alert.MigrationModelID != nil && *alert.MigrationModelID == migrationID {
			return alert.ID, true
		}
	}
	return 0, false
}
