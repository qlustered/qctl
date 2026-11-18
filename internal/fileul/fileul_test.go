package fileul

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func uploadFixture(t *testing.T) (string, []byte) {
	t.Helper()
	content := []byte("id,name\n1,Ada\n\x00\xff")
	path := filepath.Join(t.TempDir(), "data.csv")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func TestUploadRawRequest(t *testing.T) {
	path, content := uploadFixture(t)
	cases := []struct {
		name       string
		method     string
		fields     map[string]string
		wantMethod string
		wantType   string
		status     int
	}{
		{name: "default PUT", wantMethod: http.MethodPut, status: http.StatusNoContent},
		{
			name: "GCS PUT with header fields", method: http.MethodPut,
			fields:     map[string]string{"Content-Type": "application/octet-stream"},
			wantMethod: http.MethodPut, wantType: "application/octet-stream", status: http.StatusOK,
		},
		{
			name: "POST without policy fields", method: http.MethodPost, fields: map[string]string{},
			wantMethod: http.MethodPost, status: http.StatusCreated,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// TLS also verifies that Upload uses the caller's trusted client.
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != tc.wantMethod {
					t.Errorf("method = %s, want %s", r.Method, tc.wantMethod)
				}
				if got := r.Header.Get("Content-Type"); got != tc.wantType {
					t.Errorf("Content-Type = %q, want %q", got, tc.wantType)
				}
				if r.ContentLength != int64(len(content)) {
					t.Errorf("Content-Length = %d, want %d", r.ContentLength, len(content))
				}
				body, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(body, content) {
					t.Errorf("body = %q, err = %v, want %q", body, err, content)
				}
				w.WriteHeader(tc.status)
			}))
			defer server.Close()

			target := Target{URL: server.URL, Method: tc.method, Fields: tc.fields}
			if err := Upload(server.Client(), target, path, 0); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUploadMultipartRequest(t *testing.T) {
	path, content := uploadFixture(t)
	fields := map[string]string{"key": "templates/data.csv", "policy": "signed-policy", "x-amz-signature": "signature"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		gotFields := make(map[string]string)
		var names []string
		var fileCount int
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Error(err)
				return
			}
			name := part.FormName()
			names = append(names, name)
			body, err := io.ReadAll(part)
			if err != nil {
				t.Error(err)
				return
			}
			if name != "file" {
				gotFields[name] = string(body)
				continue
			}
			fileCount++
			if got := part.FileName(); got != filepath.Base(path) {
				t.Errorf("filename = %q, want %q", got, filepath.Base(path))
			}
			if !bytes.Equal(body, content) {
				t.Errorf("file bytes = %q, want %q", body, content)
			}
		}
		if !reflect.DeepEqual(gotFields, fields) {
			t.Errorf("fields = %v, want %v", gotFields, fields)
		}
		if fileCount != 1 || len(names) != len(fields)+1 || names[len(names)-1] != "file" {
			t.Errorf("parts = %v, want policy fields followed by one file", names)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	target := Target{URL: server.URL, Method: http.MethodPost, Fields: fields}
	if err := Upload(server.Client(), target, path, 0); err != nil {
		t.Fatal(err)
	}
}

func TestUploadRejectsNon2xxResponse(t *testing.T) {
	path, _ := uploadFixture(t)
	for _, status := range []int{http.StatusFound, http.StatusForbidden, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "storage rejected upload")
			}))
			defer server.Close()

			err := Upload(server.Client(), Target{URL: server.URL}, path, 0)
			want := fmt.Sprintf("upload failed with status %d: storage rejected upload", status)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("error = %v, want %q", err, want)
			}
		})
	}
}
