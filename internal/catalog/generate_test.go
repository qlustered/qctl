package catalog

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// dispatcherTree returns a tree with a single Generic dispatcher accepting
// the given kinds and nothing else — enough to exercise the dispatcher
// branch of GenerateUsage/GenerateUsageWith without a real command tree.
func dispatcherTree(kinds ...string) *cobra.Command {
	root := &cobra.Command{Use: "qctl", Annotations: Parent()}
	root.AddCommand(&cobra.Command{Use: "apply", Annotations: Dispatcher(kinds)})
	return root
}

// TestGenerateUsageWithInjectedKindReachesDispatcher is the -create bug (7
// skeleton pages shipped without their dispatcher line, hand-fixed in
// 68bc68e) expressed as a test: for a resource with no page in the embedded
// FS yet, GenerateUsageWith must still emit the dispatcher line because its
// kind was passed explicitly rather than looked up.
func TestGenerateUsageWithInjectedKindReachesDispatcher(t *testing.T) {
	got := GenerateUsageWith(dispatcherTree("Widget"), "gadget-not-a-real-resource", "Widget")
	want := []string{"qctl apply -f <file.yaml>   # kind: Widget"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("GenerateUsageWith = %v, want %v", got, want)
	}
}

// TestGenerateUsageDelegatesKindLookup shows GenerateUsage (the two-arg form)
// still resolves kind from the embedded page when the caller does not supply
// one — using "table", a resource that really is embedded with a kind, so
// resources.LookupPage has something to find.
func TestGenerateUsageDelegatesKindLookup(t *testing.T) {
	page, ok := resources.LookupPage("table")
	if !ok || page.Kind == "" {
		t.Fatal("fixture assumption broken: embedded \"table\" page must exist with a kind")
	}
	got := GenerateUsage(dispatcherTree(page.Kind), "table")
	want := "qctl apply -f <file.yaml>   # kind: " + page.Kind
	if len(got) != 1 || got[0] != want {
		t.Errorf("GenerateUsage = %v, want [%q]", got, want)
	}
}

func TestUsageLineAppendsRequiredFlagWithShorthand(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	cmd := &cobra.Command{Use: "table"}
	root.AddCommand(cmd)
	cmd.Flags().StringP("filename", "f", "", "")
	if err := cmd.MarkFlagRequired("filename"); err != nil {
		t.Fatal(err)
	}
	if got, want := UsageLine(cmd), "qctl table -f <file.yaml>"; got != want {
		t.Errorf("UsageLine = %q, want %q", got, want)
	}
}

func TestUsageLineAppendsRequiredFlagWithoutShorthand(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	cmd := &cobra.Command{Use: "assign <review-id>"}
	root.AddCommand(cmd)
	cmd.Flags().StringArray("reviewer", nil, "")
	if err := cmd.MarkFlagRequired("reviewer"); err != nil {
		t.Fatal(err)
	}
	if got, want := UsageLine(cmd), "qctl assign <review-id> --reviewer <reviewer>"; got != want {
		t.Errorf("UsageLine = %q, want %q", got, want)
	}
}

func TestUsageLineOmitsNonRequiredFlag(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	cmd := &cobra.Command{Use: "linked-table"}
	root.AddCommand(cmd)
	cmd.Flags().Bool("validate-only", false, "")
	if got, want := UsageLine(cmd), "qctl linked-table"; got != want {
		t.Errorf("UsageLine = %q, want %q", got, want)
	}
}

// TestUsageLineOmitsInheritedParentFlag proves required-flag rendering looks
// only at a command's own flags: a required flag declared on a parent (even
// a persistent one) must never leak into a child's usage line.
func TestUsageLineOmitsInheritedParentFlag(t *testing.T) {
	root := &cobra.Command{Use: "qctl"}
	root.PersistentFlags().StringP("output", "o", "table", "")
	if err := root.MarkPersistentFlagRequired("output"); err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{Use: "table"}
	root.AddCommand(cmd)
	if got, want := UsageLine(cmd), "qctl table"; got != want {
		t.Errorf("UsageLine = %q, want %q (a parent's required flag must not leak into a child's usage line)", got, want)
	}
}

