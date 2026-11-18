package dataset_kinds //nolint:revive // Public package name matches the established resource domain.

import "github.com/qlustered/qctl/internal/comments"

// APIError carries the backend's error code and context.
type APIError = comments.APIError

var wrapAPIError = comments.WrapAPIError
