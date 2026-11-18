package root

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"github.com/spf13/pflag"
)

// preBootstrapUUID is a well-formed id no check below rejects.
const preBootstrapUUID = "018f3c2e-4d5a-7b89-a1c2-3d4e5f607182"

// manifestArg stands in for the path of a case's manifest in its args.
const manifestArg = "@manifest"

const (
	validDryRunSpec = "apiVersion: qluster.ai/v1\nkind: DryRunJob\nmetadata:\n  cloud_source_id: 456\nspec:\n" +
		"  rule_run_specs:\n    - dataset_rule_id: \"550e8400-e29b-41d4-a716-446655440099\"\n"
	tableRulePatchHead = "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + preBootstrapUUID + "\nspec:\n"
)

// runOutsideGetWithoutContext executes the real command tree with HOME at an
// empty directory and the QCTL_* overrides cleared, so there is no config,
// context, server or credential, and returns the error. Every flag the
// invocation set is reset afterwards, because the tree is a package singleton.
func runOutsideGetWithoutContext(t *testing.T, args ...string) error {
	t.Helper()
	testutil.NewTestEnv(t)
	for _, name := range []string{"QCTL_SERVER", "QCTL_ORG", "QCTL_KINDE_HOST", "QCTL_KINDE_CLIENT_ID", "QCTL_INSECURE_HTTP"} {
		t.Setenv(name, "")
	}
	cmd := GetRootCmd()
	for _, f := range []struct{ name, def string }{
		{"org", ""}, {"output", "table"}, {"config", ""}, {"server", ""},
	} {
		resetPersistentFlag(t, cmd, f.name, f.def)
	}
	config.SetPathOverride("")
	cmd.SetArgs(args)
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	executed, err := cmd.ExecuteC()
	t.Cleanup(func() {
		cmd.SetArgs(nil)
		cmd.SetOut(nil)
		cmd.SetErr(nil)
		config.SetPathOverride("")
		if executed == nil {
			return
		}
		executed.Flags().Visit(func(f *pflag.Flag) {
			if slice, ok := f.Value.(pflag.SliceValue); ok {
				_ = slice.Replace(nil)
			} else if setErr := f.Value.Set(f.DefValue); setErr != nil {
				t.Errorf("resetting --%s: %v", f.Name, setErr)
			}
			f.Changed = false
		})
	})
	return err
}

