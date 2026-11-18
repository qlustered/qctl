package apply

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

const unknownOrganizationName = "Unknown"

// applyKindsLine lists the manifest kinds `qctl apply -f` accepts, generated
// from the dispatch table so --help can never advertise a kind genericApply
// rejects.
func applyKindsLine() string {
	return "Valid kinds: " + strings.Join(applyKinds(), ", ")
}

// NewCommand creates the apply command
func NewCommand() *cobra.Command {
	long := fmt.Sprintf(`Commands for applying configurations declaratively from files.

If you specify -f with a YAML file, the resource type is inferred from
the 'kind' field in the manifest (like kubectl). Otherwise, use a
subcommand to specify the resource type explicitly.

%s

A file may hold several documents separated by ---; each is applied in file
order and reported on its own line. Kinds may be mixed. Use --fail-fast to stop
at the first failure instead of attempting the rest.

--wait waits, one document at a time, for a linked table's composite dry run,
or for the validation dry run of a table rule a TableRule document creates.
Each wait prints a line naming it to stderr, then its progress; the outcome
is prose on stdout in the table view and stderr under -o json|yaml|name.
A table rule's wait exits 0 only when validation passed: the
rule is enabled, or it waits on a draft migration, and --wait points at the
needs_review_migration alert that applies it. A failed or cancelled
validation, a validation that is no longer queued, and a rule that moves to
any other state exit 1; running out of --wait-timeout exits 124. Ctrl-C
during a wait ends the whole apply (exit 130) without applying the documents
after it. A patch, a staged create, and a create that starts enabled or with
no validation queued wait for nothing; --wait says so on stderr.

Examples:
  # Generic apply (infers kind from file)
  qctl apply -f table.yaml
  qctl apply -f destination.yaml

  # Explicit subcommand
  qctl apply table -f table.yaml
  qctl apply destination -f dest.yaml

  # Several resources in one file (--- separated), applied in order
  qctl apply -f table-and-rules.yaml

  # Non-interactive (skip confirmation prompts)
  qctl apply -f table-rule.yaml --yes

  # Create table rules and wait for each one's validation dry run
  qctl apply -f table-rule.yaml --yes --wait

First time instantiating a rule on a table? Run:
  qctl guide instantiate-rule`, applyKindsLine())

	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply configurations",
		Long:  long,
		// The generic runner renders results, as the subcommands do.
		Annotations: cmdutil.WithFormats(catalog.Dispatcher(applyKinds()), "table", "json", "yaml", "name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)\n\nUsage:\n  qctl apply -f <file.yaml>\n  qctl apply <resource> -f <file.yaml>")
			}
			if _, _, err := linkedTableWaitFlags(cmd); err != nil {
				return err
			}
			return genericApply(cmd, filePath)
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file")
	// Persistent for the same reason --yes is: every apply subcommand reads a
	// -f file, and every -f file may hold several documents.
	cmd.PersistentFlags().Bool("fail-fast", false, "Stop processing on first document failure")
	cmd.Flags().Bool("validate-only", false, "Validate the manifest server-side without persisting (linked tables only; a file holding another kind is refused)")
	cmdutil.AddWaitFlags(cmd, "a linked table's composite dry run, or the validation of a table rule the file creates, to settle")
	cmd.PersistentFlags().BoolP("yes", "y", false, "Skip interactive confirmation prompts (for scripted/non-interactive use)")

	// Add subcommands
	cmd.AddCommand(NewDestinationCommand())
	cmd.AddCommand(NewBackupSettingsCommand())
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewCloudSourceCommand())
	cmd.AddCommand(NewLinkedTableCommand())

	return cmd
}
