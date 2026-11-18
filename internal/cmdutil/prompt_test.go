package cmdutil

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"golang.org/x/term"
)

func TestConfirmYesNo(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"y returns true", "y\n", true},
		{"yes returns true", "yes\n", true},
		{"Y returns true", "Y\n", true},
		{"YES returns true", "YES\n", true},
		{"Yes returns true", "Yes\n", true},
		{"n returns false", "n\n", false},
		{"no returns false", "no\n", false},
		{"N returns false", "N\n", false},
		{"NO returns false", "NO\n", false},
		{"empty returns false", "\n", false},
		{"random text returns false", "maybe\n", false},
		{"whitespace y returns true", "  y  \n", true},
		{"whitespace no returns false", "  no  \n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetReader(strings.NewReader(tt.input))
			defer ResetReader()

			got, err := ConfirmYesNo(io.Discard, "Test prompt")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ConfirmYesNo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConfirmYesNoDefault(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		defaultYes bool
		want       bool
	}{
		// defaultYes = false
		{"default no, empty input returns false", "\n", false, false},
		{"default no, y returns true", "y\n", false, true},
		{"default no, n returns false", "n\n", false, false},
		{"default no, yes returns true", "yes\n", false, true},

		// defaultYes = true
		{"default yes, empty input returns true", "\n", true, true},
		{"default yes, y returns true", "y\n", true, true},
		{"default yes, n returns false", "n\n", true, false},
		{"default yes, no returns false", "no\n", true, false},
		{"default yes, random returns false", "maybe\n", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetReader(strings.NewReader(tt.input))
			defer ResetReader()

			got, err := ConfirmYesNoDefault(io.Discard, "Test prompt", tt.defaultYes)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ConfirmYesNoDefault() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple input", "hello\n", "hello"},
		{"input with spaces", "hello world\n", "hello world"},
		{"empty input", "\n", ""},
		{"leading whitespace trimmed", "  hello\n", "hello"},
		{"trailing whitespace trimmed", "hello  \n", "hello"},
		{"both whitespace trimmed", "  hello  \n", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetReader(strings.NewReader(tt.input))
			defer ResetReader()

			got, err := PromptString(io.Discard, "Enter value: ")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("PromptString() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestConfirmYesNo_ReadError(t *testing.T) {
	// Empty reader without newline will cause EOF error
	SetReader(strings.NewReader(""))
	defer ResetReader()

	_, err := ConfirmYesNo(io.Discard, "Test prompt")
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestPromptString_ReadError(t *testing.T) {
	// Empty reader without newline will cause EOF error
	SetReader(strings.NewReader(""))
	defer ResetReader()

	_, err := PromptString(io.Discard, "Enter: ")
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestCanPrompt_TrueWhenReaderOverridden(t *testing.T) {
	SetReader(strings.NewReader("y\n"))
	t.Cleanup(ResetReader)
	if !CanPrompt() {
		t.Fatal("expected CanPrompt to be true when reader is overridden")
	}
}

func TestConfirmYesNo_NonInteractiveStdinFailsClearly(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	ResetReader() // reader = os.Stdin, which is not a terminal here
	_, err := ConfirmYesNo(io.Discard, "Proceed?")
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("expected ErrNonInteractive, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Errorf("expected error to mention --yes, got: %v", err)
	}
}

func TestConfirmYesNoDefault_NonInteractiveStdinFailsClearly(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	ResetReader()
	_, err := ConfirmYesNoDefault(io.Discard, "Proceed?", true)
	if !errors.Is(err, ErrNonInteractive) {
		t.Fatalf("expected ErrNonInteractive, got: %v", err)
	}
}

// Confirm's two failure exits differ deliberately, and the exit code is the
// thing under test: a substring assertion on the message passes against a
// version that returns a plain fmt.Errorf and silently exits 1.
func TestConfirm_YesSkipsThePrompt(t *testing.T) {
	// reader is os.Stdin (a non-TTY under `go test`), so a Confirm that did
	// not short-circuit on yes would fail rather than return nil.
	ResetReader()
	if err := Confirm(io.Discard, true, "non-interactive", "Proceed?"); err != nil {
		t.Fatalf("--yes must skip the prompt entirely, got: %v", err)
	}
}

func TestConfirm_NonInteractiveIsBadUsage(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	ResetReader() // reader = os.Stdin, which is not a terminal here

	err := Confirm(io.Discard, false, "pass --yes to do the thing", "Proceed?")
	if err == nil {
		t.Fatal("expected a non-interactive confirmation error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "pass --yes to do the thing") {
		t.Errorf("the caller's message must reach the user verbatim, got: %v", err)
	}
}

func TestConfirm_AcceptedAtThePrompt(t *testing.T) {
	SetReader(strings.NewReader("y\n"))
	t.Cleanup(ResetReader)
	if err := Confirm(io.Discard, false, "non-interactive", "Proceed?"); err != nil {
		t.Fatalf("an accepted prompt must return nil, got: %v", err)
	}
}

// Declining is a choice, not a usage error: exit 1, not 2.
func TestConfirm_DeclinedIsNotBadUsage(t *testing.T) {
	SetReader(strings.NewReader("n\n"))
	t.Cleanup(ResetReader)

	err := Confirm(io.Discard, false, "non-interactive", "Proceed?")
	if err == nil {
		t.Fatal("expected an error when the prompt is declined")
	}
	if code := qerrors.GetExitCode(err); code == qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want anything but %d", code, qerrors.ExitBadUsage)
	}
}

// The eighteen ConfirmYesNo call sites across fourteen commands all return
// this one value, so its exit code is theirs. Test the code, not the text: a
// substring assertion passes against the errors.New version that exits 1.
func TestErrNonInteractiveIsBadUsage(t *testing.T) {
	if code := qerrors.GetExitCode(ErrNonInteractive); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}

func TestConfirmYesNo_NonInteractiveIsBadUsage(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	ResetReader() // reader = os.Stdin, which is not a terminal here
	_, err := ConfirmYesNo(io.Discard, "Proceed?")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	// The sentinel must stay comparable, or every errors.Is caller breaks.
	if !errors.Is(err, ErrNonInteractive) {
		t.Error("ErrNonInteractive must remain matchable with errors.Is")
	}
}

func TestConfirmYesNoDefault_NonInteractiveIsBadUsage(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("test requires non-TTY stdin (as under 'go test' / CI)")
	}
	ResetReader()
	_, err := ConfirmYesNoDefault(io.Discard, "Proceed?", true)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !errors.Is(err, ErrNonInteractive) {
		t.Error("ErrNonInteractive must remain matchable with errors.Is")
	}
}

// Nearly every Confirm caller interpolates a server-supplied name into its
// prompt -- "Delete cloud source 42 (<name>)?" -- so the prompt is untrusted
// text on its way to the terminal. Sanitizing inside the helper is what covers
// all of those call sites at once; this is the test that holds it there.
func TestConfirmYesNoEscapesControlCharactersInThePrompt(t *testing.T) {
	SetReader(strings.NewReader("n\n"))
	defer ResetReader()

	var out strings.Builder
	if _, err := ConfirmYesNo(&out, "Delete table \x1b[31morders"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()

	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", got)
	}
	if !strings.Contains(got, `\x1b[31morders`) {
		t.Errorf("prompt must show a visible escape, got: %q", got)
	}
}

func TestConfirmYesNoDefaultEscapesControlCharactersInThePrompt(t *testing.T) {
	SetReader(strings.NewReader("\n"))
	defer ResetReader()

	var out strings.Builder
	if _, err := ConfirmYesNoDefault(&out, "Overwrite \x1b]0;pwned\x07file", true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := out.String()

	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("a raw control character reached the terminal: %q", got)
	}
	// The [Y/n] suffix is ours, not the caller's, and must survive intact.
	if !strings.Contains(got, "[Y/n]") {
		t.Errorf("prompt lost its suffix, got: %q", got)
	}
}

func TestPromptStringEscapesControlCharactersInThePrompt(t *testing.T) {
	SetReader(strings.NewReader("value\n"))
	defer ResetReader()

	var out strings.Builder
	if _, err := PromptString(&out, "Rename \x1b[2Jtable to: "); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	got := out.String()

	if strings.Contains(got, "\x1b") {
		t.Errorf("a raw ESC reached the terminal: %q", got)
	}
}
