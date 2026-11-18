package get

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestGetThirdPartyData(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			mock := dataTestMock(t)
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/third-party/datasets", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("name") != "shared" {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, 200, json.RawMessage(`{"results":[{"id":42,"name":"shared"}]}`))
			})
			mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/third-party/datasets/42/data", func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("alert_item_id") != "8817" || r.URL.Query().Get("is_bad_data") != "true" {
					t.Errorf("query = %s", r.URL.RawQuery)
				}
				testutil.RespondJSON(w, 200, json.RawMessage(`{"dataset_name":"shared","fields":["id"],"labels":[null],"field_types":["integer"],"field_display_formats":[null],"results":[[1234567890123456789]],"total_page_count":1,"total_rows":1}`))
			})
			out, err := execData(t, []string{"--table", "shared", "--shared", "--is-bad-data", "--alert-item-id", "8817", "-o", format})
			if err != nil || !strings.Contains(out, "1234567890123456789") {
				t.Fatalf("err=%v output=%s", err, out)
			}
			if len(mock.Requests()) != 2 {
				t.Fatalf("requests = %v", mock.Requests())
			}
			for _, req := range mock.Requests() {
				if !strings.Contains(req, "/third-party/") {
					t.Fatalf("first-party request: %s", req)
				}
			}
		})
	}
}

func TestThirdPartyDataRefusesUnsupportedHelpers(t *testing.T) {
	for _, flags := range [][]string{
		{"--shared", "--rule", "email"},
		{"--shared", "--include-issues"},
		{"--shared", "--include-comment-threads"},
	} {
		t.Run(strings.Join(flags, "_"), func(t *testing.T) {
			mock := dataTestMock(t)
			_, err := execData(t, append([]string{"shared"}, flags...))
			if qerrors.GetExitCode(err) != 2 || len(mock.Requests()) != 0 {
				t.Fatalf("err=%v requests=%v", err, mock.Requests())
			}
		})
	}
}
