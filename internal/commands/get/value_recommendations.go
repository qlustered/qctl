package get

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/qlustered/qctl/internal/value_mapping"
	"github.com/spf13/cobra"
)

// valueRecommendationColumns is the table view's default column set, and the
// whole response: the wire schema has exactly the two fields
// (api.ValueRecommendationTinySchema).
const valueRecommendationColumns = "value,strong"

// NewValueRecommendationsCommand creates the get value-recommendations command.
func NewValueRecommendationsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "value-recommendations [table-id-or-name]",
		Short: "List the replacement values the recommender proposes for a value in a field",
		Long: `Ask the value-mapping recommender what a cell should say.

  qctl get value-recommendations orders --field state --value CA
  qctl get value-recommendations 42 --field state --value Calif. --alert-item-id 8817
  qctl get value-recommendations orders --field state --value "" -o name

This is the list the web grid's cell editor offers while you type: values the
backend believes belong in this field, best first. It is a READ — nothing is
written and nothing is resolved — and the answer is a suggestion, not a verdict.
Apply one with 'qctl rows set-cell' on a quarantined row, or through
'qctl alert resolve'.

--field and --value are both required, and --value "" is legal: with nothing to
match against, the recommender answers with the values the field already holds,
which is the way to ask "what does this column normally contain?".

--alert-item-id or --warning-item-id names the issue that holds the cell. Its
own allowed values are searched BEFORE the value mappings and the boundary
data, and when they answer nothing else is consulted -- so the flag replaces
the rest of the search rather than adding to it. When no cloud source was
given the issue also supplies one as the scope. An issue id that does not exist
is a 404 (exit 3), never an ordinary recommendation list. The two cannot be
combined (exit 2): the server would silently let the warning win.

--cloud-source/--cloud-source-id narrows the value mappings consulted to one
cloud source of this table. A name is matched exactly and case-sensitively
within the table, the way it is on the 'qctl rows' verbs.

STRONG says the server believes the value is probably valid; a weak row is a
candidate that was worth printing rather than a recommendation. The recommender
calls its own ranking brute force, so read the whole list rather than taking the
first row.

An empty list is a normal answer at exit 0. Array and JSON fields never get
recommendations, and a field can simply have nothing to suggest. A field name
this table does not have also answers empty rather than failing -- unless an
issue flag is given, because the issue's allowed values are looked up by issue
id alone and do not consult the field name at all.

A recommended value of the literal NULL!~~ is the backend's SQL NULL sentinel,
not a string to store: it is offered for every nullable field EXCEPT an array
or JSON one, which returns empty before the sentinel is appended, and writing
it back with 'qctl rows set-cell --value NULL!~~' empties the cell.

On a field you see masked, every value but NULL!~~ is masked text: the table
prints it as sent and names the field on stderr, and -o name leaves it out,
since a masked value written back to its column is refused.

Exit codes:
  0  answered, including an empty list
  2  bad usage, or a table that has no settings model yet
  3  table, cloud source, alert or warning not found
  4  unauthorized — the route needs settings:read, which an external user lacks`,
		Annotations: cmdutil.WithArgKinds(catalog.For("data"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			positional := ""
			if len(args) == 1 {
				positional = args[0]
			}
			return runValueRecommendations(cmd, positional)
		},
	}
	addValueRecommendationFlags(cmd)
	cmdutil.DeclareColumns(cmd, []recommendationRow(nil))
	return cmd
}

func addValueRecommendationFlags(cmd *cobra.Command) {
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().String("field", "", "Field (column) the value appears in (required)")
	cmd.Flags().String("value", "", `Value to find replacements for (required; --value "" lists what the field already holds)`)
	cmd.Flags().String("cloud-source", "", "Narrow the value mappings to this cloud source by NAME (exact, case-sensitive)")
	cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Narrow the value mappings to this cloud source by ID")
	cmdutil.RefInt(cmd.Flags(), "alert-item-id", "alert", "Rank for the alert holding this cell")
	cmdutil.RefInt(cmd.Flags(), "warning-item-id", "warning", "Rank for the warning holding this cell")
}

// valueRecommendationTerms is the request as far as the command line alone can
// decide it. Nothing in it cost a request.
type valueRecommendationTerms struct {
	field           string
	value           string
	cloudSourceName string
	cloudSourceID   int
	alertItemID     int
	warningItemID   int
}

