package create

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/deliverable_run"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableRunCommand creates the 'create deliverable-run' command.
func NewDeliverableRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-run <deliverable-ref>",
		Short: "Create a run of a deliverable: a new period or a re-issue",
		Long: `Create a run of the deliverable named by slug, name or UUID.

Supply run parameters as a YAML scalar mapping with --params-file ("-" reads
stdin), or use --period for the period parameter alone. Each value keeps the
type the YAML wrote: 2026 and 2026.0 are different parameters.

The server decides what the run is. Parameters equal, as text, to a delivered
run's re-issue that period, on the delivered run's release, or on the enabled
release with --use-enabled-revision. Any other parameters start a new period on
the enabled release, and every parameter that release declares is required; a
missing one, or no enabled release, exits 2 before anything is created.
--allow-empty lets the run render with no rows.

Output: deliverable-run/<id> created, its issue number, revision mode, release
and parameters, and a Next: line. -o json and -o yaml print the server's
answer; -o name prints deliverable-run/<id> alone. Notes go to stderr.

--render then requests the run's render, as 'qctl deliverable-run render'
does, printing deliverable-run/<id> render requested under the table format;
--wait and --wait-timeout wait for it as that command's do. Under -o json,
yaml or name, stdout is the create answer alone.

Examples:
  qctl create deliverable-run monthly-bordereau --period 2026-09
  qctl create deliverable-run monthly-bordereau --params-file params.yaml --allow-empty
  qctl create deliverable-run monthly-bordereau --period 2026-08 --use-enabled-revision -o json
  qctl create deliverable-run monthly-bordereau --period 2026-09 --render --wait`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        runCreateDeliverableRun,
	}
	paramflags.RegisterRunParamFlags(cmd)
	cmd.Flags().Bool("allow-empty", false, "Let the run render with no rows")
	cmd.Flags().Bool("use-enabled-revision", false, "Re-issue on the enabled release instead of the delivered run's")
	cmd.Flags().Bool("render", false, "Request the run's render once it is created")
	cmdutil.AddWaitFlags(cmd, "the render to finish (requires --render)")
	return cmd
}

