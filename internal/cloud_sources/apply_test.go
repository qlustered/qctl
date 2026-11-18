package cloud_sources

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

// planAgainstRead prepares m's apply against a server whose table holds one
// cloud source, live, under m's name.
func planAgainstRead(t *testing.T, live *CloudSourceFull, m *CloudSourceManifest) *ApplyPlan {
	t.Helper()
	c, mock := newCSClient(t)
	id := derefInt(live.ID)
	mock.RegisterHandler(http.MethodGet, "/api/orgs/"+csOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, map[string]any{
			"results":    []map[string]any{{"id": id, "name": derefString(live.Name)}},
			"page":       1,
			"total_rows": 1,
		})
	})
	mock.RegisterHandler(http.MethodGet, fmt.Sprintf("/api/orgs/%s/data-sources/%d", csOrgID, id), func(w http.ResponseWriter, r *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, live)
	})
	plan, err := c.PrepareApply("tok", m)
	if err != nil {
		t.Fatalf("PrepareApply: %v", err)
	}
	if plan.IsCreate() {
		t.Fatalf("PrepareApply planned a create against the existing cloud source %d", id)
	}
	return plan
}

// bodyMembers decodes a request body as one JSON object.
func bodyMembers(t *testing.T, route string, body []byte) map[string]json.RawMessage {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("%s body does not decode: %v\n%s", route, err, body)
	}
	return members
}

// A zero value is a setting, not an absence: an empty schedule is how a
// manifest stops polling, and false and 0 are real values. On both routes each
// must reach the wire as itself, neither dropped nor sent as null.
func TestZeroValuesReachTheWire(t *testing.T) {
	live := cursorSource(nil)
	live.Schedule = cloudSourcePtr("0 * * * *")
	live.DeleteSourceFileAfterBackup = cloudSourcePtr(true)
	live.SftpPort = cloudSourcePtr(22)
	m, err := APIResponseToManifest(live)
	if err != nil {
		t.Fatalf("APIResponseToManifest: %v", err)
	}
	empty, off, zero := "", false, 0
	m.Spec.Schedule = &empty
	m.Spec.DeleteSourceFileAfterBackup = &off
	m.Spec.SftpPort = &zero

	post, err := createBody(m)
	if err != nil {
		t.Fatalf("createBody: %v", err)
	}
	patch := planAgainstRead(t, live, m).body

	bodies := map[string]map[string]json.RawMessage{
		"POST":  bodyMembers(t, "POST", post),
		"PATCH": bodyMembers(t, "PATCH", patch),
	}
	for route, members := range bodies {
		for key, want := range map[string]string{
			"schedule":                        `""`,
			"delete_source_file_after_backup": "false",
			"sftp_port":                       "0",
		} {
			if got, ok := members[key]; !ok || string(got) != want {
				t.Errorf("%s %s = %s (present %v), want %s", route, key, got, ok, want)
			}
		}
	}
	// A create sends only what the manifest sets, so none of it is null. An
	// update may send null, to clear a field the manifest leaves out.
	for key, value := range bodies["POST"] {
		if string(value) == "null" {
			t.Errorf("POST %s sent as null", key)
		}
	}
}
