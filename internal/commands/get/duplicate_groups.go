package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

const (
	duplicateGroupColumns       = "id,origin,state,held,copies,key,file,decision,pending,responses,can_decide,age"
	senderDuplicateGroupColumns = "id,origin,state,held,copies,key,file,decision,responses,can_respond,age"
)

// The list route's filter values.
var (
	duplicateGroupOrigins = []string{"repeated_key", "identical_rows", "person_marked"}
	duplicateGroupStates  = []string{
		"needs_attention", "awaiting_approval", "needs_decision", "exceeds_allowed", "kept_content_not_found",
		"flagged", "decided", "no_decision_needed", "ended",
	}
	duplicateGroupResponses = []string{"separate_rows", "corrected_file_follows", "none"}

	// reviewerOnlyFlags are the reviewer view's filters, which the source
	// route does not take.
	reviewerOnlyFlags = []string{"origin", "state", "sender-response", "row-id"}
)

// duplicateGroupRow is one group as the table view shows it.
type duplicateGroupRow struct {
	ID        string `json:"id"`
	Origin    string `json:"origin"`
	State     string `json:"state"`
	Held      int    `json:"held"`
	Copies    int    `json:"copies"`
	Key       string `json:"key"`
	File      string `json:"file"`
	Decision  string `json:"decision"`
	Pending   string `json:"pending"`
	Responses string `json:"responses"`
	CanDecide string `json:"can_decide"`
	Age       string `json:"age"`
}

// senderDuplicateGroupRow is one group as the sender view's table shows it.
type senderDuplicateGroupRow struct {
	ID         string `json:"id"`
	Origin     string `json:"origin"`
	State      string `json:"state"`
	Held       int    `json:"held"`
	Copies     int    `json:"copies"`
	Key        string `json:"key"`
	File       string `json:"file"`
	Decision   string `json:"decision"`
	Responses  string `json:"responses"`
	CanRespond string `json:"can_respond"`
	Age        string `json:"age"`
}

// NewDuplicateGroupsCommand creates the get duplicate-groups command.
func NewDuplicateGroupsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "duplicate-groups",
		Short: "List a mirror or append table's duplicate groups",
		Long: `List the duplicate groups of a mirror or append table, groups needing
attention first, then newest first: rows whose key repeats (repeated_key),
identical rows on a keyless mirror table (identical_rows), and rows a person
marked as duplicates (person_marked).

  qctl get duplicate-groups --table orders
  qctl get duplicate-groups --table orders --state needs_attention
  qctl get duplicate-groups --table-id 42 --file-id 311 --origin repeated_key
  qctl get duplicate-groups --table orders --row-id 812
  qctl get duplicate-groups --table orders --cloud-source-id 7

--state takes a group state or needs_attention (needs_decision,
exceeds_allowed, kept_content_not_found, flagged) or awaiting_approval (the
group's own decision is pending). Ended groups are listed only with
--include-ended, which --state ended implies.

--cloud-source-id filters reviewer groups by cloud source, including marked
groups with a listed member from that source.

--row-id finds the groups with a member on that row: qctl reads every page
of the list (100 groups a page) and keeps the matches, because the route has
no row filter. It takes no --page or --limit. A group lists only its current
members; describe duplicate-group shows every member.

COPIES counts the listed members. KEY is a repeated_key group's key, masked
where your role masks the column. DECISION is the effective decision, the
group's own or the one it carries; PENDING is the group's own pending
decision with its counted approvals ("change" when it would replace the
effective one, "Kx disagree" while K counted disagreements hold it).
CAN-DECIDE says whether you may decide the group; describe duplicate-group
shows everything else you may do on it.

--sender lists the groups as a data source's sender sees them: the groups
with a copy on your cloud sources (an external user's granted sources; every
source of the table for any other role), and only those copies. It takes
--cloud-source-id, --file-id, --include-ended, --page and --limit, not the
reviewer filters. STATE is held (with why: undecided, more_than_allowed,
kept_line_changed), decided, loaded or ended; DECISION is the decision in
effect, never a pending one; RESPONSES are your sources' live responses;
CAN-RESPOND says whether you may answer.

  qctl get duplicate-groups --table-id 42 --sender
  qctl get duplicate-groups --table-id 42 --sender --cloud-source-id 7

-o json|yaml print a List of the server's groups; -o name prints
duplicate-group/<uuid>. Decide a group with 'qctl duplicate-group'.`,
		Annotations: catalog.For("duplicate-group"),
		Args:        cobra.NoArgs,
		RunE:        runDuplicateGroups,
	}
	cmdutil.AddTableFlags(cmd, "Table (a mirror or append table)")
	cmd.Flags().String("origin", "", "Only groups of this origin: "+strings.Join(duplicateGroupOrigins, "|"))
	cmd.Flags().String("state", "", "Only groups in this state: "+strings.Join(duplicateGroupStates, "|"))
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Only groups of this upload (stored item)")
	cmd.Flags().String("sender-response", "", "Only groups with this live sender response: "+
		strings.Join(duplicateGroupResponses, "|"))
	cmd.Flags().Bool("include-ended", false, "Include ended groups")
	cmd.Flags().Bool("sender", false, "List the groups with a copy on your cloud sources, as their sender sees them")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Only groups from this cloud source (marked groups: a listed member from it)")
	cmd.Flags().Int("row-id", 0, "Only groups with a member on this row (reads every page)")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, fmt.Sprintf("Groups per page (1-%d)", duplicate_groups.MaxLimit))
	cmdutil.DeclareColumns(cmd, []duplicateGroupRow(nil), []senderDuplicateGroupRow(nil))
	return cmd
}

