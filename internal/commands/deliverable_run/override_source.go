package deliverable_run //nolint:revive // Package name mirrors the hyphenated command.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newOverrideSourceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "override-source <id> [<source-id>]",
		Short: "Point one sheet or total of a run at another table, with a reason",
		Long: `Point one of a run's sources at another table ("Change table…" in the web
app). A source is the table the run reads for one sheet, or for one total cell
that reads a table of its own. Name it with --sheet, with --cell Sheet!A1, or
by its id from get deliverable-run -o json; a revision enable recreates the ids,
so prefer --sheet or --cell. Only this run changes: the revision and other runs
keep their tables.

The run must be open, rendered or stale, and the new table must differ from
the one the source reads now. --reason must be non-blank. Any table of the
organization that is not deleted is accepted: a table from another period only
sets the source's warning, which qctl prints, and missing columns show up as
readiness errors. A rendered run goes stale and needs a re-render.

There is no way to clear an override. Pointing the source back at the table
resolution picked is another override.

Examples:
  qctl deliverable-run override-source 42 --sheet Claims --table claims-2026-09-corrected --reason "Corrected file from the carrier"
  qctl deliverable-run override-source 42 --cell 'Summary!B4' --table-id 103 --reason "Totals from the restated table"
  qctl deliverable-run override-source deliverable-run/42 7 --table-id 103 --reason "Corrected file" -o json`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-run"), "table", "json", "yaml", "name"), "deliverable-run", "-"),
		Args:        cobra.RangeArgs(1, 2),
		RunE:        runOverrideSource,
	}
	cmd.Flags().String("sheet", "", "Sheet whose table to change (exact sheet name)")
	cmd.Flags().String("cell", "", "Total cell whose table to change, as Sheet!A1")
	cmdutil.AddTableFlags(cmd, "New table")
	cmd.Flags().String("reason", "", "Why the run reads another table (required, non-blank)")
	return cmd
}

func runOverrideSource(cmd *cobra.Command, args []string) error {
	runID, err := deliverables.ParseRunID(args[0])
	if err != nil {
		return err
	}
	ref, err := sourceRefFromFlags(cmd, args[1:])
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	reason, _ := cmd.Flags().GetString("reason")
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return qerrors.NewBadUsage("--reason is required and must be non-blank")
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	run, err := c.GetRun(token, runID)
	if err != nil {
		return err
	}
	if !deliverables.CanOverrideSources(run.State) {
		return qerrors.NewBadUsagef("a run's tables can only be overridden while it is open, rendered or stale; run %d is %s", runID, run.State)
	}
	revision, err := c.GetRevision(token, run.RevisionID)
	if err != nil {
		return err
	}
	source, err := deliverables.FindRunSource(run, revision, ref)
	if err != nil {
		return err
	}
	if source.DatasetID == tableID {
		return qerrors.NewBadUsagef("%s of run %d already reads table %d; name a different table", source.Name, runID, tableID)
	}
	result, err := c.OverrideSource(token, runID, source.ID, tableID, reason)
	if err != nil {
		return err
	}
	return printOverride(cmd, source, run.State, result)
}

// printOverride prints result in the -o format, then the source's warning and
// stale notices: after the table view on stdout with a Next: line, otherwise
// on stderr. beforeState is the run's state before the override.
func printOverride(cmd *cobra.Command, source *deliverables.RunSource, beforeState api.DeliverableRunState, result *deliverables.OverrideResult) error {
	notices := deliverables.SourceOverrideNotices(source, beforeState, result.Run)
	format, _ := cmd.Flags().GetString("output")
	table := false
	var err error
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML:
		err = output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	case output.FormatName:
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "deliverable-run/%d\n", result.Run.ID)
	default:
		table = true
		_, err = io.WriteString(cmd.OutOrStdout(), deliverables.FormatSourceOverride(source, result.Run))
	}
	if err != nil {
		return err
	}
	// Under -o json|yaml|name stdout stays the server's answer.
	notes := cmd.ErrOrStderr()
	if table {
		notes = cmd.OutOrStdout()
		notices = append(notices, fmt.Sprintf("Next: qctl deliverable-run readiness %d", result.Run.ID))
	}
	_, err = io.WriteString(notes, strings.Join(append(notices, ""), "\n"))
	return err
}

// sourceRefFromFlags reads the source --sheet, --cell or the positional
// source id names, refusing anything but exactly one of them with exit 2.
func sourceRefFromFlags(cmd *cobra.Command, positional []string) (deliverables.SourceRef, error) {
	var given []string
	for _, name := range []string{"sheet", "cell"} {
		if cmd.Flags().Changed(name) {
			given = append(given, "--"+name)
		}
	}
	if len(positional) > 0 {
		given = append(given, "a source id")
	}
	switch len(given) {
	case 0:
		return deliverables.SourceRef{}, qerrors.NewBadUsage("name the source with --sheet, --cell or a source id")
	case 1:
	default:
		return deliverables.SourceRef{}, qerrors.NewBadUsagef("source named more than once (%s): use exactly one of --sheet, --cell or a source id", strings.Join(given, ", "))
	}
	var ref deliverables.SourceRef
	switch given[0] {
	case "--sheet":
		ref.Sheet, _ = cmd.Flags().GetString("sheet")
		if ref.Sheet == "" {
			return ref, qerrors.NewBadUsage("--sheet must name a sheet")
		}
	case "--cell":
		value, _ := cmd.Flags().GetString("cell")
		sheet, cellRef, err := deliverables.ParseSourceCell(value)
		if err != nil {
			return ref, fmt.Errorf("--cell: %w", err)
		}
		ref.CellSheet, ref.CellRef = sheet, cellRef
	default:
		id, err := strconv.Atoi(positional[0])
		if err != nil || id <= 0 {
			return ref, qerrors.NewBadUsagef("source id must be a positive integer, got %q", positional[0])
		}
		ref.ID = id
	}
	return ref, nil
}
