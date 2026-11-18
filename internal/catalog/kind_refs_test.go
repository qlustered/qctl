package catalog_test

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// nameOutputExceptions are commands whose -o name needs more than one
// reference kind or prints values instead. Adding a line is a claim about the output
// contract, so the diff is the review.
var nameOutputExceptions = map[string]string{
	"qctl get tables-overview":            "mixed table/<id> and table-series/<uuid> references; tested by TestTablesOverviewNames",
	"qctl deliverable-run waive":          "numeric waiver id for deliverable-run unwaive's second positional",
	"qctl get table-schema":               "column names",
	"qctl get value-recommendations":      "recommended values",
	"qctl get standard-field-suggestions": "<column>/<standard field slug>",
	"qctl get submission-statuses":        "cloud-source/<id>, table/<id> or table-series/<uuid> by scope; tested by TestGetSubmissionStatusesNames",
}

// refIDFor returns an id of the shape kind's references carry.
func refIDFor(t *testing.T, kind string) string {
	t.Helper()
	for _, id := range []string{"7", "5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"} {
		if got, err := cmdutil.ParseKindRef(kind, kind+"/"+id); err == nil && got == id {
			return id
		}
	}
	t.Fatalf("ParseKindRef strips no %s/<id>", kind)
	return ""
}

// nameProducers returns, by path, the kind each leaf prints under root's
// -o name. A command with a local --output shadows root's flag and is no
// producer.
func nameProducers(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Cmd.RunE == nil || n.Cmd.HasSubCommands() || n.Cmd.LocalNonPersistentFlags().Lookup("output") != nil {
			continue
		}
		if slices.Contains(cmdutil.SupportedOutputFormats(n.Cmd), "name") {
			out[n.Path] = output.NameKind(n.Cmd)
		}
	}
	return out
}

func TestNameOutputPrintsAReferenceKind(t *testing.T) {
	producers := nameProducers(t)
	for path, kind := range producers {
		if _, ok := nameOutputExceptions[path]; ok {
			continue
		}
		if !slices.Contains(cmdutil.RefKinds(), kind) {
			t.Errorf("%s prints %s/<id> under -o name, which no reference slot "+
				"recognizes. Print a kind in cmdutil's refKinds (output.WithNameKind "+
				"when the objects are not the command's resource), narrow the command "+
				"with cmdutil.WithFormats, or record it in nameOutputExceptions.", path, kind)
		}
	}
	for path := range nameOutputExceptions {
		if _, ok := producers[path]; !ok {
			t.Errorf("nameOutputExceptions lists %s, which no longer prints -o name", path)
		}
	}
}

func TestEveryRefKindIsAResource(t *testing.T) {
	root := realRoot(t)
	for _, kind := range cmdutil.RefKinds() {
		r, ok := catalog.Lookup(root, kind)
		if !ok || r.Name != kind {
			t.Errorf("refKinds has %q, which is not a catalog resource name", kind)
		}
	}
}

// Every kind -o name prints is taken back by some positional or flag, so
// `get X -o name | xargs qctl <verb>` has somewhere to go.
func TestEveryPrintedKindHasAConsumer(t *testing.T) {
	printed := map[string]bool{}
	for path, kind := range nameProducers(t) {
		if _, ok := nameOutputExceptions[path]; !ok {
			printed[kind] = true
		}
	}
	taken := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		var slots []string
		if len(positionalTokens(n.Cmd.Use)) > 0 {
			slots = cmdutil.ArgKinds(n.Cmd)
		}
		n.Cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if kind := cmdutil.RefFlagKind(f); kind != "" {
				slots = append(slots, kind)
			}
		})
		for kind := range printed {
			for _, slot := range slots {
				id := refIDFor(t, kind)
				if got, err := cmdutil.ParseKindRef(slot, kind+"/"+id); err == nil && got == id {
					taken[kind] = true
				}
			}
		}
	}
	var orphans []string
	for kind := range printed {
		if !taken[kind] {
			orphans = append(orphans, kind)
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("-o name prints these kinds, and no positional or flag takes them back: %s",
			strings.Join(orphans, ", "))
	}
}

