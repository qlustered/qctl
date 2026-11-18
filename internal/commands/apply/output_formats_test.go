package apply

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// Apply commands declare their document, reference and prose formats.
func TestApplyCommandsDeclareTableJSONYAMLAndName(t *testing.T) {
	for _, cmd := range []*cobra.Command{
		NewDatasetCommand(), NewDestinationCommand(), NewBackupSettingsCommand(),
		NewCloudSourceCommand(), NewLinkedTableCommand(),
	} {
		if got := strings.Join(cmdutil.SupportedOutputFormats(cmd), ","); got != "table,json,yaml,name" {
			t.Errorf("apply %s supports %q, want \"table,json,yaml,name\"", cmd.Name(), got)
		}
	}
}

// `qctl apply -f x.yaml` renders through the same functions as
// `qctl apply table -f x.yaml`, so the parent must not advertise more than the
// subcommands do. It has a RunE, which is what makes it a rendering command
// rather than a dispatch node that inherits the root flag's four.
func TestApplyParentDeclaresTheSameSetAsItsSubcommands(t *testing.T) {
	parent := NewCommand()
	if parent.RunE == nil {
		t.Fatal("apply has no RunE; if -f moved to a subcommand this declaration is no longer needed")
	}
	if got := strings.Join(cmdutil.SupportedOutputFormats(parent), ","); got != "table,json,yaml,name" {
		t.Errorf("apply supports %q, want \"table,json,yaml,name\"", got)
	}
	if err := cmdutil.ValidateOutputFormatValue("name", cmdutil.SupportedOutputFormats(parent)); err != nil {
		t.Fatalf("apply -f rejected -o name: %v", err)
	}
}
