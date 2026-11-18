package upload

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestUploadStorageFailureReportsCleanupOutcome(t *testing.T) {
	cases := []struct {
		name, format string
		cleanupCode  int
		verbose      bool
	}{
		{"cleaned up", "table", http.StatusOK, false},
		{"cleaned up without body", "json", http.StatusNoContent, false},
		{"policy denied", "json", http.StatusForbidden, false},
		{"policy denied verbose", "table", http.StatusForbidden, true},
		{"bad request", "yaml", http.StatusBadRequest, false},
		{"conflict", "name", http.StatusConflict, false},
		{"server failure", "table", http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil),
				storageCode: http.StatusForbidden, cleanupCode: tc.cleanupCode,
			}
			if tc.cleanupCode == http.StatusForbidden {
				f.cleanupBody = map[string]any{"detail": map[string]any{
					"msg": "policy refuses cleanup", "error_code": "CHANGE_REVIEW_POLICY_DENIED",
					"context": map[string]any{"operation": "stored_item_delete", "verb": "resolve", "denied_keys": [][]string{{"file"}}},
				}}
			}
			args := []string{"--table-id", "42", "--cloud-source-id", "7", "-o", tc.format}
			if tc.verbose {
				args = append(args, "-v")
			}
			mock, out, _, err := runDeltaUploadSplit(t, f, args...)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Fatalf("exit code = %d, want 1: %v", got, err)
			}
			message := err.Error()
			if !strings.Contains(message, "failed to upload file to storage") || !strings.Contains(message, "storage upload refused") {
				t.Errorf("missing upload failure: %v", err)
			}
			if tc.cleanupCode < 300 {
				if !strings.Contains(message, "stored_item 99 has been cleaned up") {
					t.Errorf("missing successful cleanup: %v", err)
				}
			} else {
				for _, want := range []string{"failed to clean up stored_item 99", "Next: qctl describe file 99"} {
					if !strings.Contains(message, want) {
						t.Errorf("missing %q: %v", want, err)
					}
				}
				if strings.Contains(message, "has been cleaned up") {
					t.Errorf("failed cleanup claimed success: %v", err)
				}
				wantReason := "cleanup refused"
				if tc.cleanupCode == http.StatusForbidden {
					wantReason = "policy refuses cleanup"
				}
				if !strings.Contains(message, wantReason) {
					t.Errorf("missing cleanup refusal: %v", err)
				}
			}
			if n := countRequests(mock, isZombieCleanup); n != 1 {
				t.Errorf("cleanup calls = %d, want 1", n)
			}
			if n := countRequests(mock, func(r string) bool { return strings.HasSuffix(r, "/publish-for-ingestion") }); n != 0 {
				t.Errorf("publish calls = %d after failed storage upload", n)
			}
			if tc.format != "table" && out != "" {
				t.Errorf("failed structured upload wrote stdout: %q", out)
			}
		})
	}
}
