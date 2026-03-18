package cmdutil

import (
	"strings"
	"testing"
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

			got, err := ConfirmYesNo("Test prompt")
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

			got, err := ConfirmYesNoDefault("Test prompt", tt.defaultYes)
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

			got, err := PromptString("Enter value: ")
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

	_, err := ConfirmYesNo("Test prompt")
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestPromptString_ReadError(t *testing.T) {
	// Empty reader without newline will cause EOF error
	SetReader(strings.NewReader(""))
	defer ResetReader()

	_, err := PromptString("Enter: ")
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}
