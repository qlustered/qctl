package completion

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestInstallConfirmation(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		interactive bool
		flags       []string
		wantCode    int
		wantErr     error
		wantHint    bool
	}{
		{name: "non-interactive affirmative input", input: "yes\n", wantCode: qerrors.ExitBadUsage, wantHint: true},
		{name: "non-interactive empty input", wantCode: qerrors.ExitBadUsage, wantHint: true},
		{name: "declined", input: "n\n", interactive: true, wantCode: qerrors.ExitGenericError, wantErr: cmdutil.ErrDeclined},
		{name: "default decline", input: "\n", interactive: true, wantCode: qerrors.ExitGenericError, wantErr: cmdutil.ErrDeclined},
		{name: "read failure", interactive: true, wantCode: qerrors.ExitGenericError, wantErr: io.EOF},
		{name: "confirmed", input: "y\n", interactive: true},
		{name: "confirmed full word", input: " YES \n", interactive: true},
		{name: "yes skips non-interactive input", flags: []string{"--yes"}},
		{name: "short yes skips non-interactive input", flags: []string{"-y"}},
		{name: "yes skips declined input", input: "n\n", interactive: true, flags: []string{"--yes"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("USERPROFILE", home)
			rcFile := filepath.Join(home, ".zshrc")
			const originalRC = "autoload -U compinit\ncompinit\n"
			if err := os.WriteFile(rcFile, []byte(originalRC), 0600); err != nil {
				t.Fatal(err)
			}

			stdin, err := os.CreateTemp(t.TempDir(), "stdin")
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if _, err := stdin.WriteString(tc.input); err != nil {
				t.Fatal(err)
			}
			if _, err := stdin.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			savedStdin := os.Stdin
			os.Stdin = stdin
			cmdutil.ResetReader()
			t.Cleanup(func() {
				os.Stdin = savedStdin
				cmdutil.ResetReader()
			})
			if tc.interactive {
				cmdutil.SetReader(strings.NewReader(tc.input))
			}

			root := &cobra.Command{Use: "qctl", SilenceErrors: true, SilenceUsage: true}
			root.AddCommand(NewCommand(root))
			var stdout, stderr bytes.Buffer
			root.SetOut(&stdout)
			root.SetErr(&stderr)
			root.SetArgs(append([]string{"completion", "install", "--shell", "zsh"}, tc.flags...))
			err = root.Execute()
			if got := qerrors.GetExitCode(err); got != tc.wantCode {
				t.Fatalf("exit code = %d, want %d; error: %v", got, tc.wantCode, err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantHint && !strings.Contains(err.Error(), "--yes") {
				t.Errorf("non-interactive error must name --yes: %v", err)
			}
			if strings.Contains(stdout.String(), "Proceed?") {
				t.Errorf("confirmation question leaked to stdout: %q", stdout.String())
			}
			if got := strings.Contains(stderr.String(), "Proceed? [y/N]:"); got != (tc.interactive && len(tc.flags) == 0) {
				t.Errorf("unexpected confirmation question on stderr: %q", stderr.String())
			}

			rc, readErr := os.ReadFile(rcFile)
			if readErr != nil {
				t.Fatal(readErr)
			}
			completionDir := filepath.Join(home, ".zsh", "completions")
			if tc.wantCode != qerrors.ExitSuccess {
				if _, statErr := os.Stat(completionDir); !os.IsNotExist(statErr) {
					t.Errorf("refused installation created the completion directory: %v", statErr)
				}
				if string(rc) != originalRC {
					t.Errorf("refused installation changed .zshrc: %q", rc)
				}
				return
			}

			script, readErr := os.ReadFile(filepath.Join(completionDir, "_qctl"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Contains(script, []byte("#compdef qctl")) {
				t.Errorf("installed script is not a qctl zsh completion: %q", script)
			}
			if !strings.Contains(string(rc), "fpath=(~/.zsh/completions $fpath)") || !strings.Contains(string(rc), originalRC) {
				t.Errorf("installation did not preserve and configure .zshrc: %q", rc)
			}
		})
	}
}
