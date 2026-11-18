package get

import (
	"fmt"
	"sort"
	"time"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/rule_row_ignores"
	"github.com/spf13/cobra"
)

const (
	ruleIgnoreColumns        = "id,row_id,table_rule,cloud_source_id,reason,created_at"
	ruleIgnoreVerboseColumns = "id,row_id,table_rule,table_rule_id,rule_revision_id,cloud_source_id,reason,decided_by_id,created_at,updated_at"
)

// NewRuleIgnoresCommand creates the get rule-ignores command.
func NewRuleIgnoresCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-ignores [table-id-or-name]",
		Short: "List the rows a table rule has been told to stop flagging",
		Long: `List the active row-level ignores of a table's rules: which rule is not
flagging which row, who decided it, and why.

  qctl get rule-ignores orders --rule email_check
  qctl get rule-ignores orders --row-id 1042 --cloud-source s3-nightly
  qctl get rule-ignores 42 --cloud-source-id 7 --rule email_check --row-id 1042 -o yaml

Ask by rule, by row, or both. There is no "every ignore on the table" form: the
backend lists by one of the two, so at least one of --rule and --row-id is
required.

The listing belongs to ONE cloud source. Name it with --cloud-source or
--cloud-source-id; a table with exactly one cloud source is used without being
named, and a table with several must be told which.

Only ACTIVE ignores are listed: an ignore removed with 'qctl rows unignore-rule'
is not shown, and there is no flag to include removed ones. An ignore holds
only while the rule keeps the revision it had when the ignore was decided
(RULE-REVISION-ID under -v) and the row's values in the columns the rule reads
are unchanged; a load that finds either changed discards it with no actor,
the ignore drops out of this list, and the rule can flag the row again. An ignore stays on
its row after the row leaves quarantine, so a listed row can be clean or
quarantined; ROW-ID is what 'qctl rows unignore-rule --row-id' takes.

Ignores are written with 'qctl rows ignore-rule' and removed with
'qctl rows unignore-rule'.`,
		Annotations: cmdutil.WithFormats(cmdutil.WithArgKinds(catalog.For("data"), "table"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			return runRuleIgnores(cmd, positional)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.RefString(cmd.Flags(), "rule", "table-rule", "Only this table rule's ignores (instance name, short ID, UUID or r-* alias)")
	cmd.Flags().Int("row-id", 0, "Only this row's ignores (clean or quarantined)")
	cmd.Flags().String("cloud-source", "", "The cloud source to list by NAME (exact, case-sensitive)")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "The cloud source to list by ID")
	cmdutil.DeclareColumns(cmd, []ruleIgnoreRow(nil))
	return cmd
}

// ruleIgnoreTerms is the request as far as the command line alone decides it.
type ruleIgnoreTerms struct {
	rule          string
	rowID         int
	cloudSourceID int
	cloudSource   string
}

// ruleIgnoreFlags is every refusal decided from the flags, all of them before
// Bootstrap and all exit 2.
func ruleIgnoreFlags(cmd *cobra.Command) (ruleIgnoreTerms, error) {
	var t ruleIgnoreTerms
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"cloud-source-id", "cloud-source"}); err != nil {
		return t, err
	}
	// The route answers 400 with neither filter, so the refusal is ours to
	// make first, and for free.
	if err := cmdutil.RequireOneOfFlags(cmd, []string{"rule", "row-id"}); err != nil {
		return t, err
	}
	if cmd.Flags().Changed("rule") {
		t.rule, _ = cmd.Flags().GetString("rule")
		if t.rule == "" {
			return t, qerrors.NewBadUsage("--rule cannot be empty: name the table rule")
		}
	}
	var err error
	if t.rowID, _, err = cmdutil.PositiveIDFlag(cmd, "row-id"); err != nil {
		return t, err
	}
	if t.cloudSourceID, _, err = cmdutil.PositiveIDFlag(cmd, "cloud-source-id"); err != nil {
		return t, err
	}
	t.cloudSource, _ = cmd.Flags().GetString("cloud-source")
	if cmd.Flags().Changed("cloud-source") && t.cloudSource == "" {
		// Falling through would auto-detect a source, answering a different
		// question than the one typed.
		return t, qerrors.NewBadUsage(
			"--cloud-source cannot be empty: name the cloud source, or use --cloud-source-id <id>")
	}
	return t, nil
}

