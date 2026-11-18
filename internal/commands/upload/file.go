package upload

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	unknownValue = "Unknown"

	// routingUploaderChooses is the routing mode of a series cloud source
	// whose uploader names the period.
	routingUploaderChooses = "uploader_chooses"
	// routingLatestUnlocked is the routing mode of a series cloud source that
	// routes each file to the latest unlocked period.
	routingLatestUnlocked = "latest_unlocked"
)

// uploadContext holds resolved values for the upload operation. datasetID and
// datasetName are the table the file lands on, or waits on until it is routed;
// datasetID is 0 when that table is not known.
type uploadContext struct {
	filePath        string
	fileName        string
	datasetName     string
	cloudSourceName string
	orgID           string
	orgName         string
	fileSize        int
	datasetID       int
	cloudSourceID   int

	// series marks a series cloud source. periodKey is the period_key sent,
	// nil when none; routingMode is the source's routing_mode.
	series      bool
	periodKey   *string
	routingMode string
	// periodInferred marks a periodKey read from the file name, and
	// periodSource is what the server read it from.
	periodInferred bool
	periodSource   *api.PeriodSource

	// delta marks a partial CDC delivery, sent with deltaCursor.
	// appliedCursor is the applied cursor deltaCursor must beat: the cloud
	// source's, or its period's for an uploader_chooses source; nil when none
	// is known.
	delta         bool
	deltaCursor   int
	appliedCursor *int

	// replaces is the --replaces file ID, 0 when not given.
	// confirmReplacement asks whether the upload may replace a loaded file;
	// nil under --yes.
	replaces           int
	confirmReplacement confirmReplacementFunc
}

// fileOptions holds the upload file flags read after validation.
type fileOptions struct {
	cloudSourceName string
	period          string
	cloudSourceID   int
	replaces        int
	deltaCursor     int64
	delta           bool
}

