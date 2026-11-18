package notification

import (
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDismissOneTakesItOutOfTheInbox(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				api.DismissNotificationResponseSchema{Success: true, Message: "Notification dismissed"})
		})

	out, _, err := runNotification(t, "dismiss", testNotificationUUID.String())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "notification/" + testNotificationUUID.String() + " dismissed"
	if !strings.Contains(out, want) {
		t.Errorf("output %q, want %q", out, want)
	}
}

// Dismissing must never mark read. The backend keeps the columns apart and a
// CLI that set both would make `--unread` stop meaning what it says.
func TestDismissNeverCallsRead(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissNotificationResponseSchema{Success: true})
		})
	read := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
		func(w http.ResponseWriter, r *http.Request) {
			read = true
			testutil.RespondJSON(w, http.StatusOK, api.MarkReadResponseSchema{Success: true})
		})

	if _, _, err := runNotification(t, "dismiss", testNotificationUUID.String()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if read {
		t.Error("dismiss called the read route; the two axes are independent")
	}
}

// The backend filters dismissed_at IS NULL and turns a zero rowcount into a
// 404, so a repeated dismiss is an error rather than a no-op. A script needs
// to be able to tell those apart.
func TestDismissTwiceIsExitThree(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound,
				map[string]any{"detail": "Notification not found or already dismissed"})
		})

	_, _, err := runNotification(t, "dismiss", testNotificationUUID.String())
	if err == nil {
		t.Fatal("a 404 was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound)", got, qerrors.ExitNotFound)
	}
}

func TestDismissAllReportsTheCount(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-all",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK,
				api.DismissAllResponseSchema{DismissedCount: 7, Message: "ok"})
		})

	out, _, err := runNotification(t, "dismiss", "--all", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "dismissed 7 notification(s)") {
		t.Errorf("output %q", out)
	}
}

// dismiss-by-type takes the type in the PATH, so several types are several
// requests. The per-type counts are what let the caller see that.
func TestDismissAllWithTypesFansOut(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 5})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/validation_failed",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 2})
		})

	out, _, err := runNotification(t, "dismiss", "--all",
		"--type", "rule_upgrade", "--type", "validation_failed", "--yes")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "dismissed 7 notification(s) (rule_upgrade: 5, validation_failed: 2)") {
		t.Errorf("output %q", out)
	}
}

// A failure part-way through must not erase what already landed: those rows
// ARE dismissed, and a bare error would send the caller to re-run the whole
// set. The exit code still comes from the failure.
func TestDismissFanOutReportsPartialSuccessAndStillFails(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 5})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/validation_failed",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusInternalServerError, map[string]any{"detail": "boom"})
		})

	out, _, err := runNotification(t, "dismiss", "--all",
		"--type", "rule_upgrade", "--type", "validation_failed", "--yes")
	if err == nil {
		t.Fatal("a failed type was swallowed")
	}
	if !strings.Contains(out, "rule_upgrade: 5") {
		t.Errorf("the counts that landed were not reported: %q", out)
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Errorf("the error does not name the failed type: %v", err)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code %d, want %d (a 5xx)", got, qerrors.ExitGenericError)
	}
}

// The fan-out's exit code must come from the FAILURE, not from "something went
// wrong". The sibling test above cannot see this: it fails with a 5xx, which is
// exit 1, and a %v in dismissByTypes' final wrap -- which severs the Unwrap
// chain and floors every code at 1 -- satisfies it by accident. A 404 is exit 3
// and only survives the wrap if it is %w.
func TestDismissFanOutCarriesTheFailuresExitCode(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissByTypeResponseSchema{DismissedCount: 5})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/validation_failed",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "not found"})
		})

	out, _, err := runNotification(t, "dismiss", "--all",
		"--type", "rule_upgrade", "--type", "validation_failed", "--yes")
	if err == nil {
		t.Fatal("a failed type was swallowed")
	}
	if !strings.Contains(out, "rule_upgrade: 5") {
		t.Errorf("the counts that landed were not reported: %q", out)
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound) -- the fan-out wrap severed the Unwrap chain", got, qerrors.ExitNotFound)
	}
}

// The bulk form has no un-dismiss verb to walk it back -- so it is gated.
// A non-interactive caller with no --yes is bad usage (exit 2): the fix is to
// change the command line.
func TestDismissAllWithoutYesIsBadUsageWhenNonInteractive(t *testing.T) {
	newNotificationTestEnv(t)
	_, _, err := runNotification(t, "dismiss", "--all")
	if err == nil {
		t.Fatal("--all ran without confirmation")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code %d, want %d (ExitBadUsage)", got, qerrors.ExitBadUsage)
	}
}

