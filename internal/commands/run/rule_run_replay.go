package run

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_run_replays"
	"github.com/spf13/cobra"
)

// defaultReplayMaxRows mirrors the backend's own default for max_rows
// (atlas RuleRunReplayLaunchSchema.max_rows,
// capi/capi/schemas/rule_run_schema.py). qctl sends the value on every launch
// rather than letting the field default server-side, because the cap decides
// WHICH rows are pinned -- the run's first that many evidence rows in row-id
// order -- so a launch that named no cap would stop being reproducible the
// moment the server's default moved.
const defaultReplayMaxRows = 1000

// NewRuleRunReplayCommand creates the 'run rule-run-replay' command.
func NewRuleRunReplayCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-run-replay <rule-run-id>",
		Short: "Re-run one historical rule run over the rows it left evidence for",
		Long: `Re-execute the rules a historical run executed, over the rows that run
recorded verdicts for, and compare the verdicts.

The positional is a RULE RUN id. 'qctl describe row <row-id> --table <table>'
prints one block per run that touched a row, headed "## RUN <id>" -- that id is
what this command takes.

The replay writes nothing to the table: the work rides a dry-run job, whose id
is printed alongside the replay id, and which 'qctl describe dry-run-job' reads
like any other. 'qctl describe rule-run-replay <id>' is the replay's own view
and carries the comparison report once it finishes.

--max-rows caps how many of the run's evidence rows are reproduced. The run's
first that many rows in row-id order are pinned, so the same cap on the same
run pins the same rows; a different cap is a different set. It must be a
positive integer.

The launch is NOT idempotent: a second invocation starts a second replay of the
same run rather than returning the first, so there is no --force and nothing to
resume. What bounds concurrent launches is the backend's per-user replay cap,
which answers 429 with a Retry-After telling you how long to wait; qctl does
not wait for you, so re-run the command yourself once it expires.

Under -o json or yaml, the server's answer is printed: {replay_id,
dry_run_job_id, rows_in_evidence, rows_pinned}. -o name prints
rule-run-replay/<replay-id>. The prose goes to stderr.

Examples:
  # replay run 4711 over up to 1000 of its evidence rows
  qctl run rule-run-replay 4711

  # replay a small slice of it instead
  qctl run rule-run-replay 4711 --max-rows 50

  # then read the comparison
  qctl describe rule-run-replay 7`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-run-replay"), "table", "json", "yaml", "name"), "-"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ids, err := cmdutil.ParseIntArgs(args, "rule run id")
			if err != nil {
				return err
			}
			ruleRunID := ids[0]

			// The bound is enforced here because oapi-codegen dropped it: the
			// spec's `exclusiveMinimum: 0` on max_rows did not survive into
			// api.RuleRunReplayLaunchSchema, whose MaxRows is a plain *int. A
			// zero or negative would otherwise reach the backend and come back
			// as a pydantic 422 -- the same exit code, one round trip later,
			// with a message written for a form field.
			maxRows, err := cmdutil.PositiveCountFlag(cmd, "max-rows")
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client, err := rule_run_replays.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// No RetryOnThrottle here. That helper's contract is written for
			// the exploratory dry-run budget whose launches it retries, and a
			// replay spends a separate one (MAX_CONCURRENT_RULE_RUN_REPLAYS_PER_USER,
			// atlas check_rule_run_replay_limit in
			// cettings/cettings/controllers/dry_run_helpers.py).
			// The 429 still arrives fully formed -- apierror wraps it as a
			// *ThrottleError carrying the server's Retry-After -- and the
			// server's own sentence names the counts, so the user is told what
			// to wait for. Blocking the terminal on a job start that is not
			// idempotent is the part that would be new behaviour.
			resp, err := client.LaunchReplay(ctx.Credential.AccessToken, ruleRunID, maxRows)
			if err != nil {
				return err
			}

			w := cmdutil.NewResultWriter(cmd)
			out := w.Human
			fmt.Fprintf(out, "Replay %d launched for rule run %d.\n", resp.ReplayID, ruleRunID)
			fmt.Fprintf(out, "  dry-run job:      %d\n", resp.DryRunJobID)
			fmt.Fprintf(out, "  rows in evidence: %d\n", resp.RowsInEvidence)
			fmt.Fprintf(out, "  rows pinned:      %d\n", resp.RowsPinned)
			fmt.Fprintf(out, "\nRead the comparison with: qctl describe rule-run-replay %d\n", resp.ReplayID)
			fmt.Fprintf(out, "The carrier job:           qctl describe dry-run-job %d\n", resp.DryRunJobID)
			return w.Write(resp, fmt.Sprintf("rule-run-replay/%d", resp.ReplayID))
		},
	}

	cmd.Flags().Int("max-rows", defaultReplayMaxRows,
		"Cap how many of the run's evidence rows are replayed (positive integer)")

	return cmd
}
