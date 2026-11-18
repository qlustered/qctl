package get

import (
	"context"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/queue"
	"github.com/spf13/cobra"
)

// validQueueMessageSortFields mirrors api.QueueOrderBy exactly, for the reason
// validIngestionJobSortFields does: the value is cast straight to that enum and
// sent as order_by, so the server is the only thing that enforces it. Spelled
// with the generated constants so a regenerated client that renames or drops a
// member breaks the build rather than drifting.
//
// data_source_model_name is in the list although no QueueSchema field carries
// it: the backend accepts it as an order_by_on_the_fly column that sorts by
// data_source_model_id. Sorting by a column you cannot see is odd but it is
// what the API offers, and omitting it here would reject a value the server
// takes.
var validQueueMessageSortFields = []string{
	string(api.QueueOrderByAcknowledged),
	string(api.QueueOrderByDataSourceModelID),
	string(api.QueueOrderByDataSourceModelName),
	string(api.QueueOrderByDequeuedAt),
	string(api.QueueOrderByEnqueuedAt),
	string(api.QueueOrderByExpectedAt),
	string(api.QueueOrderByID),
	string(api.QueueOrderByKey),
	string(api.QueueOrderByMaxReceiveCount),
	string(api.QueueOrderByPutCount),
	string(api.QueueOrderByQName),
	string(api.QueueOrderByReceiveCount),
	string(api.QueueOrderByRescheduleCount),
	string(api.QueueOrderByScheduleAt),
}

const defaultQueueMessageColumns = "id,q_name,key,dataset_id,is_dead,receive_count,enqueued_at"

// NewQueueMessagesCommand creates the get queue-messages command.
func NewQueueMessagesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "queue-messages",
		Aliases: []string{"queue-message"},
		Short:   "List messages on the platform's internal job queue",
		Long: `List messages on the platform's internal job queue.

This is an operations view: the routes behind it require the ops role, and an
admin or editor gets a 403. It shows the messages underneath the jobs -- what
has been enqueued, how many delivery attempts it has survived, and which ones
have used up their retries.

A message is DEAD once it is unacknowledged and has reached the queue's maximum
receive count. --dead selects exactly those; --dead=false selects the rest.

A message tied to your organization by a table, profiling job, composite spec
group or deliverable run rather than a cloud source has no data_source_model_id.
A deliverable render has no dataset_id, and neither does a group-scoped
composite run once a worker or a recovery has taken it up, so --table cannot
find those; select them with --queue.

Repair verbs live under 'qctl queue-message'.`,
		Annotations: catalog.For("queue-message"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
				return err
			}
			// An interval below 1 used to reach time.NewTicker and panic.
			watchInterval, err := cmdutil.PositiveSecondsFlag(cmd, "watch-interval")
			if err != nil {
				return err
			}
			watch, watching, err := lwWatchFlags(cmd, watchInterval)
			if err != nil {
				return err
			}

			params, err := parseQueueMessagesParams(cmd)
			if err != nil {
				return err
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			datasetID, haveTable, err := cmdutil.OptionalTableID(ctx, cmd, "")
			if err != nil {
				return err
			}
			if haveTable {
				params.DatasetID = &datasetID
			}

			client, err := queue.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return err
			}
			token := ctx.Credential.AccessToken

			if watching {
				return watchQueueMessages(client, token, params, cmd, watch)
			}
			return fetchAndPrintQueueMessages(client, token, params, cmd, ctx)
		},
	}

	addQueueMessagesFlags(cmd)
	return cmd
}

// parseQueueMessagesParams extracts and validates parameters from command
// flags, except the --table/--table-id filter.
func parseQueueMessagesParams(cmd *cobra.Command) (queue.GetMessagesParams, error) {
	var params queue.GetMessagesParams

	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy
	params.Reverse, _ = cmd.Flags().GetBool("reverse")

	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	if sortBy != "" {
		if err := validateSortField(sortBy, validQueueMessageSortFields); err != nil {
			return params, err
		}
	}

	if params.SearchQuery, err = parseSearchFlag(cmd); err != nil {
		return params, err
	}

	if err := parseQueueMessageFilters(cmd, &params); err != nil {
		return params, err
	}
	return params, nil
}

