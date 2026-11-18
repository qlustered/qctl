package version

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	internalversion "github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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

// runVersionCommand runs args under a root that validates -o and applies a
// context's output, as root.go does.
func runVersionCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("output", "o", "table", "")
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		cmdutil.ApplyContextOutputFormat(cmd)
		return nil
	}
	root.AddCommand(NewCommand())
	var stdout bytes.Buffer
	root.SetOut(&stdout)
	root.SetArgs(args)
	err := root.Execute()
	return stdout.String(), err
}

// setupContext writes a config whose current context is "dev", carrying output.
func setupContext(t *testing.T, output string) {
	t.Helper()
	testutil.NewTestEnv(t)
	cfg := &config.Config{APIVersion: config.APIVersion, CurrentContext: "dev", Contexts: map[string]*config.Context{
		"dev": {Server: "http://localhost:8000", Output: output},
	}}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
}

func TestVersionJSONDocument(t *testing.T) {
	setupContext(t, "")
	stdout, err := runVersionCommand(t, "version", "-o", "json")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	want := map[string]any{
		"version": internalversion.Version, "commit": internalversion.Commit, "current_context": "dev",
	}
	if !reflect.DeepEqual(doc, want) {
		t.Errorf("document = %v, want %v", doc, want)
	}
}

// With no config, current_context is still written, as null.
func TestVersionCurrentContextIsNullWithoutAConfig(t *testing.T) {
	testutil.NewTestEnv(t)
	stdout, err := runVersionCommand(t, "version", "-o", "yaml")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not one YAML document: %v\n%s", err, stdout)
	}
	if v, ok := doc["current_context"]; !ok || v != nil {
		t.Errorf("current_context = %v (present %t), want null", v, ok)
	}
	if doc["version"] != internalversion.Version {
		t.Errorf("version = %v, want %q", doc["version"], internalversion.Version)
	}
}

func TestVersionTableIsUnchanged(t *testing.T) {
	setupContext(t, "")
	stdout, err := runVersionCommand(t, "version")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	want := "qctl version " + internalversion.Version + " (commit: " + internalversion.Commit + ")\nCurrent context: dev\n"
	if stdout != want {
		t.Errorf("stdout = %q, want %q", stdout, want)
	}
}

// A context's output: json reaches a plain qctl version.
func TestVersionFollowsTheContextOutput(t *testing.T) {
	setupContext(t, "json")
	stdout, err := runVersionCommand(t, "version")
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if !json.Valid([]byte(stdout)) {
		t.Errorf("stdout = %q, want the JSON document", stdout)
	}
}

func TestVersionHasNoNameView(t *testing.T) {
	setupContext(t, "")
	stdout, err := runVersionCommand(t, "version", "-o", "name")
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Fatalf("exit code = %d (err %v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want nothing", stdout)
	}
}
