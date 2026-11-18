package get

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/notifications"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

// notificationColumns omits `dismissed`, which is "no" on every row of the
// default listing (dismissed notifications are hidden unless asked for). It is
// appended when --include-dismissed makes it mean something, and is always
// reachable through --columns.
const notificationColumns = "id,type,msg,actor,table,read,age"

// notificationRow is the table-display projection of a notification.
type notificationRow struct {
	ID                   string                       `json:"id"`
	Type                 string                       `json:"type"`
	Msg                  string                       `json:"msg"`
	Actor                string                       `json:"actor"`
	Table                string                       `json:"table"`
	Read                 string                       `json:"read"`
	Dismissed            string                       `json:"dismissed"`
	Count                int                          `json:"count"`
	Age                  string                       `json:"age"`
	JobID                *int                         `json:"job_id"`
	JobType              *api.LogicalJobType          `json:"job_type"`
	QuarantinedRowsCount *int                         `json:"quarantined_rows_count"`
	RowID                *int                         `json:"row_id"`
	FieldName            *string                      `json:"field_name"`
	ChangeProposalID     *uuid.UUID                   `json:"change_proposal_id"`
	AlertItemID          *int                         `json:"alert_item_id"`
	WarningItemID        *int                         `json:"warning_item_id"`
	StoredItemID         *int                         `json:"stored_item_id"`
	Trigger              *api.SeriesEscalationTrigger `json:"trigger"`
	PeriodKey            *string                      `json:"period_key"`
	CloudSourceID        *int                         `json:"cloud_source_id"`
	SeriesID             *uuid.UUID                   `json:"series_id"`
	ThreadID             *uuid.UUID                   `json:"thread_id"`
	CommentID            *uuid.UUID                   `json:"comment_id"`
	DuplicateGroupID     *uuid.UUID                   `json:"duplicate_group_id"`
	SupportStep          *string                      `json:"support_step"`
	BrokerReportRefusal  *api.BrokerReportRefusal     `json:"broker_report_refusal"`
}

// NewNotificationsCommand creates the get notifications command.
func NewNotificationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notifications",
		Short: "List your notification inbox",
		Long: `List your notification inbox for the current organization.

Review requests, review decisions, @mentions on comments, rule upgrades and
severe rule bugs are all delivered here, and several of them have no other
read path. The inbox is per-user and per-org: it is yours, in the org the
current context points at. A rule_upgrade row is a pointer --
'qctl get rule-upgrades <rule>' says which tables it affects.

Read and dismissed are two INDEPENDENT states. --unread narrows the read axis;
--include-dismissed widens the dismissed axis; they compose. Marking something
read never dismisses it and dismissing never marks it read.

--count answers the badge instead of the list: needs_you (what is actually
asking you for something -- mentions, review/data-review requests and
assignments, submitted change proposals, and a file ready to download,
which an older server leaves out)
and total_unread (everything unread). Both are counted over
notifications that are unread AND undismissed. It takes no filters.

Available columns: id, type, msg, actor, table, read, dismissed, count, age,
job_id, job_type, quarantined_rows_count, row_id, field_name,
change_proposal_id, alert_item_id, warning_item_id, stored_item_id, trigger,
period_key, cloud_source_id, series_id, thread_id, comment_id, support_step,
broker_report_refusal. trigger, period_key, cloud_source_id and series_id come
from a series_escalation; broker_report_refusal says why a file_download_failed
row's issues report was refused, and is blank when it failed instead.
Use --columns to pick; -o json and -o yaml both emit the raw wire object, the
only views carrying review_id, dataset_rule_id, read_at and dismissed_at,
which is what you act on.

The table view ends with one stderr line per distinct next step: the change
proposal a row names, and for series_escalation the period's submission
statuses and the table, for problem_on_our_side the ingestion job,
for comment_reply the comment thread, and for support_request and
file_needs_decision the alert and the file.

Clear the inbox with:
  qctl notification read <id>       # or --all
  qctl notification dismiss <id>    # or --all

Examples:
  qctl get notifications
  qctl get notifications --unread
  qctl get notifications --unread --type review_requested --type comment_mentioned
  qctl get notifications --include-dismissed
  qctl get notifications --unread -o json
  qctl get notifications --count`,
		Annotations: catalog.For("notification"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Decided locally, so both run before Bootstrap: a bad flag value
			// or a bad combination must cost no credential read and no request,
			// the same rule `comment-thread resolve` applies to a malformed id.
			count, _ := cmd.Flags().GetBool("count")
			if count {
				if err := rejectCountFilters(cmd); err != nil {
					return err
				}
				ctx, err := cmdutil.Bootstrap(cmd)
				if err != nil {
					return err
				}
				return runNotificationCount(cmd, ctx)
			}
			opts, err := notificationListOptions(cmd)
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			return runNotifications(cmd, ctx, opts)
		},
	}

	cmd.Flags().Bool("unread", false, "Only notifications you have not read yet")
	cmd.Flags().Bool("include-dismissed", false, "Also list dismissed notifications (default: active only)")
	cmd.Flags().StringArray("type", nil,
		"Filter by notification type (repeatable). Values: "+strings.Join(notifications.TypeNames(), ", "))
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, "Notifications per page (max 100)")
	cmd.Flags().Bool("count", false, "Print the unread counts (needs_you, total_unread) instead of the list")
	cmdutil.DeclareColumns(cmd, []notificationRow(nil), []unreadCountRow(nil))
	return cmd
}