// NewFileCommand creates the 'upload file' command
func NewFileCommand() *cobra.Command {
	var (
		serverFlag string
		userFlag   string
		yes        bool
		opts       fileOptions
	)

	cmd := &cobra.Command{
		Use:   "file PATH",
		Short: "Upload a file for ingestion",
		Long: `Upload a file to a cloud source for ingestion.

The upload key is PATH's bare file name, with no directory or added timestamp.
The file is uploaded to backup storage and then published for ingestion into
the cloud source's table. A series cloud source whose routing_mode is
uploader_chooses takes it into the period of --table, or --period; any other
series source routes it to a period after upload.
With --cloud-source-id and --period, a listed creatable period needs no table
yet: the server creates its table when it accepts the upload request.

--table-id or --table (dataset) is required unless --cloud-source-id and
--period choose a period directly. With --cloud-source-id naming an
uploader_chooses series source and neither --table nor --period, qctl asks
the server which period the file name names, in the series' frequency, and
uploads to it as --period would; a file name that names no single period
exits 2. --cloud-source-id or --cloud-source
(data source) is required only when the table has more than one cloud source;
with one, qctl uses it.

External uploaders use --table-id, or --cloud-source-id with --period.
Their assigned source and accessible submission periods identify the target;
table names require table-list permission. The server checks delta settings
before issuing an upload URL.

--delta delivers a partial CDC file whose --delta-cursor must beat
the applied cursor of the cloud source, or of the period for an
uploader_chooses series source. A latest_unlocked series source refuses it;
a file_name or column one checks the cursor at ingestion, against the period
the file is routed to.

Before confirmation, qctl checks how many manually fixed rows this upload may
drop and warns on stderr when the count is positive, including with --yes.
An unavailable check is reported on stderr and does not stop the upload.

After the bytes are uploaded and before the file is published, qctl asks the
server which loaded file of the period the upload would replace, if any, and
asks before replacing it. A yes publishes the file confirming that replacement,
so ingestion does not hold it for it; a no removes the unpublished file record.
With --replaces <file-id>, a file the upload replaces is confirmed when it is
that file, and the upload is refused (exit 2) when it is another. With --yes
and no --replaces, the file is published unconfirmed and ingestion holds it on
a preload_check_held alert.

Under -o json or yaml, {stored_item_id, ingestion_job_id, table_id, table,
period_key, routing_pending, cloud_source_id, cloud_source,
replaces_stored_item_id} is printed; -o
name prints file/<stored-item-id>. The summary and progress lines go to stderr.

Examples:
  # Upload to a specific table and cloud source
  qctl upload file data.csv --table-id 123 --cloud-source-id 456

  # Upload using names (requires quotes if names contain spaces)
  qctl upload file data.csv --table "Customer Data" --cloud-source "S3 Import"

  # Skip confirmation prompt
  qctl upload file data.csv --table-id 123 --cloud-source-id 456 --yes

  # Deliver a partial CDC file to a keyed mirror table
  qctl upload file delta.csv --table orders --cloud-source "Airbyte CDC" --delta --delta-cursor 1042

  # Upload into period 2026-07 of an uploader_chooses series cloud source
  qctl upload file july.csv --table orders_2026_07 --cloud-source "Monthly drop" --period 2026-07

  # Choose a period directly, without naming its table
  qctl upload file q3.csv --cloud-source-id 456 --period 2026-Q3

  # Take the period from the file name (here 2026-10 for a monthly series)
  qctl upload file Premium_Bordereau_Oct26.csv --cloud-source-id 456

  # Confirm, without a prompt, that the upload replaces loaded file 812
  qctl upload file aug.csv --cloud-source-id 456 --period 2026-08 --yes --replaces 812`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml", "name"), "-"), // PATH is a local file
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tableID, _ := cmd.Flags().GetInt("table-id")
			tableName, _ := cmd.Flags().GetString("table")

			filePath := args[0]

			// Validate file
			fileSize, err := validateFile(filePath)
			if err != nil {
				return err
			}
			// Every flag-only refusal is settled before Bootstrap.
			if err = validateFileFlags(cmd, opts.delta, opts.deltaCursor, opts.period); err != nil {
				return err
			}
			if opts.delta && opts.replaces != 0 {
				return qerrors.NewBadUsage("--replaces is not valid with --delta: a delta file replaces no loaded file")
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			fileName := filepath.Base(filePath)
			uc, err := resolveUpload(cmd, ctx, tableID, tableName, fileName, &opts)
			if err != nil {
				return err
			}
			uc.filePath, uc.fileSize, uc.fileName = filePath, fileSize, fileName

			// Display confirmation and prompt
			w := cmdutil.NewResultWriter(cmd)
			displayUploadConfirmation(w.Human, uc)
			warnDroppedFixes(cmd.ErrOrStderr(), ctx, uc)
			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Proceed with upload and ingestion?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("upload cancelled")
				}
				errOut := cmd.ErrOrStderr()
				uc.confirmReplacement = func(r *stored_items.ReplacedFile, _ bool) (bool, error) {
					return cmdutil.ConfirmYesNo(errOut, replacementPrompt(r))
				}
			}
			uc.replaces = opts.replaces

			// Perform upload
			result, err := performUpload(w.Human, ctx, uc)
			if err != nil {
				return err
			}
			return w.Write(result, fmt.Sprintf("file/%d", result.StoredItemID))
		},
	}

	// Add flags
	cmd.Flags().StringVar(&serverFlag, "server", "", "Override server URL")
	cmd.Flags().StringVar(&userFlag, "user", "", "Override user")
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.RefIntVar(cmd.Flags(), &opts.cloudSourceID, "cloud-source-id", "cloud-source", "Cloud source ID (data source model ID)")
	cmd.Flags().StringVar(&opts.cloudSourceName, "cloud-source", "", "Cloud source name (requires quotes if name contains spaces)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().BoolVar(&opts.delta, "delta", false, "Deliver the file as a partial CDC delivery to a keyed mirror table (requires --delta-cursor)")
	cmd.Flags().Int64Var(&opts.deltaCursor, "delta-cursor", 0, "Source cursor of a --delta delivery; must be greater than the applied cursor of the cloud source, or of the period for a series cloud source")
	cmdutil.RefIntVar(cmd.Flags(), &opts.replaces, "replaces", "file", "Loaded file the upload replaces in its period; confirms that replacement without a prompt, and refuses the upload when it replaces another file")
	cmd.Flags().StringVar(&opts.period, "period", "", "Period of the series to upload to (uploader_chooses series cloud sources only); defaults to the --table's own period, else the period the file name names")

	return cmd
}

