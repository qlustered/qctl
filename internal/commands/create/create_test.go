package create

import (
	"bytes"
	"strings"
	"testing"
)

func TestCreateLongPointsToApplyAndGuide(t *testing.T) {
	long := NewCommand().Long
	for _, want := range []string{"qctl apply table", "qctl guide onboard-table"} {
		if !strings.Contains(long, want) {
			t.Errorf("create Long should contain %q; got:\n%s", want, long)
		}
	}
}

// The eight apply-managed resources as of this change, with aliases, in the
// order resources.Pages() returns them. A fixture rather than the live
// catalog on purpose: this test pins *rendering*, and must not start failing
// the day a ninth resource is added. TestCreateLongListsApplyManagedResources
// is what keeps this fixture honest against reality.
var managedFixture = []string{
	"cloud-source (data-source)",
	"data-review-policy",
	"destination",
	"linked-table (composite-dataset)",
	"rule",
	"rule-review-policy",
	"table (dataset)",
	"table-rule",
}

func TestCreateLongRendering(t *testing.T) {
	want := `Create a resource imperatively. Valid resource types: dry-run-job,
composite-dry-run-job, deliverable-run

Most other resources are managed declaratively from a YAML manifest with
'qctl apply', which reads the kind from the file:

  qctl apply -f <file.yaml>        # kind is read from the manifest
  qctl apply table -f table.yaml   # or name the resource explicitly

Managed this way: cloud-source (data-source), data-review-policy,
  destination, linked-table (composite-dataset), rule,
  rule-review-policy, table (dataset), table-rule

Run 'qctl api-resources' for every resource and how to manage it.

New to tables? Run: qctl guide onboard-table`
	if got := buildLong(managedFixture); got != want {
		t.Errorf("buildLong mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// resources.Pages() reads a compiled embed that TestEveryResourceHasPage keeps
// parseable, so applyManagedNames can only return nil in a build that is
// already broken. NewCommand cannot return an error, so it must still render
// something coherent: no list at all, and the api-resources pointer that
// answers the same question. A partial list would be worse than none.
func TestCreateLongDegrades(t *testing.T) {
	got := buildLong(nil)
	if strings.Contains(got, "Managed this way:") {
		t.Errorf("degraded Long should carry no list; got:\n%s", got)
	}
	for _, want := range []string{"qctl api-resources", "qctl apply -f <file.yaml>", "qctl guide onboard-table"} {
		if !strings.Contains(got, want) {
			t.Errorf("degraded Long should still contain %q; got:\n%s", want, got)
		}
	}
}

func TestCreateRedirectsApplyResources(t *testing.T) {
	cases := []struct {
		arg  string
		hint string
	}{
		{"table", "qctl apply table"},
		{"dataset", "qctl apply table"},
		{"tables", "qctl apply table"},
		{"datasets", "qctl apply table"},
		// The two folds createRedirect applies before the plural retry. Both
		// matter only on the RETRY path -- catalog.Lookup folds its own input,
		// so a direct hit like " Table " resolves either way -- and each of
		// the two below is the only case holding one of them. Verified by
		// removing each fold in turn:
		//   TABLES    -- TrimSuffix is case-SENSITIVE and leaves a capital
		//                "S", so with no ToLower the retry is a no-op and the
		//                lookup misses.
		//   " tables " -- the retry transforms this LOCAL value, so with no
		//                TrimSpace it becomes " tables " again (the string
		//                ends in a space, not an "s") and misses.
		// The equivalents under `qctl delete` have carried both all along,
		// which is why a padded plural resolved there and not here.
		{"TABLES", "qctl apply table"},
		{" tables ", "qctl apply table"},
		{" Table ", "qctl apply table"},
		{"destination", "qctl apply destination"},
		{"cloud-source", "qctl apply cloud-source"},
		// data-source is cloud-source's backend-term alias (page frontmatter:
		// internal/resources/pages/cloud-source.md's `aliases: [data-source]`).
		{"data-source", "qctl apply cloud-source"},
		{"linked-table", "qctl apply linked-table"},
		{"composite-dataset", "qctl apply linked-table"},
		// Neither policy has a typed `apply <name>` subcommand, so the
		// redirect names the generic `-f` form and the manifest kind instead.
		{"data-review-policy", "qctl apply -f <file.yaml> --yes   # kind: DataReviewPolicy"},
		{"rule-review-policy", "qctl apply -f <file.yaml> --yes   # kind: RuleReviewPolicy"},
	}
	for _, tc := range cases {
		cmd := setupCreateTestCommand()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs([]string{"create", tc.arg})
		err := cmd.Execute()
		if err == nil {
			t.Fatalf("[%s] expected redirect error, got nil", tc.arg)
		}
		if !strings.Contains(err.Error(), tc.hint) {
			t.Errorf("[%s] error should point at %q; got: %v", tc.arg, tc.hint, err)
		}
		if !strings.Contains(err.Error(), `unknown command`) {
			t.Errorf("[%s] redirect should still name the unknown command; got: %v", tc.arg, err)
		}
	}
}

func TestCreateUnknownArgKeepsGenericBehavior(t *testing.T) {
	cmd := setupCreateTestCommand()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"create", "bogus"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for unknown arg")
	}
	if !strings.Contains(err.Error(), `unknown command "bogus"`) {
		t.Errorf("expected generic unknown-command error; got: %v", err)
	}
	if strings.Contains(err.Error(), "qctl apply") {
		t.Errorf("generic unknown arg must not emit an apply redirect; got: %v", err)
	}
}
