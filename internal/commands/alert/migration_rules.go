package alert

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/terminal"
)

// migrationRuleWarning reports rule effects of the alert's draft deletions,
// from migration, the read of the alert's table.
func migrationRuleWarning(ctx *cmdutil.CommandContext, alert *alerts.AlertFull, migration *alerts.MigrationRead) string {
	if alert.MigrationModelID == nil {
		return migrationRuleCheckUnavailable("the alert names no draft migration")
	}
	if migration.Err != nil {
		return migrationRuleCheckUnavailable(migration.Err.Error())
	}
	detail := migration.Detail
	if detail.DatasetID != alert.DatasetID || detail.MigrationID != *alert.MigrationModelID ||
		detail.State != api.MigrationStateDraft ||
		(detail.MigrationState != api.DraftOnly && detail.MigrationState != api.DraftWithCurrent) {
		return migrationRuleCheckUnavailable(fmt.Sprintf("the schema response does not describe draft migration %d on table %d; re-read with qctl describe table-schema %d",
			*alert.MigrationModelID, alert.DatasetID, alert.DatasetID))
	}
	return cmdutil.DeletedColumnRuleWarning(ctx, alert.DatasetID, datasets.DraftDeletedColumns(detail))
}

func migrationRuleCheckUnavailable(reason string) string {
	line, _, _ := strings.Cut(reason, "\n")
	return "Warning: could not check the draft migration's effects on table rules: " + terminal.Sanitize(line) + "\n\n"
}
