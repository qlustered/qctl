package apply

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

func TestApplyLongPointsToGuide(t *testing.T) {
	long := NewCommand().Long
	if !strings.Contains(long, "qctl guide instantiate-rule") {
		t.Errorf("apply Long should point to the guide; got:\n%s", long)
	}
}

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

func TestApplyYesFlagIsPersistent(t *testing.T) {
	f := NewCommand().PersistentFlags().Lookup("yes")
	if f == nil {
		t.Fatal("expected apply to register --yes as a persistent flag")
	}
	if f.Shorthand != "y" {
		t.Errorf("expected --yes shorthand 'y', got %q", f.Shorthand)
	}
}

func TestApplySubcommandsAcceptYesFlag(t *testing.T) {
	for _, sub := range []string{"table", "destination", "cloud-source", "linked-table"} {
		root := setupApplyRoot()
		var buf bytes.Buffer
		root.SetOut(&buf)
		root.SetErr(&buf)
		root.SetArgs([]string{"apply", sub, "--yes"})
		err := root.Execute()
		if err == nil {
			t.Fatalf("[%s] expected an error (filename is required), got nil", sub)
		}
		if strings.Contains(err.Error(), "unknown flag") {
			t.Errorf("[%s] --yes should be accepted, got: %v", sub, err)
		}
	}
}

// The four typed subcommands load their -f file through readManifestDocuments,
// which reports a missing file and a broken manifest in its own words: the file
// is named on the read failure, and both envelope failures read "failed to
// parse manifest". That dropped the older "failed to load manifest: " prefix,
// and the wording was accepted deliberately — the generic and typed paths now
// agree. Nothing else enforces it, so this is what stops a later change from
// silently restoring the prefix and reversing the ruling.
func TestApplyTypedSubcommandManifestLoadErrors(t *testing.T) {
	env := testutil.NewTestEnv(t)
	defer env.Cleanup()

	missingPath := filepath.Join(t.TempDir(), "missing.yaml")
	noAPIVersion := env.CreateFile("no-apiversion.yaml", "kind: Table\nmetadata:\n  name: orders\n")
	brokenYAML := env.CreateFile("broken.yaml", "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: [unclosed\n")

	paths := []struct {
		name     string
		path     string
		want     string
		wantCode int
	}{
		// An unreadable path is an I/O failure, not a malformed manifest: it keeps
		// exit 1 on purpose. The other two are the user's file being wrong.
		{"missing file", missingPath, "failed to read file " + missingPath + ": ", qerrors.ExitGenericError},
		{"missing apiVersion", noAPIVersion, "failed to parse manifest: validation error: apiVersion: required field is missing", qerrors.ExitBadUsage},
		{"YAML syntax error", brokenYAML, "failed to parse manifest: yaml: line 3: ", qerrors.ExitBadUsage},
	}

	for _, sub := range []string{"table", "destination", "cloud-source", "linked-table"} {
		for _, tc := range paths {
			t.Run(sub+"/"+tc.name, func(t *testing.T) {
				root := setupApplyRoot()
				root.SilenceUsage = true
				root.SilenceErrors = true
				root.SetOut(&bytes.Buffer{})
				root.SetErr(&bytes.Buffer{})
				root.SetArgs([]string{"apply", sub, "-f", tc.path})

				err := root.Execute()
				if err == nil {
					t.Fatal("expected an error")
				}
				if !strings.Contains(err.Error(), tc.want) {
					t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
				}
				if strings.Contains(err.Error(), "failed to load manifest") {
					t.Errorf("the 'failed to load manifest: ' prefix was deliberately dropped from this path; got: %v", err)
				}
				if got := qerrors.GetExitCode(err); got != tc.wantCode {
					t.Errorf("exit code = %d, want %d", got, tc.wantCode)
				}
			})
		}
	}
}

func TestApplyDatasetShortMentionsDataset(t *testing.T) {
	if got := NewDatasetCommand().Short; got != "Apply a table (dataset) configuration from a file" {
		t.Errorf("dataset Short should mention (dataset); got: %q", got)
	}
}

func TestApplyTableCrossReferencesLinkedTable(t *testing.T) {
	if long := NewDatasetCommand().Long; !strings.Contains(long, "qctl apply linked-table") {
		t.Errorf("apply table Long should cross-reference linked-table; got:\n%s", long)
	}
}

func TestApplyLinkedTableCrossReferencesTable(t *testing.T) {
	if long := NewLinkedTableCommand().Long; !strings.Contains(long, "qctl apply table") {
		t.Errorf("apply linked-table Long should cross-reference table; got:\n%s", long)
	}
}

func TestApplyLongListsValidKinds(t *testing.T) {
	long := NewCommand().Long
	// Generated from genericKinds (sorted), so it can never fall out of sync
	// with the generic dispatcher. Asserting the exact comma-delimited set
	// (not just substring containment) catches drift where a kind name that
	// is a substring of another (e.g. "Table" in "TableRule", "Rule" in
	// "TableRule"/"RuleReviewPolicy") would otherwise mask a dropped row.
	var kindsLine string
	for _, ln := range strings.Split(long, "\n") {
		if strings.HasPrefix(ln, "Valid kinds:") {
			kindsLine = ln
			break
		}
	}
	if kindsLine == "" {
		t.Fatalf("apply Long missing the 'Valid kinds:' line; got:\n%s", long)
	}
	gotKinds := strings.Split(strings.TrimPrefix(kindsLine, "Valid kinds: "), ", ")
	wantKinds := []string{"BackupSettings", "ChangeReviewPolicy", "CloudSource", "DataReviewPolicy", "Deliverable", "Destination", "LinkedTable", "NotificationPreferences", "Rule", "RuleReviewPolicy", "StandardField", "Table", "TableKind", "TableRule", "TableSchema", "TableSeries"}
	if len(gotKinds) != len(wantKinds) {
		t.Fatalf("got %d kinds, want %d; line: %q", len(gotKinds), len(wantKinds), kindsLine)
	}
	for i, want := range wantKinds {
		if gotKinds[i] != want {
			t.Errorf("kind %d = %q, want %q; line: %q", i, gotKinds[i], want, kindsLine)
		}
	}
}

// `-f ""` satisfies cobra's MarkFlagRequired (the flag was Changed) and lands
// on each subcommand's own check. Naming no file is a command line to fix.
func TestApplyTypedSubcommandsRejectAnEmptyFilenameWithExitTwo(t *testing.T) {
	for _, sub := range []string{"table", "destination", "backup-settings", "cloud-source", "linked-table"} {
		t.Run(sub, func(t *testing.T) {
			_, err := execApplySub(t, sub, "")
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), "filename is required (-f or --filename)") {
				t.Errorf("error = %q", err.Error())
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}
