package output

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

// stubTerminal makes isTerminal report true for tty alone, for the rest of
// the test.
func stubTerminal(t *testing.T, tty io.Writer) {
	t.Helper()
	orig := isTerminal
	isTerminal = func(w io.Writer) bool { return w == tty }
	t.Cleanup(func() { isTerminal = orig })
}

// maxColumnWidthFor runs a subcommand of a root that registers
// --max-column-width as the real root does — persistent, default 0 — and
// returns what MaxColumnWidth saw from inside it.
func maxColumnWidthFor(t *testing.T, stdout, stderr io.Writer, args ...string) int {
	t.Helper()
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().Int("max-column-width", 0, "")
	got := -1
	root.AddCommand(&cobra.Command{
		Use: "sub",
		RunE: func(cmd *cobra.Command, _ []string) error {
			got = MaxColumnWidth(cmd)
			return nil
		},
	})
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.SetArgs(append([]string{"sub"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return got
}

func TestMaxColumnWidth(t *testing.T) {
	tty := &bytes.Buffer{}
	pipe := &bytes.Buffer{}
	stubTerminal(t, tty)

	tests := []struct {
		name   string
		stdout io.Writer
		stderr io.Writer
		args   []string
		want   int
	}{
		{name: "a terminal gets 80", stdout: tty, stderr: pipe, want: 80},
		{name: "a pipe gets no limit", stdout: pipe, stderr: pipe, want: 0},
		{name: "a terminal on stderr alone is not one on stdout", stdout: pipe, stderr: tty, want: 0},
		{name: "a passed width wins on a pipe", stdout: pipe, stderr: pipe, args: []string{"--max-column-width=40"}, want: 40},
		{name: "a passed width wins on a terminal", stdout: tty, stderr: pipe, args: []string{"--max-column-width", "120"}, want: 120},
		{name: "a passed 0 lifts the terminal's cap", stdout: tty, stderr: pipe, args: []string{"--max-column-width=0"}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxColumnWidthFor(t, tt.stdout, tt.stderr, tt.args...); got != tt.want {
				t.Errorf("MaxColumnWidth() = %d, want %d", got, tt.want)
			}
		})
	}
}

// A command that never registered the flag still gets the stdout default.
func TestMaxColumnWidthWithoutTheFlag(t *testing.T) {
	tty := &bytes.Buffer{}
	stubTerminal(t, tty)

	cmd := &cobra.Command{}
	cmd.SetOut(tty)
	if got := MaxColumnWidth(cmd); got != 80 {
		t.Errorf("on a terminal: MaxColumnWidth() = %d, want 80", got)
	}
	cmd.SetOut(&bytes.Buffer{})
	if got := MaxColumnWidth(cmd); got != 0 {
		t.Errorf("on a pipe: MaxColumnWidth() = %d, want 0", got)
	}
}

// The real check: a buffer and a regular file are not terminals.
func TestIsTerminalRejectsNonTerminals(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) {
		t.Error("isTerminal(*bytes.Buffer) = true")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "out"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("isTerminal(regular file) = true")
	}
}
