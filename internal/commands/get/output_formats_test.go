package get

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

func TestBespokeGetCommandsDeclareTheirFormats(t *testing.T) {
	cases := []struct {
		cmd  *cobra.Command
		want string
	}{
		// NewDataCommand's renderer is internal/dataview: table, name and
		// anything else all come out as Markdown, so only json and yaml are
		// distinct machine formats.
		{NewDataCommand(), "table,json,yaml"},
		// NewTableKindGetCommand's default arm does reach tableui.PrintFromCmd,
		// so name would render -- but only after a Name:/Slug:/ID:/Built-in:
		// header block that no name consumer can parse. Rejected on purpose;
		// a real name view is separate work (see the ticket's follow-ups).
		{NewTableKindGetCommand(), "table,json,yaml"},
		// NewRuleCommand's writeCode arm prints the raw Python source for
		// -o code; its default arm is tableui.PrintFromCmd, which honors name.
		{NewRuleCommand(), "table,json,yaml,code,name"},
	}
	for _, tc := range cases {
		got := strings.Join(cmdutil.SupportedOutputFormats(tc.cmd), ",")
		if got != tc.want {
			t.Errorf("get %s supports %q, want %q", tc.cmd.Name(), got, tc.want)
		}
	}
}

func TestGetRuleStillAcceptsCode(t *testing.T) {
	if err := cmdutil.ValidateOutputFormatValue("code", cmdutil.SupportedOutputFormats(NewRuleCommand())); err != nil {
		t.Errorf("-o code rejected on get rule: %v", err)
	}
}

// Each of these refuses -o name: get data renders Markdown, and the others
// list objects no command takes as a reference (audit entries, rule ignores,
// a table's review policy).
func TestGetCommandsRejectName(t *testing.T) {
	for _, cmd := range []*cobra.Command{
		NewDataCommand(),
		NewPrivilegeAuditCommand(),
		NewRuleIgnoresCommand(),
		NewDataReviewPolicyCommand(),
		NewRuleReviewPolicyCommand(),
	} {
		err := cmdutil.ValidateOutputFormatValue("name", cmdutil.SupportedOutputFormats(cmd))
		if err == nil {
			t.Errorf("get %s -o name was accepted", cmd.Name())
			continue
		}
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Errorf("get %s: exit code = %d, want %d", cmd.Name(), code, qerrors.ExitBadUsage)
		}
	}
}
