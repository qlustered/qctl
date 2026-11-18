package diff

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// patchRefusals are the manifests dataset_rules.ValidatePatchManifest exists
// to refuse -- every verdict `qctl apply -f` reaches on a patch manifest
// before its request. The corpus is shared with the cross-verb parity test in
// internal/commands/root, which runs each of these through BOTH verbs and
// compares the two errors; here they establish that diff refuses them at all.
//
// Before this, diff made only the third of the three: a manifest carrying
// spec.column, or missing spec.dataset_id, got a clean field-level preview
// from `qctl diff -f` and then exit 2 from `qctl apply -f`. A preview that
// blesses a request apply refuses is the one thing a preview must never do.
var patchRefusals = []struct {
	name     string
	spec     string
	wantText string
}{
	{
		name: "spec.column is instantiate-only",
		// instance_name rides along on purpose. Without a patchable key
		// beside it the manifest was refused for setting nothing patchable,
		// which is exit 2 for the wrong reason and hides the divergence this
		// corpus is about.
		spec:     "  dataset_id: 1\n  instance_name: renamed\n  column: email_col\n",
		wantText: "spec.column is instantiate-only and has no effect in patch mode",
	},
	{
		name:     "patch without dataset_id",
		spec:     "  instance_name: renamed\n",
		wantText: "spec.dataset_id is required for patch in",
	},
	{
		name:     "spec.state: error is the server's to set",
		spec:     "  dataset_id: 1\n  state: error\n",
		wantText: "cannot be requested",
	},
	{
		name:     "spec.state: pending_validation is the server's to set",
		spec:     "  dataset_id: 1\n  state: pending_validation\n",
		wantText: "cannot be requested",
	},
	{
		name:     "spec.state: validation_failed is the server's to set",
		spec:     "  dataset_id: 1\n  state: validation_failed\n",
		wantText: "cannot be requested",
	},
}

func patchRefusalManifest(spec string) string {
	return "apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: " + testTableRuleID + "\nspec:\n" + spec
}

// The refusals are verdicts on the manifest, so diff reaches every one of them
// without asking the server anything -- asserted here by the empty request
// log, with registerDetailGet installed so a regression that DOES ask has
// somewhere to land instead of failing as a transport error.
func TestDiffTableRulePatchValidationExitsTwo(t *testing.T) {
	for _, tc := range patchRefusals {
		t.Run(tc.name, func(t *testing.T) {
			env, mock := newDiffTestEnv(t)
			registerDetailGet(mock)

			path := env.CreateFile("table-rule.yaml", patchRefusalManifest(tc.spec))
			stdout, _, err := execDiff(t, path)
			if err == nil {
				t.Fatal("expected diff to refuse the manifest")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.wantText)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
			// A refused manifest gets no preview at all: printing the old ->
			// new lines and then failing would be the same lie in a quieter
			// form.
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing printed for a refused manifest", stdout)
			}
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none -- these are verdicts on the manifest", got)
			}
		})
	}
}

// The refusals are reached before diff bootstraps auth, which is what lets a
// user fix a manifest without being logged in. This env has a config but no
// stored credential, so anything reaching cmdutil.Bootstrap fails with an
// auth error instead -- a different message and a different exit code, so
// moving the validation below the bootstrap turns this red.
func TestDiffTableRulePatchValidationPrecedesAuth(t *testing.T) {
	for _, tc := range patchRefusals {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			t.Cleanup(env.Cleanup)
			mock := testutil.NewMockAPIServer()
			t.Cleanup(mock.Close)
			env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)
			// Deliberately no SetupCredential: NewTestEnv stubs the keyring
			// empty, so there is no token to find anywhere.

			path := env.CreateFile("table-rule.yaml", patchRefusalManifest(tc.spec))
			_, _, err := execDiff(t, path)
			if err == nil {
				t.Fatal("expected diff to refuse the manifest")
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Errorf("error = %q, want the manifest refusal %q, not an auth failure", err.Error(), tc.wantText)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// Guard the guard: the no-credential env above has to be able to produce an
// auth failure, or TestDiffTableRulePatchValidationPrecedesAuth proves nothing
// about ordering -- it would pass just as happily if Bootstrap never failed.
// A manifest that passes validation must therefore fail on auth in that env.
func TestDiffWithoutCredentialFailsOnAuth(t *testing.T) {
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	env.SetupConfigWithOrg(mock.Server.URL, "test@example.com", testOrgID)

	path := env.CreateFile("table-rule.yaml", patchRefusalManifest("  dataset_id: 1\n  instance_name: renamed\n"))
	_, _, err := execDiff(t, path)
	if err == nil {
		t.Fatal("expected diff to fail without a credential")
	}
	// Two assertions, because either alone can be satisfied by the wrong
	// failure: the exit code separates an auth failure from a manifest
	// refusal (bad usage), and the message names WHICH failure it was, so a
	// broken env failing for some third reason cannot pass as one.
	if got := qerrors.GetExitCode(err); got == qerrors.ExitBadUsage {
		t.Errorf("error = %q (exit %d) -- a valid manifest must fail on AUTH here, "+
			"otherwise the ordering test above is vacuous", err.Error(), got)
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error = %q, want the not-logged-in failure", err.Error())
	}
}
