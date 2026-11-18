package notifications

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDownloadURLMintsAPresignedGet(t *testing.T) {
	notifID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var gotPath string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications/"+notifID.String()+"/download-url",
		func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			testutil.RespondJSON(w, http.StatusOK, api.NotificationDownloadURLSchema{
				Filename:       "orders_export.csv",
				NotificationID: notifID,
				URL:            "https://example.invalid/presigned",
			})
		})

	c := NewClient(mock.URL(), testOrgID, 0)
	got, err := c.DownloadURL("token", notifID)
	if err != nil {
		t.Fatalf("DownloadURL: %v", err)
	}
	if got.Filename != "orders_export.csv" {
		t.Errorf("Filename = %q, want orders_export.csv", got.Filename)
	}
	if got.URL != "https://example.invalid/presigned" {
		t.Errorf("URL = %q", got.URL)
	}
	if gotPath == "" {
		t.Error("handler never fired; the id is not in the path")
	}
}

func TestDownloadURLNotFoundExitsThree(t *testing.T) {
	notifID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications/"+notifID.String()+"/download-url",
		func(w http.ResponseWriter, _ *http.Request) {
			// The 404 body is atlas's own sentence for a row that does not
			// exist, is not this user's, or is in another org. It is NOT the
			// "carries no downloadable file" sentence -- that one is a 409 and
			// exits 2, not 3.
			testutil.RespondJSON(w, http.StatusNotFound,
				map[string]string{"detail": "Notification not found"})
		})

	c := NewClient(mock.URL(), testOrgID, 0)
	_, err := c.DownloadURL("token", notifID)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("GetExitCode = %d, want %d (ExitNotFound)", got, qerrors.ExitNotFound)
	}
}

// The route's OTHER failure mode, and the reason the two must not be conflated:
// 409 is not a missing notification, it is a notification that exists and has
// no file to hand over (wrong notification_type, no file_download_key, no
// backup_settings_id, or a BackupSettings row that is gone). It rides the
// generic >= 400 arm of FromHTTPStatusWithCause and so exits 2, not 3.
//
// A missing OBJECT in the bucket is a third thing again and is NOT a 409:
// minting a presigned GET never touches the object.
func TestDownloadURLConflictExitsTwo(t *testing.T) {
	notifID := uuid.MustParse("11111111-2222-3333-4444-555555555555")

	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications/"+notifID.String()+"/download-url",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusConflict, map[string]string{
				"detail": "Notification " + notifID.String() + " carries no downloadable file.",
			})
		})

	c := NewClient(mock.URL(), testOrgID, 0)
	_, err := c.DownloadURL("token", notifID)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("GetExitCode = %d, want %d (ExitBadUsage); a 409 is not a missing notification", got, qerrors.ExitBadUsage)
	}
}
