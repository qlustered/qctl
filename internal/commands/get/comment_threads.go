package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/comment_threads"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

const commentThreadColumns = "id,rows,fields,comments,resolved,title,duplicate_group"

// NewCommentThreadsCommand creates the get comment-threads command.
func NewCommentThreadsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "comment-threads [thread-id...]",
		Short: "List open comment threads or show threads by id",
		Long: `List the table's readable unresolved comment threads, most recently
active first. --page and --limit select one page (default: page 1, limit 20).
The listing includes targets, comment counts and the duplicate group id when
present; read the transcript with
'qctl describe comment-thread <id> --table <name>'.

Pass thread ids to fetch specific threads, including resolved ones, with
their transcripts. --page and --limit apply only when no ids are given.
Rule-review discussions use 'qctl describe rule-review <id>'.

Examples:
  qctl get comment-threads --table orders
  qctl get comment-threads --table-id 42 --page 2 --limit 20
  qctl get comment-threads --table orders 0190bbbb-1111-2222-3333-444444444444
  qctl get comment-threads --table-id 42 <id-a> <id-b>
  qctl get comment-threads --table orders <id> -o yaml`,
		Annotations: catalog.For("comment-thread"),
		Args: func(_ *cobra.Command, args []string) error {
			return cmdutil.ValidateUUIDArgs(args, "comment thread id")
		},
		RunE: runCommentThreads,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().Int("page", 1, "Page number (without thread ids)")
	cmd.Flags().Int("limit", 20, "Threads per page (max 100; without thread ids)")
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []comment_threads.SummaryRow(nil)), ", "))
	return cmd
}

func runCommentThreads(cmd *cobra.Command, args []string) error {
	// Settled before Bootstrap and the table lookup, both of which cost a
	// request: a typo in an id is decidable locally and must cost nothing.
	if err := cmdutil.ValidateUUIDArgs(args, "comment thread id"); err != nil {
		return err
	}
	page, limit, err := commentThreadsPagination(cmd, args)
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
	datasetID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return err
	}
	client := comment_threads.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if len(args) == 0 {
		list, err := client.List(ctx.Credential.AccessToken, datasetID, page, limit)
		if err != nil {
			return fmt.Errorf("failed to list comment threads: %w", err)
		}
		lp := listPage{
			noun: "comment threads", page: &page, limit: &limit,
			total: list.TotalRows, more: list.Next != nil, noFilters: true,
		}
		return printCommentThreads(cmd, ctx, list.Results, lp)
	}
	threads := make([]comment_threads.Thread, 0, len(args))
	for _, id := range args {
		// One request per id: there is no batch endpoint.
		thread, err := client.Get(ctx.Credential.AccessToken, datasetID, id)
		if err != nil {
			return cmdutil.HintOnNotFound(err, fmt.Sprintf(cmdutil.CommentThreadNotFoundHint, id))
		}
		threads = append(threads, *thread)
	}
	return printCommentThreads(cmd, ctx, threads, listPage{})
}

func commentThreadsPagination(cmd *cobra.Command, args []string) (page, limit int, err error) {
	if len(args) > 0 {
		for _, flag := range []string{"page", "limit"} {
			if cmd.Flags().Changed(flag) {
				return 0, 0, qerrors.NewBadUsagef("--%s applies only when no thread ids are given", flag)
			}
		}
	}
	page, limit, err = paginationFlags(cmd)
	if err != nil {
		return 0, 0, err
	}
	if limit > 100 {
		return 0, 0, qerrors.NewBadUsage("--limit must be at most 100")
	}
	return page, limit, nil
}

func printCommentThreads(cmd *cobra.Command, ctx *cmdutil.CommandContext, threads []comment_threads.Thread, lp listPage) error {
	if len(threads) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "open comment threads", []comment_threads.SummaryRow{}, lp.total)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	return printListPage(cmd, threads, lp, func(data any) error {
		if list, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, list)
		}
		if threads, ok := data.([]comment_threads.Thread); ok {
			data = comment_threads.SummaryRows(threads)
		}
		return tableui.PrintFromCmd(cmd, data, commentThreadColumns)
	})
}
