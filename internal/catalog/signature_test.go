package catalog

import (
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// sigTree returns a tree and the data page it documents. mutate lets each test
// perturb one thing and observe whether the signature moves.
func sigTree(mutate func(get, data, apply *cobra.Command)) (*cobra.Command, resources.Page) {
	root := &cobra.Command{Use: "qctl", Annotations: Parent()}
	root.PersistentFlags().StringP("output", "o", "table", "output format")

	get := &cobra.Command{Use: "get", Annotations: Parent()}
	data := &cobra.Command{
		Use:         "data <table-id-or-name>",
		Short:       "Display a page of a table's data",
		Long:        "long prose",
		Annotations: For("data"),
		Run:         func(*cobra.Command, []string) {},
	}
	data.Flags().Int("limit", 20, "rows per page")
	get.AddCommand(data)
	root.AddCommand(get)

	apply := &cobra.Command{Use: "apply", Annotations: Dispatcher([]string{"Table"})}
	root.AddCommand(apply)

	if mutate != nil {
		mutate(get, data, apply)
	}
	return root, resources.Page{Name: "data"}
}

func TestSignatureIsSevenHexChars(t *testing.T) {
	root, page := sigTree(nil)
	sig := Signature(root, page)
	if len(sig) != 7 {
		t.Fatalf("Signature = %q, want 7 characters", sig)
	}
	for _, r := range sig {
		if !strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("Signature = %q, want lowercase hex", sig)
		}
	}
}

func TestSignatureIsStable(t *testing.T) {
	a, page := sigTree(nil)
	b, _ := sigTree(nil)
	if Signature(a, page) != Signature(b, page) {
		t.Error("Signature is not deterministic across identical trees")
	}
}

func TestSignatureIgnoresHelpProse(t *testing.T) {
	base, page := sigTree(nil)
	want := Signature(base, page)

	edited, _ := sigTree(func(_, data, _ *cobra.Command) {
		data.Short = "totally rewritten short"
		data.Long = "totally rewritten long"
		data.Flags().Lookup("limit").Usage = "rewritten flag usage"
	})
	if got := Signature(edited, page); got != want {
		t.Errorf("a help-text edit moved the signature (%q → %q); it would cry wolf", want, got)
	}
}

func TestSignatureTracksBehaviorBearingSurface(t *testing.T) {
	base, page := sigTree(nil)
	want := Signature(base, page)

	cases := map[string]func(get, data, apply *cobra.Command){
		"changed args":    func(_, data, _ *cobra.Command) { data.Use = "data <table> <column>" },
		"changed default": func(_, data, _ *cobra.Command) { data.Flags().Lookup("limit").DefValue = "50" },
		"new flag":        func(_, data, _ *cobra.Command) { data.Flags().Int("page", 1, "") },
	}
	for name, mutate := range cases {
		edited, _ := sigTree(mutate)
		if got := Signature(edited, page); got == want {
			t.Errorf("%s did not move the signature", name)
		}
	}
}

// TestSignatureCoversEverythingUsageLineRenders pins the two fields the
// generated USAGE line depends on beyond a flag's name and default: its
// shorthand and its required-ness. `qctl apply table -f <file.yaml>` is
// generated from exactly those.
//
// Before they were hashed, changing `apply table`'s -f to -F left every page
// signature unchanged: TestPageSignatures passed (nobody was asked to re-read
// the gotchas that -f appears in), TestPageUsageMatchesGenerated failed, and
// the -update it prescribed exited 0 having written nothing, because the
// writer only ran for pages whose *signature* was stale.
func TestSignatureCoversEverythingUsageLineRenders(t *testing.T) {
	base, page := sigTree(nil)
	want := Signature(base, page)

	shorthand, _ := sigTree(func(_, data, _ *cobra.Command) {
		data.Flags().Lookup("limit").Shorthand = "l"
	})
	if got := Signature(shorthand, page); got == want {
		t.Error("a flag shorthand change did not move the signature, but UsageLine renders shorthands")
	}

	var markErr error
	required, _ := sigTree(func(_, data, _ *cobra.Command) {
		markErr = data.MarkFlagRequired("limit")
	})
	if markErr != nil {
		t.Fatalf("MarkFlagRequired: %v", markErr)
	}
	if got := Signature(required, page); got == want {
		t.Error("marking a flag required did not move the signature, but UsageLine appends required flags")
	}
}

