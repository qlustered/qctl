package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newSignOffCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sign-off <id>",
		Short: "Approve a rendered run and lock its report",
		Long: `Sign off one rendered run, by its integer id or deliverable-run/<id>.

Before confirmation, stderr shows the render attempt, parameters, last-render
totals, waived row count and live readiness summary, including under --yes.
The request sends that attempt as expected_attempt_id: if another render
starts meanwhile, re-read the run and review its current render before retrying.
The server checks readiness, period order and whether the live rows still
match the rendered report.

Signing off locks the report. A signed-off new-period run cannot be cancelled
and must be delivered before another new period or re-issue can be created.
This requires deliverable:run:signoff, held by member, admin and ops roles;
editor and agent roles cannot sign off.

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context; --yes does not satisfy
that gate. -o json and -o yaml print the server's signed-off run; -o name prints
deliverable-run/<id> alone. The preview and prompts stay on stderr.

Examples:
  qctl deliverable-run sign-off 42 --context prod
  qctl deliverable-run sign-off deliverable-run/42 --context prod --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runSignOff,
	}
	cmd.Flags().Bool("yes", false, "Skip sign-off confirmation")
	return cmd
}

func runSignOff(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	run, err := c.GetRun(token, runID)
	if err != nil {
		return err
	}
	if run.State != api.DeliverableRunStateRendered {
		return qerrors.NewBadUsagef("only rendered runs can be signed off; run %d is %s. Re-read it with: qctl describe deliverable-run %d",
			runID, terminal.Sanitize(deliverables.RunStateLabel(run.State)), runID)
	}
	readiness, err := c.Readiness(token, runID, deliverables.ReadinessParams{Limit: 1})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(cmd.ErrOrStderr(), deliverables.FormatSignOffPreview(run, &readiness.ReadinessSummary)); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"sign-off requires confirmation; review the run and pass --yes to lock its report",
		fmt.Sprintf("Sign off run %d, attempt %d, and lock its report?", runID, run.AttemptID)); err != nil {
		return err
	}
	result, err := c.SignOff(token, runID, run.AttemptID)
	if err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	case output.FormatName:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d\n", result.Run.ID)
	default:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d signed off; its report is locked\n", result.Run.ID)
	}
	return err
}
