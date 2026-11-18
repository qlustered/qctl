package describe

import (
	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/dataview"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// NewRowCommand creates the describe row command.
func NewRowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "row <row-id>",
		Short: "Show every rule verdict recorded for one row",
		Long: `Show why one row was treated the way it was: every rule run that produced a
verdict for it, newest first, decoded against the manifest each run executed.

For each run: which pipeline execution it was and when, the job behind it, the
manifest and its sha256, and one line per configured rule with the verdict it
reached, the revision that ran, and the alert and warning tokens it raised.
Rules are named by the same r-* aliases 'qctl get data --include-dataset-rule-ids'
uses, and issues by the same [A<id>]/[W<id>] markers it puts on a cell — read
their messages with 'qctl describe alert <id>' or 'qctl describe warning <id>'.

The row id is a data row id and reaches either side of the table: the route
resolves it against the clean rows first and the quarantined rows second. A
deleted row is not found rather than empty.

By default only the rules that did something to the row are listed, with the
rest counted; -v lists every entry in each run's manifest. Use -o json|yaml for
the raw API response.

The table is required: the route is dataset-scoped, so there is no org-wide
lookup. Row ids come from 'qctl get data'.

Examples:
  qctl describe row 1042 --table orders
  qctl describe row 1042 --table-id 42 -v
  qctl describe row 1042 --table orders -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("data"), outputFormatTable, outputFormatJSON, outputFormatYAML),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Before Bootstrap and the table lookup: a typo in the id is
			// decidable locally, is bad usage (exit 2), and must cost nothing.
			rowIDs, err := cmdutil.ParseIntArgs(args, "row id")
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

			client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			evidence, err := client.GetRowRuleEvidence(ctx.Credential.AccessToken, datasetID, rowIDs[0])
			if err != nil {
				return err
			}

			outputFormat, _ := cmd.Flags().GetString("output")
			if cmd.Flags().Changed("output") {
				switch outputFormat {
				case outputFormatJSON, outputFormatYAML:
					return output.WriteRaw(cmd.OutOrStdout(), outputFormat, evidence)
				}
			}
			return renderRowEvidence(cmd, ctx, evidence)
		},
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	return cmd
}

// renderRowEvidence opens the alias store and writes the default payload.
//
// The store is opened for writing because r-* aliases are allocated on first
// sight, exactly as `get data --include-dataset-rule-ids` allocates them: the
// two commands must name the same rule the same way, and only a shared store
// can promise that.
func renderRowEvidence(cmd *cobra.Command, ctx *cmdutil.CommandContext, evidence *datasets.RowRuleEvidence) error {
	store, err := aliasstore.Open()
	if err != nil {
		return err
	}
	defer store.Close()
	serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
	if err != nil {
		return err
	}

	// Seeded through the handle already held, NOT through
	// rule_versions.RememberRevisionShortIDs: that helper calls
	// aliasstore.Open() itself, and a second read-write open blocks on the
	// exclusive flock this one holds for the full 5s openTimeout and then
	// fails — five silent seconds per invocation, and no ids cached.
	//
	// Best-effort either way: a revision the reader was just shown should
	// resolve in `qctl get rule <short>` without listing the catalog, but a
	// cache write must never fail a read.
	if ids := datasets.RevisionIDs(evidence); len(ids) > 0 {
		_ = store.PutShortIDs(serverKey, ctx.OrganizationID, aliasstore.ResourceRuleRevision, ids)
	}

	showAll := ctx.Verbosity >= 1
	return datasets.RenderRowEvidence(cmd.OutOrStdout(), evidence,
		ruleAliasAdapter{store: store, serverKey: serverKey},
		datasets.RowEvidenceOptions{ShowAllVerdicts: showAll})
}

// ruleAliasAdapter binds the bbolt alias store to the active server scope and
// the dataset-rule resource type. It is the twin of the adapter `get data`
// uses; both must allocate from the same bucket for an alias to mean one rule.
type ruleAliasAdapter struct {
	store     *aliasstore.Store
	serverKey string
}

func (a ruleAliasAdapter) Alias(uuid string) (string, error) {
	return a.store.Allocate(a.serverKey, aliasstore.ResourceDatasetRule, uuid)
}

var _ dataview.RuleAliaser = ruleAliasAdapter{}
