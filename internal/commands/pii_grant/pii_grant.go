// Package pii_grant implements the qctl pii-grant command group: an admin's
// temporary unmasked access to one table's personal data for one user.
package pii_grant //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"strconv"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pii_grants"
	"github.com/spf13/cobra"
)

// NewCommand creates the pii-grant parent command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pii-grant",
		Short: "Grant or revoke temporary unmasked access to a table's personal data",
		Long: `Give one user temporary access to a table's unmasked personal data, or take
it back before it expires.

  create   grant a user unmasked access for 1h, 8h, 24h or 7d
  revoke   end an active grant early

These routes need the pii:grant permission, which the admin and ops roles
hold; anyone else gets a 403 (exit 4).

List a table's grants, active and past, with:
  qctl get pii-grants <table>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newCreateCommand(), newRevokeCommand())
	return cmd
}

// printGrant reports a mutation: the grant as the server sent it for
// -o json|yaml, pii-grant/<id> for -o name, and otherwise line followed by the
// command that lists the table's grants.
func printGrant(cmd *cobra.Command, grant *pii_grants.Grant, line string) error {
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml":
		return output.WriteRaw(out, format, grant)
	case "name":
		_, err := fmt.Fprintf(out, "pii-grant/%d\n", grant.ID)
		return err
	}
	if _, err := fmt.Fprintln(out, line); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "Next: qctl get pii-grants --table-id %d\n", grant.DatasetID)
	return err
}

// expiry renders a grant's expiry in the local zone.
func expiry(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

// tableRef names a table in prompts and results.
func tableRef(id int) string {
	return "table/" + strconv.Itoa(id)
}
