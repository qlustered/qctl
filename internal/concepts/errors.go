package concepts

import "github.com/qlustered/qctl/internal/comments"

// APIError carries the backend's error code and context.
type APIError = comments.APIError

var wrapAPIError = comments.WrapAPIError
