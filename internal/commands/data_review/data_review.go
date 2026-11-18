// Package data_review implements the qctl data-review command group for the
// data-row review workflow.
package data_review //nolint:revive // Package name mirrors the hyphenated command.

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the data-review parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data-review",
		Short: "Participate in the data-row review workflow",
		Long: `Participate in the data-row review workflow that gates quarantined rows; qctl review gates rules instead.

Read submissions and their changes with:
  qctl get data-review-groups
  qctl get data-review-changes
  qctl get data-reviews
  qctl describe data-review <review-id>

Assign, re-request or cancel a whole request with:
  qctl data-review-group assign|re-request|cancel <group-id>

New to the workflow? Read the guide:
  qctl guide review-data   # the requester and reviewer loops, and who may do what`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(
		newProgressCommand(),
		newApproveCommand(),
		newWithdrawCommand(),
		newAcceptCellCommand(),
		newClearCellCommand(),
		newWithdrawCellCommand(),
		newRequestCommand(),
		newAssignCommand(),
		newUnassignCommand(),
		newRerequestCommand(),
		newCloseCommand(),
		newCommentCommand(),
	)
	return cmd
}

// The --output values that make stdout a document rather than prose.
const (
	outputFormatJSON = "json"
	outputFormatYAML = "yaml"
)

// structuredOutput reports an explicit --output value and whether it asks for
// a document (json or yaml) rather than prose. An --output left unset is prose.
func structuredOutput(cmd *cobra.Command) (string, bool) {
	if !cmd.Flags().Changed("output") {
		return "", false
	}
	format, _ := cmd.Flags().GetString("output")
	return format, format == outputFormatJSON || format == outputFormatYAML
}
