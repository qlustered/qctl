package catalog_test

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/privilege_requests"
)

// TestPrivilegeNextActionsResolve resolves every command that
// `describe privilege-request` tells its reader to run against the real tree.
// The payload printed `qctl privilege approve|reject <id>` for a whole ticket
// before either verb existed, and nothing noticed.
func TestPrivilegeNextActionsResolve(t *testing.T) {
	root := realRoot(t)
	id := uuid.MustParse("0190aaaa-0000-4000-8000-000000000001")
	payload := privilege_requests.RenderDetail(&privilege_requests.Detail{
		ID: id, Status: api.PrivilegeRequestStatusPending,
		PermissionKey: "is_ops", OldValue: "false", NewValue: "true",
		Justification: "INC-4411: on-call needs ops access",
		RequestedAt:   time.Now(),
		RequestorID:   uuid.MustParse("0190bbbb-0000-4000-8000-000000000002"),
		TargetUserID:  uuid.MustParse("0190bbbb-0000-4000-8000-000000000001"),
	})

	var lines []string
	for _, l := range strings.Split(payload, "\n") {
		if i := strings.Index(l, "qctl "); i >= 0 {
			lines = append(lines, l[i:])
		}
	}
	if len(lines) == 0 {
		t.Fatalf("a Pending request's payload names no qctl command:\n%s", payload)
	}
	for _, line := range lines {
		cmd, err := catalog.ResolveCommandLine(root, line)
		if err != nil {
			t.Errorf("describe privilege-request prints %v", err)
			continue
		}
		rest := strings.Fields(strings.TrimSpace(strings.TrimPrefix(line, cmd.CommandPath())))
		if err := cmd.ParseFlags(rest); err != nil {
			t.Errorf("%q has invalid flags: %v", line, err)
			continue
		}
		if args := cmd.Flags().Args(); len(args) != 1 || args[0] != id.String() {
			t.Errorf("%q resolves to %s with arguments %q, want only the request id", line, cmd.CommandPath(), args)
		}
	}
}
