package download

import (
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "download" {
		t.Errorf("Expected Use to be 'download', got '%s'", cmd.Use)
	}

	if cmd.Short != "Download resources" {
		t.Errorf("Expected Short to be 'Download resources', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) == 0 {
		t.Error("Expected download command to have subcommands")
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
		t.Error("Expected download command to have file subcommand")
	}
}
