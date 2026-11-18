package root

import (
	"os"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
	"golang.org/x/term"
)

const closeTestReviewID = "0190bbbb-1111-4222-8333-444444444444"

// Closing a data review or a rule review is final, so a protected context
// must be named with --context; --yes does not count.
func TestReviewCloseRequiresANamedProtectedContext(t *testing.T) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		t.Skip("requires non-TTY stdin")
	}
	for _, tc := range []struct {
		args []string
		path string
	}{
		{[]string{"data-review", "close", closeTestReviewID, "--yes"}, "/data-reviews/"},
		{[]string{"review", "close", closeTestReviewID}, "/rule-reviews/"},
	} {
		cmd, _, err := GetRootCmd().Find(tc.args[:2])
		if err != nil || !cmdutil.IsDestructive(cmd) {
			t.Fatalf("qctl %s is not destructive: %v", strings.Join(tc.args[:2], " "), err)
		}
		if f := cmd.Flags().Lookup("yes"); f != nil {
			t.Cleanup(func() { _ = f.Value.Set(f.DefValue); f.Changed = false })
		}
		for _, named := range []bool{false, true} {
			t.Run(strings.Join(tc.args[:2], " ")+map[bool]string{false: "/unnamed", true: "/named"}[named], func(t *testing.T) {
				env := testutil.NewTestEnv(t)
				mock := testutil.NewMockAPIServer()
				t.Cleanup(mock.Close)
				endpoint, _ := config.NormalizeEndpointKey(mock.URL())
				env.SetupConfigWithOrg(mock.URL(), "test@example.com", scopeTestOrgID)
				env.SetupCredential(endpoint, scopeTestOrgID, "close-token")
				cfg, err := config.Load()
				if err != nil {
					t.Fatal(err)
				}
				cfg.Contexts[cfg.CurrentContext].Protected = nil
				if err := cfg.Save(); err != nil {
					t.Fatal(err)
				}
				args := append([]string{}, tc.args...)
				if named {
					args = append(args, "--context", cfg.CurrentContext)
				}
				_, err = runRootWithContextFlag(t, args...)
				want := "POST /api/orgs/" + scopeTestOrgID + tc.path + closeTestReviewID + "/close"
				if named {
					// The mock answers 404, which proves the close was sent.
					if got := mock.Requests(); len(got) != 1 || got[0] != want {
						t.Fatalf("requests = %v, want [%s] (err: %v)", got, want, err)
					}
					return
				}
				if qerrors.GetExitCode(err) != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "--context") {
					t.Fatalf("err = %v, want the protected-context refusal (exit 2)", err)
				}
				if got := mock.Requests(); len(got) != 0 {
					t.Fatalf("requests = %v, want none", got)
				}
			})
		}
	}
}