// duplicateGroupTerms is the request as far as the flags decide it.
type duplicateGroupTerms struct {
	opts  duplicate_groups.ListOptions
	rowID int
}

// enumFlag returns the named flag with hyphens read as underscores, nil when
// it was not given; a value outside valid exits 2.
func enumFlag(cmd *cobra.Command, name string, valid []string) (*string, error) {
	if !cmd.Flags().Changed(name) {
		return nil, nil
	}
	raw, _ := cmd.Flags().GetString(name)
	v := strings.ReplaceAll(strings.TrimSpace(raw), "-", "_")
	for _, ok := range valid {
		if v == ok {
			return &v, nil
		}
	}
	return nil, qerrors.NewBadUsagef("invalid --%s %q (valid: %s)", name, raw, strings.Join(valid, ", "))
}

// duplicateGroupFlags is every refusal decided from the flags, all exit 2.
func duplicateGroupFlags(cmd *cobra.Command) (duplicateGroupTerms, error) {
	var t duplicateGroupTerms
	if err := duplicateGroupPaging(cmd, &t); err != nil {
		return t, err
	}
	if err := duplicateGroupFilters(cmd, &t.opts); err != nil {
		return t, err
	}
	return t, nil
}

// duplicateGroupPaging sets t's row id, or its page and limit.
func duplicateGroupPaging(cmd *cobra.Command, t *duplicateGroupTerms) error {
	var err error
	if cmd.Flags().Changed("row-id") {
		for _, f := range []string{"page", "limit"} {
			if cmd.Flags().Changed(f) {
				return qerrors.NewBadUsagef("--row-id reads every page itself; it takes no --%s", f)
			}
		}
		t.rowID, err = cmdutil.PositiveCountFlag(cmd, "row-id")
		return err
	}
	t.opts.Page, t.opts.Limit, err = duplicateGroupPageFlags(cmd)
	return err
}

// duplicateGroupPageFlags reads --page and --limit, the limit at most MaxLimit.
func duplicateGroupPageFlags(cmd *cobra.Command) (page, limit int, err error) {
	if page, limit, err = paginationFlags(cmd); err != nil {
		return 0, 0, err
	}
	if limit > duplicate_groups.MaxLimit {
		return 0, 0, qerrors.NewBadUsagef("--limit must be at most %d, got %d", duplicate_groups.MaxLimit, limit)
	}
	return page, limit, nil
}