// valueRecommendationFlags is every refusal this command decides from its own
// flags, and all of them run BEFORE cmdutil.Bootstrap — the order the `qctl
// rows` group's resolveTarget records and for its reason: a usage error in the
// command line the user just typed should be reported before a config error in
// a file they did not, and a wrong command line must cost no request. Every one
// is exit 2 through qerrors.NewBadUsage.
//
// --field is required AND non-empty. `--field ""` marks the flag Changed while
// naming nothing and would reach the wire as field_name:"", which the
// recommender answers with an empty list rather than an error — a silent wrong
// answer, which is exactly what the guard on `qctl rows set-cell --field` exists
// to prevent for the same reason.
//
// --value is required by PRESENCE only. "" is a legal and useful value: the
// server's matcher takes its empty-query branch and answers with the field's
// own values (atlas ValueMappingRecommender._get_matching_strings,
// capi/capi/controllers/value_mapping_recommender.py), which the web grid
// relies on when it opens an editor before the user types.
// Testing the value would make that unreachable.
//
// The two issue ids are one selector, not two filters, and this is the one
// refusal here the server does not make: it accepts both and the WARNING wins
// outright, silently, because the recommender assigns alert_item_id first and
// warning_item_id_ second over the same field (ValueMappingRecommender.__init__,
// value_mapping_recommender.py). A request naming two issues can only mean the
// caller believes both are being used.
//
// --cloud-source/--cloud-source-id are one selector for the ordinary reason:
// without this the id wins and the name the user also typed is never checked
// against it.
func valueRecommendationFlags(cmd *cobra.Command) (valueRecommendationTerms, error) {
	var t valueRecommendationTerms

	if err := cmdutil.ValidateConflictingFlags(cmd,
		[]string{"cloud-source-id", "cloud-source"},
		[]string{"alert-item-id", "warning-item-id"},
	); err != nil {
		return t, err
	}

	if !cmd.Flags().Changed("field") {
		return t, qerrors.NewBadUsage(
			"'qctl get value-recommendations' needs --field <name>: the field the value appears in")
	}
	t.field, _ = cmd.Flags().GetString("field")
	if t.field == "" {
		return t, qerrors.NewBadUsage("--field cannot be empty: name the field the value appears in")
	}

	if !cmd.Flags().Changed("value") {
		return t, qerrors.NewBadUsage(
			`'qctl get value-recommendations' needs --value <text>: pass --value "" for the values the field already holds`)
	}
	t.value, _ = cmd.Flags().GetString("value")

	var err error
	if t.cloudSourceID, _, err = cmdutil.PositiveIDFlag(cmd, "cloud-source-id"); err != nil {
		return t, err
	}
	t.cloudSourceName, _ = cmd.Flags().GetString("cloud-source")
	if cmd.Flags().Changed("cloud-source") && t.cloudSourceName == "" {
		// `--cloud-source ""` marks the flag Changed while naming nothing.
		// Falling through would send no source at all, answering a WIDER
		// question than the one asked — the same back door
		// checkScopeFlags closes in the rows group.
		return t, qerrors.NewBadUsage(
			"--cloud-source cannot be empty: name the cloud source, or use --cloud-source-id <id>")
	}
	if t.alertItemID, _, err = cmdutil.PositiveIDFlag(cmd, "alert-item-id"); err != nil {
		return t, err
	}
	if t.warningItemID, _, err = cmdutil.PositiveIDFlag(cmd, "warning-item-id"); err != nil {
		return t, err
	}
	return t, nil
}

func runValueRecommendations(cmd *cobra.Command, tableArg string) error {
	terms, err := valueRecommendationFlags(cmd)
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

	req, err := valueRecommendationRequest(ctx, cmd, tableArg, tableID, terms)
	if err != nil {
		return err
	}

	client := value_mapping.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	recommendations, err := client.Recommend(ctx.Credential.AccessToken, req)
	if err != nil {
		return err
	}

	if len(recommendations) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "value recommendations", []recommendationRow{}, nil)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	return printRecommendations(cmd, recommendations)
}

// valueRecommendationRequest builds the body. The table always rides in
// dataset_id: the view resolves the settings model from it server-side, and a
// settings-model id is an internal handle no qctl surface exposes.
//
// A cloud source given as a NAME is resolved through the shared resolver, which
// scopes its search by dataset_id and then matches the name exactly — so a name
// is checked against THIS table. An ID is passed straight through, and the two
// are not equivalent: the view verifies only that the source is in the
// organization (atlas get_value_mapping_recommendations,
// capi/capi/views/value_mapping_view.py), so an id belonging to another table of
// the same org is accepted and narrows the mappings to a source this table does
// not have. Resolving the id here would buy that check at the price of a GET on
// every call whose only other product is a name nothing prints; the page records
// the hole instead.
func valueRecommendationRequest(
	ctx *cmdutil.CommandContext,
	cmd *cobra.Command,
	tableArg string,
	tableID int,
	terms valueRecommendationTerms,
) (value_mapping.RecommendRequest, error) {
	req := value_mapping.RecommendRequest{
		DatasetID: tableID,
		FieldName: terms.field,
		Value:     terms.value,
	}
	switch {
	case terms.cloudSourceName != "":
		id, _, err := cmdutil.ResolveCloudSource(ctx, tableID, tableNameForMessage(cmd, tableArg), 0, terms.cloudSourceName)
		if err != nil {
			return req, err
		}
		req.DataSourceModelID = &id
	case terms.cloudSourceID > 0:
		req.DataSourceModelID = &terms.cloudSourceID
	}
	if terms.alertItemID > 0 {
		req.AlertItemID = &terms.alertItemID
	}
	if terms.warningItemID > 0 {
		req.WarningItemID = &terms.warningItemID
	}
	return req, nil
}

