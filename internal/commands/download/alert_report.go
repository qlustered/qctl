package download

import (
	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/spf13/cobra"
)

// NewAlertReportCommand creates the 'download alert-report' command.
func NewAlertReportCommand() *cobra.Command {
	opts := &exportOptions{}

	cmd := &cobra.Command{
		Use:   "alert-report [table-id-or-name]",
		Short: "Export a table's unresolved row-level alerts and warnings as an Excel workbook",
		Long: `Export a table's unresolved alerts and warnings, with the quarantine rows
each one holds, as an Excel workbook.

The report runs on the server. By default qctl waits for it to finish and
writes the file; --no-wait queues it and returns.

The workbook has an Overview sheet, a Rules sheet with one line per failing
rule, and an Issues sheet listing the failing rows, fitted by the server to a
fixed size. Only issues carried on quarantine rows are
listed, and only those rows are counted: a warning carried only by clean rows,
or an alert that holds no row, never appears.

The file is named by the server. A server built from atlas b072ae28b on sends
the workbook itself (.xlsx); one built before it sends a ZIP ARCHIVE holding
the workbook (.xlsx.zip), and qctl writes the bytes as it receives them.

--key names the columns that identify each listed row, in order; the row id
is always listed after them. Without --key the server picks: a composite
table's natural key columns, else the table's sync key fields, else none.
--no-key lists the row id alone. The table can also be named with --table
<name> or --table-id <id>.

Examples:
  # the unresolved alerts and warnings on a table's quarantine rows
  qctl download alert-report orders

  # alerts only, each row identified by two columns
  qctl download alert-report orders --include-warnings=false --key region --key order_no

  # only the rows one cloud source loaded
  qctl download alert-report orders --data-source-model-id 7

  # queue the report and return immediately
  qctl download alert-report --table-id 42 --no-wait`,
		Annotations: cmdutil.WithArgKinds(catalog.For("alert"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return opts.runAlertReport(cmd, firstArg(args))
		},
	}

	cmdutil.AddTableFlags(cmd, "Table")
	cmd.Flags().StringArray("key", nil, "Column identifying each listed row; repeat for several, in order (default: the server's choice)")
	cmd.Flags().Bool("no-key", false, "Identify each listed row by its row id alone")
	cmd.Flags().Bool("include-warnings", true, "List warnings as well as alerts")
	cmdutil.RefInt(cmd.Flags(), "data-source-model-id", "cloud-source", "Only the quarantine rows this cloud source (data source model) loaded; required for an external user")
	opts.registerFlags(cmd, "the workbook", "report")
	return cmd
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func (o *exportOptions) runAlertReport(cmd *cobra.Command, tableArg string) error {
	// Every refusal below is exit 2 and none costs a request, so all of them
	// sit above the bootstrap: resolving a table NAME is itself a request.
	if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
		return err
	}
	pollEvery, waitBudget, err := o.validate(cmd)
	if err != nil {
		return err
	}
	body, err := alertReportBody(cmd)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	datasetID, err := cmdutil.ResolveTableID(ctx, cmd, tableArg)
	if err != nil {
		return err
	}
	client := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	return o.queueAndCollect(cmd, ctx, queuedJob{
		label:        "alert report",
		otherRequest: "change the flags to request a different one",
		send: func() (string, error) {
			return client.PutAlertReportForDownload(ctx.Credential.AccessToken, datasetID, body)
		},
	}, notifications.WaitOptions{
		DatasetID: datasetID,
		// No FileNameHint, for the reason `download data` has none: from atlas
		// b072ae28b two different reports of one table on one UTC day share a
		// name. The download_handle the POST returns is what tells them apart;
		// queueAndCollect fills it in.
		Producer: notifications.ProducerAlertReport,
		Interval: pollEvery,
		Timeout:  waitBudget,
	})
}

// alertReportBody builds the request from the flags. include_warnings is
// always set; every other field is left unset (null or omitted) unless its
// flag was given, so the server's defaults stay the server's.
func alertReportBody(cmd *cobra.Command) (api.AlertReportRequestSchema, error) {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"key", "no-key"}); err != nil {
		return api.AlertReportRequestSchema{}, err
	}

	includeWarnings, _ := cmd.Flags().GetBool("include-warnings")
	body := api.AlertReportRequestSchema{IncludeWarnings: &includeWarnings}

	if cmd.Flags().Changed("key") {
		keys, _ := cmd.Flags().GetStringArray("key")
		// len == 0 is `--key ""` alone: pflag's StringArray reads a lone
		// empty value back as no values, which would reach the wire as
		// "key": [] -- --no-key's meaning, silently.
		if len(keys) == 0 {
			return api.AlertReportRequestSchema{}, emptyKeyErr()
		}
		for _, k := range keys {
			if k == "" {
				return api.AlertReportRequestSchema{}, emptyKeyErr()
			}
		}
		body.Key = &keys
	}
	if noKey, _ := cmd.Flags().GetBool("no-key"); noKey {
		body.Key = &[]string{}
	}

	sourceID, haveSource, err := cmdutil.PositiveIDFlag(cmd, "data-source-model-id")
	if err != nil {
		return api.AlertReportRequestSchema{}, err
	}
	if haveSource {
		body.DataSourceModelID = &sourceID
	}
	return body, nil
}

func emptyKeyErr() error {
	return qerrors.NewBadUsage("--key cannot be empty: name a column, or pass --no-key to list the row id alone")
}
