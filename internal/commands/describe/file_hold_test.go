package describe

import (
	"bytes"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDescribeFileRecordedHold(t *testing.T) {
	for _, tt := range []struct {
		name     string
		fields   string
		decision any
		at       any
		by       any
	}{
		{
			name: "released",
			fields: `{"hold_decision":"release","hold_decided_at":"2026-11-04T12:30:00Z",` +
				`"hold_decided_by_name":"Carol Smith"}`,
			decision: "release", at: "2026-11-04T12:30:00Z", by: "Carol Smith",
		},
		{
			name: "rejected",
			fields: `{"hold_decision":"reject","hold_decided_at":"2026-11-04T12:30:00Z",` +
				`"hold_decided_by_name":"Carol Smith"}`,
			decision: "reject", at: "2026-11-04T12:30:00Z", by: "Carol Smith",
		},
		{
			name: "system supersession",
			fields: `{"hold_decision":"superseded","hold_decided_at":"2026-11-04T12:30:00Z",` +
				`"hold_decided_by_name":null}`,
			decision: "superseded", at: "2026-11-04T12:30:00Z",
		},
		{
			name:   "undecided",
			fields: `{"hold_decision":null,"hold_decided_at":null,"hold_decided_by_name":null}`,
		},
		{name: "absent fields", fields: `{}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)

			body, err := json.Marshal(sampleStoredItemFull())
			if err != nil {
				t.Fatal(err)
			}
			var item, fields map[string]json.RawMessage
			if err := json.Unmarshal(body, &item); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"hold_decision", "hold_decided_at", "hold_decided_by_name"} {
				delete(item, key)
			}
			if err := json.Unmarshal([]byte(tt.fields), &fields); err != nil {
				t.Fatal(err)
			}
			for key, value := range fields {
				item[key] = value
			}
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/stored-items/1", func(w http.ResponseWriter, r *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, item)
			})
			env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, testOrgID, "test-token")

			run := func(args ...string) string {
				t.Helper()
				cmd := setupFileDescribeTestCommand()
				var stdout, stderr bytes.Buffer
				cmd.SetOut(&stdout)
				cmd.SetErr(&stderr)
				cmd.SetArgs(append([]string{"describe", "file", "1"}, args...))
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				return stdout.String()
			}
			jsonOut := run("-o", "json")
			var got struct {
				Status map[string]any `json:"status"`
			}
			if err := json.Unmarshal([]byte(jsonOut), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"decision": tt.decision, "decided_at": tt.at, "decided_by": tt.by}
			if !reflect.DeepEqual(got.Status["hold"], want) {
				t.Errorf("status.hold = %#v, want %#v", got.Status["hold"], want)
			}
			testutil.AssertYAMLReadsAsJSON(t, run(), jsonOut)
			testutil.AssertYAMLReadsAsJSON(t, run("-o", "yaml"), jsonOut)
		})
	}
}
