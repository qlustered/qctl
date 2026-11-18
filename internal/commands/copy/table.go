package copycmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// Flag names of copy table other than --include, --exclude and
// --data-sources-file, without the leading dashes.
const (
	nameFlag          = "name"
	tableNameFlag     = "table-name"
	databaseNameFlag  = "database-name"
	schemaNameFlag    = "schema-name"
	destinationIDFlag = "destination-id"
	dryRunFlag        = "dry-run"
	yesFlag           = "yes"
)

// NewTableCommand creates the 'copy table' command.
func NewTableCommand() *cobra.Command {
	var lists copyTableLists
	cmd := &cobra.Command{
		Use:         "table [table]",
		Short:       "Copy a table into a new table",
		Long:        copyTableLong(),
		Annotations: output.WithNameKind(cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("table-copy"), "table", "json", "yaml", "name"), "table"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCopyTable(cmd, args, lists)
		},
	}
	cmdutil.AddTableFlags(cmd, "Source table")
	cmd.Flags().String(nameFlag, "", "Display name of the new table (default: the name the server suggests; not with --series)")
	cmd.Flags().String(tableNameFlag, "", "Table name of the new table in the destination (default: the table name the server suggests; not with --series)")
	cmd.Flags().String(databaseNameFlag, "", "Database of the new table in the destination (default: the source table's)")
	cmd.Flags().String(schemaNameFlag, "", "Schema of the new table in the destination (default: the source table's)")
	cmdutil.RefInt(cmd.Flags(), destinationIDFlag, "destination", "ID of the destination of the new table (default: the source table's)")
	cmdutil.RefString(cmd.Flags(), seriesFlag, "table-series", "Series name or full UUID for the new table (requires --period-key)")
	cmd.Flags().String(periodKeyFlag, "", "Explicit reporting period for the new table in --series")
	cmd.Flags().StringArrayVar(&lists.include, includeFlag, nil,
		"Copy only these options, plus those always copied (repeatable, or comma-separated)")
	cmd.Flags().StringArrayVar(&lists.exclude, excludeFlag, nil,
		"Copy the default options except these (repeatable, or comma-separated)")
	cmd.Flags().String(dataSourcesFileFlag, "",
		`YAML list of the data sources to copy as well, one entry per copy ("-" reads stdin)`)
	cmd.Flags().Bool(dryRunFlag, false, "Print the plan and copy nothing")
	cmd.Flags().BoolP(yesFlag, "y", false, "Copy without prompting")
	return cmd
}

