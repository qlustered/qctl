package apierror

import (
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// withholdable are the permissions an organization can withhold from a role
// that grants them by turning off data editing (atlas common/common/auth_types.py
// role_permissions).
var withholdable = map[string]bool{"dataset:data:write": true, "source:data:write": true}

// WithPermissionNote appends the operation's required permission and granting roles to a 403 error.
func WithPermissionNote(err error, statusCode int, operationID string) error {
	if err == nil || statusCode != http.StatusForbidden {
		return err
	}
	permission := api.OperationPermissions[operationID]
	if permission == "" {
		return err
	}
	var roles []string
	for role, permissions := range api.RolePermissions {
		if slices.Contains(permissions, permission) {
			roles = append(roles, role)
		}
	}
	slices.Sort(roles)
	note := "This operation needs " + permission
	if len(roles) > 0 {
		note += ", held by " + strings.Join(roles, ", ")
	}
	if withholdable[permission] {
		note += "; your organization can withhold it by turning off data editing, " +
			"so check your role and its withheld permissions with `qctl auth me`."
	} else {
		note += "; check your role with `qctl auth me`."
	}
	return fmt.Errorf("%w\n\n%s", err, note)
}
