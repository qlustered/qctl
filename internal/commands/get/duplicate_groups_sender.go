package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/spf13/cobra"
)

// senderDuplicateGroupFlags is the source route's request as far as the flags
// decide it; every refusal exits 2.
func senderDuplicateGroupFlags(cmd *cobra.Command) (duplicate_groups.SourceListOptions, error) {
	var opts duplicate_groups.SourceListOptions
	for _, f := range reviewerOnlyFlags {
		if cmd.Flags().Changed(f) {
			return opts, qerrors.NewBadUsagef("--%s is a reviewer-view filter; --sender takes --cloud-source-id, "+
				"--file-id, --include-ended, --page and --limit", f)
		}
	}
	var err error
	if opts.Page, opts.Limit, err = duplicateGroupPageFlags(cmd); err != nil {
		return opts, err
	}
	sourceID, ok, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	if err != nil {
		return opts, err
	}
	if ok {
		opts.CloudSourceID = &sourceID
	}
	fileID, ok, err := cmdutil.PositiveIDFlag(cmd, "file-id")
	if err != nil {
		return opts, err
	}
	if ok {
		opts.StoredItemID = &fileID
	}
	opts.IncludeEnded, _ = cmd.Flags().GetBool("include-ended")
	return opts, nil
}

func runSenderDuplicateGroups(cmd *cobra.Command) error {
	opts, err := senderDuplicateGroupFlags(cmd)
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
	page, err := client.ListSource(ctx.Credential.AccessToken, tableID, opts)
	if err != nil {
		return fmt.Errorf("failed to list duplicate groups: %w", err)
	}
	groups := page.Results
	if groups == nil {
		groups = []duplicate_groups.SourceSummary{}
	}
	lp := listPage{noun: "duplicate groups", page: &opts.Page, limit: &opts.Limit, total: page.TotalRows, more: page.Next != nil}
	if len(groups) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "duplicate groups", []senderDuplicateGroupRow{}, lp.total)
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
		return tableui.PrintFromCmd(cmd, senderDuplicateGroupRows(groups), senderDuplicateGroupColumns)
	})
}

func senderDuplicateGroupRows(groups []duplicate_groups.SourceSummary) []senderDuplicateGroupRow {
	rows := make([]senderDuplicateGroupRow, len(groups))
	for i := range groups {
		g := &groups[i]
		file := "-"
		if g.StoredItemID != nil {
			file = fmt.Sprintf("file/%d", *g.StoredItemID)
		}
		rows[i] = senderDuplicateGroupRow{
			ID:         g.ID.String(),
			Origin:     string(g.Origin),
			State:      duplicate_groups.SenderStateColumn(g.State, g.HeldReason),
			Held:       duplicate_groups.HeldCopies(g.Members),
			Copies:     len(g.Members),
			Key:        duplicate_groups.KeyColumn(g.Origin, g.KeyValues, nil),
			File:       file,
			Decision:   duplicate_groups.SourceDecisionColumn(g.Decision),
			Responses:  duplicate_groups.SourceResponsesColumn(g.Responses),
			CanRespond: duplicate_groups.CanRespondColumn(&g.Caller),
			Age:        timeutil.FormatRelative(g.CreatedAt),
		}
	}
	return rows
}
