package version

import (
	"bytes"
	"os"
	"strings"
	"testing"

	internalversion "github.com/qlustered/qctl/internal/version"
)

func TestNewCommand(t *testing.T) {
	cmd := NewCommand()
	if cmd == nil {
		t.Fatal("NewCommand returned nil")
	}

	if cmd.Use != "version" {
		t.Errorf("Expected Use to be 'version', got '%s'", cmd.Use)
	}
}

func TestRunVersion(t *testing.T) {
	// Save original stdout
	oldStdout := os.Stdout
	defer func() { os.Stdout = oldStdout }()

	// Capture stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := NewCommand()
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// Restore stdout and read captured output
	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Check output contains version info
	if !strings.Contains(output, "qctl version") {
		t.Errorf("Output should contain 'qctl version', got: %s", output)
	}

	if !strings.Contains(output, internalversion.Version) {
		t.Errorf("Output should contain version '%s', got: %s", internalversion.Version, output)
	}

	if !strings.Contains(output, internalversion.Commit) {
		t.Errorf("Output should contain commit '%s', got: %s", internalversion.Commit, output)
	}

	// Note: Output may optionally contain "Current context:" if a context is set
	// but we don't require it in this test since no config file is set up
}
