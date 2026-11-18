package table_rule //nolint:revive // Matches the command directory.

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newTestFilterCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test-filter [<name-or-id>]",
		Short: "Count the rows of a table that a row filter keeps",
		Long: `Count the rows of a table that a row filter keeps. Nothing is saved.

Give exactly one of:
  <name-or-id>               a table rule's saved row filter: an instance name,
                             short ID or UUID with --table or --table-id, or an
                             r-* alias alone, as 'table-rule move' takes it
  -f <file>                  spec.row_filter of one kind: TableRule manifest,
                             create or patch shape, counted on the table in
                             spec.dataset_id
  --row-filter-file <file>   a bare row filter document (conditions, predicate,
                             description), counted on --table or --table-id

A saved filter prints "Checks M of T rows" and a draft "Keeps M of T rows",
where T is the table's live clean and quarantined rows. The server counts
within its scan limits; a capped count is a lower bound and is marked "+".
Rows whose predicate fails are not counted, and the first failure is printed.
A draft's description is not sent. A draft that does not compile, or a saved
filter that no longer does, exits 2.

Examples:
  qctl table-rule test-filter vin_check --table orders
  qctl table-rule test-filter -f table-rule.yaml
  qctl table-rule test-filter --row-filter-file filter.yaml --table-id 42
  qctl table-rule test-filter r-3 -o json

-o json|yaml prints the server's {matched, total, capped, predicate_errors,
first_predicate_error}; prose goes to stderr.`,
		Annotations: cmdutil.WithFormats(catalog.For("table-rule"), "table", "json", "yaml"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runTestFilter,
	}
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringP("filename", "f", "", "TableRule manifest whose spec.row_filter is counted on spec.dataset_id")
	cmd.Flags().String("row-filter-file", "", "Row filter file (YAML or JSON) to count on --table or --table-id")
	return cmd
}

// testFilterInput is what test-filter counts. filter is nil in rule mode.
type testFilterInput struct {
	rule    string
	filter  *row_filters.Filter
	tableID int
}

func runTestFilter(cmd *cobra.Command, args []string) error {
	in, err := readTestFilterInput(cmd, args)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	w := cmdutil.NewResultWriter(cmd)
	if in.filter == nil {
		rule, count, err := countSavedFilter(ctx, cmd, in.rule)
		if err != nil {
			return err
		}
		if err := writeSavedFilterCount(w.Human, rule, count); err != nil {
			return err
		}
		return w.Write(count, "")
	}
	count, err := countDraftFilter(ctx, cmd, in)
	if err != nil {
		return err
	}
	if err := row_filters.WriteCount(w.Human, "Keeps", count); err != nil {
		return err
	}
	return w.Write(count, "")
}

// countSavedFilter resolves input to a rule and reads its saved filter's count.
func countSavedFilter(ctx *cmdutil.CommandContext, cmd *cobra.Command, input string) (dataset_rules.DatasetRuleTiny, *api.RowFilterCountSchema, error) {
	found, err := resolveRuleOnTable(ctx, cmd, input)
	if err != nil {
		return dataset_rules.DatasetRuleTiny{}, nil, err
	}
	count, err := found.client.RowFilterCount(ctx.Credential.AccessToken, found.tableID, found.rule.ID.String())
	return found.rule, count, err
}

// countDraftFilter counts in.filter on in.tableID, or on the table the flags
// name when in.tableID is 0.
func countDraftFilter(ctx *cmdutil.CommandContext, cmd *cobra.Command, in testFilterInput) (*api.RowFilterCountSchema, error) {
	tableID := in.tableID
	if tableID == 0 {
		var err error
		if tableID, err = cmdutil.ResolveTableIDFromFlags(ctx, cmd); err != nil {
			return nil, err
		}
	}
	cl, err := row_filters.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, err
	}
	return cl.CountKept(ctx.Credential.AccessToken, tableID, in.filter)
}

// readTestFilterInput validates the inputs and reads any filter file; it sends
// no request.
func readTestFilterInput(cmd *cobra.Command, args []string) (testFilterInput, error) {
	var given []string
	if len(args) == 1 {
		given = append(given, "a rule")
	}
	if cmd.Flags().Changed("filename") {
		given = append(given, "-f")
	}
	if cmd.Flags().Changed("row-filter-file") {
		given = append(given, "--row-filter-file")
	}
	switch {
	case len(given) == 0:
		return testFilterInput{}, qerrors.NewBadUsage("nothing to test: name a rule (with --table or --table-id, or an r-* alias), " +
			"or pass -f <table-rule.yaml>, or --row-filter-file <file> with --table or --table-id")
	case len(given) > 1:
		return testFilterInput{}, qerrors.NewBadUsagef("give one of a rule, -f or --row-filter-file, not %s", strings.Join(given, " and "))
	}

	switch {
	case len(args) == 1:
		return readRuleInput(cmd, args[0])
	case cmd.Flags().Changed("filename"):
		return readManifestInput(cmd)
	default:
		return readRowFilterFileInput(cmd)
	}
}