// resolveUpload resolves an upload's target and organization. With no table
// and no --period, an uploader_chooses series source takes the period that
// fileName names.
func resolveUpload(cmd *cobra.Command, ctx *cmdutil.CommandContext, tableID int, tableName, fileName string, opts *fileOptions) (*uploadContext, error) {
	user, err := auth.NewClient(ctx.ServerURL, ctx.Verbosity).GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
	if err != nil {
		return nil, fmt.Errorf("failed to get current user info: %w", err)
	}
	external := user.Role == string(api.RoleEnumExternal)
	var table cmdutil.ResolvedTable
	var cs cmdutil.ResolvedCloudSource
	if external {
		table, cs, err = resolveUploaderTarget(ctx, tableID, tableName, opts)
	} else {
		table, cs, err = resolveReaderTarget(ctx, tableID, tableName, opts)
	}
	if err != nil {
		return nil, err
	}

	periodSet := cmd.Flags().Changed("period")
	var periodSource *api.PeriodSource
	inferred := table.ID == 0 && !periodSet
	if inferred {
		if cs.TableSeriesID == nil || cs.RoutingMode != routingUploaderChooses {
			return nil, qerrors.NewBadUsagef(
				"table is required: pass --table <name> or --table-id <id>; cloud source %d is not a series cloud source whose routing_mode is %s, the only kind that can take its period from --period or the file name",
				cs.ID, routingUploaderChooses)
		}
		if opts.period, periodSource, err = inferPeriod(ctx, cs, fileName); err != nil {
			return nil, err
		}
		periodSet = true
	}
	target, err := decideUploadTarget(table, cs, opts.period, periodSet)
	if err != nil {
		return nil, err
	}
	if table.ID == 0 {
		table, err = resolvePeriodTable(ctx, cs, opts.period, external)
		if err != nil {
			return nil, err
		}
		target, err = chosenPeriodTarget(table, cs, opts.period, true)
		if err != nil {
			return nil, err
		}
	}

	var appliedCursor *int
	if opts.delta {
		appliedCursor, err = checkDeltaUpload(ctx, table, &cs, target, int(opts.deltaCursor))
		if err != nil {
			return nil, err
		}
	}

	orgID, orgName := getOrgContext(user)
	return &uploadContext{
		datasetID:       target.tableID,
		datasetName:     target.tableName,
		series:          target.series,
		periodKey:       target.periodKey,
		routingMode:     target.routingMode,
		periodInferred:  inferred,
		periodSource:    periodSource,
		cloudSourceID:   cs.ID,
		cloudSourceName: cs.Name,
		orgID:           orgID,
		orgName:         orgName,
		delta:           opts.delta,
		deltaCursor:     int(opts.deltaCursor),
		appliedCursor:   appliedCursor,
	}, nil
}

// resolveReaderTarget resolves the table and source using table-read permissions.
func resolveReaderTarget(ctx *cmdutil.CommandContext, tableID int, tableName string, opts *fileOptions) (cmdutil.ResolvedTable, cmdutil.ResolvedCloudSource, error) {
	var table cmdutil.ResolvedTable
	var err error
	if tableID > 0 || tableName != "" {
		table, err = cmdutil.ResolveTableRecord(ctx, tableID, tableName)
		if err != nil {
			return table, cmdutil.ResolvedCloudSource{}, err
		}
	}
	cs, err := cmdutil.ResolveCloudSourceRecord(ctx, table, opts.cloudSourceID, opts.cloudSourceName)
	return table, cs, err
}

// resolvePeriodTable resolves an accessible submission period's table.
func resolvePeriodTable(ctx *cmdutil.CommandContext, cs cmdutil.ResolvedCloudSource, period string, external bool) (cmdutil.ResolvedTable, error) {
	client := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	periods, err := client.ListSubmissionPeriods(ctx.Credential.AccessToken, *cs.TableSeriesID)
	if err != nil {
		return cmdutil.ResolvedTable{}, fmt.Errorf("failed to list periods of cloud source %d: %w", cs.ID, err)
	}
	for _, p := range periods {
		if p.PeriodKey == period {
			if p.DatasetID == nil {
				if p.Creatable {
					return cmdutil.ResolvedTable{}, nil
				}
				return cmdutil.ResolvedTable{}, qerrors.NewBadUsagef(
					"period '%s' of cloud source %d has no table and is not creatable; choose an accessible submission period", period, cs.ID)
			}
			if external {
				return uploaderPeriodTable(cs, p), nil
			}
			table, err := cmdutil.ResolveTableRecord(ctx, *p.DatasetID, "")
			if err != nil {
				return cmdutil.ResolvedTable{}, err
			}
			if table.SeriesID == nil || *table.SeriesID != *cs.TableSeriesID || table.PeriodKey != period {
				return cmdutil.ResolvedTable{}, qerrors.NewBadUsagef(
					"period '%s' of cloud source %d changed while resolving its table; retry the upload", period, cs.ID)
			}
			return table, nil
		}
	}
	return cmdutil.ResolvedTable{}, qerrors.NewBadUsagef(
		"--period '%s' is not an accessible submission period of cloud source %d's series", period, cs.ID)
}