// copyTableLong is the Long help of copy table.
func copyTableLong() string {
	return fmt.Sprintf(`Copy a table into a new table.

The command first reads what a copy would carry and prints the plan: the new
table's names and location, each copy option with the number of items it
would carry if every option were copied, and the data sources a copy can take.
--dry-run stops there. Otherwise it asks to confirm; --yes skips the
prompt, and is required when stdin is not an interactive terminal.

The new table is named --name and --table-name, or the names the server
suggests. Its destination, database and schema are the source table's unless
--destination-id, --database-name or --schema-name is given.

--series and --period-key add the new table to an existing series as that
explicit reporting period. The series matches by exact, case-sensitive name
or full UUID. The server derives the period's dates from the key under the
series frequency, and the source table's membership is not inherited. The
server also names the new table from the series and the period, so --name
and --table-name cannot be given with --series; the plan shows the names it
previews. The table name is previewed at the source table's location, so at
another destination, database or schema its _N suffix may differ. A missing
series, a period key the series frequency rejects or an occupied period
refuses the plan, --dry-run included. Adding to a series also needs
deliverable:admin.

The copy options, which --include and --exclude name:

  %[1]s

Without --include or --exclude, the copy carries every option the plan marks
DEFAULT or ALWAYS. --include carries the options named plus the ALWAYS ones;
--exclude carries the DEFAULT and ALWAYS options except the ones named. An
ALWAYS option (today, settings) cannot be excluded: that exits 2. Options may
be spelled with hyphens (value-maps).

Data sources are not copied unless --data-sources-file lists them. The file
is a YAML list with one entry per copy; "-" reads it from stdin:

  - source_id: 12
    name: Orders EU S3
    s3_prefix: eu/
    s3_secret_key: ${EU_S3_SECRET}
  - source_id: 15
    state: draft
    sftp_folder: /eu

source_id, the id of a data source the plan lists, is required. These keys
are optional, and each one left out or null keeps the source's value:

  %[2]s

state is one of %[3]s. A source takes only the distinguishing
fields the plan lists for it. "" clears a field, which is how the server
stores an unset one; name and state cannot be "".

A copied source inherits no secret and no URL from its source. These keys
supply them, and each one left out or null stays empty:

  %[4]s

A secret whose whole value is ${VAR} is read from the environment variable
VAR, which must be set, not empty and valid UTF-8; any other value is taken
as written. A secret cannot be "" or contain the mask character •, since the
server reads either as not supplied. The plan shows a supplied secret as
(supplied), and the --dry-run document as ••••••••.

Quote a secret that starts with *, &, ! or another YAML indicator, that holds
" #", or that is null or ~ in any case: unquoted, an anchor no alias uses or a
tag other than !!str exits 2, while null is read as no value, so the secret is
left out, and " #" starts a comment.

The plan's SECRETS column names the secrets each source holds, and the plan
lists, for each copy, the secrets supplied and the ones missing. A copy that
is active or disabled, by its state key or its source's state, must supply
every secret its source holds; otherwise the command exits 2 after the plan,
before the prompt, --dry-run included. Supply the missing secrets in the
file, or set state: draft, which saves without them. dropbox_access_token
stands in for dropbox_authorization_code. The server checks the connection of
every copy that is not a draft, with only the secrets its entry supplies,
before it saves anything; a failure copies nothing.

A copy of an ACTIVE data source must change at least one of that source's
distinguishing fields, whatever state the copy is given: the server refuses a
source that reads the same files as an active one. A copy of a draft or
disabled source may keep every field, and the server still refuses it when
another active source reads the same files. A simple_url source is the
exception: the server withholds its URL, which the plan shows as
simple_url=(withheld), so its copy may keep every field, and the server
refuses a supplied URL that reads the same files as an active source. Two
copies of one simple_url source are refused only when both supply the same
URL and the same distinguishing fields and the earlier one is active. The
copies of a table need distinct names, so copying one source twice needs a
name on at least one of the two. When a copied source is active, the new
table's upload source is disabled.

The plan and the copy both need the dataset:create permission, which editors
do not have, so an editor's --dry-run is refused as well. Copying a data
source also needs source:create, which only the copy checks: a --dry-run
without it succeeds, and the copy is then refused as a whole.

The plan goes to stdout, or to stderr under -o json|yaml|name. After the copy,
-o json|yaml prints the server's response as received and -o name prints
table/<new id>; the default view lists what the copy carried, the secrets
each copied source still lacks, and for a draft copy the commands that add
them and turn it on. An error the server reports about one entry names it as
"--data-sources-file entry N (source_id ID)", counted from 1. With --dry-run,
-o json|yaml prints one document: options, what the server offered, and
request, the request a copy would send; -o name is refused, since there is no
id yet.

Examples:
  # See what a copy would carry, and copy nothing
  qctl copy table orders --dry-run

  # Copy under chosen names without a prompt
  qctl copy table orders --name "Orders EU" --table-name orders_eu --yes

  # Copy into a series as the next explicitly chosen reporting period
  qctl copy table orders --series "Monthly orders" --period-key 2026-10 --yes

  # Copy everything but the learned values and the access grants
  qctl copy table --table-id 42 --exclude learned-values,access

  # Copy the settings and the rules only
  qctl copy table orders --include rules

  # Also copy the data sources eu-sources.yaml lists
  qctl copy table orders --data-sources-file eu-sources.yaml`,
		strings.Join(api.EnumValues[api.DatasetCopyOption](), ", "),
		strings.Join(dataSourceKeys, ", "),
		joinStates(),
		strings.Join(dataSourceSecretKeys, ", "))
}

// copyTableLists holds the values of the repeatable flags of copy table as
// given. They are bound rather than read back with GetStringArray, which
// returns no value for a flag given once as "".
type copyTableLists struct {
	include []string
	exclude []string
}

// copyTableFlags is every flag of copy table, read and checked without a
// request. A nil pointer is a flag that was not given.
type copyTableFlags struct {
	name          *string
	tableName     *string
	databaseName  *string
	schemaName    *string
	destinationID *int
	seriesRef     string
	series        *api.DatasetCopySeriesRequest
	include       []api.DatasetCopyOption
	exclude       []api.DatasetCopyOption
	dataSources   []dataSourceSpec
	dryRun        bool
	yes           bool
}

func runCopyTable(cmd *cobra.Command, args []string, lists copyTableLists) error {
	positional := ""
	if len(args) == 1 {
		positional = args[0]
	}
	flags, err := readCopyTableFlags(cmd, positional, lists)
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
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	if flags.series != nil {
		seriesClient := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if flags.series.SeriesID, err = seriesClient.ResolveID(token, flags.seriesRef); err != nil {
			return err
		}
	}
	offered, offeredBody, err := client.CopyOptions(token, tableID, flags.series)
	if err != nil {
		return err
	}
	request, err := buildCopyRequest(offered, flags)
	if err != nil {
		return err
	}
	plan := &copyTablePlan{
		source:      copySourceLabel(cmd, positional, tableID),
		offered:     offered,
		offeredBody: offeredBody,
		flags:       flags,
		request:     request,
	}

	format, _ := cmd.Flags().GetString("output")
	planOut := cmd.OutOrStdout()
	switch output.Format(format) {
	case output.FormatJSON, output.FormatYAML, output.FormatName:
		planOut = cmd.ErrOrStderr()
	}
	if err := writeCopyPlan(planOut, plan); err != nil {
		return err
	}
	if err := refuseMissingSecrets(offered.DataSources, flags.dataSources); err != nil {
		return err
	}
	if flags.dryRun {
		return writeCopyDryRun(cmd.OutOrStdout(), format, plan)
	}

	phrase := fmt.Sprintf("copy %s to a new table %s", plan.source, strconv.Quote(plan.newName()))
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), flags.yes,
		"confirmation required but stdin is not an interactive terminal: pass --yes to "+phrase,
		strings.ToUpper(phrase[:1])+phrase[1:]+"?"); err != nil {
		return err
	}

	result, raw, err := client.Copy(token, tableID, request)
	if err != nil {
		return locateDataSourceError(err, request)
	}
	return writeCopyResult(cmd.OutOrStdout(), format, plan, result, raw)
}

