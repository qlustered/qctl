package table_series //nolint:revive // Package name mirrors the command.

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func newDeletePeriodCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete-period <name-or-id> --period <key>",
		Short: "Delete one period of a table series (Qluster ops)",
		Long: `Delete one period of a table series, by exact series name or full UUID and
exact period key. Qluster ops only: any other account gets not found.

The period's table leaves the series and is soft-deleted, keeping its files;
its destination tables move to a new table name, printed so the table can be
recovered under it. A series cloud source whose template is the table moves to
the latest other active period's table; with no other active period the
delete is refused. The period shows as missing until a file for it creates it
again. Delivered deliverable runs never block the delete and are listed as
runs to reissue.

A locked period, active jobs on the table, or a linked table, enabled
deliverable revision or undelivered deliverable run reading the table refuses
the delete (exit 2), with one hint line per blocker.

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context. -o json and -o yaml
print the server's answer; -o name prints table/<id> of the deleted table.

Examples:
  qctl table-series delete-period "Monthly bordereau" --period 2026-09 --context prod`,
		Annotations: output.WithNameKind(cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml", "name"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE:        runDeletePeriod,
	}
	cmd.Flags().String("period", "", "Exact reporting period key (required)")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("period")
	return cmd
}

// periodTarget is the series and member a period operation acts on.
type periodTarget struct {
	client *table_series.Client
	token  string
	series *table_series.Series
	member table_series.Member
}

// resolvePeriod validates ref and --period, then reads the series and finds
// the member holding the period.
func resolvePeriod(cmd *cobra.Command, ref string) (*periodTarget, error) {
	if err := table_series.ValidateReference(ref); err != nil {
		return nil, err
	}
	period, _ := cmd.Flags().GetString("period")
	if err := table_series.ValidatePeriodKey(period); err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	id, err := c.ResolveID(token, ref)
	if err != nil {
		return nil, err
	}
	series, err := c.Get(token, id)
	if err != nil {
		return nil, err
	}
	for _, member := range series.Members {
		if member.PeriodKey == period {
			return &periodTarget{client: c, token: token, series: series, member: member}, nil
		}
	}
	return nil, qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
		"period %q not found in table series %q; list its periods with qctl describe table-series %s",
		terminal.Sanitize(period), terminal.Sanitize(series.Name), id)))
}

func runDeletePeriod(cmd *cobra.Command, args []string) error {
	target, err := resolvePeriod(cmd, args[0])
	if err != nil {
		return err
	}
	key, name := target.member.PeriodKey, target.series.Name
	yes, _ := cmd.Flags().GetBool("yes")
	question := fmt.Sprintf("delete period %q of series %q? Table %d leaves the series and is soft-deleted",
		terminal.Sanitize(key), terminal.Sanitize(name), target.member.DatasetID)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"deleting a series period requires confirmation; re-run with --yes", question); err != nil {
		return err
	}
	result, body, err := target.client.DeletePeriod(target.token, target.series.ID, key)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	writeDeletedPeriod(w.Human, key, name, result)
	return writePeriodResult(cmd, w, body, fmt.Sprintf("table/%d", result.DatasetID))
}

// writePeriodResult prints body under -o json|yaml and names under -o name.
func writePeriodResult(cmd *cobra.Command, w cmdutil.ResultWriter, body []byte, names ...string) error {
	switch format, _ := cmd.Flags().GetString("output"); format {
	case "json":
		return output.WriteIndentedJSON(cmd.OutOrStdout(), body)
	case "yaml":
		return output.WriteJSONAsYAML(cmd.OutOrStdout(), body)
	}
	return w.Write(nil, names...)
}

func writeDeletedPeriod(out io.Writer, key, series string, result *table_series.PeriodDeleteResult) {
	fmt.Fprintf(out, "table/%d period %q of series %q deleted\n", result.DatasetID,
		terminal.Sanitize(key), terminal.Sanitize(series))
	fmt.Fprintf(out, "  moved table name : %s\n", terminal.Sanitize(result.TableName))
	for _, moved := range result.MovedSourceTemplates {
		fmt.Fprintf(out, "  template moved   : cloud-source/%d %q now uses table/%d\n",
			moved.DataSourceModelID, terminal.Sanitize(moved.Name), moved.TemplateDatasetID)
	}
	writeRunsToReissue(out, result.RunsToReissue)
}

func writeRunsToReissue(out io.Writer, runs []api.SeriesPeriodRunToReissueSchema) {
	for _, run := range runs {
		fmt.Fprintf(out, "  run to reissue   : deliverable-run/%d of %q\n", run.RunID, terminal.Sanitize(run.DeliverableName))
	}
}