// uploadTarget is the table an upload lands on, or waits on until it is
// routed, and the period_key it sends. tableID is 0 when the table is not
// known; periodKey is nil when no period_key is sent.
type uploadTarget struct {
	tableName   string
	routingMode string
	periodKey   *string
	tableID     int
	series      bool
}

// decideUploadTarget picks the upload's target from the resolved table and
// cloud source and the --period value; periodSet reports whether --period was
// given. A --period the source cannot take is an exit-2 error, and so is an
// uploader_chooses source with no period to send.
func decideUploadTarget(table cmdutil.ResolvedTable, cs cmdutil.ResolvedCloudSource, period string, periodSet bool) (uploadTarget, error) {
	switch {
	case cs.TableSeriesID == nil:
		if periodSet {
			return uploadTarget{}, qerrors.NewBadUsagef(
				"--period is only valid for a series cloud source whose routing_mode is %s; cloud source %d feeds no series",
				routingUploaderChooses, cs.ID)
		}
		return uploadTarget{tableID: table.ID, tableName: table.Name}, nil
	case cs.RoutingMode == routingUploaderChooses:
		return chosenPeriodTarget(table, cs, period, periodSet)
	default:
		return routedTarget(table, cs, periodSet)
	}
}

// chosenPeriodTarget is decideUploadTarget for an uploader_chooses series
// source: the period is --period, else --table's own period in the source's
// series, and the two must agree when both are known.
func chosenPeriodTarget(table cmdutil.ResolvedTable, cs cmdutil.ResolvedCloudSource, period string, periodSet bool) (uploadTarget, error) {
	own := ""
	if table.SeriesID != nil && *table.SeriesID == *cs.TableSeriesID {
		own = table.PeriodKey
	}
	t := uploadTarget{series: true, routingMode: routingUploaderChooses, tableID: table.ID, tableName: table.Name}
	switch {
	case periodSet && own != "" && period != own:
		return uploadTarget{}, qerrors.NewBadUsagef(
			"--period '%s' differs from period '%s' of table %s; name the table of period '%s' with --table, or leave --period out",
			period, own, tableRef(table.ID, table.Name), period)
	case periodSet:
		if own == "" {
			// The file goes to the period's table, which --table is not known to be.
			t.tableID, t.tableName = 0, ""
		}
		t.periodKey = &period
	case own == "":
		return uploadTarget{}, qerrors.NewBadUsagef(
			"cloud source %d has routing_mode %s and table %s holds no period of its series; pass --period",
			cs.ID, routingUploaderChooses, tableRef(table.ID, table.Name))
	default:
		t.periodKey = &own
	}
	return t, nil
}

// routedTarget is decideUploadTarget for a series source whose routing_mode is
// not uploader_chooses: no period_key is sent, and the file waits on the
// source's own table until it is routed.
func routedTarget(table cmdutil.ResolvedTable, cs cmdutil.ResolvedCloudSource, periodSet bool) (uploadTarget, error) {
	mode := cs.RoutingMode
	if mode == "" {
		mode = unknownValue
	}
	if periodSet {
		return uploadTarget{}, qerrors.NewBadUsagef(
			"--period is only valid for routing_mode %s; cloud source %d has routing_mode %s, which routes the file to a period after upload",
			routingUploaderChooses, cs.ID, mode)
	}
	t := uploadTarget{series: true, routingMode: mode, tableID: cs.DatasetID, tableName: cs.DatasetName}
	if cs.DatasetID == 0 || cs.DatasetID == table.ID {
		t.tableID, t.tableName = table.ID, table.Name
	}
	return t, nil
}

// tableRef renders a table for an error message.
func tableRef(id int, name string) string {
	if name == "" {
		return fmt.Sprintf("ID %d", id)
	}
	return fmt.Sprintf("'%s' (id=%d)", name, id)
}