func notificationListOptions(cmd *cobra.Command) (notifications.ListOptions, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return notifications.ListOptions{}, err
	}
	raw, _ := cmd.Flags().GetStringArray("type")
	types, err := notifications.ParseTypes(raw)
	if err != nil {
		return notifications.ListOptions{}, err
	}
	unread, _ := cmd.Flags().GetBool("unread")
	includeDismissed, _ := cmd.Flags().GetBool("include-dismissed")
	return notifications.ListOptions{
		Types:            types,
		UnreadOnly:       unread,
		IncludeDismissed: includeDismissed,
		Page:             page,
		Limit:            limit,
	}, nil
}

func runNotifications(cmd *cobra.Command, ctx *cmdutil.CommandContext, opts notifications.ListOptions) error {
	client := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	list, err := client.List(ctx.Credential.AccessToken, opts)
	if err != nil {
		return fmt.Errorf("failed to list notifications: %w", err)
	}

	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, list)
		}
	}

	if len(list.Results) == 0 {
		// get_notifications counts before slicing to the page (atlas
		// capi/capi/views/notification_view.py, controller
		// get_user_notifications_async: a plain count(*) over base_filters,
		// then the paginated select), so TotalRows is a real cross-page total.
		skip, err := printEmptyResult(cmd, ctx, "notifications", []notificationRow{}, list.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)

	columns := notificationColumns
	if opts.IncludeDismissed {
		columns += ",dismissed"
	}

	rows := make([]notificationRow, 0, len(list.Results))
	for _, n := range list.Results {
		row := notificationRow{
			ID:                   n.ID.String(),
			Type:                 string(n.NotificationType),
			Msg:                  derefStr(n.Msg),
			Actor:                actorLabel(n.Actor),
			Table:                tableLabel(n.DatasetID, n.DatasetName),
			Read:                 boolYesNo(n.ReadAt != nil),
			Dismissed:            boolYesNo(n.DismissedAt != nil),
			Count:                n.NotificationCount,
			Age:                  timeutil.FormatRelative(n.LastNotifiedAt),
			JobID:                n.JobID,
			JobType:              n.JobType,
			QuarantinedRowsCount: n.QuarantinedRowsCount,
			RowID:                n.RowID,
			FieldName:            n.FieldName,
			ChangeProposalID:     n.ChangeProposalID,
			AlertItemID:          n.AlertItemID,
			WarningItemID:        n.WarningItemID,
			StoredItemID:         n.StoredItemID,
			ThreadID:             n.ThreadID,
			CommentID:            n.CommentID,
			DuplicateGroupID:     n.DuplicateGroupID,
			SupportStep:          n.SupportStep,
			BrokerReportRefusal:  n.BrokerReportRefusal,
		}
		if e := n.SeriesEscalation; e != nil {
			row.Trigger = &e.Trigger
			row.PeriodKey = &e.PeriodKey
			row.CloudSourceID = &e.DataSourceModelID
			row.SeriesID = e.SeriesID
		}
		rows = append(rows, row)
	}
	// msg is backend Markdown: a rule-repair failure embeds an ingestion
	// job's alert message, value markers and all (atlas
	// _notify_candidate_author,
	// cettings/cettings/controllers/rule_revalidation.py).
	if err := tableui.PrintMessagesFromCmd(cmd, rows, columns, []string{"msg"}); err != nil {
		return err
	}
	printNotificationHints(cmd, list.Results)
	// The server computes the next page from its own total, so this is its
	// answer rather than arithmetic on the page we happen to hold.
	if list.Next != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"More notifications — fetch the next page with --page %d.\n", list.Next.Page)
	}
	return nil
}

