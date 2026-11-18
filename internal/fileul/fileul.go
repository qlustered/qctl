// Package fileul uploads local files to presigned URLs.
package fileul

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/qlustered/qctl/internal/terminal"
)

// Target describes a presigned upload request.
type Target struct {
	URL, Method string
	Fields      map[string]string
}

// Upload sends filePath to t using httpClient, defaulting to PUT.
// POST targets with fields use multipart form data.
func Upload(httpClient *http.Client, t Target, filePath string, verbosity int) error {
	method := t.Method
	if method == "" {
		method = http.MethodPut
	}

	var req *http.Request
	var err error
	if method == http.MethodPost && len(t.Fields) > 0 {
		req, err = buildMultipartUploadRequest(t.URL, filePath, t.Fields)
	} else {
		req, err = buildPutUploadRequest(method, t.URL, filePath, t.Fields)
	}
	if err != nil {
		return err
	}
	return executeUpload(httpClient, req, method, verbosity)
}

// buildMultipartUploadRequest creates a POST request with multipart form data.
func buildMultipartUploadRequest(url, filePath string, fields map[string]string) (*http.Request, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add fields first
	for key, val := range fields {
		if err := writer.WriteField(key, val); err != nil {
			return nil, fmt.Errorf("failed to write field %s: %w", key, err)
		}
	}

	// Add file
	part, err := writer.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return nil, fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(part, file); err != nil {
		return nil, fmt.Errorf("failed to copy file content: %w", err)
	}

	writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	return req, nil
}

// buildPutUploadRequest creates a request with the file as its body.
func buildPutUploadRequest(method, url, filePath string, fields map[string]string) (*http.Request, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	req, err := http.NewRequest(method, url, file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Get file stats for content length
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	req.ContentLength = stat.Size()

	// Set content type if available
	if contentType, ok := fields["Content-Type"]; ok {
		req.Header.Set("Content-Type", contentType)
	}

	return req, nil
}

// executeUpload sends the upload request and handles the response.
func executeUpload(httpClient *http.Client, req *http.Request, method string, verbosity int) error {
	if verbosity > 0 {
		fmt.Fprintf(os.Stderr, "→ %s %s (file upload)\n", method, req.URL)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("upload failed: %w", err)
	}
	defer resp.Body.Close()

	if verbosity > 0 {
		// The reason phrase is whatever the storage host wrote on the status
		// line; net/http hands it back verbatim. req.URL above is an already
		// parsed *url.URL, which cannot hold an ASCII control character.
		fmt.Fprintf(os.Stderr, "← %d %s\n", resp.StatusCode, terminal.Sanitize(resp.Status))
	}

	// Accept 200, 201, 204 as success for uploads
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("upload failed with status %d (could not read response body)", resp.StatusCode)
		}
		return fmt.Errorf("upload failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}
