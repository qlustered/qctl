package prompt

import (
	"os"
	"strings"
	"testing"
)

func TestSuggestedYesFlag(t *testing.T) {
	pipe, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pipe.Close()
	defer writer.Close()
	stdin := os.Stdin
	os.Stdin = pipe
	t.Cleanup(func() { os.Stdin = stdin; ResetReader() })
	for _, tc := range []struct {
		interactive, yes bool
		want             string
	}{
		{false, false, " --yes"},
		{false, true, " --yes"},
		{true, false, ""},
		{true, true, " --yes"},
	} {
		ResetReader()
		if tc.interactive {
			SetReader(strings.NewReader("y\n"))
		}
		if got := YesFlag(tc.yes); got != tc.want {
			t.Errorf("interactive=%t yes=%t: suffix=%q, want %q", tc.interactive, tc.yes, got, tc.want)
		}
	}
}