// printNotificationHints prints, on stderr and for table output only, each
// distinct next-step command the listed notifications point at.
func printNotificationHints(cmd *cobra.Command, items []notifications.Notification) {
	format, _ := cmd.Flags().GetString("output")
	if format != "" && format != "table" { //nolint:goconst // Keep output format handling local to this command.
		return
	}
	seen := make(map[string]bool)
	for _, n := range items {
		for _, hint := range notificationHints(n) {
			if seen[hint] {
				continue
			}
			seen[hint] = true
			fmt.Fprintln(cmd.ErrOrStderr(), hint)
		}
	}
}

// notificationHints returns the next-step lines for one notification.
func notificationHints(n notifications.Notification) []string {
	var hints []string
	if n.ChangeProposalID != nil {
		hints = append(hints, "Inspect change proposal: qctl describe change-proposal "+n.ChangeProposalID.String())
	}
	switch n.NotificationType {
	case api.SeriesEscalation:
		if hint := seriesEscalationHint(n.SeriesEscalation); hint != "" {
			hints = append(hints, hint)
		}
		if n.DatasetID != nil {
			hints = append(hints, fmt.Sprintf("Inspect table: qctl describe table %d", *n.DatasetID))
		}
	case api.ProblemOnOurSide:
		if n.JobID != nil && (n.JobType == nil || *n.JobType == api.IngestionJob) {
			hints = append(hints, fmt.Sprintf("Inspect ingestion job: qctl describe ingestion-job %d", *n.JobID))
		}
	case api.DuplicateGroupRowsChanged, api.DuplicateGroupResponse:
		hints = append(hints, tableScopedHint("duplicate group", "duplicate-group", n.DuplicateGroupID, n.DatasetID)...)
	case api.DuplicateGroupDecisionChanged:
		if n.DatasetID != nil {
			hints = append(hints, fmt.Sprintf("Inspect duplicate groups: qctl get duplicate-groups --sender --table-id %d", *n.DatasetID))
		}
	case api.CommentReply, api.DuplicateThreadShared:
		hints = append(hints, tableScopedHint("comment thread", "comment-thread", n.ThreadID, n.DatasetID)...)
	case api.SupportRequest, api.FileNeedsDecision:
		hints = append(hints, alertAndFileHints(n)...)
	}
	return hints
}

// tableScopedHint returns the describe line for a resource read through a
// table-scoped route, which needs the table as well as the id; nil when
// either is missing.
func tableScopedHint(label, resource string, id *uuid.UUID, tableID *int) []string {
	if id == nil || tableID == nil {
		return nil
	}
	return []string{fmt.Sprintf("Inspect %s: qctl describe %s %s --table-id %d", label, resource, id.String(), *tableID)}
}

// alertAndFileHints returns the describe lines for n's alert and file.
func alertAndFileHints(n notifications.Notification) []string {
	var hints []string
	if n.AlertItemID != nil {
		hints = append(hints, fmt.Sprintf("Inspect alert: qctl describe alert %d", *n.AlertItemID))
	}
	if n.StoredItemID != nil {
		hints = append(hints, fmt.Sprintf("Inspect file: qctl describe file %d", *n.StoredItemID))
	}
	return hints
}

