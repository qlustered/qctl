package secrets

import "testing"

func TestExactEnvRefMatchesOnlyAWholeReference(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
		ok    bool
	}{
		{"${SECRET}", "SECRET", true},
		{"${A_B_1}", "A_B_1", true},
		{"$SECRET", "", false},
		{"pa$$w0rd", "", false},
		{"x${SECRET}", "", false},
		{"${SECRET}x", "", false},
		{"${SECRET}${SECRET}", "", false},
		{"${}", "", false},
		{"${A$B}", "", false},
		{"${A{B}", "", false},
		{"", "", false},
	} {
		got, ok := ExactEnvRef(tc.input)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ExactEnvRef(%q) = %q, %v; want %q, %v", tc.input, got, ok, tc.want, tc.ok)
		}
	}
}