// validateFileFlags checks upload flags.
func validateFileFlags(cmd *cobra.Command, delta bool, deltaCursor int64, period string) error {
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"cloud-source-id", "cloud-source"},
	); err != nil {
		return err
	}
	if _, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id"); err != nil {
		return err
	}
	if !cmd.Flags().Changed("cloud-source-id") {
		if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
			return err
		}
	}
	if cmd.Flags().Changed("period") {
		if err := cmdutil.ValidatePeriodKey("--period", period); err != nil {
			return err
		}
	}
	return validateDeltaFlags(cmd, delta, deltaCursor)
}

// validateDeltaFlags refuses, with exit 2, a --delta /
// --delta-cursor combination the server would reject.
func validateDeltaFlags(cmd *cobra.Command, delta bool, deltaCursor int64) error {
	cursorSet := cmd.Flags().Changed("delta-cursor")
	switch {
	case cursorSet && !delta:
		return qerrors.NewBadUsage("--delta-cursor is only valid with --delta")
	case delta && !cursorSet:
		return qerrors.NewBadUsage("--delta requires --delta-cursor")
	case deltaCursor < 0:
		return qerrors.NewBadUsagef("--delta-cursor must be a non-negative integer, got %d", deltaCursor)
	}
	return nil
}

// checkDeltaUpload applies the server's delta rules to the
// resolved upload, with exit 2, and returns the applied cursor deltaCursor must
// beat, nil when none is known. For an uploader_chooses source found without
// its full record, it fetches that record into cs.Full.
func checkDeltaUpload(ctx *cmdutil.CommandContext, table cmdutil.ResolvedTable, cs *cmdutil.ResolvedCloudSource, target uploadTarget, deltaCursor int) (*int, error) {
	if cs.RoutingMode == routingLatestUnlocked {
		return nil, qerrors.NewBadUsagef(
			"--delta is not valid for routing_mode %s; cloud source %d routes each file to the latest unlocked period after upload",
			routingLatestUnlocked, cs.ID)
	}
	if err := refuseDeltaTable(table, *cs, target); err != nil {
		return nil, err
	}
	if cs.TableSeriesID != nil && cs.RoutingMode == routingUploaderChooses && cs.Full == nil {
		client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
		if err != nil {
			return nil, fmt.Errorf("failed to create cloud sources client: %w", err)
		}
		if cs.Full, err = client.GetCloudSource(ctx.Credential.AccessToken, cs.ID); err != nil {
			return nil, fmt.Errorf("failed to fetch cloud source %d: %w", cs.ID, err)
		}
	}
	return refuseDeltaCursor(*cs, target, deltaCursor)
}

// refuseDeltaTable checks, with exit 2, the data_loading_process and
// key_columns of the table the server checks a delta upload
// against, when table.Full is that table: the target's period table for an
// uploader_chooses source, else the cloud source's own table.
func refuseDeltaTable(table cmdutil.ResolvedTable, cs cmdutil.ResolvedCloudSource, target uploadTarget) error {
	checked := cs.DatasetID == table.ID
	if cs.TableSeriesID != nil && cs.RoutingMode == routingUploaderChooses {
		checked = target.tableID == table.ID
	}
	if table.Full == nil || !checked {
		return nil
	}
	if process := table.Full.DataLoadingProcess; process != api.DataLoadingProcessMirror {
		return qerrors.NewBadUsagef("--delta is only valid for data_loading_process=mirror; table '%s' is %s",
			table.Name, process)
	}
	if keys := table.Full.SettingsModel.KeyColumns; keys == nil || len(*keys) == 0 {
		return qerrors.NewBadUsagef("--delta requires key_columns; table '%s' has none", table.Name)
	}
	return nil
}

// refuseDeltaCursor refuses, with exit 2, a deltaCursor at or below the
// applied cursor it must beat, and returns that cursor: the cloud source's for
// a source in no series, the target period's for an uploader_chooses source,
// and nil for any other series source.
func refuseDeltaCursor(cs cmdutil.ResolvedCloudSource, target uploadTarget, deltaCursor int) (*int, error) {
	switch {
	case cs.TableSeriesID == nil:
		if cs.DeltaAppliedCursor != nil && deltaCursor <= *cs.DeltaAppliedCursor {
			return nil, qerrors.NewBadUsagef("--delta-cursor %d must be greater than the applied cursor %d of cloud source %d",
				deltaCursor, *cs.DeltaAppliedCursor, cs.ID)
		}
		return cs.DeltaAppliedCursor, nil
	case cs.RoutingMode == routingUploaderChooses && target.periodKey != nil:
		applied := periodAppliedCursor(cs.Full, *target.periodKey)
		if applied != nil && deltaCursor <= *applied {
			return nil, qerrors.NewBadUsagef("--delta-cursor %d must be greater than the applied cursor %d of period '%s' of cloud source %d",
				deltaCursor, *applied, *target.periodKey, cs.ID)
		}
		return applied, nil
	default:
		return nil, nil
	}
}

