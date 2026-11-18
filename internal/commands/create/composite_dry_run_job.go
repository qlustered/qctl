package create

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/composite_dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/linked_tables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewCompositeDryRunJobCommand creates the 'create composite-dry-run-job' command.
func NewCompositeDryRunJobCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "composite-dry-run-job [table]",
		Short: "Launch a composite (linked-table) dry run",
		Long: `Launch a composite dry run: a preview of what a linked table's spec revision
would produce, sampled from its spine table, writing nothing.

The table is the linked table, as a positional id-or-name, --table <name> or
--table-id <id>. The dry run previews the table's latest spec revision -- the
newest one, whatever its state -- unless --revision names another revision of
the same linked table.

A launch stops your own dry runs of the same revision that are still queued
or running. When you already have 3 dry runs executing in this organization
the backend answers 429; the command waits and retries, up to 5 attempts,
printing each wait to stderr.

Sampling:
  --max-spine-rows  how many spine rows to preview (the server defaults to
                    1000); pinned rows count against it
  --seed            the same seed samples the same rows; without one the
                    sample is the lowest row ids
  --pin-spine-row   the id of a clean spine-table row to always include, as
                    'qctl get data <spine>' shows it (repeatable); an id that
                    is not a clean row of the spine table is dropped

Output: composite-dry-run-job/<id> created, the revision it previews, and a
Next: line. -o json and -o yaml print the server's answer; -o name prints
composite-dry-run-job/<id> alone.

Waiting:
  --wait            poll the dry run until it finishes, printing a line that
                    names the wait and then each change of state to stderr,
                    then print how it ended: its state, the revision's
                    state, the sample and activation-blocker counts, and a
                    Next: line. It exits 1 with the reason unless the dry
                    run finished -- and, for one 'qctl apply' launched to
                    enable its revision, the enable succeeded.
  --wait-timeout    seconds to wait (default 900); running out exits 124,
                    and the dry run carries on, as it does when Ctrl-C
                    stops the wait (exit 130)
  Under -o json, yaml or name, stdout is the launch answer as without --wait,
  and how the dry run ended goes to stderr.

Examples:
  qctl create composite-dry-run-job orders_enriched
  qctl create composite-dry-run-job --table orders_enriched --seed 7 --max-spine-rows 200
  qctl create composite-dry-run-job --table-id 12 --revision 550e8400-e29b-41d4-a716-446655440000
  qctl create composite-dry-run-job orders_enriched --pin-spine-row 1024 --pin-spine-row 2048
  qctl create composite-dry-run-job orders_enriched --wait`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("composite-dry-run-job"), "table", "json", "yaml", "name"), "linked-table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runCreateCompositeDryRunJob,
	}

	cmdutil.AddTableFlags(cmd, "Linked table")
	cmd.Flags().String("revision", "", "Spec revision UUID to dry-run (default: the table's latest revision)")
	cmd.Flags().Int("max-spine-rows", 0, "Spine rows to sample, pins included (default: the server's, 1000)")
	cmd.Flags().Int("seed", 0, "Sampling seed; the same seed samples the same rows (default: the lowest row ids)")
	cmd.Flags().IntSlice("pin-spine-row", nil, "Clean spine-table row id to always include (repeatable)")
	cmdutil.AddWaitFlags(cmd, "the dry run to finish")

	return cmd
}

func runCreateCompositeDryRunJob(cmd *cobra.Command, args []string) error {
	positional := ""
	if len(args) > 0 {
		positional = args[0]
	}
	params, revisionID, err := compositeDryRunLaunchFlags(cmd, positional)
	if err != nil {
		return err
	}
	waitTimeout, wait, err := cmdutil.WaitFlags(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}

	ltClient, err := linked_tables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	revision, err := ltClient.ResolveRevision(ctx.Credential.AccessToken, tableID, revisionID)
	if err != nil {
		return err
	}
	params.RevisionID = revision.ID

	client, err := composite_dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	// The launch spends the exploratory dry-run cap and answers 429 when it is
	// full (check_dry_run_rate_limit in create_and_run_composite_dry_run,
	// atlas capi/capi/controllers/composite_dry_run_controller.py). The check
	// runs after the launch has stopped the caller's in-flight dry runs of
	// this revision, and the 429 rolls that back with everything else
	// (get_session_for_fastapi, cettings/cettings/db.py), so re-sending the
	// identical request is safe. Progress goes to stderr: under -o json|yaml
	// stdout is the server's answer.
	type launchOutcome struct {
		result *composite_dry_runs.LaunchResult
		body   []byte
	}
	outcome, err := cmdutil.RetryOnThrottle(cmd.ErrOrStderr(), "dry runs", func() (launchOutcome, error) {
		result, body, launchErr := client.Launch(ctx.Credential.AccessToken, params)
		return launchOutcome{result: result, body: body}, launchErr
	})
	if err != nil {
		return err
	}

	if err := writeCompositeDryRunLaunched(cmd, revision, outcome.result, outcome.body, wait); err != nil {
		return err
	}
	if !wait {
		return nil
	}
	return waitForCompositeDryRun(cmd, client, ctx.Credential.AccessToken,
		outcome.result.CompositeDryRunJobID, revision.Name(), waitTimeout)
}

