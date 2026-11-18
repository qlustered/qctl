package prompt

import (
	"io"
	"os"

	"golang.org/x/term"
)

var reader io.Reader = os.Stdin

// Reader returns the input source for prompts.
func Reader() io.Reader {
	return reader
}

// SetReader overrides the input source for prompts.
func SetReader(r io.Reader) {
	reader = r
}

// ResetReader restores stdin as the input source for prompts.
func ResetReader() {
	reader = os.Stdin
}

// CanPrompt reports whether the input source can answer a prompt.
func CanPrompt() bool {
	return reader != os.Stdin || term.IsTerminal(int(os.Stdin.Fd()))
}

// YesFlag returns a suggested command's confirmation suffix.
func YesFlag(yes bool) string {
	if yes || !CanPrompt() {
		return " --yes"
	}
	return ""
}