// TestPositionalsTakeTheirOwnNameOutput proves cmdutil.StripKindRefs is
// installed on the real tree: every reference positional refuses a reference
// of a kind it does not take, before the command itself runs.
func TestPositionalsTakeTheirOwnNameOutput(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	checked := 0
	for _, n := range catalog.Walk(realRoot(t)) {
		kinds := cmdutil.ArgKinds(n.Cmd)
		if n.Cmd.RunE == nil || len(kinds) == 0 || kinds[0] == "-" || len(positionalTokens(n.Cmd.Use)) == 0 {
			continue
		}
		ref, want := foreignRef(t, kinds[0])
		err := n.Cmd.RunE(n.Cmd, []string{ref})
		if err == nil || err.Error() != want.Error() || qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
			t.Errorf("%s %s: got %v (exit %d), want the exit-2 refusal %q.\n"+
				"    Fix: internal/commands/root.init() must call cmdutil.StripKindRefs(rootCmd)\n"+
				"    after every AddCommand, and nothing may assign RunE after that point.",
				n.Path, ref, err, qerrors.GetExitCode(err), want)
		}
		checked++
	}
	if checked < 100 {
		t.Fatalf("checked only %d commands; the walk no longer sees the reference positionals", checked)
	}
}

// foreignRef returns a reference slot does not accept, and the error
// ParseKindRef refuses it with.
func foreignRef(t *testing.T, slot string) (string, error) {
	t.Helper()
	for _, kind := range cmdutil.RefKinds() {
		ref := kind + "/" + refIDFor(t, kind)
		if _, err := cmdutil.ParseKindRef(slot, ref); err != nil {
			return ref, err
		}
	}
	t.Fatalf("slot %s accepts every kind", slot)
	return "", nil
}

// Every command whose positional is a table takes the table/<id> that
// `get tables -o name` prints.
func TestTablePositionalsTakeTableRefs(t *testing.T) {
	byPath := map[string]*cobra.Command{}
	for _, n := range catalog.Walk(realRoot(t)) {
		byPath[n.Path] = n.Cmd
	}
	for path, mode := range tableScopedCommands {
		if mode == selectorFlagsOnly {
			continue
		}
		cmd := byPath[path]
		if cmd == nil {
			continue // TestTableSelectorInventoryIsComplete reports it
		}
		kinds := cmdutil.ArgKinds(cmd)
		if len(kinds) == 0 {
			t.Errorf("%s takes a table positional but declares no slot kind; "+
				"add cmdutil.WithArgKinds(..., \"table\")", path)
			continue
		}
		if got, err := cmdutil.ParseKindRef(kinds[0], "table/42"); err != nil || got != "42" {
			t.Errorf("%s: its positional takes table/42 as %q, %v; want 42", path, got, err)
		}
	}
}

// idFlagKinds is the kind/<id> reference each id flag takes, by flag name,
// on every command that registers it.
var idFlagKinds = map[string]string{
	"alert-item-id":            "alert",
	"cloud-source-id":          "cloud-source",
	"comment-thread-id":        "comment-thread",
	"data-source-id":           "cloud-source",
	"data-source-model-id":     "cloud-source",
	"dataset-id":               "table",
	"dataset-rule-ids":         "table-rule",
	"deliverable":              "deliverable",
	"destination-id":           "destination",
	"dry-run-job":              "dry-run-job",
	"file-id":                  "file",
	"for-rule":                 "rule",
	"for-table":                "table",
	"from-id":                  "table",
	"group":                    "data-review-group",
	"history-id":               "history",
	"history-ids":              "history",
	"problem-dataset-rule-id":  "table-rule",
	"replaces":                 "file",
	"request-id":               "privilege-request",
	"review-change-history-id": "history",
	"review-group-id":          "data-review-group",
	"series":                   "table-series",
	"stored-item-id":           "file",
	"table-id":                 "table",
	"table-kind":               "table-kind",
	"template-id":              "deliverable-template",
	"to-table-id":              "table",
	"warning-item-id":          "warning",
}