func readRuleInput(cmd *cobra.Command, input string) (testFilterInput, error) {
	if err := validateRuleSelector(cmd, input); err != nil {
		return testFilterInput{}, err
	}
	return testFilterInput{rule: input}, nil
}

func readManifestInput(cmd *cobra.Command) (testFilterInput, error) {
	path, _ := cmd.Flags().GetString("filename")
	if path == "" {
		return testFilterInput{}, qerrors.NewBadUsage("-f cannot be empty: name a TableRule manifest")
	}
	var tableFlags []string
	for _, name := range []string{"table", "table-id"} {
		if cmd.Flags().Changed(name) {
			tableFlags = append(tableFlags, "--"+name)
		}
	}
	if len(tableFlags) > 0 {
		return testFilterInput{}, qerrors.NewBadUsagef("%s cannot be used with -f: the table is the manifest's spec.dataset_id",
			strings.Join(tableFlags, " and "))
	}
	tableID, filter, err := loadTestFilterManifest(path)
	if err != nil {
		return testFilterInput{}, err
	}
	return testFilterInput{filter: filter, tableID: tableID}, nil
}

func readRowFilterFileInput(cmd *cobra.Command) (testFilterInput, error) {
	path, _ := cmd.Flags().GetString("row-filter-file")
	if path == "" {
		return testFilterInput{}, qerrors.NewBadUsage("--row-filter-file cannot be empty: name a row filter file")
	}
	if err := cmdutil.ValidateTableSelectorFromFlags(cmd); err != nil {
		return testFilterInput{}, err
	}
	filter, err := row_filters.ReadFile(path, "--row-filter-file "+path, false)
	if err != nil {
		return testFilterInput{}, err
	}
	return testFilterInput{filter: filter}, nil
}

// loadTestFilterManifest returns spec.dataset_id and spec.row_filter of the one
// TableRule document at path.
func loadTestFilterManifest(path string) (int, *row_filters.Filter, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return 0, nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", path))
	}
	switch {
	case len(docs) == 0:
		return 0, nil, qerrors.NewBadUsagef("%s is empty: give one kind: TableRule document", path)
	case len(docs) > 1:
		return 0, nil, qerrors.NewBadUsagef("test-filter reads one TableRule document; %s has %d", path, len(docs))
	}
	var manifest dataset_rules.TableRuleApplyManifest
	if err := yaml.Unmarshal(docs[0], &manifest); err != nil {
		return 0, nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("failed to parse YAML manifest %s", path))
	}
	if err := dataset_rules.ValidateManifestAPIVersion(manifest.APIVersion, path); err != nil {
		return 0, nil, err
	}
	if manifest.Kind != "TableRule" {
		return 0, nil, qerrors.NewBadUsagef("unsupported kind %q in %s (expected: TableRule; a bare row filter goes in --row-filter-file)", manifest.Kind, path)
	}
	switch {
	case manifest.Spec.DatasetID == 0:
		return 0, nil, qerrors.NewBadUsagef("spec.dataset_id is required in %s: it names the table to count", path)
	case manifest.Spec.DatasetID < 0:
		return 0, nil, qerrors.NewBadUsagef("spec.dataset_id must be a positive integer in %s, got %d", path, manifest.Spec.DatasetID)
	}
	switch {
	case !manifest.RowFilterSet:
		return 0, nil, qerrors.NewBadUsagef("spec.row_filter is missing from %s: give the row filter to test", path)
	case manifest.Spec.RowFilter == nil:
		return 0, nil, qerrors.NewBadUsagef("spec.row_filter is null in %s: give the row filter to test", path)
	}
	if err := manifest.Spec.RowFilter.Validate(false, "spec.row_filter in "+path); err != nil {
		return 0, nil, err
	}
	return manifest.Spec.DatasetID, manifest.Spec.RowFilter, nil
}

// writeSavedFilterCount writes count for rule. A rule with no row filter whose
// count is every row of a non-empty table gets one line instead of
// row_filters.WriteCount's.
func writeSavedFilterCount(out io.Writer, rule dataset_rules.DatasetRuleTiny, count *api.RowFilterCountSchema) error {
	if rule.RowFilter == nil && count.Total > 0 && count.Matched == count.Total {
		_, err := fmt.Fprintf(out, "No row filter: table-rule/%s checks every row (%s).\n",
			terminal.Sanitize(rule.InstanceName), row_filters.FormatRows(count.Total, false))
		return err
	}
	return row_filters.WriteCount(out, "Checks", count)
}
