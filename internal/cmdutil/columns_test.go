package cmdutil

import (
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

type columnsListRow struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type columnsMatchRow struct {
	Name  string `json:"name"`
	Match string `json:"match"`
}

// newColumnsRoot mirrors the real root's two flags and its hook: a persistent
// --output and --columns, and ValidateDeclaredColumns in PersistentPreRunE.
// ran reports whether the leaf's RunE was reached.
func newColumnsRoot(declare func(*cobra.Command)) (root *cobra.Command, ran *bool) {
	ran = new(bool)
	leaf := &cobra.Command{
		Use:  "thing",
		RunE: func(*cobra.Command, []string) error { *ran = true; return nil },
	}
	if declare != nil {
		declare(leaf)
	}
	root = &cobra.Command{Use: "qctl", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().StringP("output", "o", "table", "output format")
	root.PersistentFlags().String("columns", "", "columns")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return ValidateDeclaredColumns(cmd)
	}
	root.AddCommand(leaf)
	return root, ran
}

func declareListAndMatch(cmd *cobra.Command) {
	DeclareColumns(cmd, []columnsListRow(nil), []columnsMatchRow(nil))
}

func TestDeclareColumnsRecordsTheUnionInOrder(t *testing.T) {
	cmd := &cobra.Command{Use: "thing", Annotations: map[string]string{"resource": "table"}}
	got := DeclareColumns(cmd, []columnsListRow(nil), []columnsMatchRow(nil))

	want := "id,name,match"
	if strings.Join(got, ",") != want {
		t.Errorf("DeclareColumns returned %v, want %s", got, want)
	}
	if strings.Join(DeclaredColumns(cmd), ",") != want {
		t.Errorf("DeclaredColumns = %v, want %s", DeclaredColumns(cmd), want)
	}
	if cmd.Annotations["resource"] != "table" {
		t.Errorf("resource annotation = %q, want it kept", cmd.Annotations["resource"])
	}
}

func TestDeclaredColumnsIsNilWhenNothingWasDeclared(t *testing.T) {
	if got := DeclaredColumns(&cobra.Command{Use: "thing"}); got != nil {
		t.Errorf("DeclaredColumns = %v, want nil", got)
	}
}

func TestValidateDeclaredColumnsRejectsAnUnknownNameWithExitTwo(t *testing.T) {
	for _, args := range [][]string{
		{"thing", "--columns", "id,nope"},
		{"thing", "-o", "json", "--columns", "nope"},
		{"thing", "-o", "yaml", "--columns", "nope"},
		{"thing", "-o", "name", "--columns", "nope"},
	} {
		root, ran := newColumnsRoot(declareListAndMatch)
		root.SetArgs(args)
		err := root.Execute()
		if err == nil {
			t.Errorf("%v: accepted an undeclared column", args)
			continue
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("%v: exit code = %d, want %d (ExitBadUsage)", args, code, qerrors.ExitBadUsage)
		}
		if !strings.Contains(err.Error(), `unknown column "nope"`) || !strings.Contains(err.Error(), "valid: id, name, match") {
			t.Errorf("%v: message must name the column and the declared set: %q", args, err)
		}
		if *ran {
			t.Errorf("%v: RunE ran after a rejected --columns", args)
		}
	}
}

func TestValidateDeclaredColumnsAcceptsDeclaredNamesInEveryFormat(t *testing.T) {
	for _, args := range [][]string{
		{"thing"},
		{"thing", "--columns", ""},
		{"thing", "--columns", " id , ,match "},
		{"thing", "-o", "json", "--columns", "name,match"},
	} {
		root, ran := newColumnsRoot(declareListAndMatch)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Errorf("%v: %v", args, err)
		}
		if !*ran {
			t.Errorf("%v: RunE did not run", args)
		}
	}
}

func TestValidateDeclaredColumnsSkipsAnUndeclaredCommand(t *testing.T) {
	root, ran := newColumnsRoot(nil)
	root.SetArgs([]string{"thing", "--columns", "nope"})
	if err := root.Execute(); err != nil {
		t.Errorf("a command that declared no columns was checked: %v", err)
	}
	if !*ran {
		t.Error("RunE did not run")
	}
}
