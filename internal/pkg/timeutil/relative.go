package timeutil

import (
	"fmt"
	"time"
)

// FormatRelative returns a human-friendly relative time string like "5 minutes ago".
// For zero times, it returns an empty string.
func FormatRelative(t time.Time) string {
	if t.IsZero() {
		return ""
	}

	now := time.Now()
	diff := now.Sub(t)

	// Handle future times
	if diff < 0 {
		return t.Format(time.RFC3339)
	}

	seconds := int64(diff.Seconds())
	minutes := int64(diff.Minutes())
	hours := int64(diff.Hours())
	days := hours / 24
	weeks := days / 7
	months := days / 30
	years := days / 365

	switch {
	case seconds < 60:
		return pluralize(seconds, "second")
	case minutes < 60:
		return pluralize(minutes, "minute")
	case hours < 24:
		return pluralize(hours, "hour")
	case days < 7:
		return pluralize(days, "day")
	case days < 30:
		return pluralize(weeks, "week")
	case days < 365:
		return pluralize(months, "month")
	default:
		return pluralize(years, "year")
	}
}

// FormatRelativePtr handles nil pointers, returning an empty string for nil.
func FormatRelativePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return FormatRelative(*t)
}

// pluralize returns a formatted string like "5 minutes ago" or "1 minute ago".
func pluralize(n int64, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s ago", n, unit)
	}
	return fmt.Sprintf("%d %ss ago", n, unit)
}
