package slug

import (
	"strings"
	"testing"
)

func TestGenerate(t *testing.T) {
	for name, want := range map[string]string{
		"Car Policy":          "car-policy",
		"123 Bags":            "d-123-bags",
		"東京":                  "",
		"Café + More":         "cafe-+-more",
		"Carrier A bordereau": "carrier-a-bordereau",
		"Ünïcødé  Name!":      "unicde-name",
		"+plus":               "d-+plus",
		"_under":              "d-_under",
	} {
		if got := Generate(name, 0); got != want {
			t.Errorf("Generate(%q, 0) = %q, want %q", name, got, want)
		}
	}
}

func TestGenerateDoesNotCutWhenMaxLenIsZero(t *testing.T) {
	name := strings.Repeat("a", 70)
	if got := Generate(name, 0); got != name {
		t.Errorf("Generate(70 a's, 0) = %q (len %d), want uncut", got, len(got))
	}
}

// Expected values are the outputs of atlas's generate_slug(name, 59).
func TestGenerateCutsToMaxLen(t *testing.T) {
	long := strings.Repeat("x", 60)
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"plain", "Carrier A bordereau", "carrier-a-bordereau"},
		{"cut to 59 a's", strings.Repeat("a", 70), strings.Repeat("a", 59)},
		{"cut after d- prefix", "2026 report " + long, "d-2026-report-" + strings.Repeat("x", 45)},
		{"trailing hyphen kept", strings.Repeat("x", 58) + " y", strings.Repeat("x", 58) + "-"},
		{"non-ascii", "Ünïcødé  Name!", "unicde-name"},
		{"plus prefix", "+plus", "d-+plus"},
		{"underscore prefix", "_under", "d-_under"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Generate(tc.in, 59)
			if got != tc.want {
				t.Errorf("Generate(%q, 59) = %q, want %q", tc.in, got, tc.want)
			}
			if len(got) > 59 {
				t.Errorf("Generate(%q, 59) has length %d", tc.in, len(got))
			}
		})
	}
}
