package upload

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
)

func activeJobRefusal(state, id string) string {
	return fmt.Sprintf("[Ingestion Job #%s](/orgs/%s/datasets/42/ingestion-jobs/%s) is already in `%s` state for `direct-uploads/data-source-model-7/delta.csv`.", id, testUploadOrgID, id, state)
}

func TestUploadFileActiveJobRefusalCleansUpAndNamesBlockingJob(t *testing.T) {
	for i, state := range []string{"created", "pending", "running", "requeued"} {
		t.Run(state, func(t *testing.T) {
			format := []string{"table", "json", "yaml", "name"}[i]
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil),
				publishCode: http.StatusUnprocessableEntity,
				publishBody: map[string]any{"detail": map[string]any{"msg": activeJobRefusal(state, "555")}},
			}
			mock, out, _, err := runDeltaUploadSplit(t, f, "--table-id", "42", "--cloud-source-id", "7", "-o", format)
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("exit code = %d, want 2; err: %v", got, err)
			}
			var apiErr *stored_items.APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("object-shaped 422 lost its APIError: %v", err)
			}
			for _, want := range []string{
				"Unpublished file record 99 was removed; uploaded bytes remain in backup storage",
				"Wait for ingestion job 555 to finish, then upload the file again.",
				"Next: qctl describe ingestion-job 555",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if strings.Contains(out, "queued for ingestion") || strings.Contains(err.Error(), "re-running ingestion") {
				t.Errorf("refusal claims success or gives the old retry hint: stdout=%q, err=%v", out, err)
			}
			if format != "table" && out != "" {
				t.Errorf("failed structured upload wrote stdout: %q", out)
			}
			requests := mock.Requests()
			wantTail := []string{"PUT /upload/99", "GET " + deltaOrgPath("/stored-items/99/replaced-file"), "PUT " + deltaOrgPath("/stored-items/99/publish-for-ingestion"), "POST " + deltaOrgPath("/stored-items/99/delete")}
			if len(requests) < len(wantTail) || strings.Join(requests[len(requests)-len(wantTail):], "\n") != strings.Join(wantTail, "\n") {
				t.Errorf("upload/publish/cleanup order: %v", requests)
			}
			if n := countRequests(mock, isZombieCleanup); n != 1 {
				t.Errorf("cleanup calls = %d, want 1", n)
			}
		})
	}
}

func TestUploadFileActiveJobCleanupFailureKeepsPublishExitCode(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusConflict, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil),
				publishCode: http.StatusUnprocessableEntity, cleanupCode: status,
				publishBody: map[string]any{"detail": map[string]any{"msg": activeJobRefusal("running", "555")}},
			}
			mock, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Fatalf("cleanup %d changed publish exit code to %d: %v", status, got, err)
			}
			for _, want := range []string{"Failed to remove unpublished file record 99", "cleanup refused", "qctl describe file 99", "qctl describe ingestion-job 555"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "was removed") {
				t.Errorf("failed cleanup reported success: %v", err)
			}
			if n := countRequests(mock, isZombieCleanup); n != 1 {
				t.Errorf("cleanup calls = %d, want 1", n)
			}
		})
	}
}

func TestUploadFileOtherPublishFailuresKeepTheRecord(t *testing.T) {
	cases := []struct {
		name, message string
		status, exit  int
	}{
		{"blocked job", activeJobRefusal("blocked", "555"), 422, 2},
		{"finished job", activeJobRefusal("finished", "555"), 422, 2},
		{"zero job id", activeJobRefusal("pending", "0"), 422, 2},
		{"overflowing job id", activeJobRefusal("pending", "999999999999999999999999"), 422, 2},
		{"unrecognized validation", "cannot publish this file", 422, 2},
		{"unauthenticated", "login expired", 401, 4},
		{"forbidden", "publish denied", 403, 4},
		{"missing file", "file not found", 404, 3},
		{"conflict with matching text", activeJobRefusal("pending", "555"), 409, 2},
		{"server failure with matching text", activeJobRefusal("running", "555"), 500, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &deltaFixture{
				table: deltaTable("mirror", []string{"id"}), source: deltaSource(deltaTableID, nil),
				publishCode: tc.status, publishBody: map[string]any{"detail": map[string]any{"msg": tc.message}},
			}
			mock, _, err := runDeltaUpload(t, f, "--table-id", "42", "--cloud-source-id", "7")
			if got := qerrors.GetExitCode(err); got != tc.exit {
				t.Fatalf("exit code = %d, want %d: %v", got, tc.exit, err)
			}
			for _, want := range []string{"File record 99 was kept", "Next: qctl describe file 99"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error missing %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "Wait for ingestion job") || strings.Contains(err.Error(), "re-running ingestion") {
				t.Errorf("unrelated refusal has active-job retry advice: %v", err)
			}
			if n := countRequests(mock, isZombieCleanup); n != 0 {
				t.Errorf("cleanup calls = %d, want 0", n)
			}
		})
	}
}
