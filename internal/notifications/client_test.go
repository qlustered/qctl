package notifications

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const testOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

var testNotificationUUID = uuid.MustParse("0190bbbb-1111-2222-3333-444444444444")

func sampleNotification() api.NotificationSchema {
	return api.NotificationSchema{
		ID:                testNotificationUUID,
		UserID:            uuid.MustParse("0190aaaa-1111-2222-3333-444444444444"),
		OrganizationID:    uuid.MustParse(testOrgID),
		NotificationType:  api.ReviewRequested,
		NotificationCount: 1,
		CreatedAt:         time.Now().Add(-2 * time.Hour),
		LastNotifiedAt:    time.Now().Add(-2 * time.Hour),
		Msg:               testutil.StringPtr("please review dedupe-emails@2.1.0"),
	}
}

// Both axes reach the wire as their own query parameter, and neither implies
// the other. A client that sent one for the other would answer a different
// question than the caller asked at exit 0.
func TestListSendsBothAxesIndependently(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{
				Results:   []api.NotificationSchema{sampleNotification()},
				Page:      testutil.IntPtr(1),
				TotalRows: testutil.IntPtr(1),
			})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.List("test-token", ListOptions{
		Types:            []Type{api.ReviewRequested, api.CommentMentioned},
		UnreadOnly:       true,
		IncludeDismissed: true,
		Page:             2,
		Limit:            50,
	}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if got := query["notification_type"]; len(got) != 2 || got[0] != "review_requested" || got[1] != "comment_mentioned" {
		t.Errorf("notification_type = %v, want [review_requested comment_mentioned]", got)
	}
	for param, want := range map[string]string{
		"unread_only": "true", "include_dismissed": "true", "page": "2", "limit": "50",
	} {
		if got := query.Get(param); got != want {
			t.Errorf("%s = %q, want %q", param, got, want)
		}
	}
}

// An unset axis must send nothing rather than an explicit false: the backend
// defaults both to false, and sending them anyway would make every request
// carry filters the user never typed.
func TestListOmitsUnsetFilters(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.NotificationListSchema{Results: nil})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if _, err := client.List("test-token", ListOptions{Page: 1, Limit: 20}); err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, param := range []string{"notification_type", "unread_only", "include_dismissed"} {
		if _, ok := query[param]; ok {
			t.Errorf("%s was sent for an unset filter: %q", param, query.Get(param))
		}
	}
}

func TestUnreadCountReturnsBothNumbers(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/notifications/unread-count",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.UnreadCountSchema{NeedsYou: 3, TotalUnread: 11})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := client.UnreadCount("test-token")
	if err != nil {
		t.Fatalf("UnreadCount: %v", err)
	}
	if got.NeedsYou != 3 || got.TotalUnread != 11 {
		t.Errorf("got %+v, want {NeedsYou:3 TotalUnread:11}", *got)
	}
}

func TestMarkReadPostsToTheReadRoute(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, api.MarkReadResponseSchema{Success: true, Message: "ok"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	if err := client.MarkRead("test-token", testNotificationUUID.String()); err != nil {
		t.Fatalf("MarkRead: %v", err)
	}
	if !called {
		t.Error("the read route was never called")
	}
}

func TestMarkAllReadScopesByTypeAndReturnsTheCount(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	var query url.Values
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/mark-all-read",
		func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.Query()
			testutil.RespondJSON(w, http.StatusOK, api.MarkAllReadResponseSchema{ReadCount: 12, Message: "ok"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := client.MarkAllRead("test-token", []Type{api.RuleUpgrade, api.ValidationFailed})
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if got != 12 {
		t.Errorf("read count %d, want 12", got)
	}
	if types := query["notification_type"]; len(types) != 2 {
		t.Errorf("notification_type = %v, want two values in ONE request", types)
	}
}

// The dismiss route answers 404 for an already-dismissed notification, not a
// no-op. The exit code must reflect that, so a script can tell "nothing to do"
// from "call failed".
func TestDismissTwiceSurfacesTheBackends404(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound,
				map[string]any{"detail": "Notification not found or already dismissed"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	err := client.Dismiss("test-token", testNotificationUUID.String())
	if err == nil {
		t.Fatal("Dismiss accepted a 404")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound)", got, qerrors.ExitNotFound)
	}
}

func TestDismissAllReturnsTheCount(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-all",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissAllResponseSchema{DismissedCount: 7, Message: "ok"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := client.DismissAll("test-token")
	if err != nil {
		t.Fatalf("DismissAll: %v", err)
	}
	if got != 7 {
		t.Errorf("dismissed count %d, want 7", got)
	}
}

// The type travels in the PATH, which is why the vocabulary is checked client
// side: a wrong value here is a 422, not an ignored filter.
func TestDismissByTypePutsTheTypeInThePath(t *testing.T) {
	mock := testutil.NewMockAPIServer()
	defer mock.Close()

	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 5, Message: "ok"})
		})

	client := NewClient(mock.Server.URL, testOrgID, 0)
	got, err := client.DismissByType("test-token", api.RuleUpgrade)
	if err != nil {
		t.Fatalf("DismissByType: %v", err)
	}
	if got != 5 {
		t.Errorf("dismissed count %d, want 5", got)
	}
}

// A short id is a typo, not something to resolve, so both single-notification
// verbs reject it before any request goes out -- and they must do so at
// ExitBadUsage. A bare fmt.Errorf here would keep the same message while
// silently demoting the exit code to 1, so the assertion is on the code.
func TestShortIDIsRejectedAtExitTwo(t *testing.T) {
	// No mock server is registered: reaching the wire at all would fail this.
	client := NewClient("http://localhost:1", testOrgID, 0)

	for name, call := range map[string]func(string) error{
		"MarkRead": func(id string) error { return client.MarkRead("test-token", id) },
		"Dismiss":  func(id string) error { return client.Dismiss("test-token", id) },
	} {
		t.Run(name, func(t *testing.T) {
			err := call("0190bbbb")
			if err == nil {
				t.Fatalf("%s accepted a short id", name)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
			}
		})
	}
}