// parseQueueMessageFilters handles the filter flags other than the table
// selector. An ID flag the user passed is applied or refused — never dropped,
// which would widen the listing to the whole organization without saying so.
func parseQueueMessageFilters(cmd *cobra.Command, params *queue.GetMessagesParams) error {
	// PositiveIDFlag: a negative value names no cloud source, and every row it
	// matches comes back with data_source_model_id null; --table and --queue
	// scope those messages.
	dataSourceID, haveDataSourceID, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	if haveDataSourceID {
		params.DataSourceModelID = &dataSourceID
	}

	// Changed(), not the value: --dead and --acknowledged are tri-state on the
	// API, and an unset flag must leave the filter off rather than send false.
	if cmd.Flags().Changed("dead") {
		isDead, _ := cmd.Flags().GetBool("dead")
		params.IsDead = &isDead
	}
	if cmd.Flags().Changed("acknowledged") {
		acknowledged, _ := cmd.Flags().GetBool("acknowledged")
		params.Acknowledged = &acknowledged
	}
	if qName, _ := cmd.Flags().GetString("queue"); qName != "" {
		params.QName = &qName
	}
	return nil
}

// watchQueueMessages polls the queue-messages page, writing each change as
// the List the one-shot listing prints.
func watchQueueMessages(
	client *queue.Client,
	accessToken string,
	params queue.GetMessagesParams,
	cmd *cobra.Command,
	watch lwWatch,
) error {
	setDefaultColumns(cmd, defaultQueueMessageColumns)

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	return watchList(cmd, printer, "queue messages", watch, func(waitCtx context.Context) (any, error) {
		resp, err := client.GetMessagesContext(waitCtx, accessToken, params)
		if err != nil {
			return nil, fmt.Errorf("failed to get queue messages: %w", err)
		}
		return queueMessagesPage(params, resp).list(resp.Results), nil
	})
}

// queueMessagesPage is where resp sits among the queue messages params asks for.
func queueMessagesPage(params queue.GetMessagesParams, resp *queue.MessagesPage) listPage {
	return listPage{noun: "queue messages", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
}

// fetchAndPrintQueueMessages fetches and prints queue messages.
func fetchAndPrintQueueMessages(
	client *queue.Client,
	accessToken string,
	params queue.GetMessagesParams,
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
) error {
	setDefaultColumns(cmd, defaultQueueMessageColumns)

	resp, err := client.GetMessages(accessToken, params)
	if err != nil {
		return fmt.Errorf("failed to get queue messages: %w", err)
	}

	if len(resp.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "queue messages", []queue.Message{}, resp.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	printer, err := output.NewPrinterFromCmdWithMarkdown(cmd, []string{"msg"})
	if err != nil {
		return fmt.Errorf("failed to create output printer: %w", err)
	}
	return printListPage(cmd, resp.Results, queueMessagesPage(params, resp), func(data any) error {
		if err := printer.Print(data); err != nil {
			return fmt.Errorf("failed to print output: %w", err)
		}
		return nil
	})
}

// addQueueMessagesFlags adds all flags for the queue-messages command.
func addQueueMessagesFlags(cmd *cobra.Command) {
	cmd.Flags().Int("limit", 20, "Maximum number of queue messages to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Built from the allowlist so --help can never advertise a field the
	// validator (and therefore the API) rejects.
	cmd.Flags().String("order-by", "id",
		"Order by field ("+strings.Join(validQueueMessageSortFields, ", ")+")")
	// The endpoint defaults reverse=true, so this flag un-reverses. Said in the
	// help text rather than silently inverted, because "--reverse gives me
	// ascending ids" reads as a bug otherwise.
	cmd.Flags().Bool("reverse", false,
		"Reverse the sort order (the server sorts descending by default, so this gives ascending)")

	cmd.Flags().Bool("dead", false,
		"Only messages that are out of retries (--dead=false for only the live ones)")
	cmd.Flags().Bool("acknowledged", false,
		"Only acknowledged messages (--acknowledged=false for only the unacknowledged ones)")
	cmd.Flags().String("queue", "", "Filter by queue name, matched exactly (e.g. CUEUE_INGESTION)")
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Filter by cloud source model ID")
	cmd.Flags().String("search", "",
		"Search queue messages by key, queue name or cloud source name (at least 3 characters)")

	cmd.Flags().Bool("watch", false, "Watch for changes (polls every --watch-interval seconds)")
	cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")
	cmdutil.AddWatchTimeoutFlag(cmd, 0)

	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []queue.Message(nil)), ", "))
}
