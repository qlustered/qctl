package get

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/pkg/timeutil"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/spf13/cobra"
)

const (
	ruleSuggestionColumns         = "id,rule,dataset_columns,action,state,source,created"
	ruleSuggestionColumnsWithKind = "id,rule,type,dataset_columns,action,state,source,created"
)

// suggestionStates is the complete SuggestionState vocabulary, in the order
// atlas declares it (common/common/types.py). `failed` is reachable
// only by a revalidation candidate the backend resolved as failed (see
// rule_suggestions.PhaseFailed) — an ordinary staged edit never reaches it.
//
// Every element is an api.* constant rather than a string literal, so a
// RENAMED value fails to compile. TestSuggestionStatesCoverTheGeneratedEnum
// catches the other half: a value ADDED to the spec that nobody added here,
// which would make --state reject a state the backend considers legitimate.
var suggestionStates = []api.SuggestionState{
	api.SuggestionStatePending,
	api.SuggestionStateApplied,
	api.SuggestionStateDismissed,
	api.SuggestionStateFailed,
}

// suggestionSources is the complete SuggestionSource vocabulary. As with
// suggestionStates, typed generated constants make a rename fail to compile,
// while the matching AST test catches a value added by a later client regen.
var suggestionSources = []api.SuggestionSource{
	api.Periodic,
	api.Profiling,
	api.RecreatedPeriod,
	api.User,
}

// validSuggestionStates is suggestionStates as strings, in declaration order.
// It is what --state accepts, what the rejection message lists and what builds
// the flag's help text, so the vocabulary a user is shown and the vocabulary
// the CLI accepts cannot drift apart.
var validSuggestionStates = suggestionStateNames()

// validSuggestionSources drives validation, the error message, and flag help.
var validSuggestionSources = suggestionSourceNames()

func suggestionStateNames() []string {
	out := make([]string, len(suggestionStates))
	for i, s := range suggestionStates {
		out[i] = string(s)
	}
	return out
}

func suggestionSourceNames() []string {
	out := make([]string, len(suggestionSources))
	for i, source := range suggestionSources {
		out[i] = string(source)
	}
	return out
}

// ruleSuggestionRow is the table-display projection of a suggestion.
// Type is blank unless --show-kind was passed.
//
// Severity is opt-in rather than default: on a suggestion staged under a
// draft migration it is a snapshot of what the live rule already holds
// (../atlas patch_dataset_rule, capi/capi/views/rule_view.py), so as a
// default column it would mostly restate `qctl get table-rules`. It is here
// because it is what `qctl suggestion edit --severity` writes and what
// accepting the suggestion puts on the rule (suggestion_view.py
// _apply_accept), and until now no list view could show it at all.
type ruleSuggestionRow struct {
	ID             string `json:"id"`
	Rule           string `json:"rule"`
	Type           string `json:"type"`
	DatasetColumns string `json:"dataset_columns"`
	Action         string `json:"action"`
	State          string `json:"state"`
	Source         string `json:"source"`
	Severity       string `json:"severity"`
	Params         string `json:"params"`
	Created        string `json:"created"`
}

