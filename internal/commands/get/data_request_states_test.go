package get

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const requestGroupID = "11111111-2222-4333-8444-555555555555"

func TestGetDataRequestStatePairings(t *testing.T) {
	for _, view := range []string{"", "approver", "sender"} {
		for _, state := range []string{"to_review", "flagged_by_me", "waiting", "flagged", "approved", "on_hold"} {
			t.Run(view+"/"+state, func(t *testing.T) {
				args := []string{"--is-bad-data", "--review-group-id", requestGroupID, "--request-state", state}
				if view != "" {
					args = append(args, "--request-view", view)
				}
				valid := state == "approved" || state == "on_hold" ||
					(view == "sender" && (state == "waiting" || state == "flagged")) ||
					(view != "sender" && (state == "to_review" || state == "flagged_by_me"))
				if !valid {
					mock := dataTestMock(t)
					_, err := execData(t, append([]string{"orders"}, args...))
					if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
						t.Fatalf("error = %v, want exit 2", err)
					}
					if !strings.Contains(err.Error(), "--request-view") {
						t.Errorf("error = %v, want view pairing advice", err)
					}
					if got := mock.Requests(); len(got) != 0 {
						t.Errorf("requests = %v, want none before refusal", got)
					}
					return
				}
				saw := false
				runData(t, append([]string{"1"}, args...), func(w http.ResponseWriter, r *http.Request) {
					saw = true
					query := r.URL.Query()
					for key, want := range map[string]string{
						"is_bad_data": "true", "review_group_id": requestGroupID,
						"request_state": state, "request_view": view,
					} {
						if got := query.Get(key); got != want {
							t.Errorf("%s = %q, want %q", key, got, want)
						}
					}
					testutil.RespondJSON(w, http.StatusOK, sampleGetData())
				})
				if !saw {
					t.Fatal("no data request")
				}
			})
		}
	}
}

func TestGetDataRequestStateRequiresQuarantineAndGroup(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"missing group", []string{"--is-bad-data", "--request-state", "to_review"}, "--review-group-id"},
		{"missing quarantine", []string{"--review-group-id", requestGroupID, "--request-state", "to_review"}, "--is-bad-data"},
		{"explicit clean", []string{"--is-bad-data=false", "--review-group-id", requestGroupID, "--request-state", "to_review"}, "--is-bad-data"},
		{"malformed group", []string{"--is-bad-data", "--review-group-id", "not-a-uuid", "--request-state", "to_review"}, "--review-group-id"},
		{"empty group", []string{"--is-bad-data", "--review-group-id", "", "--request-state", "to_review"}, "--review-group-id"},
		{"invalid state", []string{"--is-bad-data", "--review-group-id", requestGroupID, "--request-state", "pending"}, "--request-state"},
		{"empty state", []string{"--is-bad-data", "--review-group-id", requestGroupID, "--request-state", ""}, "--request-state"},
		{"invalid view", []string{"--is-bad-data", "--review-group-id", requestGroupID, "--request-state", "approved", "--request-view", "reviewer"}, "--request-view"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := dataTestMock(t)
			_, err := execData(t, append([]string{"orders"}, tc.args...))
			if err == nil || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Fatalf("error = %v, want exit 2", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %s advice", err, tc.want)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none", got)
			}
		})
	}
}

func TestGetDataRequestViewAloneIsAcceptedOnCleanData(t *testing.T) {
	runData(t, []string{"1", "--request-view", "sender"}, func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if query.Get("request_view") != "sender" || query.Has("request_state") || query.Has("review_group_id") || query.Has("is_bad_data") {
			t.Errorf("unexpected query: %v", query)
		}
		testutil.RespondJSON(w, http.StatusOK, sampleGetData())
	})
}
