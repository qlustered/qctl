// Package file implements the `qctl file` command group: verbs that act on
// one stored file. Reads live under `qctl get files` and `qctl describe file`.
package file

import (
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

// NewCommand creates the `file` parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "file",
		Aliases: []string{"files"},
		Short:   "Act on one uploaded file",
		Long: `Act on one uploaded file (stored item).

  set-period      send a file that waits for its reporting period to one
  replace-older   answer that a file replaces the older file it asks about
  keep-both       answer that it does not
  send-issues-report
                  email a file's issues report to the broker who sent it

Read files with:
  qctl get files
  qctl describe file <id>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newSetPeriodCommand(), newReplaceOlderCommand(), newKeepBothCommand(),
		newSendIssuesReportCommand())
	return cmd
}
