package stored_items

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestUploadFileAdaptsPresignedResponse(t *testing.T) {
	content := []byte("id,name\n1,Ada\n")
	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.Header.Get("Content-Type") != "application/octet-stream" {
			t.Errorf("request = %s with headers %v, want raw PUT with Content-Type", r.Method, r.Header)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, content) {
			t.Errorf("body = %q, err = %v, want %q", body, err, content)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	c := &Client{httpClient: server.Client()}
	method := api.PUT
	fields := map[string]string{"Content-Type": "application/octet-stream"}
	response := &StoredItemURLResponse{URL: server.URL, HTTPMethod: &method, Fields: &fields}
	if err := c.UploadFile(response, path); err != nil {
		t.Fatal(err)
	}
}

// The publish response names the ingestion job it queued, and the client hands
// that ID back; a response that names none is nil rather than an error.
func TestPublishStoredItemForIngestionReturnsTheJobID(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		want   int
		wantID bool
	}{
		{name: "queued", body: `{"ingestion_job_id": 1234}`, want: 1234, wantID: true},
		{name: "null id", body: `{"ingestion_job_id": null}`},
		{name: "no id", body: `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, mock := newSIClient(t)
			mock.RegisterHandler(http.MethodPut, "/api/orgs/"+siOrgID+"/stored-items/3/publish-for-ingestion",
				func(w http.ResponseWriter, _ *http.Request) {
					testutil.RespondJSON(w, http.StatusOK, json.RawMessage(tc.body))
				})

			got, err := c.PublishStoredItemForIngestion("tok", 3, nil)
			if err != nil {
				t.Fatalf("PublishStoredItemForIngestion: %v", err)
			}
			switch {
			case !tc.wantID && got != nil:
				t.Errorf("ingestion job ID = %d, want nil", *got)
			case tc.wantID && got == nil:
				t.Errorf("ingestion job ID = nil, want %d", tc.want)
			case tc.wantID && *got != tc.want:
				t.Errorf("ingestion job ID = %d, want %d", *got, tc.want)
			}
		})
	}
}
