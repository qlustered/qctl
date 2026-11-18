package apierror

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestPermissionNoteNamesTheEndpointRequirement(t *testing.T) {
	for _, tc := range []struct {
		operation, permission, roles string
	}{
		{"sign_off_deliverable_run", "deliverable:run:signoff", "admin, member, ops"},
		{"create_deliverable", "deliverable:admin", "admin, agent, editor, member, ops"},
		{"post_destination_model", "destination:create", "admin, ops"},
		{"get_data", "source:data:read", "admin, agent, editor, external, member, ops, read_only"},
		{"submit_data_review_batch", "data_review:request", "admin, agent, editor, external, member, ops"},
		{"publish_stored_item_for_ingestion", "dataset:ingest:upload", "admin, agent, editor, external, member, ops"},
		{"run_sensor_cronjob_now", "dataset:ingest:launch", "admin, agent, editor, member, ops"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			original := HandleHTTPErrorFromBytes(http.StatusForbidden, []byte(`{"detail":{"msg":"Permission denied","context":{"run_id":42}}}`), "request failed")
			got := WithPermissionNote(original, http.StatusForbidden, tc.operation)
			want := "This operation needs " + tc.permission + ", held by " + tc.roles + "; check your role with `qctl auth me`."
			if !strings.HasPrefix(got.Error(), original.Error()) || !strings.HasSuffix(got.Error(), want) {
				t.Errorf("error = %q, want original message followed by %q", got, want)
			}
			if !errors.Is(got, original) || qerrors.GetExitCode(got) != qerrors.ExitUnauthorized {
				t.Fatalf("original error or exit code lost: %v", got)
			}
			var operational *OperationalError
			if !errors.As(got, &operational) || operational.Message != "Permission denied" {
				t.Errorf("operational error lost: %#v", operational)
			}
		})
	}
}

// A role that holds a data-write permission can still lack it when the
// organization turned data editing off, so the note does not stop at the roles.
func TestPermissionNoteNamesAnOrganizationWithholding(t *testing.T) {
	for _, tc := range []struct {
		operation, permission, roles string
	}{
		{"modify_clean_cell", "source:data:write", "admin, agent, editor, external, member, ops"},
		{"delete_rows_in_dataset", "dataset:data:write", "admin, agent, editor, member, ops"},
	} {
		got := WithPermissionNote(qerrors.NewUnauthorized("refused"), http.StatusForbidden, tc.operation)
		want := "This operation needs " + tc.permission + ", held by " + tc.roles +
			"; your organization can withhold it by turning off data editing, " +
			"so check your role and its withheld permissions with `qctl auth me`."
		if !strings.HasSuffix(got.Error(), want) {
			t.Errorf("%s: error = %q, want suffix %q", tc.operation, got, want)
		}
	}
}

func TestPermissionNoteLeavesOtherErrorsAlone(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusUnprocessableEntity, http.StatusInternalServerError} {
		original := HandleHTTPErrorFromBytes(status, []byte(`{"detail":"refused"}`), "request failed")
		if got := WithPermissionNote(original, status, "create_deliverable"); got != original {
			t.Errorf("status %d changed: %v", status, got)
		}
	}
	original := qerrors.NewUnauthorized("refused")
	for _, operation := range []string{"", "unknown_operation", "get_me"} {
		if got := WithPermissionNote(original, http.StatusForbidden, operation); got != original {
			t.Errorf("operation %q changed: %v", operation, got)
		}
	}
	if got := WithPermissionNote(nil, http.StatusForbidden, "create_deliverable"); got != nil {
		t.Errorf("nil error changed: %v", got)
	}
}

func TestPermissionNoteWithoutKnownGrantingRoles(t *testing.T) {
	const operation = "permission_note_test"
	api.OperationPermissions[operation] = "unknown:permission"
	t.Cleanup(func() { delete(api.OperationPermissions, operation) })
	got := WithPermissionNote(qerrors.NewUnauthorized("refused"), http.StatusForbidden, operation)
	if !strings.Contains(got.Error(), "needs unknown:permission; check your role") || strings.Contains(got.Error(), "held by") {
		t.Errorf("error invents granting roles: %v", got)
	}
}

func TestPermissionNotePreservesScopeAdvice(t *testing.T) {
	SetOrgScope("Acme", "--org")
	t.Cleanup(func() { SetOrgScope("", "") })
	original := HandleHTTPErrorFromBytes(http.StatusForbidden, []byte(`{"detail":{"msg":"Wrong organization","error_code":"AUTH_ERR_ORG_MISMATCH"}}`), "request failed")
	got := FinalizeScopeNote(WithPermissionNote(original, http.StatusForbidden, "create_deliverable"))
	if strings.Count(got.Error(), ScopeNote()) != 1 || !strings.HasSuffix(got.Error(), ScopeNote()) {
		t.Errorf("scope advice lost or duplicated: %v", got)
	}
	if !strings.Contains(got.Error(), "AUTH_ERR_ORG_MISMATCH") || !strings.Contains(got.Error(), "This operation needs deliverable:admin") {
		t.Errorf("error code or permission advice lost: %v", got)
	}
	if qerrors.GetExitCode(got) != qerrors.ExitUnauthorized {
		t.Fatalf("exit code lost: %v", got)
	}
}