// Flag names of recreate-period.
const (
	copyFromFlag        = "copy-from"
	schemaFlag          = "schema"
	leaveOutColumnFlag  = "leave-out-column"
	keyColumnFlag       = "key-column"
	noKeyColumnsFlag    = "no-key-columns"
	loadingProcessFlag  = "loading-process"
	includeFlag         = "include"
	reloadFileFlag      = "reload-file"
	reloadAllFilesFlag  = "reload-all-files"
	learnFromFileSchema = "learn_from_file"
)

var recreateSchemas = []string{"copy", "copy_without_columns", learnFromFileSchema}

var recreateLoadingProcesses = []string{
	string(api.DataLoadingProcessAppend),
	string(api.DataLoadingProcessAppendSkipIdenticalRows),
	string(api.DataLoadingProcessMirror),
}

func newRecreatePeriodCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recreate-period <name-or-id> --period <key>",
		Short: "Recreate one period of a table series from a copy (Qluster ops)",
		Long: `Recreate one period of a table series from a copy of a period's table, by
exact series name or full UUID and exact period key. Qluster ops only: any
other account gets not found.

The copy is of the period's own table unless --copy-from names another
period. The new table takes the period's place, display name and table name,
its cloud sources and its files. The replaced table's destination tables move
to a new table name, printed so the table can be recovered under it, and it is
soft-deleted. Its alerts, comment threads, reviews, row ignores and duplicate
groups stay with it; the command prints how many of each.

--schema picks the new table's schema:
  copy                  the copied table's schema (default)
  copy-without-columns  the copied schema minus each --leave-out-column
  learn-from-file       no schema, schema overrides, key columns or rules: the
                        first reloaded file is profiled into the table's first
                        migration review, and the other files load once that
                        migration is applied. --key-column and
                        --no-key-columns are refused with it.

Key columns default to the copied table's; --key-column sets them (repeat it
or separate with commas) and --no-key-columns gives the table none.
--loading-process is one of append, append_skip_identical_rows and mirror and
defaults to the copied table's. --include names the copy options to carry
(as copy table does; settings is always carried); without it every option is
carried.

Files are not reloaded unless asked. --reload-file names a file of the
period's table (repeat it or separate with commas), and --reload-all-files
reloads every file 'qctl get table-series <series> --reloadable-files
--period <key>' lists. Reloads run after the recreate is committed, in upload
order; a reload that fails does not undo the recreate, and is printed with
its reason. A reloaded file has an ingestion job queued, not finished.

A locked period, active jobs on the table, or a linked table, enabled
deliverable revision or undelivered deliverable run reading the table refuses
the recreate (exit 2), with one hint line per blocker. Rules of the copied
table that read a column the new schema lacks are skipped and printed.

An interactive terminal gets a y/N prompt; otherwise pass --yes. On a protected
context, --context must also name the intended context. -o json and -o yaml
print the server's answer; -o name prints table/<id> of the new table.

Examples:
  qctl table-series recreate-period "Monthly bordereau" --period 2026-09 --reload-all-files --context prod
  qctl table-series recreate-period "Monthly bordereau" --period 2026-09 --schema learn-from-file --reload-all-files --context prod
  qctl table-series recreate-period "Monthly bordereau" --period 2026-09 --copy-from 2026-08 --schema copy-without-columns --leave-out-column legacy_code --context prod`,
		Annotations: output.WithNameKind(cmdutil.WithFormats(catalog.For("table-series"), "table", "json", "yaml", "name"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE:        runRecreatePeriod,
	}
	cmd.Flags().String("period", "", "Exact reporting period key (required)")
	cmd.Flags().String(copyFromFlag, "", "Period whose table is copied (default: the period being recreated)")
	cmd.Flags().String(schemaFlag, "copy", "Schema of the new table: copy, copy-without-columns or learn-from-file")
	cmd.Flags().StringArray(leaveOutColumnFlag, nil, "Column the new table leaves out (with --schema copy-without-columns; repeatable)")
	cmd.Flags().StringArray(keyColumnFlag, nil, "Key column of the new table (repeatable; default: the copied table's)")
	cmd.Flags().Bool(noKeyColumnsFlag, false, "Give the new table no key columns")
	cmd.Flags().String(loadingProcessFlag, "", "Loading process: append, append_skip_identical_rows or mirror (default: the copied table's)")
	cmd.Flags().StringArray(includeFlag, nil, "Copy option to carry (repeatable; default: every option)")
	cmd.Flags().StringArray(reloadFileFlag, nil, "File ID of the period's table to reload into the new table (repeatable)")
	cmd.Flags().Bool(reloadAllFilesFlag, false, "Reload every reloadable file of the period's table")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompt")
	_ = cmd.MarkFlagRequired("period")
	return cmd
}

