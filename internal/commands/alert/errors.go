package alert

import (
	"github.com/qlustered/qctl/internal/alerts"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// checkDependentAlert rejects the call when another alert must be resolved
// first.
//
// DependentAlert.Msg is deliberately not echoed: the backend builds that field
// purely as web-UI copy (AlertEnricher._enrich,
// capi/controllers/alert_enricher.py) — either a "<br>"-joined restatement of
// the blocking alert's own message, or a "**Step 1: Map column names**" pointer
// to a wizard step with no CLI equivalent. The blocking alert's real text
// belongs to describe alert.
func checkDependentAlert(alert *alerts.AlertFull) error {
	if alert.DependentAlert == nil {
		return nil
	}
	// Bad usage (exit 2), matching the APIError(400) the backend raises for the
	// identical condition — AlertControllerBase.check_alert_dependencies in
	// capi/capi/controllers/alert_controller_base.py, reached from
	// AlertController.__call__ in alert_controller.py on the resolve path. A script
	// must not see a different exit code depending on whether the pre-flight or the
	// server noticed first; same reasoning as the blocker gate in resolve.go's
	// confirmMigration.
	return qerrors.NewBadUsagef(
		"alert %d cannot be resolved until alert %d is resolved first:\n"+
			"  qctl describe alert %d",
		alert.ID, alert.DependentAlert.ID, alert.DependentAlert.ID,
	)
}
