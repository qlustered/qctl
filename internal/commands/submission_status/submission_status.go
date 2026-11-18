// Package submission_status implements the commands that waive a period of a
// table-series cloud source and withdraw that waiver.
package submission_status //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/submissions"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCommand creates the submission-status group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submission-status",
		Short: "Waive a broker intake's submission for a period, or withdraw the waiver",
		Long: `Waive or un-waive one table-series cloud source's (broker intake's) submission
for one reporting period.

See where each intake's submission for a period stands:
  qctl get submission-statuses --cloud-source-id <id>

Waive a period, or withdraw the waiver in effect:
  qctl submission-status waive --cloud-source-id <id> --period <key> --reason <text>
  qctl submission-status unwaive --cloud-source-id <id> --period <key>`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}
	cmd.AddCommand(newWaiveCommand(), newUnwaiveCommand())
	return cmd
}

func newWaiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "waive",
		Short: "Waive a series cloud source's submission for a period",
		Long: `Waive one series cloud source's submission for one period, so the board shows
the period as waived. Requires deliverable:admin.

--period is sent as typed, e.g. 2026-09 or 2026-Q3. --reason is required and
must be non-blank.

The table view prints the period's status after the waiver; -o json|yaml
print the response body, with that line on stderr.

Examples:
  qctl submission-status waive --cloud-source-id 42 --period 2026-09 --reason "Broker closed for the month"
  qctl submission-status waive --cloud-source-id 42 --period 2026-Q3 --reason "No business written" -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("submission-status"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        func(cmd *cobra.Command, _ []string) error { return runWaiver(cmd, true) },
	}
	addWaiverFlags(cmd)
	cmd.Flags().String("reason", "", "Why the submission is not needed for the period (required, non-blank)")
	_ = cmd.MarkFlagRequired("reason")
	return cmd
}

func newUnwaiveCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unwaive",
		Short: "Withdraw the waiver in effect for a series cloud source's period",
		Long: `Withdraw the waiver in effect for one series cloud source's period. Requires
deliverable:admin.

--period is sent as typed, e.g. 2026-09 or 2026-Q3.

The table view prints the period's status after the withdrawal, or a line
saying the cloud source is no longer expected for the period when the server
returns no status; -o json|yaml print the response body, with that line on
stderr, and print nothing on stdout when there is no status.

Examples:
  qctl submission-status unwaive --cloud-source-id 42 --period 2026-09`,
		Annotations: cmdutil.WithFormats(catalog.For("submission-status"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        func(cmd *cobra.Command, _ []string) error { return runWaiver(cmd, false) },
	}
	addWaiverFlags(cmd)
	return cmd
}

func addWaiverFlags(cmd *cobra.Command) {
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Series cloud source ID (required)")
	cmd.Flags().String("period", "", "Period key, e.g. 2026-09 or 2026-Q3 (required)")
	_ = cmd.MarkFlagRequired("cloud-source-id")
	_ = cmd.MarkFlagRequired("period")
}

func runWaiver(cmd *cobra.Command, waive bool) error {
	cloudSourceID, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	period, _ := cmd.Flags().GetString("period")
	if err := table_series.ValidatePeriodKey(period); err != nil {
		return err
	}
	var reason string
	if waive {
		reason, _ = cmd.Flags().GetString("reason")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return qerrors.NewBadUsage("--reason is required and must be non-blank")
		}
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := submissions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	var result *submissions.WaiverResult
	if waive {
		result, err = c.Waive(ctx.Credential.AccessToken, cloudSourceID, period, reason)
	} else {
		result, err = c.WithdrawWaiver(ctx.Credential.AccessToken, cloudSourceID, period)
	}
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if result.Status == nil {
		fmt.Fprintf(w.Human, "cloud-source/%d waiver withdrawn; it is no longer expected for period %q\n",
			cloudSourceID, terminal.Sanitize(period))
		return nil
	}
	fmt.Fprintln(w.Human, submissions.StatusLine(result.Status))
	if w.Machine() {
		format, _ := cmd.Flags().GetString("output")
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return nil
}
