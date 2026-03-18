package root

import (
	"testing"

	internalversion "github.com/qlustered/qctl/internal/version"
)

func TestExecute(t *testing.T) {
	// Basic test to ensure Execute doesn't panic
	// More comprehensive tests will be added as commands are implemented
	// Expected to fail without arguments, but shouldn't panic
	_ = Execute()
}

func TestGetRootCmd(t *testing.T) {
	cmd := GetRootCmd()
	if cmd == nil {
		t.Fatal("GetRootCmd returned nil")
	}

	if cmd.Use != "qctl" {
		t.Errorf("Expected Use to be 'qctl', got '%s'", cmd.Use)
	}
}

func TestVersion(t *testing.T) {
	if internalversion.Version == "" {
		t.Error("Version should not be empty")
	}
	if internalversion.Commit == "" {
		t.Error("Commit should not be empty")
	}
}
