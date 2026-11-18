package datasets

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

const dlOrgID = "b2c3d4e5-f6a7-8901-bcde-f23456789012"

func TestPutDataForDownloadSendsFiltersAndSucceeds(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok","download_handle":"0f1e2d3c4b5a6978"}`))
	}))
	defer srv.Close()

	isBad := true
	c := NewClient(srv.URL, dlOrgID, 0)
	handle, err := c.PutDataForDownload("tok", 42, &api.PutDataForDownloadParams{IsBadData: &isBad})
	if err != nil {
		t.Fatalf("PutDataForDownload: %v", err)
	}
	if !strings.Contains(gotQuery, "is_bad_data=true") {
		t.Errorf("filter not sent, query = %q", gotQuery)
	}
	if handle != "0f1e2d3c4b5a6978" {
		t.Errorf("handle = %q, want the one the response carried", handle)
	}
}

// A server before atlas d117e8429 answers the bare {"result": "ok"}. The
// handle is then "" and that is NOT an error: it only means the wait has to
// correlate the way it did before #1068.
func TestPutDataForDownloadHasNoHandleOnAnOlderServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	}))
	defer srv.Close()

	handle, err := NewClient(srv.URL, dlOrgID, 0).PutDataForDownload("tok", 42, nil)
	if err != nil {
		t.Fatalf("PutDataForDownload: %v", err)
	}
	if handle != "" {
		t.Errorf("handle = %q, want empty", handle)
	}
}

// The route has four distinct 404s and every one of them is an informative
// answer, not a failure to explain: "there are no quarantine rows to download"
// tells the user what they needed to know. All four must reach the not-found
// exit code with the backend's own sentence intact.
func TestPutDataForDownloadNotFoundCases(t *testing.T) {
	msgs := []string{
		"This table does not have a schema yet. First upload some data to create a schema.",
		"There are no quarantine rows to download.",
		"There are no clean rows to download.",
		"There are no rows matching your criteria to download.",
	}
	for _, msg := range msgs {
		t.Run(msg, func(t *testing.T) {
			body := `{"detail":{"msg":` + strconv.Quote(msg) + `}}`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			c := NewClient(srv.URL, dlOrgID, 0)
			_, err := c.PutDataForDownload("tok", 42, nil)
			if err == nil {
				t.Fatal("expected an error on 404")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d (not found)", code, qerrors.ExitNotFound)
			}
			if !strings.Contains(err.Error(), msg) {
				t.Errorf("the backend's own sentence must survive, got: %v", err)
			}
		})
	}
}

// The client does not interpret this one: whether "already queued" is a
// failure depends on whether the caller is going to wait, which only the
// command layer knows. Here it must simply arrive intact.
func TestPutDataForDownloadPassesAlreadyQueuedThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"detail":{"msg":"This view is already queued for downloading."}}`))
	}))
	defer srv.Close()

	_, err := NewClient(srv.URL, dlOrgID, 0).PutDataForDownload("tok", 42, nil)
	if err == nil {
		t.Fatal("expected an error on 500")
	}
	if !strings.Contains(err.Error(), "already queued for downloading") {
		t.Errorf("the backend's own sentence must survive verbatim, got: %v", err)
	}
}