// idFlagKindsByCommand overrides idFlagKinds where one flag name takes a
// different kind on different commands.
var idFlagKindsByCommand = map[string]string{
	"qctl get corrected-lines --file":                       "file",
	"qctl describe deliverable-revision --revision":         "deliverable-revision",
	"qctl deliverable accept-column-suggestions --revision": "deliverable-revision",
	"qctl deliverable confirm-columns --revision":           "deliverable-revision",
	"qctl deliverable confirm-cells --revision":             "deliverable-revision",
	"qctl deliverable test-filter --revision":               "deliverable-revision",
	"qctl enable deliverable --revision":                    "deliverable-revision",
	"qctl inspect deliverable --revision":                   "deliverable-revision",
	"qctl upload deliverable-template --revision":           "deliverable-revision",
	"qctl get alerts --rule":                                "table-rule",
	"qctl get rule-ignores --rule":                          "table-rule",
	"qctl get rule-reviews --rule":                          "rule",
	"qctl rows ignore-rule --rule":                          "table-rule",
	"qctl rows unignore-rule --rule":                        "table-rule",
}

// idFlagExceptions are the *-id and *-ids flags that take no kind/<id>,
// because no command's -o name prints what they name.
var idFlagExceptions = map[string]string{
	"anchor-row-id": "a row id; no row kind",
	"delete-id":     "a row delete id; no row-delete kind",
	"group-id":      "composite spec group UUID",
	"model-id":      "an id within get history --model, of whatever kind that names",
	"revision-id":   "composite spec revision UUID",
	"row-id":        "row ids; no row kind",
	"to-id":         "a user UUID; no user kind",
}

func TestIDFlagsTakeTheirKind(t *testing.T) {
	var offenders []string
	seen := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		n.Cmd.LocalNonPersistentFlags().VisitAll(func(f *pflag.Flag) {
			got := cmdutil.RefFlagKind(f)
			want, listed := idFlagKinds[f.Name]
			if w, ok := idFlagKindsByCommand[n.Path+" --"+f.Name]; ok {
				want, listed = w, true
				seen[n.Path+" --"+f.Name] = true
			}
			_, exempt := idFlagExceptions[f.Name]
			switch {
			case listed:
				seen[f.Name] = true
				if got != want {
					offenders = append(offenders, n.Path+" --"+f.Name+": takes "+strconv.Quote(got)+", want "+want)
				}
			case exempt:
				seen[f.Name] = true
			case got != "":
				offenders = append(offenders, n.Path+" --"+f.Name+": takes "+got+" but idFlagKinds does not list it")
			case f.Value.Type() != "bool" && (strings.HasSuffix(f.Name, "-id") || strings.HasSuffix(f.Name, "-ids")):
				offenders = append(offenders, n.Path+" --"+f.Name+": an id flag in neither idFlagKinds nor idFlagExceptions")
			}
		})
	}
	for name := range idFlagKinds {
		if !seen[name] {
			offenders = append(offenders, "--"+name+": in idFlagKinds, registered by no command")
		}
	}
	for name := range idFlagExceptions {
		if !seen[name] {
			offenders = append(offenders, "--"+name+": in idFlagExceptions, registered by no command")
		}
	}
	for key := range idFlagKindsByCommand {
		if !seen[key] {
			offenders = append(offenders, key+": in idFlagKindsByCommand, registered by no command")
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("register id flags with cmdutil.RefInt/RefIntVar/RefString/RefStringArray/RefIntSlice:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}
