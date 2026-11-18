package stats

import (
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "550e8400-e29b-41d4-a716-446655440000"

func TestGetMenuStatsSendsDatasetIDAndDecodes(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotDatasetID string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/menu-stats", func(w http.ResponseWriter, r *http.Request) {
		gotDatasetID = r.URL.Query().Get("dataset_id")
		alertID := 4821
		msg := "new column `region` needs review"
		alertType := api.AlertType("needs_review_migration")
		testutil.RespondJSON(w, http.StatusOK, api.MenuStatsSchema{
			UnresolvedAlertsCount:         2,
			AlertCountThatUserCantResolve: 0,
			ErrorsCount:                   0,
			LatestAlertItemID:             &alertID,
			LatestAlertItemMsg:            &msg,
			LatestAlertItemType:           &alertType,
		})
	})

	client := NewClient(mock.URL(), testOrgID, 0)
	got, err := client.GetMenuStats("test-token", 42)
	if err != nil {
		t.Fatalf("GetMenuStats: %v", err)
	}
	if gotDatasetID != "42" {
		t.Errorf("dataset_id = %q, want %q", gotDatasetID, "42")
	}
	if got.LatestAlertItemID == nil || *got.LatestAlertItemID != 4821 {
		t.Errorf("LatestAlertItemID = %v, want 4821", got.LatestAlertItemID)
	}
	if got.UnresolvedAlertsCount != 2 {
		t.Errorf("UnresolvedAlertsCount = %d, want 2", got.UnresolvedAlertsCount)
	}
}

func TestGetMenuStatsPreservesExitCodeOn404(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/menu-stats", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusNotFound, map[string]string{"detail": "no such dataset"})
	})

	client := NewClient(mock.URL(), testOrgID, 0)
	_, err := client.GetMenuStats("test-token", 42)
	if err == nil {
		t.Fatal("want an error for 404, got nil")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (the Unwrap chain was broken)", code, qerrors.ExitNotFound)
	}
}