// periodAppliedCursor returns the applied cursor of periodKey's binding in
// full's period_delta_cursors, nil when full is nil, the period has no entry,
// or its cursor is null.
func periodAppliedCursor(full *cloud_sources.CloudSourceFull, periodKey string) *int {
	if full == nil || full.PeriodDeltaCursors == nil {
		return nil
	}
	for _, c := range *full.PeriodDeltaCursors {
		if c.PeriodKey == periodKey {
			return c.DeltaAppliedCursor
		}
	}
	return nil
}

// validateFile checks if the file exists and is not a directory.
// Returns the file size in bytes.
func validateFile(filePath string) (int, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to access file: %w", err)
	}
	if fileInfo.IsDir() {
		return 0, fmt.Errorf("path is a directory, not a file: %s", filePath)
	}
	return int(fileInfo.Size()), nil
}

// getOrgContext returns the current organization ID and name.
func getOrgContext(userMe *auth.UserMeResponse) (string, string) {
	orgName := unknownValue
	orgID := unknownValue
	if userMe.MembershipOrgID != nil && len(userMe.ActiveOrganizationNames) > 0 {
		orgID = *userMe.MembershipOrgID
		for i, id := range userMe.ActiveOrganizationIDs {
			if id == orgID && i < len(userMe.ActiveOrganizationNames) {
				orgName = userMe.ActiveOrganizationNames[i]
				break
			}
		}
	}

	return orgID, orgName
}

// displayName renders a name for the confirmation summary. A null name from the
// API arrives here as "", and a blank field in a prompt asking for confirmation
// is worse than an explicit placeholder.
//
// The name is server-supplied, and every caller writes it straight to the
// terminal, so the visible-escape pass belongs here rather than at each Printf.
func displayName(name string) string {
	if name == "" {
		return "(unnamed)"
	}
	return terminal.Sanitize(name)
}

// displayUploadConfirmation prints the upload summary for user confirmation.
// Everything but the local file path comes from the API, so it is rendered
// through terminal.Sanitize: this block is what the user reads before agreeing
// to the upload, and it must not be able to rewrite itself.
func displayUploadConfirmation(out io.Writer, uc *uploadContext) {
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(uc.orgName), terminal.Sanitize(uc.orgID))
	if uc.datasetID == 0 {
		fmt.Fprintf(out, "Table        : %s\n", tableOfPeriod(uc))
	} else {
		fmt.Fprintf(out, "Table        : %s (id=%d)\n", uploadTableName(uc.datasetName), uc.datasetID)
	}
	if uc.series {
		fmt.Fprintf(out, "Period       : %s\n", periodText(uc))
	}
	fmt.Fprintf(out, "Cloud source : %s (id=%d)\n", displayName(uc.cloudSourceName), uc.cloudSourceID)
	fmt.Fprintf(out, "File         : %s, size=%d bytes\n", filepath.Base(uc.filePath), uc.fileSize)
	if uc.delta {
		fmt.Fprintf(out, "Delta        : cursor %d (%s)\n", uc.deltaCursor, appliedCursorText(uc))
	}
	fmt.Fprintln(out)
}

// uploadTableName renders a table name when available.
func uploadTableName(name string) string {
	if name == "" {
		return "(name unavailable)"
	}
	return displayName(name)
}

// appliedCursorText renders the applied cursor a delta upload's cursor
// must beat, for the confirmation's Delta line.
func appliedCursorText(uc *uploadContext) string {
	if uc.series && uc.periodKey == nil {
		return "checked at ingestion against the applied cursor of the period it is routed to"
	}
	applied := "none"
	if uc.appliedCursor != nil {
		applied = strconv.Itoa(*uc.appliedCursor)
	}
	if uc.series {
		return fmt.Sprintf("applied cursor of period %s: %s", terminal.Sanitize(*uc.periodKey), applied)
	}
	return "applied cursor: " + applied
}

