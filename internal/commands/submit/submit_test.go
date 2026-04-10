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

	if cmd.Short != "Submit rule or table kind definitions" {
		t.Errorf("Expected Short to be 'Submit rule or table kind definitions', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) != 2 {
		t.Errorf("Expected submit command to have 2 subcommands, got %d", len(subcommands))
	}

	foundRules := false
	foundTableKinds := false
	for _, sub := range subcommands {
		if sub.Name() == "rules" {
			foundRules = true
		}
		if sub.Name() == "table-kinds" {
			foundTableKinds = true
		}
	}
	if !foundRules {
		t.Error("Expected submit command to have rules subcommand")
	}
	if !foundTableKinds {
		t.Error("Expected submit command to have table-kinds subcommand")
	}
}
