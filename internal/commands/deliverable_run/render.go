package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newRenderCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "render <id>",
		Short: "Make a run's draft file, or re-make it",
		Long: `Ask the server to render one run, by its integer id or deliverable-run/<id>:
build its draft file from the run's rows. The web app calls this "Make the
draft file", and "Re-make draft" once the run has one.

A render can be requested while the run is open, rendered or stale; any other
state answers 409 (exit 2). The request only queues the render, and the answer
is the run as it was before a worker picked it up, so without --wait the
command returns at once. A worker then renders the run, or records why it
refused in render_error: blocked rows no waiver covers, no rows without
allow_empty, a setup that does not hold for the run, among others. A failed
render changes no state: a first render leaves the run open, and a failed
re-render leaves the previous draft file in place.

Output: deliverable-run/<id> render requested, the run's state, and a Next:
line. -o json and -o yaml print the server's answer; -o name prints
deliverable-run/<id> alone.

Waiting:
  --wait            poll the run until a render attempt after the request
                    finishes or fails, or the run is signed off, delivered
                    or cancelled, printing each change to stderr, then print
                    the render attempt and time. It exits 1 with the reason
                    unless the run ends rendered with no render_error.
  --wait-timeout    seconds to wait (default 900); running out exits 124 and
                    says whether a worker ever picked the render up
  Under -o json, yaml or name, stdout is the request's answer as without
  --wait, and how the render ended goes to stderr.

Examples:
  qctl deliverable-run render 42
  qctl deliverable-run render deliverable-run/42 --wait`,
		Annotations: cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE:        runRender,
	}
	cmdutil.AddWaitFlags(cmd, "the render to finish")
	return cmd
}

func runRender(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	timeout, wait, err := cmdutil.WaitFlags(cmd)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	return Render(cmd, c, ctx.Credential.AccessToken, runID, RenderOptions{Wait: wait, Timeout: timeout, Print: true})
}

// RenderOptions configures Render.
type RenderOptions struct {
	// Wait waits for the render to settle, for at most Timeout.
	Wait    bool
	Timeout time.Duration
	// Print prints the request's answer under -o json|yaml|name as well as
	// under the table format.
	Print bool
}

// Render requests a render of run runID and prints it: under -o json|yaml the
// server's answer and under -o name deliverable-run/<id>, when opts.Print is
// set, and otherwise a requested line on stdout. With opts.Wait it then waits
// for the render to settle, printing progress to stderr, and prints how it
// ended -- to stderr under -o json|yaml|name and otherwise to stdout. A render
// that fails deliverables.RenderVerdict is the returned error.
func Render(cmd *cobra.Command, c *deliverables.Client, token string, runID int, opts RenderOptions) error {
	result, err := c.RequestRender(token, runID)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		if opts.Print {
			if err := output.WriteRaw(out, format, json.RawMessage(result.Body)); err != nil {
				return err
			}
		}
		out = cmd.ErrOrStderr()
	case output.FormatName:
		if opts.Print {
			if _, err := fmt.Fprintf(out, "deliverable-run/%d\n", result.Run.ID); err != nil {
				return err
			}
		}
		out = cmd.ErrOrStderr()
	default:
		deliverables.WriteRenderRequested(out, result.Run, opts.Wait)
	}
	if !opts.Wait {
		return nil
	}
	run, err := awaitRender(cmd, c, token, result.Run, opts.Timeout)
	if err != nil {
		return err
	}
	if err := deliverables.RenderVerdict(run); err != nil {
		return err
	}
	deliverables.WriteRendered(out, run)
	return nil
}

// awaitRender is c.WaitForRender against before's baseline, writing progress
// to stderr, inside a cmdutil.StartWait whose banner names the describe
// command, and returns what Waiter.Ended makes of the wait's error.
func awaitRender(cmd *cobra.Command, c *deliverables.Client, token string, before *deliverables.Run, timeout time.Duration) (*deliverables.Run, error) {
	ctx, w := cmdutil.StartWait(cmd, cmdutil.WaitSpec{
		Kind:      cmdutil.WaitUntilDone,
		Budget:    timeout,
		SelfTimed: true,
		Doing:     fmt.Sprintf("waiting for the render of deliverable run %d", before.ID),
		Next:      fmt.Sprintf("qctl describe deliverable-run %d", before.ID),
		NextVerb:  "check with",
	})
	defer w.Stop()
	base := deliverables.BaselineOf(before)
	stderr := cmd.ErrOrStderr()
	run, err := c.WaitForRender(ctx, token, before.ID, base, deliverables.RenderWaitOptions{
		Timeout:  timeout,
		Progress: func(run *deliverables.Run) { deliverables.WriteRenderProgress(stderr, base, run) },
	})
	return run, w.Ended(err)
}