// periodText renders a series upload's period: the period_key sent, or the
// routing that picks one after upload.
func periodText(uc *uploadContext) string {
	if uc.periodKey != nil && uc.periodInferred {
		return fmt.Sprintf("%s (%s)", terminal.Sanitize(*uc.periodKey), inferredPeriodText(uc.periodSource))
	}
	if uc.periodKey != nil {
		return terminal.Sanitize(*uc.periodKey)
	}
	return fmt.Sprintf("routed after upload (routing_mode: %s)", terminal.Sanitize(uc.routingMode))
}

// tableOfPeriod names the target table when only its period is known.
func tableOfPeriod(uc *uploadContext) string {
	return "the table of period " + periodText(uc)
}

// uploadedFile is the result document of an upload. CloudSource is nil for
// an unnamed cloud source. PeriodKey is the period_key sent, nil when none;
// RoutingPending is true for a series upload that sent none.
type uploadedFile struct {
	StoredItemID   int     `json:"stored_item_id"`
	IngestionJobID *int    `json:"ingestion_job_id"`
	TableID        int     `json:"table_id"`
	Table          string  `json:"table"`
	PeriodKey      *string `json:"period_key"`
	RoutingPending bool    `json:"routing_pending"`
	CloudSourceID  int     `json:"cloud_source_id"`
	CloudSource    *string `json:"cloud_source"`
	// ReplacesStoredItemID is the loaded file the publish confirmed this file
	// replaces, nil when it confirmed none.
	ReplacesStoredItemID *int `json:"replaces_stored_item_id"`
}

// performUpload executes the upload workflow: create URL, upload file, publish.
func performUpload(out io.Writer, ctx *cmdutil.CommandContext, uc *uploadContext) (*uploadedFile, error) {
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create stored items client: %w", err)
	}
	token := ctx.Credential.AccessToken

	// Create stored item and get pre-signed upload URL
	fmt.Fprintln(out, "Creating upload URL...")
	uploadReq := stored_items.StoredItemPutRequest{
		DataSourceModelID: uc.cloudSourceID,
		Key:               uc.fileName,
		FileSize:          &uc.fileSize,
		PeriodKey:         uc.periodKey,
	}
	if uc.delta {
		isDelta, cursor := true, uc.deltaCursor
		uploadReq.IsDelta = &isDelta
		uploadReq.DeltaCursor = &cursor
	}

	urlResp, err := client.CreateStoredItemForUpload(token, uploadReq)
	if err != nil {
		return nil, explainPeriodLocked(fmt.Errorf("failed to create upload URL: %w", err), uc)
	}

	storedItemID := urlResp.ID
	if uc.delta {
		if err := verifyDeltaUpload(client, token, storedItemID, uc.deltaCursor); err != nil {
			return nil, abortBeforeFileUpload(client, token, storedItemID, err)
		}
	}

	// Display any message from backend
	if urlResp.Msg != nil && *urlResp.Msg != "" {
		fmt.Fprintf(out, "Note: %s\n", terminal.Sanitize(*urlResp.Msg))
	}

	// Upload file
	fmt.Fprintf(out, "Uploading file (stored_item_id=%d)...\n", storedItemID)
	if err := client.UploadFile(urlResp, uc.filePath); err != nil {
		return nil, handleUploadFailure(client, token, storedItemID, err)
	}

	// Publish for ingestion
	fmt.Fprintln(out, "Publishing for ingestion...")
	pub, err := publishUpload(out, client, token, storedItemID, uc)
	if err != nil {
		return nil, err
	}

	writeUploadSummary(out, uc, storedItemID, pub)
	result := &uploadedFile{
		StoredItemID:         storedItemID,
		IngestionJobID:       pub.ingestionJobID,
		ReplacesStoredItemID: pub.replaces,
		TableID:              uc.datasetID,
		Table:                uc.datasetName,
		PeriodKey:            uc.periodKey,
		RoutingPending:       uc.series && uc.periodKey == nil,
		CloudSourceID:        uc.cloudSourceID,
	}
	if uc.cloudSourceName != "" {
		name := uc.cloudSourceName
		result.CloudSource = &name
	}
	return result, nil
}

