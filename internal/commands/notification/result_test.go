package notification

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Each form writes one document under -o json and moves its prose to stderr.
func TestReadAndDismissWriteADocument(t *testing.T) {
	id := testNotificationUUID.String()
	cases := []struct {
		name  string
		args  []string
		route string
		body  any
		prose string
		want  map[string]any
	}{
		{"read one", []string{"read", id}, id + "/read",
			api.MarkReadResponseSchema{Success: true}, "marked read",
			map[string]any{"id": id, "read": true}},
		{"read --all", []string{"read", "--all"}, "mark-all-read",
			api.MarkAllReadResponseSchema{ReadCount: 12}, "marked 12",
			map[string]any{"read_count": float64(12)}},
		{"dismiss one", []string{"dismiss", id}, id + "/dismiss",
			api.DismissNotificationResponseSchema{Success: true}, "dismissed",
			map[string]any{"id": id, "dismissed": true}},
		{"dismiss --all", []string{"dismiss", "--all", "--yes"}, "dismiss-all",
			api.DismissAllResponseSchema{DismissedCount: 4}, "dismissed 4",
			map[string]any{"dismissed_count": float64(4)}},
		{"dismiss --all --type", []string{"dismiss", "--all", "--type", "rule_upgrade", "--yes"}, "dismiss-by-type/rule_upgrade",
			api.DismissByTypeResponseSchema{DismissedCount: 5}, "rule_upgrade: 5",
			map[string]any{"dismissed_count": float64(5)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newNotificationTestEnv(t)
			mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/"+tc.route,
				func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(tc.body)
				})

			stdout, stderr, err := runNotification(t, append(tc.args, "-o", "json")...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stderr, tc.prose) {
				t.Errorf("stderr missing %q:\n%s", tc.prose, stderr)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(stdout), &got); err != nil {
				t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
			}
			if len(got) != len(tc.want) {
				t.Errorf("document = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

// --all names no notification, so neither verb has a name view.
func TestReadAndDismissRefuseName(t *testing.T) {
	newNotificationTestEnv(t)
	for _, args := range [][]string{{"read", "--all"}, {"dismiss", "--all", "--yes"}} {
		_, _, err := runNotification(t, append(args, "-o", "name")...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%s -o name: exit %d (%v), want %d", args[0], code, err, qerrors.ExitBadUsage)
		}
	}
}

// A fan-out that fails part-way still writes the count that landed, and still
// fails.
func TestDismissFanOutWritesWhatLandedUnderJSON(t *testing.T) {
	mock := newNotificationTestEnv(t)
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/rule_upgrade",
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(api.DismissByTypeResponseSchema{DismissedCount: 5})
		})
	mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/notifications/dismiss-by-type/validation_failed",
		func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})

	stdout, _, err := runNotification(t, "dismiss", "--all",
		"--type", "rule_upgrade", "--type", "validation_failed", "--yes", "-o", "json")
	if err == nil {
		t.Fatal("a failed type was swallowed")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["dismissed_count"] != float64(5) {
		t.Errorf("dismissed_count = %v, want 5", got["dismissed_count"])
	}
}
