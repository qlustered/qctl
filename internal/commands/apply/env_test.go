package apply

import "testing"

func TestExpandEnvVarsOnlyExactReference(t *testing.T) {
	t.Setenv("APPLY_SECRET", "from$env")
	t.Setenv("APPLY_EMPTY", "")
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"${APPLY_SECRET}", "from$env"},
		{"${APPLY_EMPTY}", ""},
		{"${APPLY_UNSET}", ""},
		{"pa$$w0rd", "pa$$w0rd"},
		{"p@ss$word", "p@ss$word"},
		{"x${APPLY_UNSET}", "x${APPLY_UNSET}"},
		{"${APPLY_SECRET}${APPLY_SECRET}", "${APPLY_SECRET}${APPLY_SECRET}"},
		{"${}", "${}"},
	} {
		if got := expandEnvVars(tc.input); got != tc.want {
			t.Errorf("expandEnvVars(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
