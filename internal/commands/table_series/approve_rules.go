package table_series //nolint:revive // Package name mirrors the command.

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newApproveRulesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approve-rules <name-or-id> [--fingerprint <sha256>]",
		Short: "Approve a table series' current rule set",
		Long: `Approve the rules of every table in a series, by exact series name or full UUID.

Broker report mail for the series' tables is sent only while the latest
approval matches the current rules. Approving schedules the report updates
that a rule change paused. An approval is permanent: a series with an
approval cannot be deleted.

Before confirmation, stderr shows the rule set's fingerprint, each table's rule
count and the latest approval, including under --yes. The fingerprint covers
only each table's enabled rules. The request approves that fingerprint; if
the enabled rules change meanwhile the server refuses, and you
re-review with qctl get table-series <name-or-id> --rule-approval. Pass
--fingerprint to approve only the fingerprint you reviewed: a different current
fingerprint exits 2 before any write. A series without tables exits 2.
Rules already approved succeed without writing.

This requires review:approve, a member or admin role, and a grant on every
table of the series (admins need none).

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context; --yes does not satisfy
that gate. -o json and -o yaml print the server's approval record; -o name
prints table-series/<uuid>. The preview and prompts stay on stderr.

Examples:
  qctl get table-series "Monthly bordereau" --rule-approval
  qctl table-series approve-rules "Monthly bordereau" --context prod
  qctl table-series approve-rules "Monthly bordereau" --fingerprint <sha256> --context prod --yes`,
		Annotations: cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runApproveRules,
	}
	cmd.Flags().String("fingerprint", "", "Approve only this reviewed rule-set fingerprint")
	cmd.Flags().Bool("yes", false, "Skip approval confirmation")
	return cmd
}

func runApproveRules(cmd *cobra.Command, args []string) error {
	ref := args[0]
	if err := table_series.ValidateReference(ref); err != nil {
		return err
	}
	reviewed, err := reviewedFingerprint(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, ref)
	if err != nil {
		return err
	}
	current, err := c.GetRuleApproval(token, id)
	if err != nil {
		return err
	}
	status := &current.Status
	review := fmt.Sprintf("qctl get table-series %s --rule-approval", id)
	if err := checkApprovable(ref, reviewed, review, status); err != nil {
		return err
	}
	name := "table-series/" + id.String()
	if status.Approved {
		return writeApproval(cmd, current, name, "already approved")
	}
	if _, err := io.WriteString(cmd.ErrOrStderr(), table_series.FormatRuleApprovalPreview(ref, status)); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"approving series rules requires confirmation; review the rule set and pass --yes",
		fmt.Sprintf("Approve rule set %s of table series %q?", status.Fingerprint, terminal.Sanitize(ref))); err != nil {
		return err
	}
	approved, err := c.ApproveRules(token, id, status.Fingerprint)
	if err != nil {
		var apiErr *table_series.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			return fmt.Errorf("%w\n\nReview the current rules with: %s", err, review)
		}
		return err
	}
	return writeApproval(cmd, approved, name, "approved")
}

// reviewedFingerprint returns --fingerprint normalized, or "" when it is unset.
func reviewedFingerprint(cmd *cobra.Command) (string, error) {
	if !cmd.Flags().Changed("fingerprint") {
		return "", nil
	}
	raw, _ := cmd.Flags().GetString("fingerprint")
	return table_series.NormalizeFingerprint(raw)
}

// checkApprovable refuses, with exit 2, an empty rule set or a current
// fingerprint other than reviewed when reviewed is set.
func checkApprovable(ref, reviewed, review string, status *api.SeriesRuleApprovalStatusSchema) error {
	if len(status.RuleSet) == 0 {
		return qerrors.NewBadUsagef("table series %q has no tables; add a table before approving its rules", terminal.Sanitize(ref))
	}
	if reviewed != "" && reviewed != status.Fingerprint {
		return qerrors.NewBadUsagef("the rules of table series %q changed: the current fingerprint is %s, not %s. Review them with: %s",
			terminal.Sanitize(ref), terminal.Sanitize(status.Fingerprint), reviewed, review)
	}
	return nil
}

func writeApproval(cmd *cobra.Command, approval *table_series.RuleApproval, name, state string) error {
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		fmt.Fprintf(cmd.ErrOrStderr(), "%s rules %s\n", name, state)
		return table_series.WriteRuleApproval(cmd.OutOrStdout(), format, approval)
	case output.FormatName:
		fmt.Fprintf(cmd.ErrOrStderr(), "%s rules %s\n", name, state)
		_, err := fmt.Fprintln(cmd.OutOrStdout(), name)
		return err
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s rules %s (fingerprint %s)\n", name, state,
		terminal.Sanitize(approval.Status.Fingerprint))
	return err
}
