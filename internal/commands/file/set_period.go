package file

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

const (
	// codeNoOpenRoutingAlert is the 409 for a file no open alert asks a
	// period for.
	codeNoOpenRoutingAlert = "NO_OPEN_ROUTING_ALERT"
	// codeAssignmentConflict is the 409 for a move the file's state or its
	// jobs refuse.
	codeAssignmentConflict = "REPORTING_PERIOD_ASSIGNMENT_CONFLICT"
	// codePeriodHasLaterFile is the 409 for a period a later file of the
	// same cloud source has already loaded into.
	codePeriodHasLaterFile = "PERIOD_HAS_LATER_FILE"
)

// setPeriodResult is the -o json|yaml document: the server's answer plus the
// name of its table, nil when the name could not be read.
type setPeriodResult struct {
	ID               int     `json:"id"`
	DatasetID        int     `json:"dataset_id"`
	DatasetName      *string `json:"dataset_name"`
	RoutingPeriodKey *string `json:"routing_period_key"`
}

func newSetPeriodCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "set-period <file-id> <period-key>",
		Short: "Send a file that waits for its reporting period to one",
		Long: `Send a file of a series cloud source to a reporting period of its table series.

A file waits for its period when ingestion could not route it -- no period
matched, several did, or the one it matched is locked -- and raised a
series_file_routing alert instead; its routing column in 'qctl get files'
reads "pending". This command chooses the period: the server moves the file
to that period's table, resolves the file's open routing alerts and requeues
the ingestion jobs they hold. It is the same choice as resolving the alert
with the assign_reporting_period action.

<file-id> is the id 'qctl get files' lists and 'qctl describe file' takes.
<period-key> is the period's key, e.g. 2026-07 or 2026-Q3.
A missing period within the source's creation window can be created by the
server as part of this request; no table needs to be created separately.

Examples:
  # find the files waiting for their period
  qctl get files --cloud-source-id 456 --columns id,file_name,routing

  # send file 123 to the period 2026-07
  qctl file set-period 123 2026-07

-o json|yaml prints {id, dataset_id, dataset_name, routing_period_key} and
-o name prints file/<id>; the prose moves to stderr.`,
		Annotations: cmdutil.WithArgKinds(
			cmdutil.WithFormats(catalog.For("file"), "table", "json", "yaml", "name"), "file", "-"),
		Args: cobra.ExactArgs(2),
		RunE: runSetPeriod,
	}
}

func runSetPeriod(cmd *cobra.Command, args []string) error {
	ids, err := cmdutil.ParseIntArgs(args[:1], "file ID")
	if err != nil {
		return err
	}
	fileID, periodKey := ids[0], args[1]
	if fileID <= 0 {
		return qerrors.NewBadUsagef("invalid file ID %d: file IDs are positive integers", fileID)
	}
	if err := cmdutil.ValidatePeriodKey("<period-key>", periodKey); err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create stored items client: %w", err)
	}
	assigned, err := client.AssignReportingPeriod(ctx.Credential.AccessToken, fileID, periodKey)
	if err != nil {
		return explainSetPeriodRefusal(
			fmt.Errorf("failed to send file %d to period '%s': %w", fileID, periodKey, err), fileID, periodKey)
	}

	w := cmdutil.NewResultWriter(cmd)
	name := "file/" + strconv.Itoa(assigned.ID)
	result := setPeriodResult{
		ID:               assigned.ID,
		DatasetID:        assigned.DatasetID,
		RoutingPeriodKey: assigned.RoutingPeriodKey,
	}
	if format, _ := cmd.Flags().GetString("output"); format != "name" {
		result.DatasetName = tableName(ctx, assigned.DatasetID)
	}

	period := "(not reported)"
	if assigned.RoutingPeriodKey != nil {
		period = terminal.Sanitize(*assigned.RoutingPeriodKey)
	}
	table := fmt.Sprintf("ID %d (name unavailable)", assigned.DatasetID)
	if result.DatasetName != nil {
		table = fmt.Sprintf("%s (ID %d)", terminal.Sanitize(*result.DatasetName), assigned.DatasetID)
	}
	fmt.Fprintf(w.Human, "%s sent to period %s\n", name, period)
	fmt.Fprintf(w.Human, "table: %s\n", table)
	fmt.Fprintln(w.Human, "Its open routing alerts are resolved and the ingestion jobs they held are requeued.")
	fmt.Fprintf(w.Human, "Next: qctl describe file %d\n", assigned.ID)
	return w.Write(result, name)
}

// tableName reads the name of table id, or returns nil when it cannot be
// read: the caller may lack table-read permission.
func tableName(ctx *cmdutil.CommandContext, id int) *string {
	ds, err := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
		GetDataset(ctx.Credential.AccessToken, id)
	if err != nil || ds == nil || ds.Name == "" {
		return nil
	}
	return &ds.Name
}

// labelInstead heads the advice that names another way to the same result.
const labelInstead = "Instead"

// explainSetPeriodRefusal appends a line of advice for the route's 409 codes
// and for a CHANGE_PROPOSAL_REQUIRED about the alert resolve it runs, keeping
// the %w chain, and returns any other error unchanged.
func explainSetPeriodRefusal(err error, fileID int, periodKey string) error {
	label, hint := "Hint", ""
	switch apierror.ErrorCodeOf(err) {
	case codeNoOpenRoutingAlert:
		hint = fmt.Sprintf("file %d is not waiting for its period: no open series_file_routing alert asks for one. "+
			"Its period may already be chosen, or ingestion has not routed it yet; "+
			"'qctl describe file %d' shows status.routing_pending and status.routing_period_key.", fileID, fileID)
	case apierror.CodePeriodLocked:
		label = labelInstead
		hint = fmt.Sprintf("choose an unlocked period and run 'qctl file set-period %d <period-key>' again. "+
			"Period '%s', the one you named, takes no data while it is locked.", fileID, terminal.Sanitize(periodKey))
	case codePeriodHasLaterFile:
		label = labelInstead
		hint = fmt.Sprintf("choose another period and run 'qctl file set-period %d <period-key>' again. "+
			"A later file of the same cloud source has already loaded into period '%s'.", fileID, terminal.Sanitize(periodKey))
	case codeAssignmentConflict:
		hint = "the file's state or its ingestion jobs do not allow this move; the message above says which. " +
			"A file already loaded into a table cannot move to another period's table."
	case apierror.CodeChangeProposalRequired:
		if refusal, _ := apierror.ChangeReviewRefusalOf(err); refusal.Operation != apierror.OperationAlertResolve {
			return err
		}
		label = labelInstead
		hint = fmt.Sprintf("propose the period for review on the file's series_file_routing alert. "+
			"'qctl get alerts --issue-type series_file_routing --table-id <table-id>' lists the open ones "+
			"('qctl describe file %d' shows the file's dataset_id); "+
			"'qctl alert propose <alert-id> --action assign_reporting_period --dry-run > period.yaml' writes the manifest. "+
			"Set its spec.new_value to '%s', then run 'qctl alert propose -f period.yaml'.", fileID, terminal.Sanitize(periodKey))
	default:
		return err
	}
	return fmt.Errorf("%w\n\n%s: %s", err, label, hint)
}
