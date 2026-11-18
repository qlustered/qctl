package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newMarkDeliveredCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mark-delivered <id>",
		Short: "Record how a signed-off run was delivered",
		Long: `Mark a signed-off run as delivered, by its integer id or deliverable-run/<id>.
--note is required and must be non-blank: describe how the final file was sent
("How was it sent?" in the web app). The server records the delivery time.

This records delivery; it does not send the file. Delivery cannot be undone.
The run joins the delivered periods used by later runs, and its source tables
remain locked against deletion, series removal and period changes.
The server checks the run's state and period order before recording delivery.

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context; --yes does not satisfy
that gate. -o json and -o yaml print the server's updated run; -o name prints
deliverable-run/<id> alone. Prompts stay on stderr.

Examples:
  qctl deliverable-run mark-delivered 42 --note "Emailed to the carrier" --context prod
  qctl deliverable-run mark-delivered deliverable-run/42 --note "Sent through the portal" --context prod --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runMarkDelivered,
	}
	cmd.Flags().String("note", "", "How the final file was sent (required, non-blank)")
	cmd.Flags().Bool("yes", false, "Skip delivery confirmation")
	return cmd
}

func runMarkDelivered(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	note, _ := cmd.Flags().GetString("note")
	note = strings.TrimSpace(note)
	if note == "" {
		return qerrors.NewBadUsage("--note is required and must be non-blank")
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"mark-delivered requires confirmation; pass --yes to record the delivery permanently",
		fmt.Sprintf("Mark run %d as delivered? This cannot be undone.", runID)); err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := c.MarkDelivered(ctx.Credential.AccessToken, runID, note)
	if err != nil {
		return err
	}
	return writeRunTransition(cmd, result, "marked delivered")
}

func newCancelCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Abandon a deliverable run permanently",
		Long: `Cancel a run, by its integer id or deliverable-run/<id> ("Cancel run" in the web app).
The server allows open, rendered or stale runs, and signed-off re-issues of a
delivered period that is no longer the latest. It decides eligibility when
the request is sent.

Cancellation cannot be undone. It removes this run's table locks; other runs
and enabled bindings may still lock those tables. The rendered file is kept,
and the cancelled run still counts toward its period's issue numbering.

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context; --yes does not satisfy
that gate. -o json and -o yaml print the server's updated run; -o name prints
deliverable-run/<id> alone. Prompts stay on stderr.

Examples:
  qctl deliverable-run cancel 42 --context prod
  qctl deliverable-run cancel deliverable-run/42 --context prod --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runCancel,
	}
	cmd.Flags().Bool("yes", false, "Skip cancellation confirmation")
	return cmd
}

func runCancel(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"cancel requires confirmation; pass --yes to abandon the run permanently",
		fmt.Sprintf("Cancel run %d? This cannot be undone.", runID)); err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := c.CancelRun(ctx.Credential.AccessToken, runID)
	if err != nil {
		return err
	}
	return writeRunTransition(cmd, result, "cancelled")
}

func writeRunTransition(cmd *cobra.Command, result *deliverables.RunTransitionResult, action string) error {
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	case output.FormatName:
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d\n", result.Run.ID)
		return err
	default:
		_, err := fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d %s\n", result.Run.ID, action)
		return err
	}
}