// explainPeriodLocked appends an "Instead:" line naming the upload's locked
// period and how to target another when err carries
// apierror.CodePeriodLocked, and returns any other error unchanged.
func explainPeriodLocked(err error, uc *uploadContext) error {
	if apierror.ErrorCodeOf(err) != apierror.CodePeriodLocked {
		return err
	}
	if uc.periodKey == nil {
		return fmt.Errorf("%w\n\nInstead: name a table whose period is unlocked with --table. "+
			"The file's period takes no uploads while it is locked", err)
	}
	return fmt.Errorf("%w\n\nInstead: name an unlocked period of the series with --period, or its table with --table. "+
		"Period '%s', the one this upload targets, takes no uploads while it is locked", err, terminal.Sanitize(*uc.periodKey))
}

// writeUploadSummary prints the success block. The ingestion_job_id line and
// the Next line that names it are omitted when pub names no job, and the
// replaces line when pub confirmed no replacement.
func writeUploadSummary(out io.Writer, uc *uploadContext, storedItemID int, pub *published) {
	ingestionJobID := pub.ingestionJobID
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Uploaded file and queued for ingestion.")
	fmt.Fprintf(out, "stored_item_id   : %d\n", storedItemID)
	if ingestionJobID != nil {
		fmt.Fprintf(out, "ingestion_job_id : %d\n", *ingestionJobID)
	}
	if uc.datasetID == 0 {
		fmt.Fprintf(out, "table            : %s\n", tableOfPeriod(uc))
	} else {
		fmt.Fprintf(out, "table            : %s\n", uploadTableName(uc.datasetName))
	}
	if uc.series {
		fmt.Fprintf(out, "period           : %s\n", periodText(uc))
	}
	fmt.Fprintf(out, "cloud_source     : %s\n", displayName(uc.cloudSourceName))
	if pub.replaces != nil {
		fmt.Fprintf(out, "replaces         : %s\n", replacesText(pub))
	}
	if ingestionJobID != nil {
		fmt.Fprintf(out, "Next: qctl describe ingestion-job %d\n", *ingestionJobID)
	}
}

// verifyDeltaUpload confirms that the server preserved the requested metadata.
func verifyDeltaUpload(client *stored_items.Client, token string, storedItemID, deltaCursor int) error {
	item, err := client.GetStoredItem(token, storedItemID)
	if err != nil {
		return fmt.Errorf("failed to verify delta upload metadata: %w", err)
	}
	if item.IsDelta == nil || !*item.IsDelta {
		return fmt.Errorf("server did not preserve --delta for stored_item %d; refusing to upload file", storedItemID)
	}
	if item.DeltaCursor == nil || *item.DeltaCursor != deltaCursor {
		return fmt.Errorf("server did not preserve --delta-cursor %d for stored_item %d; refusing to upload file", deltaCursor, storedItemID)
	}
	return nil
}

// abortBeforeFileUpload attempts to remove the unused stored item and returns the cause.
func abortBeforeFileUpload(client *stored_items.Client, token string, storedItemID int, cause error) error {
	if cleanupErr := client.DeleteZombieStoredItem(token, storedItemID); cleanupErr != nil {
		return fmt.Errorf("%w; failed to clean up stored_item %d: %s", cause, storedItemID, cleanupErr)
	}
	return fmt.Errorf("%w; stored_item %d was cleaned up before any file bytes were uploaded", cause, storedItemID)
}

// handleUploadFailure cleans up the zombie stored item after upload failure.
func handleUploadFailure(client *stored_items.Client, token string, storedItemID int, uploadErr error) error {
	// This direct stderr write bypasses qerrors.Exit's sanitization.
	fmt.Fprintf(os.Stderr, "Upload failed: %s\n", terminal.Sanitize(uploadErr.Error()))
	fmt.Fprintf(os.Stderr, "Cleaning up stored_item %d...\n", storedItemID)

	err := fmt.Errorf("failed to upload file to storage: %w", uploadErr)
	if cleanupErr := client.DeleteZombieStoredItem(token, storedItemID); cleanupErr != nil {
		return fmt.Errorf("%w; failed to clean up stored_item %d: %s\nNext: qctl describe file %d", err, storedItemID, cleanupErr, storedItemID)
	}

	return fmt.Errorf("%w; stored_item %d has been cleaned up", err, storedItemID)
}

// replacesText renders the replacement a publish confirmed.
func replacesText(pub *published) string {
	if pub.replaced == nil {
		return fmt.Sprintf("file/%d", *pub.replaces)
	}
	return fmt.Sprintf("%s in %s", replacedFileText(pub.replaced), terminal.Sanitize(pub.replaced.PeriodLabel))
}
