package kill

import (
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "kill" {
		t.Errorf("Expected Use to be 'kill', got '%s'", cmd.Use)
	}

	if cmd.Short != "Kill resources" {
		t.Errorf("Expected Short to be 'Kill resources', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) == 0 {
		t.Error("Expected kill command to have subcommands")
	}

	// Check for ingestion-job subcommand
	found := false
	for _, sub := range subcommands {
		if sub.Use == "ingestion-job [id...]" || sub.Name() == "ingestion-job" {
			found = true
			break
		}
	}
	if !found {
		t.Error("Expected kill command to have ingestion-job subcommand")
	}
}