// recreateFlags is the request recreate-period's flags build, before the
// reload list of --reload-all-files is read.
type recreateFlags struct {
	body      table_series.PeriodRecreateBody
	reloadAll bool
}

// splitList splits each value at commas and trims the elements; an empty
// element exits 2 naming flag.
func splitList(flag string, values []string) ([]string, error) {
	var out []string
	for _, value := range values {
		for _, element := range strings.Split(value, ",") {
			element = strings.TrimSpace(element)
			if element == "" {
				return nil, qerrors.NewBadUsagef("invalid value %q for --%s: empty element", value, flag)
			}
			if !slices.Contains(out, element) {
				out = append(out, element)
			}
		}
	}
	return out, nil
}

// parseRecreateFlags checks recreate-period's flags and builds its request.
func parseRecreateFlags(cmd *cobra.Command) (*recreateFlags, error) {
	flags := cmd.Flags()
	parsed := &recreateFlags{}
	body := &parsed.body

	if flags.Changed(copyFromFlag) {
		from, _ := flags.GetString(copyFromFlag)
		if err := table_series.ValidatePeriodKey(from); err != nil {
			return nil, qerrors.NewBadUsagef("invalid value for --%s: %v", copyFromFlag, err)
		}
		body.CopyFromPeriodKey = &from
	}

	schemaValue, _ := flags.GetString(schemaFlag)
	schema := strings.ReplaceAll(strings.TrimSpace(schemaValue), "-", "_")
	if !slices.Contains(recreateSchemas, schema) {
		return nil, qerrors.NewBadUsagef("invalid value %q for --%s: must be one of copy, copy-without-columns, learn-from-file",
			schemaValue, schemaFlag)
	}
	option := api.SeriesPeriodRecreateSchemaOption(schema)
	body.Schema = &option

	if err := parseColumnFlags(flags, schema, body); err != nil {
		return nil, err
	}

	if flags.Changed(loadingProcessFlag) {
		value, _ := flags.GetString(loadingProcessFlag)
		process := strings.TrimSpace(value)
		if !slices.Contains(recreateLoadingProcesses, process) {
			return nil, qerrors.NewBadUsagef("invalid value %q for --%s: must be one of %s",
				value, loadingProcessFlag, strings.Join(recreateLoadingProcesses, ", "))
		}
		dlp := api.DataLoadingProcess(process)
		body.DataLoadingProcess = &dlp
	}

	includeValues, _ := flags.GetStringArray(includeFlag)
	if include, err := parseIncludeOptions(includeValues); err != nil {
		return nil, err
	} else if include != nil {
		body.Include = &include
	}

	reloadAll, err := parseReloadFlags(flags, body)
	if err != nil {
		return nil, err
	}
	parsed.reloadAll = reloadAll
	return parsed, nil
}

// parseColumnFlags checks --leave-out-column, --key-column and
// --no-key-columns against schema and sets them on body.
func parseColumnFlags(flags *pflag.FlagSet, schema string, body *table_series.PeriodRecreateBody) error {
	leaveOutValues, _ := flags.GetStringArray(leaveOutColumnFlag)
	leaveOut, err := splitList(leaveOutColumnFlag, leaveOutValues)
	if err != nil {
		return err
	}
	switch {
	case schema == "copy_without_columns" && len(leaveOut) == 0:
		return qerrors.NewBadUsagef("--%s copy-without-columns needs at least one --%s", schemaFlag, leaveOutColumnFlag)
	case schema != "copy_without_columns" && len(leaveOut) > 0:
		return qerrors.NewBadUsagef("--%s is accepted only with --%s copy-without-columns", leaveOutColumnFlag, schemaFlag)
	case len(leaveOut) > 0:
		body.ColumnsToLeaveOut = &leaveOut
	}

	keyValues, _ := flags.GetStringArray(keyColumnFlag)
	keys, err := splitList(keyColumnFlag, keyValues)
	if err != nil {
		return err
	}
	noKeys, _ := flags.GetBool(noKeyColumnsFlag)
	if len(keys) > 0 && noKeys {
		return qerrors.NewBadUsagef("--%s and --%s are mutually exclusive", keyColumnFlag, noKeyColumnsFlag)
	}
	if schema == learnFromFileSchema && (len(keys) > 0 || noKeys) {
		return qerrors.NewBadUsagef("--%s learn-from-file chooses key columns at the first migration review; "+
			"--%s and --%s are refused with it", schemaFlag, keyColumnFlag, noKeyColumnsFlag)
	}
	if len(keys) > 0 {
		body.KeyColumns = &keys
	}
	if noKeys {
		body.NoKeyColumns = &noKeys
	}
	return nil
}

