package deliverables

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDriftRequestTimeout(t *testing.T) {
	assertRunRequestTimeouts(t, "drift", func(c *Client) error {
		_, err := c.Drift("token", 42, DriftParams{})
		return err
	})
}

func assertRunRequestTimeouts(t *testing.T, endpoint string, call func(*Client) error) {
	t.Helper()
	for _, tc := range []struct {
		name     string
		override time.Duration
		want     time.Duration
	}{
		{name: "default outwaits server", want: 45 * time.Second},
		{name: "shorter override", override: 7 * time.Second, want: 7 * time.Second},
		{name: "longer override", override: 90 * time.Second, want: 90 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousPolicy := client.CurrentRequestPolicy()
			client.SetRequestPolicy(client.RequestPolicy{Timeout: tc.override})
			t.Cleanup(func() { client.SetRequestPolicy(previousPolicy) })

			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			var deadline, observedAt time.Time
			var hasDeadline bool
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+testOrgID+"/deliverable-runs/42/"+endpoint,
				func(w http.ResponseWriter, r *http.Request) {
					deadline, hasDeadline = r.Context().Deadline()
					observedAt = time.Now()
					respondBody(w, http.StatusOK, `{}`)
				})

			c := NewClient(mock.URL(), testOrgID, 0)
			startedAt := time.Now()
			if err := call(c); err != nil {
				t.Fatal(err)
			}
			if !hasDeadline {
				t.Fatal("request context has no deadline")
			}
			// HTTP installs the deadline between starting the call and reaching
			// the transport; these bounds need no sleep or fixed timing tolerance.
			if deadline.Before(startedAt.Add(tc.want)) || deadline.After(observedAt.Add(tc.want)) {
				t.Errorf("request deadline = %s after call started, want %s to %s",
					deadline.Sub(startedAt), tc.want, observedAt.Sub(startedAt)+tc.want)
			}
		})
	}
}
