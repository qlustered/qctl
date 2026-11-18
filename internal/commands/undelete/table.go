package undelete

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	// recoverLossyNote describes what table recovery leaves behind.
	recoverLossyNote = `NOTE: Recovery is not a full undo. The table comes back, but:
        - its cloud sources come back DISABLED and need re-enabling
          (enable refuses a linked table's composite source)
        - alerts the deletion resolved stay RESOLVED
        - ingestion and profiling jobs the deletion killed stay KILLED`

	// recoverSuccessPrefix opens the success output.
	recoverSuccessPrefix = "Table recovered."
)

// NewTableCommand creates the 'undelete table' command.
func NewTableCommand() *cobra.Command {
	var yes bool
	var newName string

	cmd := &cobra.Command{
		Use:   "table <id-or-name>",
		Short: "Recover a deleted table",
		Long: `Recover a table that was deleted, restoring it to the active state.

` + recoverLossyNote + `

A deleted table is invisible to 'qctl get table' and 'qctl describe table' --
both answer 404. Use 'qctl get tables --state deleted' to find one.

If the table's original name has since been taken by another table, the server
refuses the recovery; re-run with --new-name to restore it under a new name.

Requires the same permission as deleting a table, which only the ops and admin
roles hold.

Under -o json or yaml, the server's answer is printed: {result, dataset_id,
name, state, data_sources_recovered}. -o name prints table/<id>. The prose
goes to stderr.

Examples:
  # Recover by name
  qctl undelete table orders

  # Recover by id, skipping the confirmation prompt
  qctl undelete table 42 --yes

  # Recover under a new name because the old one is taken
  qctl undelete table 42 --new-name orders-restored`,
		Annotations: cmdutil.WithFormats(catalog.For("table"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			dsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			// The two input forms cannot share a path. GET /datasets/{id}
			// answers 404 for a deleted table (atlas
			// _get_dataset_full, settings_view_core.py), so an integer cannot be
			// pre-fetched for a banner -- and the list has no id filter to look it
			// up with, only start/end/page. A name, on the other hand, resolves
			// exactly through the list's equality filter. So: names get a
			// resolved banner, ids let the server be authoritative.
			tableID, atoiErr := strconv.Atoi(args[0])
			label := args[0]
			if atoiErr != nil {
				deleted, resolveErr := dsClient.ResolveDeletedByName(ctx.Credential.AccessToken, args[0], yes)
				if resolveErr != nil {
					return resolveErr
				}
				tableID = deleted.ID
				label = fmt.Sprintf("%s (id=%d)", deleted.Name, deleted.ID)
			}

			w := cmdutil.NewResultWriter(cmd)
			out := w.Human
			printRecoverContext(out, label, newName)

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Proceed with recovery?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("recovery cancelled")
				}
			}

			result, err := dsClient.RecoverDataset(ctx.Credential.AccessToken, tableID, newName)
			if err != nil {
				return annotateRecoverError(err)
			}

			fmt.Fprintf(out, "%s %s (id=%d) is now %s.\n",
				recoverSuccessPrefix, terminal.Sanitize(result.Name), result.DatasetID, terminal.Sanitize(result.State))
			if result.DataSourcesRecovered > 0 {
				fmt.Fprintf(out, "Cloud sources restored: %d (all DISABLED).\n", result.DataSourcesRecovered)
			}
			fmt.Fprintln(out)
			fmt.Fprintln(out, recoverLossyNote)
			fmt.Fprintln(out)
			fmt.Fprintf(out, "  qctl get cloud-sources --table %s   # list them\n", terminal.Sanitize(result.Name))
			fmt.Fprintln(out, "  qctl enable cloud-source <id>          # turn each one back on (a composite source is refused)")

			return w.Write(result, fmt.Sprintf("table/%d", result.DatasetID))
		},
	}

	cmd.Flags().StringVar(&newName, "new-name", "", "Restore under this name instead of the original")
	addUndeleteFlags(cmd, &yes)
	return cmd
}

// printRecoverContext displays what is about to be recovered.
func printRecoverContext(w io.Writer, label, newName string) {
	// label carries the server's name for the table when the argument was a
	// name rather than an id (see the resolve above), so it is untrusted.
	fmt.Fprintf(w, "Table        : %s\n", terminal.Sanitize(label))
	if newName != "" {
		fmt.Fprintf(w, "Restore as   : %s\n", newName)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, recoverLossyNote)
	fmt.Fprintln(w)
}

// annotateRecoverError appends the remedy to the server's 409.
//
// The server's own text says "Provide a new_name", naming the wire field
// rather than the flag, which is not something a CLI user can act on. The
// wrap uses %w so the exit code still rides the chain.
func annotateRecoverError(err error) error {
	if strings.Contains(err.Error(), "already used by another") {
		return fmt.Errorf("%w\n\nRe-run with --new-name <name> to recover it under a different name", err)
	}
	return err
}