// parseReloadFlags sets --reload-file on body and reports --reload-all-files.
func parseReloadFlags(flags *pflag.FlagSet, body *table_series.PeriodRecreateBody) (bool, error) {
	reloadValues, _ := flags.GetStringArray(reloadFileFlag)
	reloadAll, _ := flags.GetBool(reloadAllFilesFlag)
	if len(reloadValues) > 0 && reloadAll {
		return false, qerrors.NewBadUsagef("--%s and --%s are mutually exclusive", reloadFileFlag, reloadAllFilesFlag)
	}
	ids, err := parseFileIDs(reloadValues)
	if err != nil {
		return false, err
	}
	if len(ids) > 0 {
		body.ReloadStoredItemIds = &ids
	}
	return reloadAll, nil
}

// parseIncludeOptions parses --include into copy options, refusing settings,
// which a recreate always carries. It returns nil when values is empty.
func parseIncludeOptions(values []string) ([]api.DatasetCopyOption, error) {
	names, err := splitList(includeFlag, values)
	if err != nil || len(names) == 0 {
		return nil, err
	}
	valid := slices.DeleteFunc(api.EnumValues[api.DatasetCopyOption](), func(name string) bool {
		return name == string(api.DatasetCopyOptionSettings)
	})
	options := make([]api.DatasetCopyOption, 0, len(names))
	for _, name := range names {
		normalized := strings.ReplaceAll(name, "-", "_")
		if normalized == string(api.DatasetCopyOptionSettings) {
			return nil, qerrors.NewBadUsagef("invalid value %q for --%s: settings is always carried", name, includeFlag)
		}
		if !slices.Contains(valid, normalized) {
			return nil, qerrors.NewBadUsagef("invalid value %q for --%s: must be one of %s",
				name, includeFlag, strings.Join(valid, ", "))
		}
		if option := api.DatasetCopyOption(normalized); !slices.Contains(options, option) {
			options = append(options, option)
		}
	}
	return options, nil
}

// parseFileIDs parses --reload-file values, each a positive id or file/<id>,
// in first-seen order without repeats.
func parseFileIDs(values []string) ([]int, error) {
	elements, err := splitList(reloadFileFlag, values)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(elements))
	for _, element := range elements {
		stripped, err := cmdutil.ParseKindRef("file", element)
		if err != nil {
			return nil, err
		}
		id, err := strconv.Atoi(stripped)
		if err != nil || id <= 0 {
			return nil, qerrors.NewBadUsagef("invalid value %q for --%s: must be a positive file ID", element, reloadFileFlag)
		}
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func runRecreatePeriod(cmd *cobra.Command, args []string) error {
	parsed, err := parseRecreateFlags(cmd)
	if err != nil {
		return err
	}
	target, err := resolvePeriod(cmd, args[0])
	if err != nil {
		return err
	}
	key, name := target.member.PeriodKey, target.series.Name
	body := parsed.body
	if parsed.reloadAll {
		files, _, err := target.client.ListReloadableFiles(target.token, target.series.ID, key)
		if err != nil {
			return err
		}
		ids := make([]int, 0, len(files))
		for _, file := range files {
			ids = append(ids, file.ID)
		}
		body.ReloadStoredItemIds = &ids
	}

	yes, _ := cmd.Flags().GetBool("yes")
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		"recreating a series period requires confirmation; re-run with --yes",
		recreateQuestion(key, name, target.member.DatasetID, body)); err != nil {
		return err
	}
	result, raw, err := target.client.RecreatePeriod(target.token, target.series.ID, key, body)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	writeRecreatedPeriod(w.Human, key, name, body, result)
	if failed := failedReloads(result.FileReloads); failed > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: %d of %d file reload(s) failed; the recreate itself is committed\n",
			failed, len(result.FileReloads))
	}
	return writePeriodResult(cmd, w, raw, fmt.Sprintf("table/%d", result.DatasetID))
}

