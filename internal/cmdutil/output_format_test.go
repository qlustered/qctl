package cmdutil

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// newValidatingRoot mirrors the real root: one persistent --output flag and one
// PersistentPreRunE. Executing through it is the only way to reproduce what
// cobra actually does -- flag parsing merges the root's persistent flags into
// the child's flag set, which is what the shadow check inspects.
func newValidatingRoot(child *cobra.Command) *cobra.Command {
	root := &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return ValidateOutputFormat(cmd)
	}
	root.AddCommand(child)
	return root
}

func newLeaf(annotations map[string]string) *cobra.Command {
	return &cobra.Command{
		Use:         "thing",
		Annotations: annotations,
		RunE:        func(*cobra.Command, []string) error { return nil },
	}
}

func TestValidateOutputFormatAcceptsTheAdvertisedFour(t *testing.T) {
	for _, format := range []string{"table", "json", "yaml", "name"} {
		root := newValidatingRoot(newLeaf(nil))
		root.SetArgs([]string{"thing", "-o", format})
		if err := root.Execute(); err != nil {
			t.Errorf("-o %s: unexpected error: %v", format, err)
		}
	}
}

func TestValidateOutputFormatRejectsUnknownWithExitTwo(t *testing.T) {
	root := newValidatingRoot(newLeaf(nil))
	root.SetArgs([]string{"thing", "-o", "jsonn"})

	err := root.Execute()
	if err == nil {
		t.Fatal("-o jsonn was accepted; a typo'd format must be an error")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage)", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), `unknown output format "jsonn"`) {
		t.Errorf("message does not name the bad value: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "valid: table, json, yaml, name") {
		t.Errorf("message does not list the valid values: %q", err.Error())
	}
}

// The flag's own default is never validated: a command whose declared set
// omits "table" (inspect dry-run-job) must still run with no -o at all.
func TestValidateOutputFormatIgnoresTheUnpassedFlag(t *testing.T) {
	root := newValidatingRoot(newLeaf(WithFormats(nil, "json", "yaml")))
	root.SetArgs([]string{"thing"})
	if err := root.Execute(); err != nil {
		t.Errorf("unpassed --output was validated: %v", err)
	}
}

func TestDeclaredFormatsNarrowTheSet(t *testing.T) {
	root := newValidatingRoot(newLeaf(WithFormats(map[string]string{"resource": "table"}, "table", "json", "yaml")))
	root.SetArgs([]string{"thing", "-o", "name"})

	err := root.Execute()
	if err == nil {
		t.Fatal("-o name was accepted by a command that declares table,json,yaml")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "valid: table, json, yaml") {
		t.Errorf("message must list the declared three: %q", err.Error())
	}
	if strings.Contains(err.Error(), ", name") {
		t.Errorf("message offers a format the command does not support: %q", err.Error())
	}
}

func TestDeclaredFormatsExtendTheSet(t *testing.T) {
	root := newValidatingRoot(newLeaf(WithFormats(nil, "table", "json", "yaml", "code")))
	root.SetArgs([]string{"thing", "-o", "code"})
	if err := root.Execute(); err != nil {
		t.Errorf("-o code rejected on a command that declares it: %v", err)
	}
}

// download file registers its own --output (a file path). It shadows the
// persistent flag by name, so validating it would reject every path.
func TestLocalOutputFlagIsNotValidated(t *testing.T) {
	leaf := newLeaf(nil)
	leaf.Flags().String("output", "", "Output file path")
	root := newValidatingRoot(leaf)
	root.SetArgs([]string{"thing", "--output", "/tmp/report.csv"})
	if err := root.Execute(); err != nil {
		t.Errorf("a command's own --output was validated as a format: %v", err)
	}
}

func TestWithFormatsKeepsTheResourceAnnotation(t *testing.T) {
	got := WithFormats(map[string]string{"resource": "rule"}, "table", "code")
	if got["resource"] != "rule" {
		t.Errorf("resource annotation = %q, want \"rule\"", got["resource"])
	}
	if got[AnnotationOutputFormats] != "table,code" {
		t.Errorf("formats annotation = %q, want \"table,code\"", got[AnnotationOutputFormats])
	}
}

func TestSupportedOutputFormatsFallsBackToTheDefaultFour(t *testing.T) {
	got := SupportedOutputFormats(newLeaf(nil))
	want := []string{"table", "json", "yaml", "name"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("SupportedOutputFormats = %v, want %v", got, want)
	}
}

func writeConfig(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config.SetPathOverride(path)
	t.Cleanup(func() { config.SetPathOverride("") })
}

const configWithOutput = `apiVersion: qctl/v1
currentContext: dev
contexts:
  dev:
    server: https://example.test
    output: %s
`

// newContextAwareRoot is newValidatingRoot with the second half of the real
// hook. It is separate so the Task 1 tests never touch config.Load -- those
// run with no path override and would otherwise read the developer's own
// ~/.qctl/config.
func newContextAwareRoot(child *cobra.Command) *cobra.Command {
	root := newValidatingRoot(child)
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := ValidateOutputFormat(cmd); err != nil {
			return err
		}
		ApplyContextOutputFormat(cmd)
		return nil
	}
	return root
}

func TestContextOutputFillsTheUnpassedFlag(t *testing.T) {
	writeConfig(t, fmt.Sprintf(configWithOutput, "json"))
	cmd := newLeaf(nil)
	root := newContextAwareRoot(cmd)
	root.SetArgs([]string{"thing"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := cmd.Flags().GetString("output"); got != "json" {
		t.Errorf("output = %q, want \"json\" from the context", got)
	}
}

func TestExplicitFlagBeatsTheContext(t *testing.T) {
	writeConfig(t, fmt.Sprintf(configWithOutput, "json"))
	cmd := newLeaf(nil)
	root := newContextAwareRoot(cmd)
	root.SetArgs([]string{"thing", "-o", "yaml"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := cmd.Flags().GetString("output"); got != "yaml" {
		t.Errorf("output = %q, want \"yaml\" -- the flag the user typed", got)
	}
}

// A stored value this command cannot render is a preference, not a demand:
// the command keeps its own default rather than failing on a flag nobody
// passed this run.
func TestContextOutputIsSkippedWhenUnsupported(t *testing.T) {
	writeConfig(t, fmt.Sprintf(configWithOutput, "name"))
	cmd := newLeaf(WithFormats(nil, "table", "json", "yaml"))
	root := newContextAwareRoot(cmd)
	root.SetArgs([]string{"thing"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := cmd.Flags().GetString("output"); got != "table" {
		t.Errorf("output = %q, want the command default \"table\"", got)
	}
}

// A config written before validation existed must not lock the user out of
// every command, config set-context included -- warn and ignore.
func TestInvalidContextOutputWarnsAndIsIgnored(t *testing.T) {
	writeConfig(t, fmt.Sprintf(configWithOutput, "bogus"))
	cmd := newLeaf(nil)
	root := newContextAwareRoot(cmd)
	var stderr bytes.Buffer
	root.SetErr(&stderr)
	root.SetArgs([]string{"thing"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := cmd.Flags().GetString("output"); got != "table" {
		t.Errorf("output = %q, want \"table\"", got)
	}
	if !strings.Contains(stderr.String(), "invalid output format") {
		t.Errorf("no warning on stderr: %q", stderr.String())
	}
}
