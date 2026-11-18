package get

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/change_proposals"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comments"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const changeProposalColumns = "id,state,operation,target,table,created_by,created,allowed"

// changeProposalRow is the table-display projection of one proposal.
type changeProposalRow struct {
	ID        string `json:"id"`
	State     string `json:"state"`
	Operation string `json:"operation"`
	Target    string `json:"target"`
	Table     string `json:"table"`
	CreatedBy string `json:"created_by"`
	Created   string `json:"created"`
	Allowed   string `json:"allowed"`
	Updated   string `json:"updated"`
	Version   int    `json:"version"`
	DependsOn string `json:"depends_on"`
}

// changeProposalQuery is what the flags settle before any request.
type changeProposalQuery struct {
	params change_proposals.ListParams
	page   int
}

// NewChangeProposalsCommand creates the get change-proposals command.
func NewChangeProposalsCommand() *cobra.Command {
	live := strings.Join(wireNames(change_proposals.LiveStates()), ", ")
	cmd := &cobra.Command{
		Use:   "change-proposals",
		Short: "List change proposals (proposed alert and warning resolutions)",
		Long: `List change proposals on the tables you can see, newest first.

By default only the live states are listed:
` + live + `.
Pass --state (repeatable) to choose the states yourself; it replaces the
default.

--reviewable keeps the pending_review proposals you may approve or reject, so
it cannot be combined with a --state that leaves out pending_review.
--mine keeps the proposals you wrote or edited.

Available columns: id, state, operation, target, table, created_by, created,
allowed, updated, version, depends_on (the proposal that must be executed or
obsolete before this one can be approved). -o json and -o yaml emit the raw
wire response;
-o name prints change-proposal/<id> for each proposal.

Examples:
  qctl get change-proposals
  qctl get change-proposals --reviewable
  qctl get change-proposals --table orders --state executed --state execution_failed
  qctl get change-proposals --mine -o json`,
		Annotations: catalog.For("change-proposal"),
		Args:        cobra.NoArgs,
		RunE:        runChangeProposals,
	}
	cmd.Flags().StringArray("state", nil, fmt.Sprintf(
		"Only proposals in this state: %s (repeatable; default: %s)",
		strings.Join(change_proposals.StateNames(), ", "), live))
	cmdutil.AddTableFlags(cmd, "Filter by table")
	cmd.Flags().String("operation", "",
		"Only proposals carrying this operation: "+strings.Join(change_proposals.OperationNames(), ", "))
	cmd.Flags().Bool("reviewable", false, "Only pending_review proposals you may approve or reject")
	cmd.Flags().Bool("mine", false, "Only proposals you wrote or edited")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", change_proposals.DefaultLimit,
		fmt.Sprintf("Proposals per page (max %d)", change_proposals.MaxLimit))
	cmdutil.DeclareColumns(cmd, []changeProposalRow(nil))
	return cmd
}

func runChangeProposals(cmd *cobra.Command, _ []string) error {
	// Everything decidable locally runs before Bootstrap, so a bad value costs
	// no credential read and no request.
	q, err := changeProposalListQuery(cmd)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableName := ""
	if id, ok, err := cmdutil.OptionalTableID(ctx, cmd, ""); err != nil {
		return err
	} else if ok {
		q.params.DatasetID = &id
		tableName, _ = cmd.Flags().GetString("table")
	}

	client := change_proposals.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	list, err := client.List(cmd.Context(), ctx.Credential.AccessToken, q.params)
	if err != nil {
		return fmt.Errorf("failed to list change proposals: %w", err)
	}
	return printChangeProposals(cmd, ctx, client, list, q, tableName)
}

// printChangeProposals prints one listed page. Only the table names people,
// so only the table sweeps the users directory.
func printChangeProposals(
	cmd *cobra.Command,
	ctx *cmdutil.CommandContext,
	client *change_proposals.Client,
	list *change_proposals.ListPage,
	q changeProposalQuery,
	tableName string,
) error {
	if cmd.Flags().Changed("output") {
		switch outputFormat, _ := cmd.Flags().GetString("output"); outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, list)
		}
	}

	if len(list.Items) == 0 {
		skip, err := printEmptyResultWithHint(cmd, ctx, "change proposals",
			[]changeProposalRow{}, nil, func() string { return changeProposalEmptyHint(cmd, q.page) })
		if err != nil || skip {
			return err
		}
	}

	var directory *comments.Directory
	if isTableFormat(cmd) && len(list.Items) > 0 {
		directory = cmdutil.FetchDisplayDirectory(cmd, client.FetchUsers, ctx.Credential.AccessToken)
	}
	printContextBanner(cmd, ctx)

	rows := changeProposalRows(list.Items, directory, tableName)
	if err := tableui.PrintFromCmd(cmd, rows, changeProposalColumns); err != nil {
		return err
	}
	if len(list.Items) >= q.params.Limit {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Showing %d change proposals, a full page; more may exist: use --page %d.\n",
			len(list.Items), q.page+1)
	}
	return nil
}