// NewRuleSuggestionsCommand creates the get rule-suggestions command.
func NewRuleSuggestionsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rule-suggestions [table-id-or-name]",
		Short: "List proposed new rules and rule changes for a table",
		Long: `List proposals to create, update or delete a table's rules.

Profiling proposes new rules. Your own rule instantiations and edits can also
be staged for schema review or validation.

By default only 'pending' suggestions are shown (the actionable inbox).
Pass --state explicitly to see applied, dismissed or failed ones.

The table can be given as a positional argument (ID or name), or via
--table <name> or --table-id <id>.

Use --show-params to add proposed parameters to the table, or select params
with --columns. This reads each displayed suggestion's full detail.
Use -o json|yaml for the compact list response; --show-params is inert there
and under -o name. For full structured parameters, describe one suggestion.

Resolve a pending suggestion with:
  qctl describe rule-suggestion <id> --table-id <id>
  qctl suggestion accept <id> --table-id <id>
  qctl suggestion dismiss <id> --table-id <id>

How staging works: qctl guide instantiate-rule`,
		Annotations: cmdutil.WithArgKinds(catalog.For("rule-suggestion"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE:        runRuleSuggestions,
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().String("columns", "", "Comma-separated list of columns to display (table format only)\n"+
		"Available: "+strings.Join(cmdutil.DeclareColumns(cmd, []ruleSuggestionRow(nil)), ", "))
	cmd.Flags().Bool("show-kind", false,
		"Fill the TYPE column with candidate|edit from the list response.")
	cmd.Flags().Bool("show-params", false,
		"Show proposed parameters (one extra request per displayed suggestion; table format only)")
	cmd.Flags().StringArray("state", nil, fmt.Sprintf(
		"Filter by state: %s (repeatable; default: pending)", strings.Join(validSuggestionStates, ", ")))
	cmd.Flags().String("source", "", "Filter by source: "+strings.Join(validSuggestionSources, ", "))
	cmd.Flags().Int("page", 1, "Page number")
	cmd.Flags().Int("limit", 20, "Suggestions per page")
	return cmd
}

func runRuleSuggestions(cmd *cobra.Command, args []string) error {
	// Settled before ResolveTableID, which costs an API call: --page and
	// --limit are decidable locally, and a bad one used to be dropped on the
	// way to the server rather than reported.
	opts, err := ruleSuggestionListOptions(cmd)
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
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, err := cmdutil.ResolveTableID(ctx, cmd, positional)
	if err != nil {
		return err
	}
	client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	list, err := client.List(ctx.Credential.AccessToken, tableID, opts)
	if err != nil {
		return fmt.Errorf("failed to list rule suggestions: %w", err)
	}
	var params map[string]string
	if wantsSuggestionParams(cmd) {
		params, err = fetchSuggestionParams(client, ctx.Credential.AccessToken, tableID, list)
		if err != nil {
			return err
		}
	}
	return printRuleSuggestions(cmd, ctx, list, opts.Page, params)
}

func wantsSuggestionParams(cmd *cobra.Command) bool {
	format, _ := cmd.Flags().GetString("output")
	if format != "table" { //nolint:goconst // Keep output format handling local to this command.
		return false
	}
	columns, _ := cmd.Flags().GetString("columns")
	if columns != "" {
		return slices.Contains(output.ParseColumns(columns), "params")
	}
	showParams, _ := cmd.Flags().GetBool("show-params")
	return showParams
}

func fetchSuggestionParams(client *rule_suggestions.Client, token string, tableID int, list *rule_suggestions.SuggestionList) (map[string]string, error) {
	params := make(map[string]string, len(list.Results))
	for _, row := range list.Results {
		id := row.ID.String()
		detail, err := client.Get(token, tableID, id)
		if err != nil {
			return nil, fmt.Errorf("failed to get parameters for rule suggestion %s: %w", id, err)
		}
		encoded, err := json.Marshal(detail.Params)
		if err != nil {
			return nil, fmt.Errorf("failed to encode parameters for rule suggestion %s: %w", id, err)
		}
		params[id] = string(encoded)
	}
	return params, nil
}

func ruleSuggestionListOptions(cmd *cobra.Command) (rule_suggestions.ListOptions, error) {
	states, _ := cmd.Flags().GetStringArray("state")
	if !cmd.Flags().Changed("state") {
		states = []string{"pending"}
	}
	if err := validateSuggestionStates(states); err != nil {
		return rule_suggestions.ListOptions{}, err
	}
	source, _ := cmd.Flags().GetString("source")
	if err := validateSuggestionSource(source); err != nil {
		return rule_suggestions.ListOptions{}, err
	}
	page, limit, err := paginationFlags(cmd)
	if err != nil {
		return rule_suggestions.ListOptions{}, err
	}
	return rule_suggestions.ListOptions{States: states, Source: source, Page: page, Limit: limit}, nil
}

// validateSuggestionSource rejects an unknown --source before bootstrap or a
// table-name lookup. The list route types this query as SuggestionSource, so a
// typo would otherwise pay for a request only to receive a 422.
func validateSuggestionSource(source string) error {
	if source == "" || slices.Contains(validSuggestionSources, source) {
		return nil
	}
	return qerrors.NewBadUsagef("invalid --source %q (valid: %s)",
		source, strings.Join(validSuggestionSources, ", "))
}

// validateSuggestionStates rejects an unknown --state client-side, at exit 2
// before any request is made.
//
// The check is not optional politeness: the flag is sent verbatim, and the
// backend answers an unknown value with a 422 (list_suggestions in
// suggestion_view.py types the query as list[SuggestionState] over a StrEnum
// with no lenient _missing_) whose object-shaped body the generated client
// cannot surface. Rejecting locally costs no request and no table-name lookup,
// and says what the valid values are.
func validateSuggestionStates(states []string) error {
	for _, s := range states {
		if !slices.Contains(validSuggestionStates, s) {
			return qerrors.NewBadUsagef("invalid --state %q (valid: %s)",
				s, strings.Join(validSuggestionStates, ", "))
		}
	}
	return nil
}

// kindOf returns the TYPE cell for one row. showKind is false when --show-kind
// was not passed, in which case the column stays blank.
func kindOf(showKind bool, s rule_suggestions.SuggestionTiny) string {
	if !showKind {
		return ""
	}
	if s.IsRevalidationCandidate != nil && *s.IsRevalidationCandidate {
		return "candidate"
	}
	return "edit"
}

func printRuleSuggestions(cmd *cobra.Command, ctx *cmdutil.CommandContext, list *rule_suggestions.SuggestionList, page int, params map[string]string) error {
	if cmd.Flags().Changed("output") {
		outputFormat, _ := cmd.Flags().GetString("output")
		switch outputFormat {
		case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
			return output.WriteRaw(cmd.OutOrStdout(), outputFormat, list)
		}
	}
	// --show-kind is inert for -o json|yaml|name, which emit the raw response,
	// matching how those formats already ignore --columns.
	showKind := false
	if requested, _ := cmd.Flags().GetBool("show-kind"); requested {
		if format, _ := cmd.Flags().GetString("output"); format != "name" { //nolint:goconst // Keep output format handling local to this command.
			showKind = true
		}
	}
	if len(list.Results) == 0 {
		skip, err := printEmptyResult(cmd, ctx, "rule suggestions", []ruleSuggestionRow{}, list.TotalRows)
		if err != nil {
			return err
		}
		if skip {
			return nil
		}
	}
	printContextBanner(cmd, ctx)
	rows := make([]ruleSuggestionRow, 0, len(list.Results))
	for _, s := range list.Results {
		columns := ""
		if s.DatasetColumns != nil {
			columns = strings.Join(*s.DatasetColumns, ",")
		}
		rows = append(rows, ruleSuggestionRow{
			ID: s.ID.String(), Rule: s.InstanceName,
			Type:           kindOf(showKind, s),
			DatasetColumns: columns,
			Action:         string(s.Action), State: string(s.State),
			Source: string(s.Source), Severity: string(s.Severity),
			Params:  params[s.ID.String()],
			Created: timeutil.FormatRelative(s.CreatedAt),
		})
	}
	defaultColumns := ruleSuggestionColumns
	if showKind {
		defaultColumns = ruleSuggestionColumnsWithKind
	}
	if showParams, _ := cmd.Flags().GetBool("show-params"); showParams {
		defaultColumns += ",params"
	}
	if err := tableui.PrintFromCmd(cmd, rows, defaultColumns); err != nil {
		return err
	}
	if list.Next != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "Showing %d results. More available: use --page %d.\n", len(list.Results), page+1)
	}
	return nil
}
