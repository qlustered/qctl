package terminal

import (
	"testing"
	"unicode/utf8"
)

func TestTruncateCell(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		width    int
		expected string
	}{
		{name: "disabled", text: "unchanged", width: 0, expected: "unchanged"},
		{name: "ASCII fits", text: "exactly10c", width: 10, expected: "exactly10c"},
		{name: "ASCII truncates", text: "exactly11ch", width: 10, expected: "exactly..."},
		{name: "multibyte bytes fit in cells", text: "éééé", width: 4, expected: "éééé"},
		{name: "multibyte cut is whole", text: "aééééé", width: 5, expected: "aé..."},
		{name: "wide clusters honor the cap", text: "東東東東", width: 7, expected: "東東..."},
		{name: "emoji sequence stays whole", text: "🇺🇸🇺🇸🇺🇸", width: 5, expected: "🇺🇸..."},
		{name: "small width has no marker", text: "long", width: 2, expected: "lo"},
		{name: "wide cluster cannot fit small width", text: "東x", width: 1, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TruncateCell(tt.text, tt.width)
			if got != tt.expected {
				t.Errorf("TruncateCell(%q, %d) = %q, want %q", tt.text, tt.width, got, tt.expected)
			}
			if !utf8.ValidString(got) {
				t.Errorf("TruncateCell(%q, %d) returned invalid UTF-8 %q", tt.text, tt.width, got)
			}
		})
	}
}
