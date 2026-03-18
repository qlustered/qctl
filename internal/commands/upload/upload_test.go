package upload

import (
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "upload" {
		t.Errorf("Expected Use to be 'upload', got '%s'", cmd.Use)
	}

	if cmd.Short != "Upload resources" {
		t.Errorf("Expected Short to be 'Upload resources', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) == 0 {
		t.Error("Expected upload command to have subcommands")
	}

	// Check for file subcommand
	found := false
	for _, sub := range subcommands {
		if sub.Name() == "file" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected upload command to have file subcommand")
	}
}
