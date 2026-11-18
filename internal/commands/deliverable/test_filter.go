package deliverable

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newTestFilterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test-filter [<ref>] --sheet <sheet>",
		Short: "Report or count the rows a sheet's row filter keeps",
		Long: `Show the rows a deliverable sheet's row filter keeps. Nothing is saved.

Give exactly one of:
  <ref>       a deliverable (slug, name or UUID): reads the row filter report
              of its sheet binding named --sheet on one revision
  -f <file>   a kind: Deliverable manifest holding one document: counts its
              spec.sheets.<sheet>.row_filter on a table without saving it

With <ref>, the revision is the enabled one, else the only one; --release
(exact label or full UUID) or --revision (full UUID) selects another, in any
state. --sheet is an exact bound sheet name. The report prints the saved row
filter, the rows it keeps, leaves out and cannot decide (kept / left out /
blocked), and the left-out rows the server samples. A sheet that follows a
table series needs --period <period key>, which names the series member the
report reads; --period on a fixed-table sheet exits 2. --table and --table-id
exit 2: the report reads the binding's table.

With -f, --sheet is a key of spec.sheets, and its row_filter must be set and
not null. The filter, without its description, is counted on --table or
--table-id when either is given, such as a series' period table, else on
spec.sheets.<sheet>.table, under the "Row filter spec.sheets.<sheet>.row_filter
on table N:" heading apply and diff print. A filter the table rejects exits 2.
--release, --revision and --period exit 2 with -f.

-o json|yaml prints the report response as the server sent it, or with -f the
server's {matched, total, capped, predicate_errors, first_predicate_error};
prose goes to stderr.

Examples:
  qctl deliverable test-filter monthly-bordereau --sheet Premium
  qctl deliverable test-filter monthly-bordereau --sheet Claims --period 2026-09
  qctl deliverable test-filter monthly-bordereau --release 2026.2 --sheet Premium -o json
  qctl deliverable test-filter -f deliverable.yaml --sheet Premium
  qctl deliverable test-filter -f deliverable.yaml --sheet Claims --table-id 42`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("deliverable-revision"), "table", "json", "yaml"), "deliverable"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runTestFilter,
	}
	cmd.Flags().String("sheet", "", "Exact sheet name: a sheet binding of the revision, or with -f a key of spec.sheets (required)")
	cmd.Flags().String("release", "", "Revision release label or full UUID (default: the enabled revision, else the only one)")
	cmdutil.RefString(cmd.Flags(), "revision", "deliverable-revision", "Revision full UUID (default: the enabled revision, else the only one)")
	cmd.Flags().String("period", "", "Period key of a series sheet: the series member the report reads")
	cmd.Flags().StringP("filename", "f", "", "Deliverable manifest whose spec.sheets.<sheet>.row_filter is counted, unsaved")
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

func runTestFilter(cmd *cobra.Command, args []string) error {
	if err := validateTestFilterInput(cmd, args); err != nil {
		return err
	}
	if len(args) == 1 {
		return runSheetReport(cmd, args[0])
	}
	sheetName, _ := cmd.Flags().GetString("sheet")
	sheet, err := readDraftSheet(cmd, sheetName)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	count, err := countDraftSheetFilter(ctx, cmd, sheetName, sheet)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if err := deliverables.WriteRowFilterCount(w.Human, count); err != nil {
		return err
	}
	return w.Write(count.Count, "")
}

// validateTestFilterInput is bad usage for anything but exactly one of a
// deliverable reference and -f, and for a flag the chosen input does not
// take; it reads no file and sends no request.
func validateTestFilterInput(cmd *cobra.Command, args []string) error {
	draft := cmd.Flags().Changed("filename")
	switch {
	case len(args) == 0 && !draft:
		return qerrors.NewBadUsage("nothing to test: name a deliverable to read a saved sheet's row filter report, " +
			"or pass -f <deliverable.yaml> to count a sheet's unsaved row filter")
	case len(args) == 1 && draft:
		return qerrors.NewBadUsage("give a deliverable or -f, not both")
	}
	if draft {
		if err := validateDraftInput(cmd); err != nil {
			return err
		}
	} else if err := validateReportInput(cmd, args[0]); err != nil {
		return err
	}
	return validateDraftSheetFlags(cmd)
}

