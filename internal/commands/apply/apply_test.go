package apply

import (
	"testing"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "apply" {
		t.Errorf("Expected Use to be 'apply', got '%s'", cmd.Use)
	}

	if cmd.Short != "Apply configurations" {
		t.Errorf("Expected Short to be 'Apply configurations', got '%s'", cmd.Short)
	}
}

func TestNewCommand_HasRunE(t *testing.T) {
	cmd := NewCommand()
	if cmd.RunE == nil {
		t.Error("Expected apply command to have RunE set for generic -f dispatch")
	}
}

func TestNewCommand_HasFilenameFlag(t *testing.T) {
	cmd := NewCommand()
	f := cmd.Flags().Lookup("filename")
	if f == nil {
		t.Fatal("Expected apply command to have 'filename' flag")
	}
	if f.Shorthand != "f" {
		t.Errorf("Expected filename flag shorthand to be 'f', got '%s'", f.Shorthand)
	}
}

func TestNewCommand_HasSubcommands(t *testing.T) {
	cmd := NewCommand()

	subcommands := cmd.Commands()
	if len(subcommands) < 3 {
		t.Errorf("Expected apply command to have at least 3 subcommands, got %d", len(subcommands))
	}

	// Check for subcommands
	foundDestination := false
	foundTable := false
	foundDatasetAlias := false
	foundCloudSource := false
	for _, sub := range subcommands {
		switch sub.Name() {
		case "destination":
			foundDestination = true
		case "table":
			foundTable = true
			for _, a := range sub.Aliases {
				if a == "dataset" {
					foundDatasetAlias = true
				}
			}
		case "cloud-source":
			foundCloudSource = true
		}
	}
	if !foundDestination {
		t.Error("Expected apply command to have destination subcommand")
	}
	if !foundTable {
		t.Error("Expected apply command to have table subcommand")
	}
	if !foundDatasetAlias {
		t.Error("Expected apply table subcommand to keep dataset alias")
	}
	if !foundCloudSource {
		t.Error("Expected apply command to have cloud-source subcommand")
	}
}
