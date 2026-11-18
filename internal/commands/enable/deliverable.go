package enable

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewDeliverableCommand creates the 'enable deliverable' command.
func NewDeliverableCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable <ref>",
		Short: "Enable a revision of a deliverable, or validate it with --dry-run",
		Long: `Enable a revision of a deliverable: the server validates it, disables the
revision enabled before it and moves the deliverable's following runs onto it.
Enabling a disabled release again is how to roll back to it.

The positional reference names the deliverable by slug, name or UUID.
--release (an exact release label or a full revision UUID) or --revision (a
full UUID) chooses the revision. With neither, the deliverable's only draft
revision is chosen, and the command refuses when it has none or several.

The following runs are the deliverable's open, rendered and stale runs in
new_period or reissue_enabled mode that are on another revision. Each moves to
the enabled revision, and a rendered one becomes stale (revision_superseded)
and has to be rendered again. The command lists them as read just before the
enable. When one cannot move, nothing is enabled and the command exits 2
naming each run and why.

A revision with validation errors is not enabled: the command exits 2 listing
each one. --dry-run only validates and persists nothing. It prints each
warning on stderr, and exits 2 listing the errors of an invalid revision; for
a valid one that is not enabled it lists the runs an enable would move.
Warnings are reported only by --dry-run, so run it first. It also validates a
revision that is already enabled, or one of a deliverable in the trash.

A deliverable in the trash is not enabled (exit 2), even when the chosen
release is already enabled; restore it with 'qctl undelete deliverable'.
Otherwise a revision that is already enabled is left as it is: the command
says so and exits 0 without sending the enable.

-o json and -o yaml print the server's revision tree, or with --dry-run its
validation result, which is printed even when the revision is invalid. -o name
prints deliverable-revision/<uuid> when the command succeeds. Under all three
the prose goes to stderr.

Examples:
  qctl enable deliverable monthly-bordereau --dry-run
  qctl enable deliverable monthly-bordereau
  qctl enable deliverable monthly-bordereau --release 2026.1
  qctl enable deliverable monthly-bordereau --release 2026.2 -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml", "name"), "deliverable"),
		Args:        cobra.ExactArgs(1),
		RunE:        runEnableDeliverable,
	}
	cmd.Flags().String("release", "", "Revision release label or full UUID (default: the only draft revision)")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Revision full UUID")
	cmd.Flags().Bool("dry-run", false, "Validate the revision and stop; persists nothing")
	return cmd
}

func runEnableDeliverable(cmd *cobra.Command, args []string) error {
	if err := deliverables.ValidateReference(args[0]); err != nil {
		return err
	}
	release, revision, err := revisionSelector(cmd)
	if err != nil {
		return err
	}
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, args[0], true)
	if err != nil {
		return err
	}
	item, err := c.Get(token, id)
	if err != nil {
		return err
	}
	slug := terminal.Sanitize(item.Slug)
	if item.ArchivedAt != nil && !dryRun {
		return qerrors.NewBadUsagef("deliverable/%s is in the trash, so no release can be enabled; restore it with 'qctl undelete deliverable %s'",
			slug, cmdutil.ShellArg(slug))
	}
	target, all, err := c.ResolveEnableTarget(token, id, release, revision)
	if err != nil {
		return err
	}
	e := newEnableOutcome(cmd, slug, item.ArchivedAt != nil, target, all)
	if dryRun {
		return e.validate(c, token, id)
	}
	return e.enable(c, token, id)
}

// revisionSelector reads --release and --revision, rejecting an empty value
// and the two together.
func revisionSelector(cmd *cobra.Command) (release, revision string, err error) {
	for _, flag := range []string{"release", "revision"} {
		if value, _ := cmd.Flags().GetString(flag); cmd.Flags().Changed(flag) && value == "" {
			return "", "", qerrors.NewBadUsagef("--%s cannot be empty", flag)
		}
	}
	release, _ = cmd.Flags().GetString("release")
	revision, _ = cmd.Flags().GetString("revision")
	return release, revision, deliverables.ValidateRevisionSelector(release, revision)
}

// enableOutcome prints what enable deliverable did or would do: prose to
// human, warnings to errOut, and the -o json, yaml or name document to out.
type enableOutcome struct {
	human    io.Writer
	out      io.Writer
	errOut   io.Writer
	format   string
	slug     string
	archived bool
	target   deliverables.Revision
	release  string
	enabled  bool
	replaces *string
}

// newEnableOutcome describes enabling target, which is one of all, the
// deliverable's revisions. Its replaces is the release of the revision in all
// that is enabled and is not target.
func newEnableOutcome(cmd *cobra.Command, slug string, archived bool, target deliverables.Revision, all []deliverables.Revision) enableOutcome {
	format, _ := cmd.Flags().GetString("output")
	e := enableOutcome{
		human:    cmdutil.NewResultWriter(cmd).Human,
		out:      cmd.OutOrStdout(),
		errOut:   cmd.ErrOrStderr(),
		format:   format,
		slug:     slug,
		archived: archived,
		target:   target,
		release:  terminal.Sanitize(target.Release),
		enabled:  target.State == api.DeliverableRevisionStateEnabled,
	}
	for _, r := range all {
		if r.State == api.DeliverableRevisionStateEnabled && r.ID != target.ID {
			replaced := terminal.Sanitize(r.Release)
			e.replaces = &replaced
		}
	}
	return e
}

// enable enables the target after reading the following runs, or reports an
// enabled target without sending the enable.
func (e enableOutcome) enable(c *deliverables.Client, token string, id uuid.UUID) error {
	if e.enabled {
		fmt.Fprintf(e.human, "deliverable/%s release %q already enabled\n", e.slug, e.release)
		if !e.machineDocument() {
			return e.writeName()
		}
		detail, err := c.GetRevision(token, e.target.ID)
		if err != nil {
			return err
		}
		return e.writeDocument(detail.Body)
	}
	runs, err := c.FollowingRuns(token, id, e.target.ID)
	if err != nil {
		return err
	}
	detail, err := c.Enable(token, e.target.ID)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("deliverable/%s release %q enabled", e.slug, e.release)
	if e.replaces != nil {
		line += fmt.Sprintf(" (replaces %q)", *e.replaces)
	}
	fmt.Fprintln(e.human, line)
	e.writeRuns(runs, fmt.Sprintf("Following runs moved to release %q:", e.release))
	fmt.Fprintf(e.human, "Next: qctl describe deliverable %s\n", cmdutil.ShellArg(e.slug))
	return e.writeDocument(detail.Body)
}

// validate runs --dry-run: one validation, then a read of the following runs
// when the revision is valid and not enabled.
func (e enableOutcome) validate(c *deliverables.Client, token string, id uuid.UUID) error {
	result, err := c.Validate(token, e.target.ID)
	if err != nil {
		return err
	}
	if result.Warnings != nil {
		for _, issue := range *result.Warnings {
			fmt.Fprintf(e.errOut, "Warning: %s\n", terminal.Sanitize(deliverables.FormatIssue(issue)))
		}
	}
	if e.machineDocument() {
		if err := output.WriteRaw(e.out, e.format, result.Body); err != nil {
			return err
		}
	}
	if !result.Valid {
		return validationError(e.slug, e.release, result)
	}
	if e.enabled {
		fmt.Fprintf(e.human, "deliverable/%s release %q is valid and already enabled\n", e.slug, e.release)
		return e.writeName()
	}
	runs, err := c.FollowingRuns(token, id, e.target.ID)
	if err != nil {
		return err
	}
	if e.replaces != nil {
		fmt.Fprintf(e.human, "deliverable/%s release %q is valid; enabling it would replace %q\n", e.slug, e.release, *e.replaces)
	} else {
		fmt.Fprintf(e.human, "deliverable/%s release %q is valid; no release is enabled for it to replace\n", e.slug, e.release)
	}
	e.writeRuns(runs, fmt.Sprintf("Enabling it would move these following runs to release %q:", e.release))
	if e.archived {
		fmt.Fprintf(e.human, "deliverable/%s is in the trash; restore it before enabling a release\n", e.slug)
		fmt.Fprintf(e.human, "Next: qctl undelete deliverable %s\n", cmdutil.ShellArg(e.slug))
	} else {
		fmt.Fprintf(e.human, "Next: qctl enable deliverable %s --release %s\n", cmdutil.ShellArg(e.slug), cmdutil.ShellArg(e.release))
	}
	return e.writeName()
}

// validationError is a bad-usage error listing each of result's errors.
func validationError(slug, release string, result *deliverables.ValidateResult) error {
	if result.Errors == nil || len(*result.Errors) == 0 {
		return qerrors.NewBadUsagef("deliverable/%s release %q failed validation (the server listed no errors)", slug, release)
	}
	lines := make([]string, len(*result.Errors))
	for i, issue := range *result.Errors {
		lines[i] = "  - " + deliverables.FormatIssue(issue)
	}
	return qerrors.NewBadUsagef("deliverable/%s release %q failed validation:\n%s", slug, release, strings.Join(lines, "\n"))
}

// writeRuns prints heading and one line per run, or nothing when runs is empty.
func (e enableOutcome) writeRuns(runs []deliverables.Run, heading string) {
	if len(runs) == 0 {
		return
	}
	fmt.Fprintln(e.human, heading)
	for _, run := range runs {
		state := terminal.Sanitize(string(run.State))
		if run.State == api.DeliverableRunStateRendered {
			state += " → stale (revision_superseded; re-render required)"
		}
		fmt.Fprintf(e.human, "  run %d (%s): %s\n", run.ID, terminal.Sanitize(string(run.RevisionMode)), state)
	}
}

// machineDocument reports whether the format is -o json or -o yaml.
func (e enableOutcome) machineDocument() bool {
	return e.format == string(output.FormatJSON) || e.format == string(output.FormatYAML)
}

// writeDocument prints body under -o json or yaml and the revision's name
// under -o name.
func (e enableOutcome) writeDocument(body json.RawMessage) error {
	if e.machineDocument() {
		return output.WriteRaw(e.out, e.format, body)
	}
	return e.writeName()
}

// writeName prints deliverable-revision/<uuid> under -o name.
func (e enableOutcome) writeName() error {
	if e.format != string(output.FormatName) {
		return nil
	}
	_, err := fmt.Fprintf(e.out, "deliverable-revision/%s\n", e.target.ID)
	return err
}
