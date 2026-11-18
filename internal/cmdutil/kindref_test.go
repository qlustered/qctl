package cmdutil

import (
	"slices"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const kindRefUUID = "5f0c8a52-3b9e-4c1d-9a7e-2d4b6f8e1a3c"

func TestParseKindRef(t *testing.T) {
	tests := []struct {
		name, slot, in, want string
	}{
		{"int kind", "table", "table/42", "42"},
		{"uuid kind", "rule-review", "rule-review/" + kindRefUUID, kindRefUUID},
		{"bare id", "table", "42", "42"},
		{"bare name", "table", "orders", "orders"},
		{"a name with a slash", "table", "Q1/2024", "Q1/2024"},
		{"a known kind before a name", "table", "table/Q1 2024", "table/Q1 2024"},
		{"zero is no id", "table", "table/0", "table/0"},
		{"a padded int is no id", "table", "table/042", "table/042"},
		{"a short uuid is no id", "rule", "rule/5f0c8a52", "rule/5f0c8a52"},
		{"an unknown kind", "table", "tables/42", "tables/42"},
		{"linked-table takes table", "linked-table", "table/42", "42"},
		{"linked-table takes its own", "linked-table", "linked-table/42", "42"},
		{"rule takes rule-revision", "rule", "rule-revision/" + kindRefUUID, kindRefUUID},
		{"only the first slash cuts", "table", "table/42/7", "table/42/7"},
		{"a context is keyed by name", "context", "context/dev", "dev"},
		{"a context name keeps its slashes", "context", "context/team/dev", "team/dev"},
		{"context/ with no name", "context", "context/", "context/"},
		{"another slot keeps context/ as a name", "table", "context/dev", "context/dev"},
		{"legacy concept alias", "standard-field", "concept/" + kindRefUUID, kindRefUUID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseKindRef(tt.slot, tt.in)
			if err != nil {
				t.Fatalf("ParseKindRef(%q, %q) error = %v", tt.slot, tt.in, err)
			}
			if got != tt.want {
				t.Errorf("ParseKindRef(%q, %q) = %q, want %q", tt.slot, tt.in, got, tt.want)
			}
		})
	}
}

func TestParseKindRefRefusesAReferenceTheSlotCannotTake(t *testing.T) {
	tests := []struct {
		name, slot, in string
	}{
		{"another kind", "table", "rule/" + kindRefUUID},
		{"a table slot refuses linked-table", "table", "linked-table/42"},
		{"a uuid for an int kind", "table", "table/" + kindRefUUID},
		{"an int for a uuid kind", "rule-review", "rule-review/42"},
		{"a context slot refuses another kind", "context", "table/42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseKindRef(tt.slot, tt.in)
			if err == nil {
				t.Fatalf("ParseKindRef(%q, %q) accepted it", tt.slot, tt.in)
			}
			if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
			}
		})
	}
}