func recreateQuestion(key, series string, datasetID int, body table_series.PeriodRecreateBody) string {
	from := "its own table"
	if body.CopyFromPeriodKey != nil && *body.CopyFromPeriodKey != key {
		from = fmt.Sprintf("the table of period %q", terminal.Sanitize(*body.CopyFromPeriodKey))
	}
	reloads := 0
	if body.ReloadStoredItemIds != nil {
		reloads = len(*body.ReloadStoredItemIds)
	}
	return fmt.Sprintf("recreate period %q of series %q from a copy of %s with schema %s, reloading %d file(s)? "+
		"Table %d is soft-deleted, and its alerts, comment threads, reviews, row ignores, duplicate groups and steward fixes stay with it",
		terminal.Sanitize(key), terminal.Sanitize(series), from, *body.Schema, reloads, datasetID)
}

func failedReloads(reloads []api.SeriesPeriodFileReloadSchema) int {
	failed := 0
	for _, reload := range reloads {
		if reload.Outcome == api.SeriesPeriodFileReloadSchemaOutcomeFailed {
			failed++
		}
	}
	return failed
}

func writeRecreatedPeriod(out io.Writer, key, series string, body table_series.PeriodRecreateBody,
	result *table_series.PeriodRecreateResult) {
	fmt.Fprintf(out, "table/%d recreated period %q of series %q, replacing table/%d\n", result.DatasetID,
		terminal.Sanitize(key), terminal.Sanitize(series), result.ReplacedDatasetID)
	fmt.Fprintf(out, "  replaced table name : %s\n", terminal.Sanitize(result.ReplacedTableName))
	fmt.Fprintf(out, "  table copy          : %d\n", result.DatasetCopyID)
	for _, source := range result.MovedDataSources {
		fmt.Fprintf(out, "  cloud source moved  : cloud-source/%d %q\n", source.ID, terminal.Sanitize(source.Name))
	}
	fmt.Fprintf(out, "  files moved         : %d\n", len(result.MovedStoredItemIds))
	if stayed := stayedSummary(result.StayedWithReplacedTable); stayed != "" {
		fmt.Fprintf(out, "  stayed with table/%d: %s\n", result.ReplacedDatasetID, stayed)
	}
	for _, rule := range result.SkippedRules {
		fmt.Fprintf(out, "  rule skipped        : %s (missing %s)\n", terminal.Sanitize(skippedRuleName(rule)),
			terminal.Sanitize(strings.Join(rule.MissingColumns, ", ")))
	}
	for _, reload := range result.FileReloads {
		fmt.Fprintf(out, "  file reload         : file/%d %s", reload.StoredItemID, reload.Outcome)
		if reload.IngestionJobID != nil {
			fmt.Fprintf(out, " ingestion-job/%d", *reload.IngestionJobID)
		}
		if reload.Reason != nil {
			fmt.Fprintf(out, ": %s", terminal.Sanitize(*reload.Reason))
		}
		fmt.Fprintln(out)
	}
	writeRunsToReissue(out, result.RunsToReissue)
	if *body.Schema == learnFromFileSchema {
		if len(result.FileReloads) == 0 {
			fmt.Fprintf(out, "Note: no file was reloaded; the table learns its schema from the next file uploaded to period %q\n",
				terminal.Sanitize(key))
		}
		fmt.Fprintf(out, "Next: qctl get alerts --table-id %d\n", result.DatasetID)
	}
}

func skippedRuleName(rule api.SeriesPeriodSkippedRuleSchema) string {
	switch {
	case rule.Name != nil && *rule.Name != "":
		return *rule.Name
	case rule.DatasetRuleID != nil:
		return "rule " + *rule.DatasetRuleID
	}
	return "a rule"
}

// stayedSummary lists the non-zero counts of what stayed with the replaced
// table, and says so when its steward fixes were not counted.
func stayedSummary(s api.SeriesPeriodStayedWithReplacedTableSchema) string {
	counts := []struct {
		n    int
		what string
	}{
		{s.UnresolvedAlerts, "unresolved alerts (now resolved)"},
		{s.CommentThreads, "comment threads"},
		{s.DataReviews, "data reviews"},
		{s.DataActionReviews, "data action reviews"},
		{s.RowIgnores, "row ignores"},
		{s.DuplicateGroups, "duplicate groups"},
		{s.DuplicateGroupResponses, "duplicate group responses"},
	}
	var parts []string
	for _, c := range counts {
		if c.n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", c.n, c.what))
		}
	}
	switch fixes := s.StewardFixes; {
	case fixes == nil:
		parts = append(parts, "steward fixes not counted")
	case *fixes > 0:
		parts = append(parts, fmt.Sprintf("%d cells with steward fixes", *fixes))
	}
	return strings.Join(parts, ", ")
}
