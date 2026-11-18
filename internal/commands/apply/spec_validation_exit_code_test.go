package apply

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// Deep spec validation -- the envelope parsed, but a field's *value* is wrong
// -- is still the user's file being wrong, not the server saying no. All six
// handlers run Validate() before cmdutil.Bootstrap, so none of these reaches
// the network and none needs a mock server.
func TestDeepSpecValidationExitsTwo(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		want     string
	}{
		{
			name:     "Table",
			manifest: "apiVersion: qluster.ai/v1\nkind: Table\nmetadata:\n  name: orders\nspec: {}\n",
			want:     "manifest validation failed:\n  - spec.destination_id: must be a positive integer",
		},
		{
			name:     "Destination",
			manifest: "apiVersion: qluster.ai/v1\nkind: Destination\nmetadata:\n  name: warehouse\nspec: {}\n",
			want:     "manifest validation failed:\n  - spec.type: required field is missing",
		},
		{
			name:     "CloudSource",
			manifest: "apiVersion: qluster.ai/v1\nkind: CloudSource\nmetadata:\n  name: bucket\nspec: {}\n",
			want:     "manifest validation failed:\n  - spec.dataset_id: must be a positive integer",
		},
		{
			name:     "LinkedTable",
			manifest: "apiVersion: qluster.ai/v1\nkind: LinkedTable\nmetadata:\n  name: joined\nspec: {}\n",
			want:     "manifest validation failed:\n  - spec.destination: required field is missing",
		},
		{
			name:     "RuleReviewPolicy",
			manifest: "apiVersion: qluster.ai/v1\nkind: RuleReviewPolicy\nmetadata:\n  name: org-default\nspec:\n  min_reviewers: 0\n",
			want:     "manifest validation failed:\n  - spec.min_reviewers must be between 1 and 10",
		},
		{
			name:     "DataReviewPolicy",
			manifest: "apiVersion: qluster.ai/v1\nkind: DataReviewPolicy\nmetadata:\n  name: org-default\nspec: {}\n",
			want:     "manifest validation failed:\n  - spec.requires_review is required for kind DataReviewPolicy",
		},
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