// TestCommandExamplesJoinsWrappedInvocation: a wrapped example used to survive
// as its own first physical line, trailing backslash and all, and take every
// example after it down with it — the continuation landed in the default arm,
// which ends the block. Both halves of that are checked here, because either
// one alone leaves a page seeded with a lie: a command cut off mid-invocation,
// or an example that silently never arrives.
func TestCommandExamplesJoinsWrappedInvocation(t *testing.T) {
	cmd := &cobra.Command{Use: "submit", Long: `Long prose.

Examples:
  # simple
  qctl alert submit --table orders --row 1042 --field amount
  # wrapped
  qctl alert submit --table orders --row 1042 --row 1043 \
      --comment "FX mismatch" --yes
  qctl alert submit --table-id 42 --row 1042 --field amount`}

	got := commandExamples(cmd)
	want := []resources.Example{
		{Description: "simple", Command: "qctl alert submit --table orders --row 1042 --field amount"},
		{Description: "wrapped", Command: `qctl alert submit --table orders --row 1042 --row 1043 --comment "FX mismatch" --yes`},
		{Command: "qctl alert submit --table-id 42 --row 1042 --field amount"},
	}
	if len(got) != len(want) {
		t.Fatalf("commandExamples returned %d examples, want %d:\n%+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("example %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestCommandExamplesSurvivesInterleavedProse is the qctl-fe9 defect: the
// default arm returned unconditionally, so ANY line that was not blank, "#" or
// "qctl " ended the block — including one inside it. Each case here is a shape
// the real tree carries, named by the command it came from, and each was
// checked against that command's Long text rather than invented.
func TestCommandExamplesSurvivesInterleavedProse(t *testing.T) {
	tests := []struct {
		name string
		long string
		want []resources.Example
	}{{
		// qctl create dry-run-job: the block OPENS with prose and puts a YAML
		// spec between each invocation, so the old walk seeded nothing at all.
		name: "indented prose and an embedded spec do not end the block",
		long: `Long prose.

Examples:

  Two rules compared by dataset_rule_id:
  --- spec.yaml ---

  apiVersion: qluster.ai/v1
  kind: DryRunJob
  spec:
    rule_run_specs:
      - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
  
  qctl create dry-run-job -f spec.yaml --table my_table

  -------------

  Test a rule by name (resolved automatically):

  qctl create dry-run-job -f spec.yaml`,
		want: []resources.Example{
			{Command: "qctl create dry-run-job -f spec.yaml --table my_table"},
			{Command: "qctl create dry-run-job -f spec.yaml"},
		},
	}, {
		// qctl apply and qctl submit rules: the prose that really does end the
		// block sits at column 0, and the invocation under it is a pointer to
		// a guide, not an example of the command. Seeding it would put
		// `qctl guide instantiate-rule` in the apply page's EXAMPLES.
		name: "a column-0 line ends the block and takes what follows with it",
		long: `Long prose.

Examples:
  # Non-interactive
  qctl apply -f table-rule.yaml --yes

First time instantiating a rule on a table? Run:
  qctl guide instantiate-rule`,
		want: []resources.Example{
			{Description: "Non-interactive", Command: "qctl apply -f table-rule.yaml --yes"},
		},
	}, {
		// qctl review comment: a shell pipeline is neither an example this can
		// seed (ResolveCommandLine cannot parse one) nor a reason to stop.
		name: "an indented pipeline is skipped, not terminal",
		long: `Long prose.

Examples:
  qctl review comment <id> --body "LGTM"
  cat notes.md | qctl review comment <id> --body-file -
  qctl review comment <id> --reply-to <comment-uuid> --body "Fixed, PTAL"`,
		want: []resources.Example{
			{Command: `qctl review comment <id> --body "LGTM"`},
			{Command: `qctl review comment <id> --reply-to <comment-uuid> --body "Fixed, PTAL"`},
		},
	}, {
		// The "#" arm runs before the column test, so a body written flush-left
		// — which no Long text in the tree is, but a cobra Example field could
		// be — still seeds instead of ending at its first line.
		name: "an unindented invocation is still harvested",
		long: "Long prose.\n\nExamples:\n# Flush left\nqctl get tables\nqctl get rules",
		want: []resources.Example{
			{Description: "Flush left", Command: "qctl get tables"},
			{Command: "qctl get rules"},
		},
	}, {
		// A description survives the prose now skipped between it and its
		// command. Prose never becomes one on its own — "the reader wants" is
		// not the description of the invocation three lines below it.
		name: "prose between a description and its command steals neither role",
		long: `Long prose.

Examples:
  # The real description
  the reader wants this line to be prose
  qctl get tables`,
		want: []resources.Example{
			{Description: "The real description", Command: "qctl get tables"},
		},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := commandExamples(&cobra.Command{Use: "x", Long: tt.long})
			if len(got) != len(tt.want) {
				t.Fatalf("commandExamples returned %d examples, want %d:\n%+v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("example %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
