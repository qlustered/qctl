package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
)

var validTableRuleSortFields = []string{
	"instance_name", "state", "position", "created_at", "id",
}

var validTableRuleStates = api.EnumValues[api.DatasetRuleState]()

// NewTableRulesCommand creates the get table-rules command
func NewTableRulesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-rules [table-id-or-name]",
		Short: "List table rules (dataset rules) for a table",
		Long: `List all table rules (instantiated rules) for a specific table.

Table rules are rule revisions that have been instantiated onto a table.
Each table rule has an instance name, position, and configuration.
The RULE column shows its family's display name. --state filters lifecycle
state across every server page; --page and --limit then page the matches.
Use --columns instance_name,filter to show row-filter descriptions.
Use --columns instance_name,validation_state,pending_migration_model_id,schema_change_blocks_enable
to inspect validation and schema readiness. Describe a rule for its recorded-validation blocker.

The table can be given as a positional argument (ID or name), or via
--table <name> or --table-id <id>.

Examples:
  qctl get table-rules 1
  qctl get table-rules orders
  qctl get table-rules --table orders
  qctl get table-rules --table-id 1
  qctl get table-rules 1 --order-by position
  qctl get table-rules orders --state disabled
  qctl get table-rules orders --columns instance_name,filter
  qctl get table-rules 1 --output json
  qctl get table-rules 1 --search email`,
		Annotations: cmdutil.WithArgKinds(catalog.For("table-rule"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Locally-decidable validation first: a bad --order-by or a search
			// the server would drop fails the command either way, and resolving
			// a table name costs an API call that can only be discarded.
			params, err := parseTableRulesParams(cmd)
			if err != nil {
				return err
			}
			state, err := tableRulesStateFlag(cmd)
			if err != nil {
				return err
			}

			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			if err := cmdutil.ValidateTableSelector(cmd, positional); err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
			if err != nil {
				return err
			}

			// Create client and fetch results
			client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			var resp *dataset_rules.DatasetRuleList
			if state != "" {
				resp, err = client.GetDatasetRulesByState(ctx.Credential.AccessToken, tableID, params, state)
			} else {
				resp, err = client.GetDatasetRules(ctx.Credential.AccessToken, tableID, params)
			}
			if err != nil {
				return fmt.Errorf("failed to get table rules: %w", err)
			}

			if len(resp.Results) == 0 {
				skip, err := printEmptyResult(cmd, ctx, "table rules", []dataset_rules.DatasetRuleDisplay{}, resp.TotalRows)
				if err != nil {
					return err
				}
				if skip {
					return nil
				}
			}
			printContextBanner(cmd, ctx)

			lp := listPage{noun: "table rules", page: &params.Page, limit: &params.Limit, total: resp.TotalRows, more: resp.Next != nil}
			return printListPage(cmd, resp.Results, lp, func(data any) error {
				return printTableRulesResults(cmd, data, ctx.Verbosity)
			})
		},
	}

	addTableRulesFlags(cmd)
	return cmd
}

// parseTableRulesParams extracts and validates parameters from command flags.
func parseTableRulesParams(cmd *cobra.Command) (dataset_rules.GetDatasetRulesParams, error) {
	var params dataset_rules.GetDatasetRulesParams

	// Sorting
	sortBy, _ := cmd.Flags().GetString("order-by")
	params.OrderBy = sortBy

	reverse, _ := cmd.Flags().GetBool("reverse")
	params.Reverse = reverse

	// Pagination
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return params, err
	}
	params.Page = page
	params.Limit = limit

	// Filters
	search, err := parseSearchFlag(cmd)
	if err != nil {
		return params, err
	}
	if search != "" {
		params.SearchQuery = &search
	}

	// Validate sort field
	if sortBy != "" {
		if err := validateSortField(sortBy, validTableRuleSortFields); err != nil {
			return params, err
		}
	}

	return params, nil
}

// printTableRulesResults prints table rules through their display projection,
// and a List of them as the API sent them. verbosity picks the default columns.
func printTableRulesResults(cmd *cobra.Command, data any, verbosity int) error {
	defaultCols := "instance_name,rule,release,position,state,severity,author,short_id"
	if verbosity >= 1 {
		defaultCols = "instance_name,rule,release,position,state,severity,author,id,dataset_columns,created_at,updated_at"
	}

	if results, ok := data.([]dataset_rules.DatasetRuleTiny); ok {
		data = dataset_rules.ToDisplayList(results)
	}
	return tableui.PrintFromCmd(cmd, data, defaultCols)
}

// addTableRulesFlags adds all flags for the table-rules command.
func addTableRulesFlags(cmd *cobra.Command) {
	// Required
	cmdutil.AddTableFlags(cmd, "Table (dataset)")

	// Pagination control
	cmd.Flags().Int("limit", 100, "Maximum number of table rules to return")
	cmd.Flags().Int("page", 1, "Page number")

	// Sorting
	cmd.Flags().String("order-by", "", "Order by field (instance_name, state, position, created_at, id)")
	cmd.Flags().Bool("reverse", false, "Reverse the sort order")

	// Filtering
	cmd.Flags().String("search", "", "Search table rules by instance name")
	cmd.Flags().String("state", "", "Filter lifecycle state ("+strings.Join(validTableRuleStates, ", ")+"); paginates matching rules")

	// Output control
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []dataset_rules.DatasetRuleDisplay(nil)), ", "))
}

// tableRulesStateFlag validates an explicitly supplied lifecycle state.
func tableRulesStateFlag(cmd *cobra.Command) (dataset_rules.DatasetRuleState, error) {
	state, _ := cmd.Flags().GetString("state")
	if cmd.Flags().Changed("state") {
		if err := validateStateValue("--state", state, validTableRuleStates); err != nil {
			return "", err
		}
	}
	return dataset_rules.DatasetRuleState(state), nil
}