func validateReportInput(cmd *cobra.Command, ref string) error {
	if err := deliverables.ValidateReference(ref); err != nil {
		return err
	}
	if given := changedFlags(cmd, "table", "table-id"); len(given) > 0 {
		return qerrors.NewBadUsagef("%s cannot be used with a deliverable: the report reads the sheet binding's own table; "+
			"to count a filter on another table, use -f", strings.Join(given, " and "))
	}
	period, _ := cmd.Flags().GetString("period")
	if cmd.Flags().Changed("period") && strings.TrimSpace(period) == "" {
		return qerrors.NewBadUsage("--period cannot be empty or blank")
	}
	return nil
}

func validateDraftInput(cmd *cobra.Command) error {
	path, _ := cmd.Flags().GetString("filename")
	if path == "" {
		return qerrors.NewBadUsage("-f cannot be empty: name a Deliverable manifest")
	}
	if given := changedFlags(cmd, "release", "revision"); len(given) > 0 {
		return qerrors.NewBadUsagef("%s cannot be used with -f: -f counts the manifest's row filter, not a saved revision's",
			strings.Join(given, " and "))
	}
	if cmd.Flags().Changed("period") {
		return qerrors.NewBadUsage("--period cannot be used with -f: name the period's table with --table or --table-id")
	}
	return cmdutil.ValidateOptionalTableSelector(cmd, "")
}

// changedFlags returns "--name" for each of names set on the command line.
func changedFlags(cmd *cobra.Command, names ...string) []string {
	var given []string
	for _, name := range names {
		if cmd.Flags().Changed(name) {
			given = append(given, "--"+name)
		}
	}
	return given
}

// runSheetReport reads and prints the row filter report of ref's sheet
// binding named --sheet.
func runSheetReport(cmd *cobra.Command, ref string) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	detail, sheet, err := reportSheet(cmd, c, token, ref)
	if err != nil {
		return err
	}
	period, _ := cmd.Flags().GetString("period")
	switch {
	case sheet.MemberSeriesID != nil && period == "":
		return qerrors.NewBadUsagef("sheet %q follows table series %s: give --period <period key> to choose the period's table; "+
			"'qctl describe table-series %s' lists the period keys", terminal.Sanitize(sheet.SheetName), *sheet.MemberSeriesID, *sheet.MemberSeriesID)
	case sheet.MemberSeriesID == nil && period != "":
		return qerrors.NewBadUsagef("sheet %q reads table %d, not a table series; --period applies only to a series sheet",
			terminal.Sanitize(sheet.SheetName), sheet.MemberDatasetID)
	}
	result, err := c.SheetRowFilterReport(token, detail.ID, sheet.ID, period)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	scope := "table " + strconv.Itoa(sheet.MemberDatasetID)
	if sheet.MemberSeriesID != nil {
		scope = fmt.Sprintf("period %q of series %s", terminal.Sanitize(period), *sheet.MemberSeriesID)
	}
	if _, err := fmt.Fprintf(w.Human, "deliverable/%s release %q sheet %q on %s\n", detail.DeliverableID,
		terminal.Sanitize(detail.Release), terminal.Sanitize(sheet.SheetName), scope); err != nil {
		return err
	}
	if err := row_filters.WriteReport(w.Human, &result.Report); err != nil {
		return err
	}
	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return output.WriteRaw(cmd.OutOrStdout(), format, result.Body)
	}
	return nil
}