// tableNameForMessage is the table as the USER named it, and it feeds exactly
// one thing: the "not found for table X" in ResolveCloudSource's refusal. It
// resolves nothing and costs nothing.
//
// --table is a NAME by construction (cmdutil.AddTableFlags), so it wins
// outright. A numeric positional is an id — datasets.Client.ResolveID tries
// strconv.Atoi before it asks the server — and refLabel prints the id in that
// case anyway. The rows group keeps the same rule in tableNameFrom, for the
// larger set of messages that group prints.
func tableNameForMessage(cmd *cobra.Command, tableArg string) string {
	if name, _ := cmd.Flags().GetString("table"); name != "" {
		return name
	}
	if _, err := strconv.Atoi(tableArg); err == nil {
		return ""
	}
	return tableArg
}

// recommendationRow is the table view's row.
//
// It exists for Strong, which is a *bool on the wire: the field is declared
// `strong: bool = True` and the route serialises with
// response_model_exclude_unset, so an ABSENT strong is the schema's default and
// means true, where a nil pointer rendered through the reflection printer would
// print an empty cell and read as false. Every constructor in the recommender
// passes strong explicitly today, which is what makes the nil arm defensive
// rather than load-bearing — and why it must not be spelled as "unknown".
type recommendationRow struct {
	Value  string `json:"value"`
	Strong bool   `json:"strong"`
}

func recommendationRows(recommendations []value_mapping.Recommendation) []recommendationRow {
	rows := make([]recommendationRow, 0, len(recommendations))
	for _, r := range recommendations {
		rows = append(rows, recommendationRow{Value: r.Value, Strong: r.Strong == nil || *r.Strong})
	}
	return rows
}

// printRecommendations renders the list. json and yaml write a List of the RAW
// wire rows with null paging — a caller reading .strong should see what the
// server sent, including the absence the row type above fills in — and name
// prints one value per line. The table view names the masked fields on stderr.
//
// The name view is hand-written rather than left to the shared printer, and it
// has to be: the shared printer prints kind/<id>, and this schema has no id. A
// value IS the identity here, which is why the format is offered at all rather
// than refused the way `get data` refuses it.
//
// Changed("output") rather than a bare read, the shape `rows check-unique` and
// `describe row` use: the flag's default is "table", and
// cmdutil.ApplyContextOutputFormat sets it from the current context when the
// user did not, so a configured `output: json` reaches this switch exactly as a
// typed -o json does.
func printRecommendations(cmd *cobra.Command, recommendations []value_mapping.Recommendation) error {
	if cmd.Flags().Changed("output") {
		format, _ := cmd.Flags().GetString("output")
		switch format {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), format, output.NewList(recommendations, nil, nil, nil, nil))
		case "name": //nolint:goconst // Keep output format handling local to this command.
			return printRecommendationNames(cmd, recommendations)
		}
	}
	if err := tableui.PrintFromCmd(cmd, recommendationRows(recommendations), valueRecommendationColumns); err != nil {
		return err
	}
	if fields := recommendationMaskedFields(recommendations); len(fields) > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Masked fields: %s. A masked value is •••••, or ••••• and its last 4 characters.\n",
			terminal.Sanitize(strings.Join(fields, ", ")))
	}
	return nil
}

// printRecommendationNames prints each recommendation's value on its own line,
// leaving out the masked ones and counting them on stderr.
func printRecommendationNames(cmd *cobra.Command, recommendations []value_mapping.Recommendation) error {
	out := cmd.OutOrStdout()
	skipped := 0
	for _, r := range recommendations {
		if r.MaskedFields != nil && len(*r.MaskedFields) > 0 {
			skipped++
			continue
		}
		if _, err := out.Write([]byte(r.Value + "\n")); err != nil {
			return err
		}
	}
	if skipped > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "%d masked value(s) left out: a masked value cannot be written back to its PII column.\n", skipped)
	}
	return nil
}

// recommendationMaskedFields is the sorted union of the recommendations'
// masked_fields.
func recommendationMaskedFields(recommendations []value_mapping.Recommendation) []string {
	seen := map[string]bool{}
	var fields []string
	for _, r := range recommendations {
		if r.MaskedFields == nil {
			continue
		}
		for _, f := range *r.MaskedFields {
			if !seen[f] {
				seen[f] = true
				fields = append(fields, f)
			}
		}
	}
	sort.Strings(fields)
	return fields
}