// duplicateGroupFilters sets opts' origin, state, response, file, source and
// include-ended filters.
func duplicateGroupFilters(cmd *cobra.Command, opts *duplicate_groups.ListOptions) error {
	origin, err := enumFlag(cmd, "origin", duplicateGroupOrigins)
	if err != nil {
		return err
	}
	if origin != nil {
		o := duplicate_groups.Origin(*origin)
		opts.Origin = &o
	}
	state, err := enumFlag(cmd, "state", duplicateGroupStates)
	if err != nil {
		return err
	}
	if state != nil {
		s := duplicate_groups.StateFilter(*state)
		opts.State = &s
	}
	response, err := enumFlag(cmd, "sender-response", duplicateGroupResponses)
	if err != nil {
		return err
	}
	if response != nil {
		r := duplicate_groups.ResponseFilter(*response)
		opts.SenderResponse = &r
	}
	fileID, ok, err := cmdutil.PositiveIDFlag(cmd, "file-id")
	if err != nil {
		return err
	}
	if ok {
		opts.StoredItemID = &fileID
	}
	sourceID, ok, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return err
	}
	if ok {
		opts.CloudSourceID = &sourceID
	}
	opts.IncludeEnded, _ = cmd.Flags().GetBool("include-ended")
	if state != nil && *state == string(duplicate_groups.GroupStateEnded) {
		opts.IncludeEnded = true
	}
	return nil
}

func runDuplicateGroups(cmd *cobra.Command, _ []string) error {
	if sender, _ := cmd.Flags().GetBool("sender"); sender {
		return runSenderDuplicateGroups(cmd)
	}
	terms, err := duplicateGroupFlags(cmd)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	client := duplicate_groups.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	groups, lp, err := fetchDuplicateGroups(client, ctx.Credential.AccessToken, tableID, &terms)
	if err != nil {
		return fmt.Errorf("failed to list duplicate groups: %w", err)
	}
	if len(groups) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "duplicate groups", []duplicateGroupRow{}, lp.total)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	format, _ := cmd.Flags().GetString("output")
	return printListPage(cmd, groups, lp, func(data any) error {
		if l, ok := data.(output.List); ok {
			return output.WriteRaw(cmd.OutOrStdout(), format, l)
		}
		if format == "name" { //nolint:goconst // Keep output format handling local to this command.
			for i := range groups {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), duplicate_groups.Ref(groups[i].ID)); err != nil {
					return err
				}
			}
			return nil
		}
		return tableui.PrintFromCmd(cmd, duplicateGroupRows(groups), duplicateGroupColumns)
	})
}

// fetchDuplicateGroups reads the groups holding terms' row id across every
// page, or the one page terms names; never a nil slice.
func fetchDuplicateGroups(client *duplicate_groups.Client, token string, tableID int,
	terms *duplicateGroupTerms) ([]duplicate_groups.Summary, listPage, error) {
	if terms.rowID > 0 {
		groups, _, err := client.FindByRow(token, tableID, terms.opts, terms.rowID)
		if err != nil {
			return nil, listPage{}, err
		}
		if groups == nil {
			groups = []duplicate_groups.Summary{}
		}
		total := len(groups)
		return groups, listPage{noun: "duplicate groups", total: &total}, nil
	}
	page, err := client.List(token, tableID, terms.opts)
	if err != nil {
		return nil, listPage{}, err
	}
	groups := page.Results
	if groups == nil {
		groups = []duplicate_groups.Summary{}
	}
	return groups, listPage{noun: "duplicate groups", page: &terms.opts.Page, limit: &terms.opts.Limit,
		total: page.TotalRows, more: page.Next != nil}, nil
}

func duplicateGroupRows(groups []duplicate_groups.Summary) []duplicateGroupRow {
	rows := make([]duplicateGroupRow, len(groups))
	for i := range groups {
		g := &groups[i]
		file := "-"
		if g.StoredItemID != nil {
			file = fmt.Sprintf("file/%d", *g.StoredItemID)
		}
		state := string(g.State)
		if g.Unapplied {
			state += " (unapplied)"
		}
		rows[i] = duplicateGroupRow{
			ID:        g.ID.String(),
			Origin:    string(g.Origin),
			State:     state,
			Held:      g.HeldCount,
			Copies:    len(g.Members),
			Key:       duplicate_groups.KeyColumn(g.Origin, g.KeyValues, g.HoldMode),
			File:      file,
			Decision:  duplicate_groups.DecisionColumn(g.Decision, g.CarriedDecision),
			Pending:   duplicate_groups.PendingColumn(g.PendingDecision),
			Responses: duplicate_groups.ResponsesColumn(g.Responses),
			CanDecide: duplicate_groups.CanDecideColumn(&g.Caller),
			Age:       timeutil.FormatRelative(g.CreatedAt),
		}
	}
	return rows
}
