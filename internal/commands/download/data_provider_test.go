package download

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestDownloadThirdPartyData(t *testing.T) {
	f := newDataTestServer(t)
	f.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/third-party/datasets", func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, 200, json.RawMessage(`{"results":[{"id":42,"name":"shared"}]}`))
	})
	hits := 0
	f.mock.RegisterHandler("POST", "/api/orgs/"+testOrgID+"/third-party/datasets/42/data/download", func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("alert_item_id") != "8817" || r.URL.Query().Get("is_bad_data") != "true" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		testutil.RespondJSON(w, 200, json.RawMessage(handleBody))
	})
	_, stderr, err := runDownloadData(t, "download", "data", "shared", "--shared", "--is-bad-data", "--alert-item-id", "8817", "--no-wait")
	if err != nil || hits != 1 {
		t.Fatalf("err=%v hits=%d stderr=%s", err, hits, stderr)
	}
	if len(f.mock.Requests()) != 2 {
		t.Fatalf("requests = %v", f.mock.Requests())
	}
	for _, req := range f.mock.Requests() {
		if !strings.Contains(req, "/third-party/") {
			t.Fatalf("first-party request: %s", req)
		}
	}
}

func TestDownloadThirdPartyDataRefusesRuleLookup(t *testing.T) {
	f := newDataTestServer(t)
	_, _, err := runDownloadData(t, "download", "data", "shared", "--shared", "--is-bad-data", "--rule", "email", "--no-wait")
	if qerrors.GetExitCode(err) != 2 || len(f.mock.Requests()) != 0 {
		t.Fatalf("err=%v requests=%v", err, f.mock.Requests())
	}
}