func TestArgKindsDefaultsToTheResource(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        []string
	}{
		{"a reference kind", catalog.For("alert"), []string{"alert"}},
		{"not a reference kind", catalog.For("table-config"), nil},
		{"declared", WithArgKinds(catalog.For("rule-review"), "rule-review", "dry-run-job"), []string{"rule-review", "dry-run-job"}},
		{"declared not a reference", WithArgKinds(catalog.For("file"), "-"), []string{"-"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ArgKinds(&cobra.Command{Annotations: tt.annotations})
			if !slices.Equal(got, tt.want) {
				t.Errorf("ArgKinds = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripKindRefsStripsEachSlot(t *testing.T) {
	var got []string
	root := &cobra.Command{Use: "qctl"}
	link := &cobra.Command{
		Use:         "link <review> <job> <note>...",
		Annotations: WithArgKinds(catalog.For("rule-review"), "rule-review", "dry-run-job", "-"),
		RunE: func(_ *cobra.Command, args []string) error {
			got = args
			return nil
		},
	}
	root.AddCommand(link)
	StripKindRefs(root)

	root.SetArgs([]string{"link", "rule-review/" + kindRefUUID, "dry-run-job/7", "table/1", "table/2"})
	if err := root.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := []string{kindRefUUID, "7", "table/1", "table/2"}
	if !slices.Equal(got, want) {
		t.Errorf("RunE saw %q, want %q", got, want)
	}

	got = nil
	root.SetArgs([]string{"link", "table/42", "dry-run-job/7"})
	err := root.Execute()
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("a table reference in a review slot: exit %d (%v), want %d", code, err, qerrors.ExitBadUsage)
	}
	if got != nil {
		t.Errorf("RunE ran with %q after a refused reference", got)
	}
}

func TestRefFlagsStripAndKeepTheirType(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RefInt(flags, "table-id", "table", "")
	RefString(flags, "group", "data-review-group", "")
	RefStringP(flags, "org", "O", "org", "")

	if err := flags.Parse([]string{"--table-id", "table/42", "--group", "data-review-group/" + kindRefUUID, "-O", "acme"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if typ := flags.Lookup("table-id").Value.Type(); typ != "int" {
		t.Errorf("--table-id type = %q, want int", typ)
	}
	if id, err := flags.GetInt("table-id"); err != nil || id != 42 {
		t.Errorf("GetInt(table-id) = %d, %v; want 42", id, err)
	}
	if g, err := flags.GetString("group"); err != nil || g != kindRefUUID {
		t.Errorf("GetString(group) = %q, %v; want %q", g, err, kindRefUUID)
	}
	if o, err := flags.GetString("org"); err != nil || o != "acme" {
		t.Errorf("GetString(org) = %q, %v; want acme", o, err)
	}
}

func TestRefFlagsRefuse(t *testing.T) {
	tests := []struct {
		name, value string
		exitTwo     bool
	}{
		{"another kind", "rule/" + kindRefUUID, true},
		{"not an int", "orders", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &refValue{slot: "table", typ: "int", value: "0"}
			err := v.Set(tt.value)
			if err == nil {
				t.Fatalf("Set(%q) accepted it", tt.value)
			}
			if tt.exitTwo && qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
			}
			if v.String() != "0" {
				t.Errorf("value = %q after a refused Set, want it unchanged", v.String())
			}
		})
	}
}

func TestRefIntVarStoresTheStrippedID(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	id := 99
	RefIntVar(flags, &id, "cloud-source-id", "cloud-source", "")
	if id != 0 {
		t.Fatalf("RefIntVar left the target at %d, want it zeroed", id)
	}
	if err := flags.Parse([]string{"--cloud-source-id", "cloud-source/7"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if id != 7 {
		t.Errorf("target = %d, want 7", id)
	}
	if got, err := flags.GetInt("cloud-source-id"); err != nil || got != 7 {
		t.Errorf("GetInt = %d, %v; want 7", got, err)
	}
}

func TestRefListFlagsStripEachElement(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RefStringArray(flags, "table-kind", "table-kind", "")
	RefIntSlice(flags, "history-ids", "history", "")

	args := []string{
		"--table-kind", "table-kind/" + kindRefUUID, "--table-kind", "Orders, EU",
		"--history-ids", "history/3,4", "--history-ids", "history/5",
	}
	if err := flags.Parse(args); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if typ := flags.Lookup("table-kind").Value.Type(); typ != "stringArray" {
		t.Errorf("--table-kind type = %q, want stringArray", typ)
	}
	if typ := flags.Lookup("history-ids").Value.Type(); typ != "intSlice" {
		t.Errorf("--history-ids type = %q, want intSlice", typ)
	}
	kinds, err := flags.GetStringArray("table-kind")
	if err != nil || !slices.Equal(kinds, []string{kindRefUUID, "Orders, EU"}) {
		t.Errorf("GetStringArray = %q, %v; want [%s \"Orders, EU\"]", kinds, err, kindRefUUID)
	}
	ids, err := flags.GetIntSlice("history-ids")
	if err != nil || !slices.Equal(ids, []int{3, 4, 5}) {
		t.Errorf("GetIntSlice = %v, %v; want [3 4 5]", ids, err)
	}
}

func TestRefListFlagsAreEmptyUntilSet(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RefStringArray(flags, "table-kind", "table-kind", "")
	RefIntSlice(flags, "history-ids", "history", "")
	if kinds, err := flags.GetStringArray("table-kind"); err != nil || len(kinds) != 0 {
		t.Errorf("GetStringArray = %q, %v; want empty", kinds, err)
	}
	if ids, err := flags.GetIntSlice("history-ids"); err != nil || len(ids) != 0 {
		t.Errorf("GetIntSlice = %v, %v; want empty", ids, err)
	}
	for _, name := range []string{"table-kind", "history-ids"} {
		if def := flags.Lookup(name).DefValue; def != "" {
			t.Errorf("--%s DefValue = %q, want empty so --help shows no default", name, def)
		}
	}
}

func TestRefListFlagsAreSliceValues(t *testing.T) {
	flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
	RefStringArray(flags, "table-kind", "table-kind", "")
	RefIntSlice(flags, "history-ids", "history", "")
	native := pflag.NewFlagSet("native", pflag.ContinueOnError)
	native.StringArray("table-kind", nil, "")
	native.IntSlice("history-ids", nil, "")
	args := []string{"--table-kind", "table-kind/" + kindRefUUID, "--table-kind", "Orders, EU", "--history-ids", "history/3,4"}
	if err := flags.Parse(args); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if err := native.Parse([]string{"--table-kind", kindRefUUID, "--table-kind", "Orders, EU", "--history-ids", "3,4"}); err != nil {
		t.Fatalf("Parse native: %v", err)
	}
	for _, name := range []string{"table-kind", "history-ids"} {
		if got, want := RenderFlag(flags.Lookup(name)), RenderFlag(native.Lookup(name)); got != want {
			t.Errorf("RenderFlag(--%s) = %q, want %q as pflag's own list renders", name, got, want)
		}
	}

	kinds := flags.Lookup("table-kind").Value.(pflag.SliceValue)
	if err := kinds.Replace([]string{"table-kind/" + kindRefUUID}); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if got := kinds.GetSlice(); !slices.Equal(got, []string{kindRefUUID}) {
		t.Errorf("GetSlice after Replace = %q, want [%s]", got, kindRefUUID)
	}
	if err := kinds.Replace([]string{"bus", "rule/" + kindRefUUID}); qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
		t.Errorf("Replace with another kind: exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
	}
	if got := kinds.GetSlice(); !slices.Equal(got, []string{kindRefUUID}) {
		t.Errorf("GetSlice after a refused Replace = %q, want it unchanged", got)
	}
	if err := kinds.Replace(nil); err != nil || len(kinds.GetSlice()) != 0 {
		t.Errorf("Replace(nil) = %v, leaving %q; want empty", err, kinds.GetSlice())
	}
}

func TestRefListFlagsRefuse(t *testing.T) {
	tests := []struct {
		name, flag, value string
		exitTwo           bool
	}{
		{"another kind in an array", "table-kind", "rule/" + kindRefUUID, true},
		{"another kind in a slice", "history-ids", "3,alert/4", true},
		{"not an int", "history-ids", "3,x", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
			RefStringArray(flags, "table-kind", "table-kind", "")
			RefIntSlice(flags, "history-ids", "history", "")
			err := flags.Lookup(tt.flag).Value.Set(tt.value)
			if err == nil {
				t.Fatalf("Set(%q) accepted it", tt.value)
			}
			if tt.exitTwo && qerrors.GetExitCode(err) != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d", qerrors.GetExitCode(err), qerrors.ExitBadUsage)
			}
			if got := flags.Lookup(tt.flag).Value.String(); got != "[]" {
				t.Errorf("value = %q after a refused Set, want it unchanged", got)
			}
		})
	}
}
