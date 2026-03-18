package cmdutil

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
)

// reader is the input source for prompts. Defaults to os.Stdin.
// Can be overridden in tests.
var reader io.Reader = os.Stdin

// SetReader sets the input reader for prompts (used in tests).
func SetReader(r io.Reader) {
	reader = r
}

// ResetReader resets the input reader to os.Stdin.
func ResetReader() {
	reader = os.Stdin
}

// ConfirmYesNo prompts the user for yes/no confirmation.
// Returns true if the user confirms (y/yes), false otherwise.
// The prompt should not include the [y/N] suffix - it will be added automatically.
func ConfirmYesNo(prompt string) (bool, error) {
	fmt.Printf("%s [y/N]: ", prompt)
	bufReader := bufio.NewReader(reader)
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

// ConfirmYesNoDefault prompts the user for yes/no confirmation with a default value.
// If defaultYes is true, pressing enter without input returns true.
func ConfirmYesNoDefault(prompt string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}

	fmt.Printf("%s %s: ", prompt, suffix)
	bufReader := bufio.NewReader(reader)
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
func PromptString(prompt string) (string, error) {
	fmt.Print(prompt)
	bufReader := bufio.NewReader(reader)
	response, err := bufReader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("failed to read input: %w", err)
	}
	return strings.TrimSpace(response), nil
}
