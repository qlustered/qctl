package resolveexplain

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/alerts"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestNotPendingAdviceMatchesOnlyItsMigrationRefusal(t *testing.T) {
	const message = "These rule suggestions are not pending on the draft migration: 22222222-2222-2222-2222-222222222222."
	for _, tc := range []struct {
		name, action, code, message string
		status                      int
	}{
		{"not pending", "apply_migration", "", message, http.StatusConflict},
		{"another action", "discard_migration", "", message, http.StatusConflict},
		{"another status", "apply_migration", "", message, http.StatusForbidden},
		{"coded conflict", "apply_migration", "ERR_MIGRATION_CONFLICT", message, http.StatusConflict},
		{"candidate acceptance", "apply_migration", "", "Revalidation candidates cannot be accepted.", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := &alerts.APIError{StatusCode: tc.status, Code: tc.code, Err: qerrors.NewBadUsage(tc.message)}
			err := explainResolveError(nil, cause, &alerts.AlertFull{ID: 9, DatasetID: 42}, tc.action)
			if got := strings.Contains(err.Error(), "Remove or replace"); got != (tc.name == "not pending") {
				t.Errorf("not-pending advice = %t: %v", got, err)
			}
			if !errors.Is(err, cause) || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("lost error cause or exit code: %v", err)
			}
		})
	}
}
