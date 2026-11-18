package cmdutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	qerrors "github.com/qlustered/qctl/internal/errors"
	promptinput "github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/terminal"
)

// SetReader sets the input reader for prompts (used in tests).
func SetReader(r io.Reader) {
	promptinput.SetReader(r)
}

// ResetReader resets the input reader to os.Stdin.
func ResetReader() {
	promptinput.ResetReader()
}

// ErrNonInteractive is returned when a confirmation prompt is required but
// stdin cannot supply an answer (scripts, CI, agent callers). Commands with a
// --yes flag should mention it; this generic message covers the rest.
//
// It is a *qerrors.Error carrying ExitBadUsage, not a bare errors.New: a
// confirmation that cannot be answered is always fixed by changing the command
// line, which is the contract's definition of bad usage (CLAUDE.md, Error and
// exit-code contract). Because every ConfirmYesNo/ConfirmYesNoDefault caller
// propagates this one value untouched, building it here is what grades all of
// them — eighteen sites across fourteen commands. It stays a comparable
// package-level sentinel, so errors.Is(err, ErrNonInteractive) still matches.
var ErrNonInteractive = qerrors.NewBadUsage(
	"confirmation required but stdin is not an interactive terminal " +
		"(pass --yes if the command supports it)")

// ErrDeclined is returned by Confirm when the prompt was answered anything
// other than yes. It is the OTHER half of ErrNonInteractive: a decline is a
// choice, not bad usage, so it stays a bare errors.New and keeps exit 1.
//
// Its message is still "aborted" — the exact string Confirm returned before
// this became a sentinel — because all thirteen Confirm call sites propagate
// it to the user untouched (checked one by one), and turning a message into a
// sentinel is not a licence to reword thirteen commands' output.
//
// It exists so a caller can tell "the user said no" apart from "the prompt
// itself failed" (a read error) without matching on that message. The one
// caller that needs the distinction today is confirmResolution on the alert
// and warning sides, which maps a decline onto pkgmanifest.ErrDeclined so
// manifest.Run skips the document instead of failing it.
var ErrDeclined = errors.New("aborted")

// CanPrompt reports whether a confirmation prompt can actually read input:
// either the reader was overridden (tests), or stdin is a terminal.
func CanPrompt() bool {
	return promptinput.CanPrompt()
}

// ConfirmYesNo writes a yes/no confirmation prompt to out.
// Returns true if the user confirms (y/yes), false otherwise.
// The prompt should not include the [y/N] suffix - it will be added automatically.
//
// The prompt is sanitized here rather than at the call sites: most callers
// interpolate a server-supplied name into it, and this is the one place all of
// them pass through.
func ConfirmYesNo(out io.Writer, prompt string) (bool, error) {
	if !CanPrompt() {
		return false, ErrNonInteractive
	}
	fmt.Fprintf(out, "%s [y/N]: ", terminal.Sanitize(prompt))
	bufReader := bufio.NewReader(promptinput.Reader())
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

// ConfirmYesNoDefault writes a yes/no confirmation prompt to out with a default value.
// If defaultYes is true, pressing enter without input returns true.
func ConfirmYesNoDefault(out io.Writer, prompt string, defaultYes bool) (bool, error) {
	if !CanPrompt() {
		return false, ErrNonInteractive
	}
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}

	fmt.Fprintf(out, "%s %s: ", terminal.Sanitize(prompt), suffix)
	bufReader := bufio.NewReader(promptinput.Reader())
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response == "" {
		return defaultYes, nil
	}
	return response == "y" || response == "yes", nil
}

// PromptString prompts the user for string input.
func PromptString(out io.Writer, prompt string) (string, error) {
	fmt.Fprint(out, terminal.Sanitize(prompt))
	bufReader := bufio.NewReader(promptinput.Reader())
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return strings.TrimSpace(response), nil
}

// Confirm gates a mutating action behind a yes/no prompt, honouring --yes.
//
// The two failure exits differ deliberately: a non-interactive stdin is BAD
// USAGE (exit 2) because the only fix is to change the command line, while
// declining at the prompt is a choice, not a usage error (exit 1). The decline
// is ErrDeclined, so a caller that treats "no" differently from a broken
// prompt can say so with errors.Is rather than a string match.
//
// nonInteractive is the already-formatted message for the first case; it
// should name --yes, since that is the fix.
func Confirm(out io.Writer, yes bool, nonInteractive, prompt string) error {
	if yes {
		return nil
	}
	if !CanPrompt() {
		return qerrors.NewBadUsage(nonInteractive)
	}
	ok, err := ConfirmYesNo(out, prompt)
	if err != nil {
		return err
	}
	if !ok {
		return ErrDeclined
	}
	return nil
}
