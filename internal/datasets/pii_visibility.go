package datasets

import (
	"time"

	"github.com/qlustered/qctl/internal/api"
)

// PiiVisibility is the caller's own visibility of a table's personal data.
// Status is the server's value verbatim; ExpiresAt is RFC3339 in UTC, or nil
// when the server sent no expiry.
type PiiVisibility struct {
	Status    string  `json:"status" yaml:"status"`
	ExpiresAt *string `json:"expires_at" yaml:"expires_at"`
}

// NewPiiVisibility converts the dataset response's pii_visibility, returning
// nil when v is nil.
func NewPiiVisibility(v *api.PiiVisibilitySchema) *PiiVisibility {
	if v == nil {
		return nil
	}
	out := &PiiVisibility{Status: string(v.Status)}
	if v.ExpiresAt != nil && !v.ExpiresAt.IsZero() {
		expires := v.ExpiresAt.UTC().Format(time.RFC3339)
		out.ExpiresAt = &expires
	}
	return out
}

// FormatPiiVisibility renders v as one line. A nil v renders "(not reported by
// this server)", and an unknown status renders verbatim.
func FormatPiiVisibility(v *PiiVisibility) string {
	if v == nil {
		return "(not reported by this server)"
	}
	var line string
	switch api.PiiVisibilitySchemaStatus(v.Status) {
	case api.Masked:
		line = "masked"
	case api.UnmaskedByRole:
		line = "unmasked by your role"
	case api.UnmaskedByGrant:
		line = "unmasked by a grant"
	default:
		line = v.Status
	}
	if v.ExpiresAt != nil {
		line += " until " + *v.ExpiresAt
	}
	return line
}
