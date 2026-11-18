package timeutil

import (
	"testing"
	"time"
)

func TestFormatRelative(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "zero time",
			input:    time.Time{},
			expected: "",
		},
		{
			name:     "1 second ago",
			input:    now.Add(-1 * time.Second),
			expected: "1 second ago",
		},
		{
			name:     "30 seconds ago",
			input:    now.Add(-30 * time.Second),
			expected: "30 seconds ago",
		},
		{
			name:     "59 seconds ago",
			input:    now.Add(-59 * time.Second),
			expected: "59 seconds ago",
		},
		{
			name:     "1 minute ago",
			input:    now.Add(-1 * time.Minute),
			expected: "1 minute ago",
		},
		{
			name:     "5 minutes ago",
			input:    now.Add(-5 * time.Minute),
			expected: "5 minutes ago",
		},
		{
			name:     "59 minutes ago",
			input:    now.Add(-59 * time.Minute),
			expected: "59 minutes ago",
		},
		{
			name:     "1 hour ago",
			input:    now.Add(-1 * time.Hour),
			expected: "1 hour ago",
		},
		{
			name:     "5 hours ago",
			input:    now.Add(-5 * time.Hour),
			expected: "5 hours ago",
		},
		{
			name:     "23 hours ago",
			input:    now.Add(-23 * time.Hour),
			expected: "23 hours ago",
		},
		{
			name:     "1 day ago",
			input:    now.Add(-24 * time.Hour),
			expected: "1 day ago",
		},
		{
			name:     "3 days ago",
			input:    now.Add(-3 * 24 * time.Hour),
			expected: "3 days ago",
		},
		{
			name:     "6 days ago",
			input:    now.Add(-6 * 24 * time.Hour),
			expected: "6 days ago",
		},
		{
			name:     "1 week ago",
			input:    now.Add(-7 * 24 * time.Hour),
			expected: "1 week ago",
		},
		{
			name:     "2 weeks ago",
			input:    now.Add(-14 * 24 * time.Hour),
			expected: "2 weeks ago",
		},
		{
			name:     "4 weeks ago",
			input:    now.Add(-28 * 24 * time.Hour),
			expected: "4 weeks ago",
		},
		{
			name:     "1 month ago",
			input:    now.Add(-30 * 24 * time.Hour),
			expected: "1 month ago",
		},
		{
			name:     "3 months ago",
			input:    now.Add(-90 * 24 * time.Hour),
			expected: "3 months ago",
		},
		{
			name:     "11 months ago",
			input:    now.Add(-330 * 24 * time.Hour),
			expected: "11 months ago",
		},
		{
			name:     "1 year ago",
			input:    now.Add(-365 * 24 * time.Hour),
			expected: "1 year ago",
		},
		{
			name:     "2 years ago",
			input:    now.Add(-730 * 24 * time.Hour),
			expected: "2 years ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatRelative(tt.input)
			if result != tt.expected {
				t.Errorf("FormatRelative() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestFormatRelative_FutureTime(t *testing.T) {
	future := time.Now().Add(1 * time.Hour)
	result := FormatRelative(future)

	// Future times should return RFC3339 format
	if result == "" {
		t.Error("FormatRelative() should not return empty string for future time")
	}
	// Verify it's a valid RFC3339 format by parsing it
	_, err := time.Parse(time.RFC3339, result)
	if err != nil {
		t.Errorf("FormatRelative() for future time should return RFC3339 format, got %q", result)
	}
}

func TestFormatRelativePtr(t *testing.T) {
	now := time.Now()
	fiveMinutesAgo := now.Add(-5 * time.Minute)

	tests := []struct {
		name     string
		input    *time.Time
		expected string
	}{
		{
			name:     "nil pointer",
			input:    nil,
			expected: "",
		},
		{
			name:     "valid time pointer",
			input:    &fiveMinutesAgo,
			expected: "5 minutes ago",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FormatRelativePtr(tt.input)
			if result != tt.expected {
				t.Errorf("FormatRelativePtr() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestPluralize(t *testing.T) {
	tests := []struct {
		n        int64
		unit     string
		expected string
	}{
		{1, "second", "1 second ago"},
		{2, "second", "2 seconds ago"},
		{1, "minute", "1 minute ago"},
		{5, "minute", "5 minutes ago"},
		{1, "hour", "1 hour ago"},
		{12, "hour", "12 hours ago"},
		{1, "day", "1 day ago"},
		{7, "day", "7 days ago"},
		{1, "week", "1 week ago"},
		{3, "week", "3 weeks ago"},
		{1, "month", "1 month ago"},
		{6, "month", "6 months ago"},
		{1, "year", "1 year ago"},
		{10, "year", "10 years ago"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := pluralize(tt.n, tt.unit)
			if result != tt.expected {
				t.Errorf("pluralize(%d, %q) = %q, want %q", tt.n, tt.unit, result, tt.expected)
			}
		})
	}
}
