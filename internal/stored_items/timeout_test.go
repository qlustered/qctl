package stored_items

import (
	"net/http"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestStoredItemRequestTimeout(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override time.Duration
		want     time.Duration
	}{
		{name: "default", want: 300 * time.Second},
		{name: "shorter override", override: 7 * time.Second, want: 7 * time.Second},
		{name: "longer override", override: 10 * time.Minute, want: 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previousPolicy := client.CurrentRequestPolicy()
			client.SetRequestPolicy(client.RequestPolicy{Timeout: tc.override})
			t.Cleanup(func() { client.SetRequestPolicy(previousPolicy) })

			c, mock := newSIClient(t)
			if got := c.httpClient.Timeout; got != tc.want {
				t.Errorf("file transfer timeout = %s, want %s", got, tc.want)
			}
			var deadline, observedAt time.Time
			var hasDeadline bool
			mock.RegisterHandler(http.MethodGet, "/api/orgs/"+siOrgID+"/stored-items/3",
				func(w http.ResponseWriter, r *http.Request) {
					deadline, hasDeadline = r.Context().Deadline()
					observedAt = time.Now()
					testutil.RespondJSON(w, http.StatusOK, map[string]int{"id": 3})
				})

			startedAt := time.Now()
			if _, err := c.GetStoredItem("token", 3); err != nil {
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
