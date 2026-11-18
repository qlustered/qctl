package table_series

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ValidateReference rejects a blank series reference.
func ValidateReference(ref string) error {
	if strings.TrimSpace(ref) == "" {
		return qerrors.NewBadUsage("table series reference cannot be empty")
	}
	return nil
}

// ResolveID resolves a full UUID or an exact series name.
func (c *Client) ResolveID(token, ref string) (uuid.UUID, error) {
	if err := ValidateReference(ref); err != nil {
		return uuid.Nil, err
	}
	if len(ref) == 36 {
		if id, err := uuid.Parse(ref); err == nil {
			return id, nil
		}
	}
	items, err := c.List(token)
	if err != nil {
		return uuid.Nil, err
	}
	for _, item := range items {
		if item.Name == ref {
			return item.ID, nil
		}
	}
	return uuid.Nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("table series %q not found in this organization", ref)))
}
