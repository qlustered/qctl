package submit

import (
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "submit" {
		t.Errorf("Expected Use to be 'submit', got '%s'", cmd.Use)
	}

	if cmd.Short != "Submit rule definitions" {
		t.Errorf("Expected Short to be 'Submit rule definitions', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) != 1 {
		t.Errorf("Expected submit command to have 1 subcommand, got %d", len(subcommands))
	}

	foundRules := false
	for _, sub := range subcommands {
		if sub.Name() == "rules" {
			foundRules = true
		}
	}
	if !foundRules {
		t.Error("Expected submit command to have rules subcommand")
	}
}