// seriesEscalationHint returns the submission-statuses line for e: the
// series board for e's period when e names its series, else the intake's
// board; "" when e names neither.
func seriesEscalationHint(e *api.SeriesEscalationTinySchema) string {
	const label = "Inspect submission statuses: qctl get submission-statuses "
	switch {
	case e == nil:
		return ""
	case e.SeriesID != nil && e.PeriodKey != "":
		return fmt.Sprintf("%s--series %s --period %s", label, e.SeriesID.String(), e.PeriodKey)
	case e.SeriesID != nil:
		return label + "--series " + e.SeriesID.String()
	case e.DataSourceModelID > 0:
		return fmt.Sprintf("%s--cloud-source-id %d", label, e.DataSourceModelID)
	}
	return ""
}

// actorLabel renders the ACTOR cell: "First Last", the email when both names
// are blank, or "-" for a notification no human caused.
//
// The field is an ALIAS the view adds beside last_blast_by, not a rename:
// both carry the same user resolved from last_blast_by_id, and all three are
// on the wire. The comment, rule-review and review-shared producers stamp it;
// rule-upgrade generation omits the column and every validation-failure caller
// omits actor_id, so "-" is a real and common answer, not a missing lookup.
func actorLabel(actor *notifications.Actor) string {
	if actor == nil {
		return "-"
	}
	if name := strings.TrimSpace(actor.FirstName + " " + actor.LastName); name != "" {
		return name
	}
	if actor.Email != "" {
		return actor.Email
	}
	return "-"
}

const unreadCountColumns = "needs_you,total_unread"

// unreadCountRow is the --count table projection. Two numbers, one row.
type unreadCountRow struct {
	NeedsYou    int `json:"needs_you"`
	TotalUnread int `json:"total_unread"`
}

// rejectCountFilters refuses the two flag combinations --count cannot honour:
// any listing filter, and -o name.
//
// The unread-count endpoint has no filter parameters at all, so accepting
// `--count --type rule_upgrade` would silently answer the whole-inbox question
// while looking like it answered a narrower one. `get rule-reviews` states the
// same rule for its mode-specific flags, for the same reason. -o name goes the
// same way: --count answers with two numbers, there is no id to print, and an
// empty line at exit 0 would read as "nothing unread". The command's declared
// format set is the default four, so root cannot reject that one for us.
//
// Both checks live here, ahead of Bootstrap, because a bad combination must
// cost no credential read and no request.
func rejectCountFilters(cmd *cobra.Command) error {
	for _, listOnly := range []string{"type", "unread", "include-dismissed", "page", "limit"} {
		if cmd.Flags().Changed(listOnly) {
			return qerrors.NewBadUsagef(
				"--%s does not apply with --count (the unread-count endpoint takes no filters)", listOnly)
		}
	}
	if outputFormat, _ := cmd.Flags().GetString("output"); outputFormat == "name" { //nolint:goconst // Keep output format handling local to this command.
		return qerrors.NewBadUsage(
			"--count has no name view: it answers with two numbers, not a list of ids.\n" +
				"Use -o json for the numbers, or drop --count to list the notifications.")
	}
	return nil
}

func runNotificationCount(cmd *cobra.Command, ctx *cmdutil.CommandContext) error {
	client := notifications.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	count, err := client.UnreadCount(ctx.Credential.AccessToken)
	if err != nil {
		return fmt.Errorf("failed to get the unread notification count: %w", err)
	}

	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case "json", "yaml":
			// The raw wire object, not the row slice: a caller reading
			// .needs_you should not have to unwrap a one-element list.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, count)
		}
	}

	return tableui.PrintFromCmd(cmd, []unreadCountRow{{
		NeedsYou:    count.NeedsYou,
		TotalUnread: count.TotalUnread,
	}}, unreadCountColumns)
}
