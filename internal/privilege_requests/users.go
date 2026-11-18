package privilege_requests //nolint:revive // Established package-path style in this repo (rule_reviews, comment_threads, value_mapping).

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Directory is the org-membership snapshot --target-user resolves through and
// the audit table names people with.
type Directory = comments.Directory

// FetchUsers sweeps the CURRENT org's membership into a Directory. Fetch once
// per command invocation.
//
// The privilege routes are platform-wide but this directory is not: from a
// customer org an HQ admin's email may not resolve, and a UUID is the only
// selector that always works (Directory.Resolve passes one through unchecked).
func (c *Client) FetchUsers(accessToken string) (*Directory, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	return comments.FetchUsers(apiClient, c.organizationID)
}

// CheckTargetUser refuses a blank --target-user as bad usage (exit 2). Every
// privilege command that takes the flag calls it before Bootstrap, because a
// blank value would otherwise cost a whole directory sweep only to match
// nobody.
func CheckTargetUser(input string) error {
	if strings.TrimSpace(input) == "" {
		return qerrors.NewBadUsage(`--target-user cannot be empty (use an email, a "First Last" name, or a UUID)`)
	}
	return nil
}

// ResolveTargetUser maps a --target-user value to a user UUID. A UUID is used
// as typed, with no directory request and no membership check (surrounding
// whitespace is ignored, as CheckTargetUser and Directory.Resolve ignore it); anything else
// resolves against the current org's directory, which is returned so the
// caller can reuse it rather than sweep the membership twice. No match and an
// ambiguous match are both bad usage (exit 2), raised by Directory.Resolve.
func (c *Client) ResolveTargetUser(accessToken, input string) (openapi_types.UUID, *Directory, error) {
	if id, err := uuid.Parse(strings.TrimSpace(input)); err == nil {
		return id, nil, nil
	}
	dir, err := c.FetchUsers(accessToken)
	if err != nil {
		return openapi_types.UUID{}, nil, fmt.Errorf("failed to resolve --target-user %q: %w", input, err)
	}
	id, err := dir.Resolve(input)
	if err != nil {
		return openapi_types.UUID{}, nil, err
	}
	return id, dir, nil
}
