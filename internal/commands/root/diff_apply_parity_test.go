package root

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const parityTableRuleID = "550e8400-e29b-41d4-a716-446655440000"

// The manifests dataset_rules.ValidatePatchManifest refuses, run through BOTH
// verbs from the real command tree -- the only place `qctl apply -f` and
// `qctl diff -f` can be compared, since neither command package imports the
// other.
//
// `qctl diff -f` exists to answer "what would apply do with this file", so on
// a file apply will not touch at all it has exactly one honest answer, and it
// is apply's. On master it had a different one for two of the three below: it
// printed the old -> new lines and the merge patch body, exited 1 (differences
// found), and left apply to reject the same file with exit 2.
//
// What actually stops that recurring is the single definition both verbs call.
// This test does not derive the corpus from anything, so it cannot see a
// FOURTH refusal added to one verb alone -- it pins these three and the shape
// of the guarantee, and the shared function is what makes a fourth land in
// both by construction.
func TestDiffAndApplyRefusePatchManifestsIdentically(t *testing.T) {
	for _, tc := range []struct {
		name string
		spec string
		want string
	}{
		{
			name: "spec.column is instantiate-only",
			spec: "  dataset_id: 1\n  instance_name: renamed\n  column: email_col\n",
			want: "spec.column is instantiate-only and has no effect in patch mode",
		},
		{
			name: "patch without dataset_id",
			spec: "  instance_name: renamed\n",
			want: "spec.dataset_id is required for patch in",
		},
		{
			name: "spec.state: error is the server's to set",
			spec: "  dataset_id: 1\n  state: error\n",
			want: "cannot be requested",
		},
		{
			name: "spec.state: pending_validation is the server's to set",
			spec: "  dataset_id: 1\n  state: pending_validation\n",
			want: "cannot be requested",
		},
		{
			name: "spec.state: validation_failed is the server's to set",
			spec: "  dataset_id: 1\n  state: validation_failed\n",
			want: "cannot be requested",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			mock := testutil.NewMockAPIServer()
			defer mock.Close()

			env.SetupConfigWithOrg(mock.URL(), "", scopeTestOrgID)
			endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
			env.SetupCredential(endpointKey, scopeTestOrgID, "token")

			// One file for both verbs, so the two errors name the same source
			// and can be compared as whole strings rather than by substring.
			manifest := env.CreateFile("table-rule.yaml",
				"apiVersion: qluster.ai/v1\nkind: TableRule\nmetadata:\n  id: "+
					parityTableRuleID+"\nspec:\n"+tc.spec)

			applyErr := runRootScoped(t, "apply", "-f", manifest)
			diffErr := runRootScoped(t, "diff", "-f", manifest)

			if applyErr == nil || diffErr == nil {
				t.Fatalf("both verbs must refuse the manifest; apply = %v, diff = %v", applyErr, diffErr)
			}
			if applyErr.Error() != diffErr.Error() {
				t.Errorf("the two verbs disagree about the same file:\n  apply: %s\n   diff: %s", applyErr, diffErr)
			}
			if !strings.Contains(applyErr.Error(), tc.want) {
				t.Errorf("apply error = %q, want it to contain %q", applyErr.Error(), tc.want)
			}
			applyExit, diffExit := qerrors.GetExitCode(applyErr), qerrors.GetExitCode(diffErr)
			if applyExit != qerrors.ExitBadUsage || diffExit != qerrors.ExitBadUsage {
				t.Errorf("exit codes = apply %d, diff %d, want %d from both",
					applyExit, diffExit, qerrors.ExitBadUsage)
			}
			// Neither verb asked the server anything. For diff this is the
			// substance of the bug: the run that printed a preview had to
			// fetch the live rule to build it, so a request here means a
			// preview happened.
			if got := mock.Requests(); len(got) != 0 {
				t.Errorf("requests = %v, want none -- these are verdicts on the manifest, "+
					"reached by both verbs before anything is sent", got)
			}
		})
	}
}