// readCopyTableFlags reads the table selector and the flags of copy table,
// refusing a malformed or conflicting one with exit 2.
func readCopyTableFlags(cmd *cobra.Command, positional string, lists copyTableLists) (copyTableFlags, error) {
	var flags copyTableFlags
	if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
		return flags, err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{includeFlag, excludeFlag}, []string{dryRunFlag, yesFlag}); err != nil {
		return flags, err
	}
	format, _ := cmd.Flags().GetString("output")
	if dryRun, _ := cmd.Flags().GetBool(dryRunFlag); dryRun && format == string(output.FormatName) {
		return flags, qerrors.NewBadUsagef("--%s creates no table, so -o name has no table/<id> to print; use -o json or yaml", dryRunFlag)
	}
	for _, f := range []struct {
		name, remedy string
		dst          **string
	}{
		{nameFlag, "omit it to use the name the server suggests", &flags.name},
		{tableNameFlag, "omit it to use the table name the server suggests", &flags.tableName},
		{databaseNameFlag, "omit it to keep the source table's database", &flags.databaseName},
		{schemaNameFlag, "omit it to keep the source table's schema", &flags.schemaName},
	} {
		value, err := nonEmptyStringFlag(cmd, f.name, f.remedy)
		if err != nil {
			return flags, err
		}
		*f.dst = value
	}
	destinationID, given, err := cmdutil.PositiveIDFlag(cmd, destinationIDFlag)
	if err != nil {
		return flags, err
	}
	if given {
		flags.destinationID = &destinationID
	}
	flags.seriesRef, flags.series, err = readCopySeriesFlags(cmd)
	if err != nil {
		return flags, err
	}
	if flags.include, err = parseCopyOptions(includeFlag, lists.include); err != nil {
		return flags, err
	}
	if flags.exclude, err = parseCopyOptions(excludeFlag, lists.exclude); err != nil {
		return flags, err
	}
	if cmd.Flags().Changed(dataSourcesFileFlag) {
		path, _ := cmd.Flags().GetString(dataSourcesFileFlag)
		if flags.dataSources, err = readDataSourcesFile(cmd, path); err != nil {
			return flags, err
		}
	}
	flags.dryRun, _ = cmd.Flags().GetBool(dryRunFlag)
	flags.yes, _ = cmd.Flags().GetBool(yesFlag)
	return flags, nil
}

// nonEmptyStringFlag returns the value of the string flag name, or nil when it
// was not given. A value given as "" is refused, with remedy.
func nonEmptyStringFlag(cmd *cobra.Command, name, remedy string) (*string, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	value, _ := cmd.Flags().GetString(name)
	if value == "" {
		return nil, qerrors.NewBadUsagef("--%s cannot be empty: %s", name, remedy)
	}
	return &value, nil
}

// buildCopyRequest returns the request that copies the table offered describes
// as flags ask. With a series, name and table_name are null.
func buildCopyRequest(offered *datasets.CopyOptions, flags copyTableFlags) (datasets.CopyRequest, error) {
	include, err := resolveInclude(offered.Options, flags.include, flags.exclude)
	if err != nil {
		return datasets.CopyRequest{}, err
	}
	name := offered.Name
	request := datasets.CopyRequest{
		DatabaseName:  flags.databaseName,
		SchemaName:    flags.schemaName,
		DestinationID: flags.destinationID,
		Include:       &include,
		Series:        flags.series,
	}
	if flags.series == nil {
		tableName := offered.TableName
		if flags.name != nil {
			name = *flags.name
		}
		if flags.tableName != nil {
			tableName = *flags.tableName
		}
		request.Name, request.TableName = &name, &tableName
	}
	if len(flags.dataSources) > 0 {
		sources, err := buildDataSourceRequests(offered.DataSources, flags.dataSources, name)
		if err != nil {
			return datasets.CopyRequest{}, err
		}
		request.DataSources = &sources
	}
	return request, nil
}

// copySourceLabel names the source table as the command line named it, with
// its id when the command line gave something other than the id.
func copySourceLabel(cmd *cobra.Command, positional string, tableID int) string {
	label := cmdutil.TableLabel(cmd, positional, tableID)
	if label == strconv.Itoa(tableID) {
		return fmt.Sprintf("table %d", tableID)
	}
	return fmt.Sprintf("table %s (id %d)", terminal.Sanitize(label), tableID)
}