func runCreateDeliverableRun(cmd *cobra.Command, args []string) error {
	ref := args[0]
	if err := deliverables.ValidateReference(ref); err != nil {
		return err
	}
	input, err := paramflags.RunParamInput(cmd)
	if err != nil {
		return err
	}
	allowEmpty, _ := cmd.Flags().GetBool("allow-empty")
	useEnabled, _ := cmd.Flags().GetBool("use-enabled-revision")
	render, wait, waitTimeout, err := renderFlags(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	d, err := runnableDeliverable(c, token, ref)
	if err != nil {
		return err
	}
	params, err := input.ForDeliverable(d)
	if err != nil {
		return err
	}
	typed, err := deliverables.TypedRunParams(params)
	if err != nil {
		return err
	}
	runs, err := c.AllRuns(token, d.ID)
	if err != nil {
		return err
	}
	enabled, notes, err := checkRunParams(c, token, d, params, runs, typed, useEnabled)
	if err != nil {
		return err
	}
	for _, note := range notes {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: %s\n", note)
	}
	result, err := c.CreateRun(token, d.ID, deliverables.CreateRunRequest{
		Ref: terminal.Sanitize(d.Slug), Params: typed, AllowEmpty: allowEmpty, UseEnabledRevision: useEnabled,
	})
	if err != nil {
		return err
	}
	if err := writeDeliverableRunCreated(cmd, c, token, result, enabled); err != nil || !render {
		return err
	}
	return deliverable_run.Render(cmd, c, token, result.Run.ID, deliverable_run.RenderOptions{Wait: wait, Timeout: waitTimeout})
}

// renderFlags reads --render, --wait and --wait-timeout. --wait without
// --render is a bad-usage error.
func renderFlags(cmd *cobra.Command) (render, wait bool, timeout time.Duration, err error) {
	render, _ = cmd.Flags().GetBool("render")
	if timeout, wait, err = cmdutil.WaitFlags(cmd); err != nil {
		return false, false, 0, err
	}
	if wait && !render {
		return false, false, 0, qerrors.NewBadUsage("--wait requires --render")
	}
	return render, wait, timeout, nil
}

// runnableDeliverable resolves ref, including the trash, and reads the
// deliverable; one in the trash is a bad-usage error.
func runnableDeliverable(c *deliverables.Client, token, ref string) (*deliverables.Deliverable, error) {
	id, err := c.ResolveID(token, ref, true)
	if err != nil {
		return nil, err
	}
	d, err := c.Get(token, id)
	if err != nil {
		return nil, err
	}
	if d.ArchivedAt != nil {
		slug := terminal.Sanitize(d.Slug)
		return nil, qerrors.NewBadUsagef("deliverable/%s is in the trash, so no run can be created; restore it with 'qctl undelete deliverable %s'",
			slug, cmdutil.ShellArg(slug))
	}
	return d, nil
}

// checkRunParams checks params against the run they would create: a re-issue
// when a delivered run's are text-equal, otherwise a new period, whose
// required parameters must all be present. It returns the enabled revision
// when a new period or useEnabled reads it, and the notes to print.
func checkRunParams(c *deliverables.Client, token string, d *deliverables.Deliverable, params deliverables.RunParams,
	runs []deliverables.Run, typed map[string]api.RunParameterValue, useEnabled bool,
) (*deliverables.RevisionDetail, []string, error) {
	reissue := slices.ContainsFunc(runs, func(run deliverables.Run) bool {
		return run.State == api.DeliverableRunStateDelivered && sameRunParams(run, typed)
	})
	var enabled *deliverables.RevisionDetail
	if !reissue || useEnabled {
		if d.EnabledRevisionID == nil {
			return nil, nil, noEnabledRevision(terminal.Sanitize(d.Slug), reissue)
		}
		var err error
		if enabled, err = c.GetRevision(token, *d.EnabledRevisionID); err != nil {
			return nil, nil, err
		}
	}
	var notes []string
	declared := func() []string {
		return deliverables.RequiredRunParams(enabled.DeliverableRevisionResponse, d.FilenamePattern, d.PeriodParam)
	}
	switch {
	case !reissue:
		required := declared()
		if err := deliverables.ValidateRequiredRunParams(params, required); err != nil {
			return nil, nil, err
		}
		notes = unusedParamNotes(params, required, enabled.Release)
		if useEnabled {
			notes = append(notes, "--use-enabled-revision has no effect: these parameters match no delivered run, so the run is a new period on the enabled release")
		}
	case useEnabled:
		notes = lackingNameNotes(params, declared(), enabled.Release)
	}
	for _, run := range runs {
		if run.State != api.DeliverableRunStateDelivered && run.State != api.DeliverableRunStateCancelled && sameRunParams(run, typed) {
			notes = append(notes, fmt.Sprintf("run #%d (deliverable-run/%d, %s) already has these parameters",
				run.Sequence, run.ID, terminal.Sanitize(deliverables.RunStateLabel(run.State))))
		}
	}
	return enabled, notes, nil
}

// sameRunParams reports whether run's typed parameters are text-equal to typed.
func sameRunParams(run deliverables.Run, typed map[string]api.RunParameterValue) bool {
	var have map[string]api.RunParameterValue
	if run.RunParamsTyped != nil {
		have = *run.RunParamsTyped
	}
	return deliverables.SameRunParamTexts(have, typed)
}

// noEnabledRevision is the refusal of a run that needs the enabled revision
// of a deliverable that has none.
func noEnabledRevision(slug string, reissue bool) error {
	why := "these parameters match no delivered run, so the run would be a new period"
	if reissue {
		why = "--use-enabled-revision re-issues on it"
	}
	return qerrors.NewBadUsagef("deliverable/%s has no enabled release, and %s; enable one first: qctl enable deliverable %s",
		slug, why, cmdutil.ShellArg(slug))
}

// unusedParamNotes notes each of params not in required, in name order.
func unusedParamNotes(params deliverables.RunParams, required []string, release string) []string {
	var notes []string
	for _, name := range sortedNames(params) {
		if !slices.Contains(required, name) {
			notes = append(notes, fmt.Sprintf("release %s does not use parameter %q; it still counts toward the run's period identity",
				terminal.Sanitize(release), terminal.Sanitize(name)))
		}
	}
	return notes
}

// lackingNameNotes notes the declared names params lack.
func lackingNameNotes(params deliverables.RunParams, declared []string, release string) []string {
	var lacking []string
	for _, name := range declared {
		if _, ok := params[name]; !ok {
			lacking = append(lacking, strconv.Quote(terminal.Sanitize(name)))
		}
	}
	if len(lacking) == 0 {
		return nil
	}
	them := "it"
	if len(lacking) > 1 {
		them = "them"
	}
	return []string{fmt.Sprintf("the enabled release %s also uses %s; adding %s would make the run a new period instead of a re-issue",
		terminal.Sanitize(release), strings.Join(lacking, ", "), them)}
}

func sortedNames(params deliverables.RunParams) []string {
	names := make([]string, 0, len(params))
	for name := range params {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// writeDeliverableRunCreated prints the created run: the server's answer for
// -o json|yaml, deliverable-run/<id> for -o name, and otherwise
// deliverables.FormatCreatedRun with the run's revision, which is enabled
// when that is the run's, and otherwise read; a failed read names the
// revision by id.
func writeDeliverableRunCreated(cmd *cobra.Command, c *deliverables.Client, token string,
	result *deliverables.CreateRunResult, enabled *deliverables.RevisionDetail,
) error {
	out := cmd.OutOrStdout()
	run := result.Run
	format, _ := cmd.Flags().GetString("output")
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		return output.WriteRaw(out, format, json.RawMessage(result.Body))
	case output.FormatName:
		_, err := fmt.Fprintf(out, "deliverable-run/%d\n", run.ID)
		return err
	}
	revision := enabled
	if revision == nil || revision.ID != run.RevisionID {
		revision, _ = c.GetRevision(token, run.RevisionID)
	}
	_, err := fmt.Fprint(out, deliverables.FormatCreatedRun(run, revision))
	return err
}