// Declining at the prompt is a choice, not a usage error: exit 1, and nothing
// is sent.
func TestDismissAllDeclinedSendsNothing(t *testing.T) {
	mock := newNotificationTestEnv(t)
	called := false
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-all",
		func(w http.ResponseWriter, r *http.Request) {
			called = true
			testutil.RespondJSON(w, http.StatusOK, api.DismissAllResponseSchema{DismissedCount: 7})
		})
	cmdutil.SetReader(strings.NewReader("n\n"))
	defer cmdutil.ResetReader()

	_, _, err := runNotification(t, "dismiss", "--all")
	if err == nil {
		t.Fatal("a declined confirmation still ran")
	}
	if called {
		t.Error("the dismiss-all route was called after the user declined")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code %d, want %d", got, qerrors.ExitGenericError)
	}
}

// A single dismiss is targeted and needs no prompt -- matching
// `comment-thread resolve`, which also mutates one named thing unprompted.
func TestDismissOneIsNotPrompted(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissNotificationResponseSchema{Success: true})
		})

	if _, _, err := runNotification(t, "dismiss", testNotificationUUID.String()); err != nil {
		t.Fatalf("a single dismiss was gated behind a prompt: %v", err)
	}
}

// --yes on a single dismiss is accepted and ignored: there is no prompt to
// skip, and it drops nothing. That is deliberately unlike --type, which
// scopeFlags REFUSES on a single id because accepting it would silently drop a
// filter the user typed. Generalizing the --type refusal to --yes would break
// scripts that pass --yes uniformly, so it is pinned here.
func TestDismissOneAcceptsAndIgnoresYes(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/dismiss",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, api.DismissNotificationResponseSchema{Success: true})
		})

	out, _, err := runNotification(t, "dismiss", testNotificationUUID.String(), "--yes")
	if err != nil {
		t.Fatalf("--yes was refused on a single dismiss: %v", err)
	}
	want := "notification/" + testNotificationUUID.String() + " dismissed"
	if !strings.Contains(out, want) {
		t.Errorf("output %q, want %q", out, want)
	}
}

// dismiss --all with no --type takes the dismiss-all endpoint, a THIRD wrap
// site beside the single form (TestDismissTwiceIsExitThree) and the fan-out
// (TestDismissFanOutCarriesTheFailuresExitCode). It had no exit-code assertion
// on it: a %v in dismiss.go's DismissAll wrap keeps the message and floors the
// code at 1, and every other test on this path asserts only err != nil.
func TestDismissAllSurfacesA404AsExitThree(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-all",
		func(w http.ResponseWriter, r *http.Request) {
			testutil.RespondJSON(w, http.StatusNotFound, map[string]any{"detail": "not found"})
		})

	_, _, err := runNotification(t, "dismiss", "--all", "--yes")
	if err == nil {
		t.Fatal("a 404 on dismiss --all was swallowed")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
		t.Errorf("exit code %d, want %d (ExitNotFound) -- the dismiss-all wrap severed the Unwrap chain", got, qerrors.ExitNotFound)
	}
}

// The bulk paths need their own independence sentinel. TestDismissNeverCallsRead
// covers the single form only, and a DismissAll -> MarkAllRead swap on either
// bulk path is caught today just incidentally, by the mock 404ing an
// unregistered route -- the weak form that was rejected for the read side,
// because it fails with the wrong reason and stops working the moment the
// mark-all-read route is registered for any purpose. Here both read routes ARE
// registered and answer 200, so the only thing that can fail this test is the
// dismiss verb actually calling one of them.
func TestDismissAllNeverCallsRead(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		route string
	}{
		{"dismiss-all", []string{"dismiss", "--all", "--yes"},
			"/api/orgs/" + testOrgID + "/notifications/dismiss-all"},
		{"fan-out", []string{"dismiss", "--all", "--type", "rule_upgrade", "--yes"},
			"/api/orgs/" + testOrgID + "/notifications/dismiss-by-type/rule_upgrade"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newNotificationTestEnv(t)
			mock.RegisterHandler("POST", tc.route, func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, api.DismissAllResponseSchema{DismissedCount: 3})
			})
			read := false
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/mark-all-read",
				func(w http.ResponseWriter, r *http.Request) {
					read = true
					testutil.RespondJSON(w, http.StatusOK, api.MarkAllReadResponseSchema{ReadCount: 3})
				})
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+testNotificationUUID.String()+"/read",
				func(w http.ResponseWriter, r *http.Request) {
					read = true
					testutil.RespondJSON(w, http.StatusOK, api.MarkReadResponseSchema{Success: true})
				})

			if _, _, err := runNotification(t, tc.args...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if read {
				t.Error("the bulk dismiss called a read route; the two axes are independent")
			}
		})
	}
}
