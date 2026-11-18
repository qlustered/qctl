package get

import (
	"bytes"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/cobra"
)

func setupTestCommandForQueueMessages() *cobra.Command {
	rootCmd := newTestRootCmd()
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display")
	getCmd := &cobra.Command{Use: "get", Short: "Display resources"}
	getCmd.AddCommand(NewQueueMessagesCommand())
	rootCmd.AddCommand(getCmd)
	return rootCmd
}

// runQueueMessages returns the query the command sent, its stdout, and its error.
// The query is what most of these assertions are about: this listing's filters
// are tri-state or inverted, and a flag that is silently dropped answers with a
// wider population at exit 0.
func runQueueMessages(t *testing.T, args ...string) (url.Values, string, error) {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.Server.URL)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	var query url.Values
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/queue-messages", func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		total, page := 1, 1
		isDead := true
		key := "files/77/orders.csv"
		datasetID := 5
		testutil.RespondJSON(w, http.StatusOK, api.QueuesListSchema{
			Results: []api.QueueSchema{{
				ID:           42,
				QName:        "CUEUE_INGESTION",
				Key:          &key,
				DatasetID:    &datasetID,
				IsDead:       &isDead,
				ReceiveCount: 4,
				EnqueuedAt:   time.Date(2025, 1, 18, 9, 0, 0, 0, time.UTC),
				Data:         map[string]interface{}{"ingestion_job_id": 101},
			}},
			TotalRows: &total,
			Page:      &page,
		})
	})

	cmd := setupTestCommandForQueueMessages()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(append([]string{"get", "queue-messages"}, args...))
	err := cmd.Execute()
	return query, out.String(), err
}

func TestQueueMessagesDefaultColumnsOmitThePayload(t *testing.T) {
	_, out, err := runQueueMessages(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, want := range []string{"42", "CUEUE_INGESTION", "files/77/orders.csv"} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Errorf("output %q is missing %q", out, want)
		}
	}
	// `data` is an arbitrary JSON payload with no schema. It is reachable
	// through --columns and belongs to `describe queue-message`, never to the
	// default listing.
	if bytes.Contains([]byte(out), []byte("ingestion_job_id")) {
		t.Errorf("output %q carries the payload in the default columns", out)
	}
}

// --dead and --acknowledged are tri-state on the API: unset means "no filter".
// Reading the VALUE instead of Changed() would send false for an unset flag and
// silently narrow the listing to the opposite set.
func TestQueueMessagesTriStateFilters(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		key   string
		want  string
		unset bool
	}{
		{name: "dead unset", args: nil, key: "is_dead", unset: true},
		{name: "dead true", args: []string{"--dead"}, key: "is_dead", want: "true"},
		{name: "dead false", args: []string{"--dead=false"}, key: "is_dead", want: "false"},
		{name: "acknowledged unset", args: nil, key: "acknowledged", unset: true},
		{name: "acknowledged true", args: []string{"--acknowledged"}, key: "acknowledged", want: "true"},
		{name: "acknowledged false", args: []string{"--acknowledged=false"}, key: "acknowledged", want: "false"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, _, err := runQueueMessages(t, tt.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, present := query[tt.key]
			if tt.unset {
				if present {
					t.Errorf("%s was sent as %v with the flag unset; it must be absent", tt.key, got)
				}
				return
			}
			if !present || got[0] != tt.want {
				t.Errorf("%s = %v, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// The endpoint defaults reverse=true, so --reverse means "un-reverse" and is
// sent as reverse=false. Without the inversion the flag would be a no-op that
// still looked like it worked.
func TestQueueMessagesReverseUnReverses(t *testing.T) {
	query, _, err := runQueueMessages(t, "--reverse")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := query.Get("reverse"); got != "false" {
		t.Errorf("reverse = %q, want \"false\"", got)
	}

	query, _, err = runQueueMessages(t)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, present := query["reverse"]; present {
		t.Error("reverse was sent with the flag unset; the server's own default must stand")
	}
}

func TestQueueMessagesForwardsItsFilters(t *testing.T) {
	query, _, err := runQueueMessages(t,
		"--queue", "CUEUE_INGESTION", "--cloud-source-id", "22",
		"--search", "orders", "--order-by", "enqueued_at", "--limit", "5", "--page", "2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for key, want := range map[string]string{
		"q_name":               "CUEUE_INGESTION",
		"data_source_model_id": "22",
		"search_query":         "orders",
		"order_by":             "enqueued_at",
		"limit":                "5",
		"page":                 "2",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestQueueMessagesUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		// The backend ignores a search of two characters or fewer and answers
		// with the unfiltered listing at exit 0, so qctl refuses it here.
		{"short search", []string{"--search", "ab"}},
		{"unknown order-by", []string{"--order-by", "not_a_column"}},
		// A negative value names no cloud source, and every row it matches
		// comes back with data_source_model_id null.
		{"negative cloud-source-id", []string{"--cloud-source-id", "-1"}},
		{"zero limit", []string{"--limit", "0"}},
		{"zero watch-interval", []string{"--watch", "--watch-interval", "0"}},
		{"both table selectors", []string{"--table", "orders", "--table-id", "5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query, _, err := runQueueMessages(t, tt.args...)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2", code)
			}
			if query != nil {
				t.Error("a client-side usage error still reached the server")
			}
		})
	}
}