// TestSignatureIgnoresCobraHelpFlag: cobra adds --help to a command's local
// flags the first time anything renders its help or usage, and LocalFlags
// memoizes. Hashing it would make the same page signature depend on what else
// ran in the test binary — differing between `go test -run TestPageSignatures`
// and a full `make test`, which would leave the gate unfixable.
func TestSignatureIgnoresCobraHelpFlag(t *testing.T) {
	base, page := sigTree(nil)
	want := Signature(base, page)

	edited, _ := sigTree(nil)
	data, _, err := edited.Find([]string{"get", "data"})
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	data.InitDefaultHelpFlag() // what Help()/UsageString()/doc generation would do
	if data.LocalFlags().Lookup("help") == nil {
		t.Fatal("fixture assumption broken: InitDefaultHelpFlag did not add --help")
	}
	if got := Signature(edited, page); got != want {
		t.Errorf("rendering help moved the signature (%q → %q); it would differ between test invocations", want, got)
	}
}

func TestSignatureIgnoresInheritedGlobalFlags(t *testing.T) {
	// A new PersistentFlag at root does not move page signatures, because cobra's
	// LocalFlags() excludes inherited flags by pointer identity. This test pins
	// that cobra behavior as a requirement for the signature mechanism.
	base, page := sigTree(nil)
	want := Signature(base, page)
	edited, _ := sigTree(func(_, _, _ *cobra.Command) {})
	edited.PersistentFlags().Bool("new-global", false, "")
	if got := Signature(edited, page); got != want {
		t.Errorf("a new global flag moved every page's signature (%q → %q)", want, got)
	}
}

func TestSignatureCoversShadowedGlobalFlags(t *testing.T) {
	// A command's own local flag shadowing a root PersistentFlag's name *does*
	// move the signature: it is behavior that the page documents. This covers the
	// false-negative case that a name-based filter would have missed.
	base, page := sigTree(nil)
	want := Signature(base, page)
	edited, _ := sigTree(func(_, data, _ *cobra.Command) {
		// root already declares "output" as a PersistentFlag in sigTree.
		// Add a local "output" flag to data: this shadows the root flag.
		data.Flags().String("output", "csv", "output format override")
	})
	if got := Signature(edited, page); got == want {
		t.Error("a shadowed global flag (local flag with same name) should move the signature")
	}
}

// TestPageNodesIncludesGenericDispatchers pins the selection Signature hashes
// and stale-page messages must name: the resource's own commands *and* every
// dispatcher that reaches it by kind.
func TestPageNodesIncludesGenericDispatchers(t *testing.T) {
	root, _ := sigTree(nil)
	var got []string
	for _, n := range PageNodes(root, resources.Page{Name: "data", Kind: "Table"}) {
		got = append(got, n.Path)
	}
	sort.Strings(got)
	want := []string{"qctl apply", "qctl get data"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("PageNodes = %v, want %v", got, want)
	}

	// A page with no kind sees only its own commands.
	got = nil
	for _, n := range PageNodes(root, resources.Page{Name: "data"}) {
		got = append(got, n.Path)
	}
	if len(got) != 1 || got[0] != "qctl get data" {
		t.Errorf("PageNodes (no kind) = %v, want [qctl get data]", got)
	}
}

func TestSignatureCoversGenericDispatchers(t *testing.T) {
	page := resources.Page{Name: "table", Kind: "Table"}
	base, _ := sigTree(nil)
	want := Signature(base, page)

	edited, _ := sigTree(func(_, _, apply *cobra.Command) {
		apply.Flags().Bool("validate-only", false, "")
	})
	if got := Signature(edited, page); got == want {
		t.Error("a change to `apply -f` must move the signature of every page it applies")
	}
}