// Flag- and argument-only refusals outside internal/commands/get run before
// Bootstrap. With no context configured, a refusal that ran after it would
// surface as Bootstrap's "no API server specified" at exit 1 instead.
func TestPreBootstrapChecksOutsideGetExitTwo(t *testing.T) {
	longTokenName := strings.Repeat("x", 300)
	cases := []struct {
		name     string
		args     []string
		manifest string
		want     string
	}{
		{name: "unsupported completion shell", args: []string{"completion", "install", "--shell", "bogus"}, want: "unsupported shell: bogus"},
		{name: "review assign", args: []string{"review", "assign", "nope", "--reviewer", "a@b.c"}, want: `invalid review id "nope"`},
		{name: "review unassign", args: []string{"review", "unassign", "nope", "--reviewer", "a@b.c"}, want: `invalid review id "nope"`},
		{name: "review audit id", args: []string{"review", "audit", "nope"}, want: `invalid review id "nope"`},
		{name: "review audit page", args: []string{"review", "audit", preBootstrapUUID, "--page", "0"}, want: "--page must be a positive integer"},
		{name: "review audit limit", args: []string{"review", "audit", preBootstrapUUID, "--limit", "-1"}, want: "--limit must be a positive integer"},
		{name: "review close", args: []string{"review", "close", "nope"}, want: `invalid review id "nope"`},
		{name: "review reopen", args: []string{"review", "reopen", "nope"}, want: `invalid review id "nope"`},
		{name: "review withdraw", args: []string{"review", "withdraw", "nope"}, want: `invalid review id "nope"`},
		{name: "review approve", args: []string{"review", "approve", "nope"}, want: `invalid review id "nope"`},
		{name: "review reject", args: []string{"review", "reject", "nope"}, want: `invalid review id "nope"`},
		{name: "review request-changes", args: []string{"review", "request-changes", "nope"}, want: `invalid review id "nope"`},
		{name: "review re-request", args: []string{"review", "re-request", "nope"}, want: `invalid review id "nope"`},
		{name: "review comment id", args: []string{"review", "comment", "nope", "--body", "x"}, want: `invalid review id "nope"`},
		{name: "review comment --reply-to", args: []string{"review", "comment", preBootstrapUUID, "--body", "x", "--reply-to", "nope"}, want: `invalid comment id "nope"`},
		{name: "review edit-comment review id", args: []string{"review", "edit-comment", "a", "b", "--body", "x"}, want: `invalid review id "a"`},
		{name: "review edit-comment comment id", args: []string{"review", "edit-comment", preBootstrapUUID, "b", "--body", "x"}, want: `invalid comment id "b"`},
		// No --yes: the ids are checked before the confirmation prompt.
		{name: "review delete-comment", args: []string{"review", "delete-comment", "a", "b"}, want: `invalid review id "a"`},
		{name: "review delete-comment comment id", args: []string{"review", "delete-comment", preBootstrapUUID, "b", "--yes"}, want: `invalid comment id "b"`},
		{name: "review link-dry-run id", args: []string{"review", "link-dry-run", "nope", "5"}, want: `invalid review id "nope"`},
		{name: "review link-dry-run --comment", args: []string{"review", "link-dry-run", preBootstrapUUID, "5", "--comment", "nope"}, want: `invalid comment id "nope"`},
		{name: "describe rule-review", args: []string{"describe", "rule-review", "nope"}, want: `invalid review id "nope"`},
		{name: "describe rule-suggestion", args: []string{"describe", "rule-suggestion", "nope", "--table-id", "1"}, want: `invalid suggestion id "nope"`},
		{name: "auth login --token-name", args: []string{"auth", "login", "--token-name", longTokenName}, want: "--token-name is 300 characters"},
		{name: "auth switch-org --token-name", args: []string{"auth", "switch-org", "acme", "--token-name", longTokenName}, want: "--token-name is 300 characters"},
		{name: "cache aliases resolve", args: []string{"cache", "aliases", "resolve", "bogus"}, want: `malformed alias "bogus"`},
		{name: "share table --to-id", args: []string{"share", "table", "orders", "--to-id", "nope", "--yes"}, want: `invalid --to-id "nope"`},
		{
			name:     "create dry-run-job --cloud-source-id",
			args:     []string{"create", "dry-run-job", "-f", manifestArg, "--cloud-source-id", "-1"},
			manifest: validDryRunSpec,
			want:     "--cloud-source-id must be a positive integer",
		},
		{
			name:     "create dry-run-job conflicting cloud source",
			args:     []string{"create", "dry-run-job", "-f", manifestArg, "--cloud-source-id", "1", "--cloud-source", "x"},
			manifest: validDryRunSpec,
			want:     "cannot use both --cloud-source-id and --cloud-source flags",
		},
		{
			name:     "apply Rule without metadata.id",
			args:     []string{"apply", "-f", manifestArg},
			manifest: "apiVersion: qluster.ai/v1\nkind: Rule\nmetadata:\n  name: bad_rule\nspec:\n  description: test\n",
			want:     "metadata.id is required",
		},
		{
			name:     "apply TableRule in neither mode",
			args:     []string{"apply", "-f", manifestArg},
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  dataset_id: 1\n",
			want:     "cannot determine operation for",
		},
		{
			name:     "apply TableRule patch with spec.column",
			args:     []string{"apply", "-f", manifestArg},
			manifest: tableRulePatchHead + "  dataset_id: 1\n  column: email_col\n",
			want:     "spec.column is instantiate-only",
		},
		{
			name:     "apply TableRule instantiate without dataset_id",
			args:     []string{"apply", "-f", manifestArg},
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  rule_revision_id: " + preBootstrapUUID + "\n",
			want:     "spec.dataset_id is required for instantiate",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runOutsideGetWithoutContext(t, withManifest(t, tc.args, tc.manifest)...)
			if err == nil {
				t.Fatalf("qctl %s exited 0", strings.Join(tc.args, " "))
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d; error: %v", got, qerrors.ExitBadUsage, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// The control: a valid invocation of each command family above still reaches
// Bootstrap, which is what fails here.
func TestPreBootstrapChecksOutsideGetPassValidInput(t *testing.T) {
	const noServer = "no API server specified"
	cases := []struct {
		name     string
		args     []string
		manifest string
		want     string
	}{
		{name: "review assign", args: []string{"review", "assign", preBootstrapUUID, "--reviewer", "a@b.c"}, want: noServer},
		{name: "review audit", args: []string{"review", "audit", preBootstrapUUID, "--page", "2", "--limit", "5"}, want: noServer},
		{name: "review close", args: []string{"review", "close", preBootstrapUUID}, want: noServer},
		{name: "review approve", args: []string{"review", "approve", preBootstrapUUID}, want: noServer},
		{name: "review comment", args: []string{"review", "comment", preBootstrapUUID, "--body", "x", "--reply-to", preBootstrapUUID}, want: noServer},
		{name: "review edit-comment", args: []string{"review", "edit-comment", preBootstrapUUID, preBootstrapUUID, "--body", "x"}, want: noServer},
		{name: "review delete-comment", args: []string{"review", "delete-comment", preBootstrapUUID, preBootstrapUUID, "--yes"}, want: noServer},
		{name: "review link-dry-run", args: []string{"review", "link-dry-run", preBootstrapUUID, "5", "--comment", preBootstrapUUID}, want: noServer},
		{name: "describe rule-review", args: []string{"describe", "rule-review", preBootstrapUUID}, want: noServer},
		{name: "describe rule-suggestion", args: []string{"describe", "rule-suggestion", preBootstrapUUID, "--table-id", "1"}, want: noServer},
		{name: "auth login", args: []string{"auth", "login", "--token-name", "laptop"}, want: noServer},
		{name: "auth switch-org", args: []string{"auth", "switch-org", "acme", "--token-name", "laptop"}, want: "no current context is set"},
		{name: "cache aliases resolve", args: []string{"cache", "aliases", "resolve", "r-1"}, want: noServer},
		{name: "share table", args: []string{"share", "table", "orders", "--to-id", preBootstrapUUID, "--yes"}, want: noServer},
		{
			name:     "create dry-run-job",
			args:     []string{"create", "dry-run-job", "-f", manifestArg, "--cloud-source-id", "1"},
			manifest: validDryRunSpec,
			want:     noServer,
		},
		{
			name:     "apply Rule",
			args:     []string{"apply", "-f", manifestArg},
			manifest: "apiVersion: qluster.ai/v1\nkind: Rule\nmetadata:\n  id: " + preBootstrapUUID + "\nspec:\n  description: test\n",
			want:     noServer,
		},
		{
			name:     "apply TableRule patch",
			args:     []string{"apply", "-f", manifestArg},
			manifest: tableRulePatchHead + "  dataset_id: 1\n  instance_name: renamed\n",
			want:     noServer,
		},
		{
			name:     "apply TableRule instantiate",
			args:     []string{"apply", "-f", manifestArg},
			manifest: "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata: {}\nspec:\n  dataset_id: 1\n  rule_revision_id: " + preBootstrapUUID + "\n",
			want:     noServer,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := runOutsideGetWithoutContext(t, withManifest(t, tc.args, tc.manifest)...)
			if err == nil {
				t.Fatalf("qctl %s exited 0 with no context configured", strings.Join(tc.args, " "))
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
				t.Errorf("exit code = %d, want %d; error: %v", got, qerrors.ExitGenericError, err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
		})
	}
}

// withManifest writes manifest to a temporary file and returns args with
// manifestArg replaced by its path. An empty manifest returns args unchanged.
func withManifest(t *testing.T, args []string, manifest string) []string {
	t.Helper()
	if manifest == "" {
		return args
	}
	path := filepath.Join(t.TempDir(), "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}
	out := make([]string, len(args))
	for i, a := range args {
		if a == manifestArg {
			a = path
		}
		out[i] = a
	}
	return out
}