// waitForCompositeDryRun is --wait: it polls the dry run until it settles,
// printing progress to stderr, and prints how it ended -- to stderr under
// -o json|yaml|name, whose stdout is the launch answer, and otherwise to
// stdout. A dry run that fails composite_dry_runs.Verdict is the returned
// error.
func waitForCompositeDryRun(cmd *cobra.Command, client *composite_dry_runs.Client, token string,
	jobID int, revisionName string, timeout time.Duration,
) error {
	detail, err := awaitCompositeDryRun(cmd, client, token, jobID, timeout)
	if err != nil {
		return err
	}
	if err := composite_dry_runs.Verdict(detail); err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json", "yaml", "name":
		out = cmd.ErrOrStderr()
	}
	composite_dry_runs.WriteOutcome(out, detail, revisionName)
	return nil
}

// awaitCompositeDryRun is client.Wait for dry run jobID, writing progress to
// stderr, inside a cmdutil.StartWait whose banner names the describe command,
// and returns what Waiter.Ended makes of the wait's error.
func awaitCompositeDryRun(cmd *cobra.Command, client *composite_dry_runs.Client, token string,
	jobID int, timeout time.Duration,
) (*composite_dry_runs.Detail, error) {
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for composite dry run %d to settle", jobID),
		Next:      fmt.Sprintf("qctl describe composite-dry-run-job %d", jobID),
		NextVerb:  "check with",
	})
	defer w.Stop()
	detail, err := client.Wait(ctx, token, jobID, composite_dry_runs.WaitOptions{
		Timeout:  timeout,
		Progress: func(d *composite_dry_runs.Detail) { composite_dry_runs.WriteProgress(cmd.ErrOrStderr(), d) },
	})
	return detail, w.Ended(err)
}

// compositeDryRunLaunchFlags validates every flag that can be checked without
// a request, and returns the launch body without its revision and the
// --revision UUID, nil when the flag is unset.
func compositeDryRunLaunchFlags(cmd *cobra.Command, positional string) (composite_dry_runs.LaunchParams, *uuid.UUID, error) {
	var params composite_dry_runs.LaunchParams
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return params, nil, err
	}

	var revisionID *uuid.UUID
	if cmd.Flags().Changed("revision") {
		raw, _ := cmd.Flags().GetString("revision")
		id, err := cmdutil.ParseUUIDArg(raw, "--revision")
		if err != nil {
			return params, nil, err
		}
		revisionID = &id
	}

	maxSpineRows, _, err := cmdutil.PositiveIDFlag(cmd, "max-spine-rows")
	if err != nil {
		return params, nil, err
	}
	params.MaxSpineRows = maxSpineRows

	if cmd.Flags().Changed("seed") {
		seed, _ := cmd.Flags().GetInt("seed")
		params.Seed = &seed
	}

	pins, err := cmd.Flags().GetIntSlice("pin-spine-row")
	if err != nil {
		return params, nil, fmt.Errorf("failed to read --pin-spine-row: %w", err)
	}
	for _, pin := range pins {
		if pin <= 0 {
			return params, nil, qerrors.NewBadUsagef("--pin-spine-row must be a positive row id, got %d", pin)
		}
	}
	params.PinnedSpineRowIDs = pins

	return params, revisionID, nil
}

// writeCompositeDryRunLaunched prints the launch: the server's answer for
// -o json|yaml, composite-dry-run-job/<id> for -o name, and otherwise a
// created line, the revision the dry run previews and, unless waiting, a Next:
// line.
func writeCompositeDryRunLaunched(cmd *cobra.Command, revision *linked_tables.Revision, result *composite_dry_runs.LaunchResult, body []byte, waiting bool) error {
	out := cmd.OutOrStdout()
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json":
		return output.WriteIndentedJSON(out, body)
	case "yaml":
		return output.WriteJSONAsYAML(out, body)
	case "name":
		_, err := fmt.Fprintf(out, "composite-dry-run-job/%d\n", result.CompositeDryRunJobID)
		return err
	}

	fmt.Fprintf(out, "composite-dry-run-job/%d created\n", result.CompositeDryRunJobID)
	_, err := fmt.Fprintf(out, "  revision : %s (%s)\n", terminal.Sanitize(revision.Name()), terminal.Sanitize(revision.State))
	if err != nil || waiting {
		return err
	}
	_, err = fmt.Fprintf(out, "Next: qctl describe composite-dry-run-job %d\n", result.CompositeDryRunJobID)
	return err
}