// reportSheet resolves ref and the revision --release or --revision selects
// (default: the enabled revision, else the only one), and returns that
// revision's tree and its sheet binding named --sheet. A revision of another
// deliverable, or a sheet the revision does not bind, is not found.
func reportSheet(cmd *cobra.Command, c *deliverables.Client, token, ref string) (*deliverables.RevisionDetail, api.SheetBindingResponse, error) {
	id, err := c.ResolveID(token, ref, true)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	release, _ := cmd.Flags().GetString("release")
	revision, _ := cmd.Flags().GetString("revision")
	revisionID, err := c.ResolveRevision(token, id, release, revision)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	detail, err := c.GetRevision(token, revisionID)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	if detail.DeliverableID != id {
		return nil, api.SheetBindingResponse{}, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf("revision %s does not belong to deliverable %s", revisionID, id)))
	}
	sheetName, _ := cmd.Flags().GetString("sheet")
	sheet, err := boundSheet(detail, sheetName)
	if err != nil {
		return nil, api.SheetBindingResponse{}, err
	}
	return detail, sheet, nil
}

// readDraftSheet returns spec.sheets.<name> of the one Deliverable document
// -f names. A sheet the document lacks, or whose row_filter is missing or
// null, is bad usage.
func readDraftSheet(cmd *cobra.Command, name string) (deliverables.ManifestSheet, error) {
	path, _ := cmd.Flags().GetString("filename")
	data, err := os.ReadFile(path)
	if err != nil {
		return deliverables.ManifestSheet{}, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return deliverables.ManifestSheet{}, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", path))
	}
	switch {
	case len(docs) == 0:
		return deliverables.ManifestSheet{}, qerrors.NewBadUsagef("%s is empty: give one kind: Deliverable document", path)
	case len(docs) > 1:
		return deliverables.ManifestSheet{}, qerrors.NewBadUsagef("test-filter reads one Deliverable document; %s has %d", path, len(docs))
	}
	m, err := deliverables.ParseManifest(docs[0])
	if err != nil {
		return deliverables.ManifestSheet{}, err
	}
	sheet, ok := m.Spec.Sheets[name]
	if !ok {
		names := make([]string, 0, len(m.Spec.Sheets))
		for _, sheetName := range slices.Sorted(maps.Keys(m.Spec.Sheets)) {
			names = append(names, strconv.Quote(terminal.Sanitize(sheetName)))
		}
		has := "none"
		if len(names) > 0 {
			has = strings.Join(names, ", ")
		}
		return deliverables.ManifestSheet{}, qerrors.NewBadUsagef("spec.sheets in %s has no sheet %q; its sheets: %s",
			path, terminal.Sanitize(name), has)
	}
	key := deliverables.SheetFilterKey(name)
	switch {
	case !sheet.Has("row_filter"):
		return deliverables.ManifestSheet{}, qerrors.NewBadUsagef("%s is missing from %s: give the row filter to test", key, path)
	case sheet.RowFilter == nil:
		return deliverables.ManifestSheet{}, qerrors.NewBadUsagef("%s is null in %s: give the row filter to test", key, path)
	}
	return sheet, nil
}

// countDraftSheetFilter counts sheet's row filter on the table --table or
// --table-id names, else on sheet's table. A 422 names the filter's key.
func countDraftSheetFilter(ctx *cmdutil.CommandContext, cmd *cobra.Command, name string, sheet deliverables.ManifestSheet) (deliverables.RowFilterCount, error) {
	token := ctx.Credential.AccessToken
	tableID, flagged, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return deliverables.RowFilterCount{}, err
	}
	if !flagged {
		tables := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if tableID, err = tables.ResolveID(token, sheet.Table); err != nil {
			return deliverables.RowFilterCount{}, fmt.Errorf("%s: %w", deliverables.SheetTableKey(name), err)
		}
	}
	counter, err := row_filters.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return deliverables.RowFilterCount{}, err
	}
	key := deliverables.SheetFilterKey(name)
	count, err := counter.CountKept(token, tableID, sheet.RowFilter)
	if err != nil {
		var apiErr *apierror.OperationalError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnprocessableEntity {
			return deliverables.RowFilterCount{}, fmt.Errorf("%s does not fit table %d: %w", key, tableID, err)
		}
		return deliverables.RowFilterCount{}, err
	}
	return deliverables.RowFilterCount{Key: key, Table: tableID, Series: !flagged && sheet.Series != nil, Count: count}, nil
}
