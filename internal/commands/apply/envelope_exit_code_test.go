package apply

import (
	"path/filepath"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// Every typed handler's manifest-load failure is the user's file being wrong,
// not the server saying no. All six parse before cmdutil.Bootstrap, so none of
// these reaches the network and none needs a mock server.
func TestTypedHandlerManifestLoadFailuresExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		sub      string // "" runs `qctl apply -f`
		manifest string
		want     string
	}{
		{
			name:     "destination/wrong kind",
			sub:      "destination",
			manifest: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n",
			want:     "failed to load manifest: expected kind 'Destination', got 'Table'",
		},
		{
			name:     "table/wrong apiVersion value",
			sub:      "table",
			manifest: "apiVersion: v2-bad\nkind: Table\nmetadata:\n  name: orders\n",
			want:     "failed to load manifest: expected apiVersion 'qluster.ai/v1', got 'v2-bad'",
		},
		{
			name:     "table/unknown spec field",
			sub:      "table",
			manifest: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec:\n  tabel_name: orders\n",
			want:     "failed to load manifest: validation error:",
		},
		{
			name:     "cloud-source/wrong apiVersion value",
			sub:      "cloud-source",
			manifest: "apiVersion: v2-bad\nkind: CloudSource\nmetadata:\n  name: bucket\n",
			want:     "failed to load manifest: expected apiVersion 'qluster.ai/v1', got 'v2-bad'",
		},
		{
			name:     "cloud-source/wrong kind",
			sub:      "cloud-source",
			manifest: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n",
			want:     "failed to load manifest: expected kind 'CloudSource', got 'Table'",
		},
		{
			name:     "linked-table/wrong kind",
			sub:      "linked-table",
			manifest: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\n",
			want:     "failed to load manifest: expected kind 'LinkedTable' or 'CompositeDataset', got 'Table'",
		},
		{
			name:     "linked-table/wrong apiVersion value",
			sub:      "linked-table",
			manifest: "apiVersion: v2-bad\nkind: LinkedTable\nmetadata:\n  name: joined\n",
			want:     "failed to load manifest: expected apiVersion 'qluster.ai/v1', got 'v2-bad'",
		},
		{
			// No typed subcommand exists for the policies; apply -f is the only door.
			name:     "rule-review-policy/wrong apiVersion value",
			manifest: "apiVersion: v2-bad\nkind: RuleReviewPolicy\nmetadata:\n  name: org-default\n",
			want:     "failed to load manifest: expected apiVersion 'qluster.ai/v1', got 'v2-bad'",
		},
		{
			name:     "data-review-policy/wrong apiVersion value",
			manifest: "apiVersion: v2-bad\nkind: DataReviewPolicy\nmetadata:\n  name: org-default\n",
			want:     "failed to load manifest: expected apiVersion 'qluster.ai/v1', got 'v2-bad'",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			path := env.CreateFile("manifest.yaml", tc.manifest)

			var err error
			if tc.sub == "" {
				_, err = execApply(t, path)
			} else {
				_, err = execApplySub(t, tc.sub, path)
			}
			if err == nil {
				t.Fatal("expected an error")
			}
			// The message must not move: only the exit code is being fixed.
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err.Error(), tc.want)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The whole contract in one table, generic dispatcher only: every shape qctl
// rejects before it opens a connection exits 2. A new envelope check added
// without qerrors fails here rather than shipping at exit 1.
func TestApplyRejectsMalformedManifestsWithExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"YAML syntax error", "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: [unclosed\n"},
		{"missing apiVersion", "kind: Table\nmetadata:\n  name: orders\n"},
		{"missing kind", "apiVersion: qluster.ai/v1\nmetadata:\n  name: orders\n"},
		{"no identity", "apiVersion: qluster.ai/v1\nkind: Table\nmetadata: {}\n"},
		{"unsupported kind", "apiVersion: qluster.ai/v1\nkind: Foobar\nmetadata:\n  name: x\n"},
		{"empty file", "# nothing here\n"},
		{"unknown spec field", "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec:\n  tabel_name: orders\n"},
		{"wrong apiVersion value", "apiVersion: v2-bad\nkind: Table\nmetadata:\n  name: orders\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			path := env.CreateFile("manifest.yaml", tc.manifest)
			_, err := execApply(t, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// applyRuleYAML validates a `kind: Rule` manifest before it bootstraps, so no
// config or credential is set up: a check reached only after Bootstrap would
// exit 1 here, with no server configured.
func TestApplyRejectsMalformedRuleManifestsWithExitTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
	}{
		{"no metadata.id", "apiVersion: qluster.ai/v1\nkind: Rule\nmetadata:\n  name: bad_rule\nspec:\n  description: test\n"},
		{"wrong apiVersion value", "apiVersion: v2-bad\nkind: Rule\nmetadata:\n  id: " + testRuleID + "\nspec:\n  description: test\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := testutil.NewTestEnv(t)
			defer env.Cleanup()

			path := env.CreateFile("rule.yaml", tc.manifest)
			_, err := execApply(t, path)
			if err == nil {
				t.Fatal("expected an error")
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

// The negative controls. Without these, a future change that wrapped every
// apply failure in ExitBadUsage would pass every test above and destroy the
// distinction this whole change exists to draw.
func TestApplyKeepsNonValidationExitCodes(t *testing.T) {
	t.Run("unreadable file still exits 1", func(t *testing.T) {
		env := testutil.NewTestEnv(t)
		defer env.Cleanup()

		_, err := execApply(t, filepath.Join(t.TempDir(), "missing.yaml"))
		if err == nil {
			t.Fatal("expected an error")
		}
		// An I/O failure is not a malformed manifest. Deliberate, per the ticket.
		if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
			t.Errorf("exit code = %d, want %d", got, qerrors.ExitGenericError)
		}
	})

	t.Run("a server 404 still exits 3", func(t *testing.T) {
		env, mock := setupRuleTest(t)
		defer env.Cleanup()
		defer mock.Close()

		// The house helper, same as TestApplyRuleFetchError404 (rules_test.go).
		mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/rule-revisions/"+testRuleID+"/details",
			testutil.MockNotFoundHandler("rule not found"))

		path := env.CreateFile("rule.yaml",
			"apiVersion: qluster.ai/v1\nkind: Rule\nmetadata:\n  id: "+testRuleID+"\nspec:\n  description: test\n")
		_, err := execApply(t, path)
		if err == nil {
			t.Fatal("expected an error")
		}
		if got := qerrors.GetExitCode(err); got != qerrors.ExitNotFound {
			t.Errorf("exit code = %d, want %d: the manifest was fine, the server said no", got, qerrors.ExitNotFound)
		}
	})
}