func runRuleIgnores(cmd *cobra.Command, tableArg string) error {
	terms, err := ruleIgnoreFlags(cmd)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, tableArg)
	if err != nil {
		return err
	}
	// An id or a name is checked against this table where the resolver can;
	// with neither, a table with one source answers for it. That auto-detect is
	// a READ's convenience — the ignore-rule writes refuse to guess.
	sourceID, _, err := cmdutil.ResolveCloudSource(ctx, tableID, tableNameForMessage(cmd, tableArg), terms.cloudSourceID, terms.cloudSource)
	if err != nil {
		return err
	}

	// The full rule list is read whatever the filter, because it is also what
	// names the table_rule column: a --row-id listing spans every rule.
	rulesClient, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	rules, err := rulesClient.ListAllDatasetRules(ctx.Credential.AccessToken, tableID)
	if err != nil {
		return fmt.Errorf("failed to list table rules: %w", err)
	}
	filter, err := ruleIgnoreFilter(ctx, rules, terms, sourceID)
	if err != nil {
		return err
	}

	client := rule_row_ignores.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	ignores, err := client.List(ctx.Credential.AccessToken, tableID, filter)
	if err != nil {
		return err
	}

	if len(ignores) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "rule ignores", []ruleIgnoreRow{}, nil)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	columns := ruleIgnoreColumns
	if ctx.Verbosity >= 1 {
		columns = ruleIgnoreVerboseColumns
	}
	return printListPage(cmd, ruleIgnoreRows(ignores, rules), listPage{noun: "rule ignores"}, func(data any) error {
		return tableui.PrintFromCmd(cmd, data, columns)
	})
}

// ruleIgnoreFilter is the listing's filter, with --rule resolved strictly on the
// table's own rules.
func ruleIgnoreFilter(ctx *cmdutil.CommandContext, rules []dataset_rules.DatasetRuleTiny, terms ruleIgnoreTerms, sourceID int) (rule_row_ignores.ListFilter, error) {
	filter := rule_row_ignores.ListFilter{CloudSourceID: sourceID}
	if terms.rule != "" {
		rule, err := cmdutil.FindTableRule(ctx, rules, terms.rule, "--rule")
		if err != nil {
			return filter, err
		}
		filter.RuleID = &rule.ID
	}
	if terms.rowID > 0 {
		filter.RowID = &terms.rowID
	}
	return filter, nil
}

// ruleIgnoreRow is one ignore as every output format shows it: the record's
// fields under their CLI names, plus the rule's instance name.
//
// Reason keeps the wire's pointer so -o json/yaml can tell "no reason given"
// (null) from an empty one.
type ruleIgnoreRow struct {
	ID          string `json:"id"`
	RowID       int    `json:"row_id"`
	TableRule   string `json:"table_rule"`
	TableRuleID string `json:"table_rule_id"`
	// RuleRevisionID is the rule's revision when the ignore was decided.
	RuleRevisionID string    `json:"rule_revision_id"`
	CloudSourceID  int       `json:"cloud_source_id"`
	Reason         *string   `json:"reason"`
	DecidedByID    string    `json:"decided_by_id"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// ruleIgnoreRows projects the records and sorts them by row, then rule
// position: the route's select carries no ORDER BY, so the server's order is
// not stable enough to print.
func ruleIgnoreRows(ignores []rule_row_ignores.Ignore, rules []dataset_rules.DatasetRuleTiny) []ruleIgnoreRow {
	type ruleInfo struct {
		name     string
		position int
	}
	byID := make(map[string]ruleInfo, len(rules))
	for _, r := range rules {
		byID[r.ID.String()] = ruleInfo{r.InstanceName, r.Position}
	}
	rows := make([]ruleIgnoreRow, len(ignores))
	positions := make([]int, len(ignores))
	for i, ig := range ignores {
		info, ok := byID[ig.DatasetRuleID.String()]
		name := info.name
		if !ok {
			// A rule missing from the list cannot be named; the id still
			// identifies it.
			name = ig.DatasetRuleID.String()
		}
		rows[i] = ruleIgnoreRow{
			ID:             ig.ID.String(),
			RowID:          ig.RowID,
			TableRule:      name,
			TableRuleID:    ig.DatasetRuleID.String(),
			RuleRevisionID: ig.RuleRevisionID.String(),
			CloudSourceID:  ig.DataSourceModelID,
			Reason:         ig.Reason,
			DecidedByID:    ig.DecidedByID.String(),
			CreatedAt:      ig.CreatedAt,
			UpdatedAt:      ig.UpdatedAt,
		}
		positions[i] = info.position
	}
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := rows[idx[a]], rows[idx[b]]
		if ra.RowID != rb.RowID {
			return ra.RowID < rb.RowID
		}
		return positions[idx[a]] < positions[idx[b]]
	})
	sorted := make([]ruleIgnoreRow, len(rows))
	for i, j := range idx {
		sorted[i] = rows[j]
	}
	return sorted
}