// changeProposalListQuery reads and validates every flag but the table
// selector.
func changeProposalListQuery(cmd *cobra.Command) (changeProposalQuery, error) {
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return changeProposalQuery{}, err
	}
	if limit > change_proposals.MaxLimit {
		return changeProposalQuery{}, qerrors.NewBadUsagef("--limit must be at most %d", change_proposals.MaxLimit)
	}
	if page-1 > math.MaxInt/limit {
		return changeProposalQuery{}, qerrors.NewBadUsagef("--page %d is out of range", page)
	}
	reviewable, _ := cmd.Flags().GetBool("reviewable")
	mine, _ := cmd.Flags().GetBool("mine")
	params := change_proposals.ListParams{
		ReviewableByMe: reviewable,
		AuthoredByMe:   mine,
		Limit:          limit,
		Offset:         (page - 1) * limit,
	}

	switch {
	case cmd.Flags().Changed("state"):
		raw, _ := cmd.Flags().GetStringArray("state")
		if params.States, err = change_proposals.ParseStates(raw); err != nil {
			return changeProposalQuery{}, err
		}
		if reviewable && !slices.Contains(params.States, api.ChangeProposalStatePendingReview) {
			return changeProposalQuery{}, qerrors.NewBadUsage(
				"--reviewable lists only pending_review proposals; add --state pending_review or drop --state")
		}
	case !reviewable:
		// --reviewable pins pending_review server-side; the default is moot.
		params.States = change_proposals.LiveStates()
	}

	if cmd.Flags().Changed("operation") {
		raw, _ := cmd.Flags().GetString("operation")
		op, err := change_proposals.ParseOperation(raw)
		if err != nil {
			return changeProposalQuery{}, err
		}
		params.Operation = &op
	}
	return changeProposalQuery{params: params, page: page}, nil
}

// changeProposalEmptyHint qualifies the empty-result sentence, which names
// only the flags the user set.
func changeProposalEmptyHint(cmd *cobra.Command, page int) string {
	var hint strings.Builder
	if page > 1 {
		fmt.Fprintf(&hint, "Page %d is empty; earlier pages may hold proposals.\n", page)
	}
	reviewable, _ := cmd.Flags().GetBool("reviewable")
	if !cmd.Flags().Changed("state") && !reviewable {
		fmt.Fprintf(&hint, "Only live states (%s) are listed by default; pass --state to see others.\n",
			strings.Join(wireNames(change_proposals.LiveStates()), ", "))
	}
	return hint.String()
}

// changeProposalRows projects the listing. A nil directory names people by
// UUID. tableName labels rows on the table --table named.
func changeProposalRows(items []change_proposals.ListItem, directory *comments.Directory, tableName string) []changeProposalRow {
	if directory == nil {
		directory = new(comments.Directory)
	}
	rows := make([]changeProposalRow, 0, len(items))
	for i := range items {
		p := &items[i]
		var name *string
		if tableName != "" {
			name = &tableName
		}
		rows = append(rows, changeProposalRow{
			ID:        p.ID.String(),
			State:     string(p.State),
			Operation: string(p.Operation),
			Target:    targetLabel(p),
			Table:     tableLabel(p.DatasetID, name),
			CreatedBy: directory.DisplayName(p.CreatedByID),
			Created:   timeutil.FormatRelative(p.CreatedAt),
			Allowed:   allowedLabel(p.AllowedActions),
			Updated:   timeutil.FormatRelative(p.UpdatedAt),
			Version:   p.VersionID,
			DependsOn: dependsOnLabel(p),
		})
	}
	return rows
}

// targetLabel renders the TARGET cell: alert/<id>, warning/<id>, rule/<uuid>,
// or "-".
func targetLabel(p *change_proposals.ListItem) string {
	if t := change_proposals.ListItemTarget(p); t != "" {
		return t
	}
	return "-"
}

// dependsOnLabel renders the DEPENDS_ON cell: the proposal's dependency, or
// "-".
func dependsOnLabel(p *change_proposals.ListItem) string {
	if p.DependsOnID != nil {
		return p.DependsOnID.String()
	}
	return "-"
}

// allowedLabel renders the ALLOWED cell: the actions comma-joined, or "-".
func allowedLabel(actions []change_proposals.Action) string {
	if len(actions) == 0 {
		return "-"
	}
	return strings.Join(wireNames(actions), ",")
}

func wireNames[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
