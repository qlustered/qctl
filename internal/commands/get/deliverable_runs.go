package get

import (
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

// NewDeliverableRunsCommand creates the get deliverable-runs command.
func NewDeliverableRunsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "deliverable-runs [<deliverable-ref>]",
		Short: "List deliverable runs in the organization or for one deliverable",
		Long: `List one page of runs across the organization, including deliverables in
the trash, with each deliverable's current name. Pass a slug, name or UUID to
list only that deliverable's runs. Both modes list newest first; --reverse
lists the oldest first, and --state keeps the runs in one state.

JSON and YAML print the API records, which leave out each run's sources, in a
List envelope; -o name prints one deliverable-run/<id> per run.`,
		Annotations: cmdutil.WithArgKinds(catalog.For("deliverable-run"), "deliverable"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if err := deliverables.ValidateReference(args[0]); err != nil {
					return err
				}
			}
			params, err := parseDeliverableRunsParams(cmd)
			if err != nil {
				return err
			}
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if len(args) == 0 {
				return listOrganizationDeliverableRuns(cmd, ctx, c, params)
			}
			id, err := c.ResolveID(ctx.Credential.AccessToken, args[0], true)
			if err != nil {
				return err
			}
			page, err := c.ListRuns(ctx.Credential.AccessToken, id, params)
			if err != nil {
				return err
			}
			var runs []deliverables.Run
			if page.Results != nil {
				runs = *page.Results
			}
			if len(runs) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "deliverable runs", []deliverables.RunDisplay{}, page.TotalRows)
				if err != nil || skip {
					return err
				}
			}
			printContextBanner(cmd, ctx)
			lp := listPage{noun: "deliverable runs", page: &params.Page, limit: &params.Limit, total: page.TotalRows}
			return printListPage(cmd, runs, lp, func(data any) error {
				if list, ok := data.(output.List); ok {
					format, _ := cmd.Flags().GetString("output")
					return output.WriteRaw(cmd.OutOrStdout(), format, list)
				}
				return tableui.PrintFromCmd(cmd, deliverables.RunRows(runs), deliverables.RunColumns)
			})
		},
	}
	cmd.Flags().String("state", "", "Keep the runs in one state: "+strings.Join(runStateNames(), ", "))
	cmd.Flags().Bool("reverse", false, "List the oldest run first")
	cmd.Flags().Int("limit", 20, "Maximum runs per page")
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().String("columns", "", "Comma-separated table columns. Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []deliverables.RunDisplay(nil)), ", "))
	return cmd
}

func listOrganizationDeliverableRuns(cmd *cobra.Command, ctx *cmdutil.CommandContext, c *deliverables.Client, params deliverables.RunListParams) error {
	page, err := c.ListOrganizationRuns(ctx.Credential.AccessToken, params)
	if err != nil {
		return err
	}
	if len(page.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "deliverable runs", []deliverables.RunDisplay{}, page.TotalRows)
		if err != nil || skip {
			return err
		}
	}
	printContextBanner(cmd, ctx)
	lp := listPage{noun: "deliverable runs", page: &params.Page, limit: &params.Limit, total: page.TotalRows, more: page.Next != nil}
	return printListPage(cmd, page.Results, lp, func(data any) error {
		if list, ok := data.(output.List); ok {
			format, _ := cmd.Flags().GetString("output")
			return output.WriteRaw(cmd.OutOrStdout(), format, list)
		}
		return tableui.PrintFromCmd(cmd, deliverables.OrganizationRunRows(page.Results), "id,deliverable,"+strings.TrimPrefix(deliverables.RunColumns, "id,"))
	})
}

func parseDeliverableRunsParams(cmd *cobra.Command) (deliverables.RunListParams, error) {
	var params deliverables.RunListParams
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page, params.Limit = page, limit
	params.Reverse, _ = cmd.Flags().GetBool("reverse")
	if cmd.Flags().Changed("state") {
		params.State, _ = cmd.Flags().GetString("state")
		if err := validateStateValue("--state", params.State, runStateNames()); err != nil {
			return params, err
		}
	}
	return params, nil
}

// runStateNames returns deliverables.RunStates as strings.
func runStateNames() []string {
	names := make([]string, len(deliverables.RunStates))
	for i, state := range deliverables.RunStates {
		names[i] = string(state)
	}
	return names
}
