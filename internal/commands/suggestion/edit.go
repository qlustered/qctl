package suggestion

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newEditCommand() *cobra.Command {
	var setParams []string
	var unsetParams []string
	var mapColumns []string
	var unsetColumns []string

	cmd := &cobra.Command{
		Use:   "edit <suggestion-id>",
		Short: "Edit a pending rule suggestion before accepting it",
		Long: `Change what a PENDING rule suggestion proposes, before you accept it.

Only pending suggestions can be edited: the backend answers 409 for a
suggestion in any other state.

--set-param and --map-column both MERGE into the suggestion's current value;
--unset-param and --unset-column remove named keys from those merged values.
qctl reads the suggestion once before every edit and re-sends the whole field.
The backend assigns each field outright rather than merging, so a key you did
not name would otherwise be dropped. The same read checks whether it is a
revalidation candidate, including for --severity alone.

A key cannot be both set and unset in one invocation, and an edit that leaves
a param holding masked text (•••••, or ••••• and 4 characters) is refused with
exit 2: the server shows every param of a suggestion on a masked PII column
that way to a viewer who has it masked, and the re-send would store that text.
Such a viewer must set or unset every param. qctl re-sends every
number exactly as the backend returned it, including numbers in existing params
that the edit does not name. That is not always the number the rule stores: on
a percent column the backend returns a range_rule's min and max in display
units -- the stored fraction times 100 -- and reads them back the same way, so
the re-send restores what was there (see 'qctl explain rule-suggestion').

A --set-param value is read as JSON when it parses as one (5 is a number,
true is a boolean) and as a plain string otherwise. Quote it to force a
string: --set-param threshold='"5"' sends the text 5, not the number.

A pending revalidation candidate cannot be edited here: qctl refuses it with
exit 2 before sending a patch, and the backend refuses it with 409. Edit its
TableRule manifest with 'qctl apply -f <file>' instead. Its validation run
decides the outcome; 'qctl suggestion accept' also refuses a candidate.

--row-filter-file replaces the suggestion's whole row filter with a bare row
filter document, YAML or JSON (conditions, predicate, description); the
description is required. The backend checks the filter against the table's
columns: 422 for one that does not compile or a predicate on a custom rule, and
409 when the suggestion has no rule release. --remove-row-filter clears it, and
accepting an update suggestion then removes the rule's row filter.
'qctl describe rule-suggestion' shows the staged filter when it differs from
the rule's.

-o json|yaml prints the edited suggestion and moves the result lines to stderr;
-o name prints rule-suggestion/<uuid>.

Examples:
  qctl suggestion edit 550e8400-... --table-id 42 --set-param threshold=5
  qctl suggestion edit 550e8400-... --table-id 42 --unset-param legacy_threshold
  qctl suggestion edit 550e8400-... --table orders --severity warning
  qctl suggestion edit 550e8400-... --table orders --map-column email=customer_email
  qctl suggestion edit 550e8400-... --table orders --row-filter-file filter.yaml
  qctl suggestion edit 550e8400-... --table orders --remove-row-filter

Then: qctl suggestion accept 550e8400-... --table orders`,
		Annotations: cmdutil.WithFormats(catalog.For("rule-suggestion"), "table", "json", "yaml", "name"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// parseIDs is the validator accept and dismiss reach through
			// parseSelection, so all three verbs refuse the same typo with the
			// same message and the same exit 2. Reusing it here is not
			// cosmetic: without it the id would first be parsed inside
			// rule_suggestions.Client, whose plain fmt.Errorf carries no exit
			// code (so GetExitCode degrades to 1), and only after Bootstrap had
			// already run. Args is ExactArgs(1), so a success is exactly one id.
			ids, err := parseIDs(args)
			if err != nil {
				return err
			}
			suggestionID := ids[0]

			fields, err := parseEditFlags(cmd, setParams, unsetParams, mapColumns, unsetColumns)
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
			client, err := rule_suggestions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			if err := mergeCurrentValues(
				client, token, tableID, suggestionID, &fields, unsetParams, unsetColumns,
			); err != nil {
				return err
			}

			result, err := client.Patch(token, tableID, suggestionID, fields)
			if err != nil {
				return err
			}
			out := cmdutil.NewResultWriter(cmd)
			fmt.Fprintf(out.Human, "suggestion %s updated — tablerule %q not changed yet\n",
				result.ID, result.InstanceName)
			fmt.Fprintf(out.Human, "Next: qctl suggestion accept %s %s\n",
				result.ID, tableSelectorArg(cmd, tableID))
			return out.Write(result, "rule-suggestion/"+result.ID.String())
		},
	}

	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmd.Flags().StringArrayVar(&setParams, "set-param", nil,
		"Set one rule parameter as key=value (repeatable; merges into the current params)")
	cmd.Flags().StringArrayVar(&unsetParams, "unset-param", nil,
		"Remove one rule parameter by key (repeatable; applies after merging current params)")
	cmd.Flags().StringArrayVar(&mapColumns, "map-column", nil,
		"Map a rule input to a dataset column as generic=dataset (repeatable; merges into the current mapping)")
	cmd.Flags().StringArrayVar(&unsetColumns, "unset-column", nil,
		"Remove one rule input mapping by key (repeatable; applies after merging the current mapping)")
	cmd.Flags().String("severity", "", fmt.Sprintf(
		"What the rule does with the severity its findings declare: %s (left alone when the flag is not given)",
		severityNames()))
	cmd.Flags().String("row-filter-file", "",
		"Row filter file (YAML or JSON, with a description) that replaces the suggestion's row filter")
	cmd.Flags().Bool("remove-row-filter", false, "Clear the suggestion's row filter")
	// The retired boolean stays REGISTERED so a script still on it gets the
	// migration message and its mapping rather than cobra's bare "unknown
	// flag". Hidden, not MarkDeprecated: deprecated flags still work, and this
	// one cannot — the wire field it wrote is gone from PatchSuggestionRequest.
	cmd.Flags().Bool("treat-as-alert", false, "")
	if err := cmd.Flags().MarkHidden("treat-as-alert"); err != nil {
		panic(err) // a hidden flag that is not registered is a programming error
	}
	return cmd
}

// tableSelectorArg keeps the runnable breadcrumb in the same selector form the
// user chose. --table is name-only, so preserving it avoids turning a readable
// name into an unrelated numeric spelling; ShellArg keeps names with spaces or
// metacharacters pasteable.
func tableSelectorArg(cmd *cobra.Command, resolvedID int) string {
	if cmd.Flags().Changed("table") {
		name, _ := cmd.Flags().GetString("table")
		return "--table " + cmdutil.ShellArg(name)
	}
	return fmt.Sprintf("--table-id %d", resolvedID)
}

// severityNames renders the accepted --severity values in the order the
// rejection message names them. dataset_rules.SeverityValues is the single
// definition of that vocabulary, shared with the TableRule manifest's
// spec.severity, so the two cannot drift apart.
func severityNames() string {
	names := make([]string, len(dataset_rules.SeverityValues))
	for i, v := range dataset_rules.SeverityValues {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// mergeCurrentValues reads the suggestion, refuses a revalidation candidate,
// and merges parameter and column edits into its current values.
func mergeCurrentValues(
	client *rule_suggestions.Client,
	token string,
	tableID int,
	suggestionID string,
	fields *rule_suggestions.PatchFields,
	unsetParams []string,
	unsetColumns []string,
) error {
	current, err := client.Get(token, tableID, suggestionID)
	if err != nil {
		return fmt.Errorf("failed to read the suggestion before editing it: %w", err)
	}
	if rule_suggestions.IsCandidate(current) {
		return qerrors.NewBadUsagef(
			"suggestion %s is a revalidation candidate; edit its TableRule manifest with 'qctl apply -f <file>' instead",
			suggestionID)
	}

	if fields.Params != nil {
		if current.Params != nil {
			if err := dataset_rules.RefuseMaskedCarriedParams("suggestion "+terminal.Sanitize(suggestionID), *current.Params, *fields.Params, unsetParams); err != nil {
				return err
			}
		}
		merged := map[string]interface{}{}
		// api.DatasetRuleSuggestionSchema.Params is a *map, so the nil check is
		// on the pointer before it is dereferenced.
		if current.Params != nil {
			for k, v := range *current.Params {
				merged[k] = v
			}
		}
		for k, v := range *fields.Params {
			merged[k] = v
		}
		for _, k := range unsetParams {
			delete(merged, k)
		}
		fields.Params = &merged
	}

	if fields.RuleColumnMapping != nil {
		merged := map[string]string{}
		// api.DatasetRuleSuggestionSchema.RuleColumnMapping is a plain map, not
		// a pointer, so ranging a suggestion that has none is a safe no-op and
		// needs no guard. Note the schema also carries ColumnMappingDict, which
		// the detail builder fills from this same column
		// (../atlas _get_suggestion_detail,
		// capi/capi/controllers/rule_view_controller.py) — the field PATCH
		// writes is this one.
		for k, v := range current.RuleColumnMapping {
			merged[k] = v
		}
		for k, v := range *fields.RuleColumnMapping {
			merged[k] = v
		}
		for _, k := range unsetColumns {
			delete(merged, k)
		}
		fields.RuleColumnMapping = &merged
	}
	return nil
}

// parseEditFlags turns the flags, and the file --row-filter-file names, into a
// PatchFields.
func parseEditFlags(
	cmd *cobra.Command,
	setParams, unsetParams, mapColumns, unsetColumns []string,
) (rule_suggestions.PatchFields, error) {
	var f rule_suggestions.PatchFields

	params, err := parseParamEditFlags(cmd, setParams, unsetParams)
	if err != nil {
		return f, err
	}
	f.Params = params

	mapping, err := parseColumnEditFlags(cmd, mapColumns, unsetColumns)
	if err != nil {
		return f, err
	}
	f.RuleColumnMapping = mapping

	if err := parseSeverityFlag(cmd, &f); err != nil {
		return f, err
	}

	rowFilter, err := parseRowFilterFlags(cmd)
	if err != nil {
		return f, err
	}
	f.RowFilter = rowFilter

	if f.Params == nil && f.RuleColumnMapping == nil && f.Severity == nil && !f.RowFilter.Named() {
		return f, qerrors.NewBadUsage(
			"nothing to edit: pass at least one of --set-param, --unset-param, --map-column, --unset-column, " +
				"--severity, --row-filter-file or --remove-row-filter")
	}
	return f, nil
}

// parseRowFilterFlags settles --row-filter-file and --remove-row-filter. Naming
// both is refused whatever their values; --remove-row-filter=false alone keeps
// the filter.
func parseRowFilterFlags(cmd *cobra.Command) (row_filters.Edit, error) {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"row-filter-file", "remove-row-filter"}); err != nil {
		return row_filters.Keep(), err
	}
	if cmd.Flags().Changed("row-filter-file") {
		path, _ := cmd.Flags().GetString("row-filter-file")
		filter, err := row_filters.ReadFile(path, "--row-filter-file", true)
		if err != nil {
			return row_filters.Keep(), err
		}
		return row_filters.Replace(filter), nil
	}
	if remove, _ := cmd.Flags().GetBool("remove-row-filter"); remove {
		return row_filters.Remove(), nil
	}
	return row_filters.Keep(), nil
}

func parseParamEditFlags(
	cmd *cobra.Command,
	setParams, unsetParams []string,
) (*map[string]interface{}, error) {
	edit, err := dataset_rules.ParseParamEdit(setParams, unsetParams)
	if err != nil {
		return nil, err
	}
	if len(setParams) == 0 && !cmd.Flags().Changed("unset-param") {
		return nil, nil
	}
	values := edit.Set
	if values == nil {
		values = map[string]interface{}{}
	}
	return &values, nil
}

func parseColumnEditFlags(
	cmd *cobra.Command,
	mapColumns, unsetColumns []string,
) (*map[string]string, error) {
	var mapping *map[string]string
	if len(mapColumns) > 0 {
		values := map[string]string{}
		for _, kv := range mapColumns {
			k, v, err := splitKV("--map-column", kv)
			if err != nil {
				return nil, err
			}
			values[k] = v
		}
		mapping = &values
	}
	if !cmd.Flags().Changed("unset-column") {
		return mapping, nil
	}
	if err := validateUnsetKeys("--unset-column", unsetColumns, mapping); err != nil {
		return nil, err
	}
	if mapping == nil {
		values := map[string]string{}
		mapping = &values
	}
	return mapping, nil
}

func validateUnsetKeys[T any](flag string, keys []string, setValues *map[string]T) error {
	if len(keys) == 0 {
		return qerrors.NewBadUsagef("%s cannot be empty: name a key to remove", flag)
	}
	for _, key := range keys {
		if key == "" {
			return qerrors.NewBadUsagef("%s cannot contain an empty key", flag)
		}
		if setValues != nil {
			if _, conflicts := (*setValues)[key]; conflicts {
				return qerrors.NewBadUsagef("key %q cannot be both set and unset in one invocation", key)
			}
		}
	}
	return nil
}

// parseSeverityFlag settles --severity, and the boolean it replaced.
//
// Only a flag the user actually typed goes on the wire, so "unset" stays
// distinct from a value that happens to match what the suggestion holds.
func parseSeverityFlag(cmd *cobra.Command, f *rule_suggestions.PatchFields) error {
	// The retired key first, so a script still on it gets the migration
	// message rather than "nothing to edit".
	//
	// Both of its values are expressible in the new field. The old split was
	// `treat_as_alert AND issue.severity == blocker`, so `true` left each
	// finding on the severity the rule declared -- what as_declared does --
	// while `false` demoted every finding to a warning, which is what warning
	// does. blocker has no old equivalent. This is a client-side reading of
	// the two mechanisms, not a backend migration: nothing in atlas maps one
	// to the other, and stored suggestions were not converted by any alembic
	// revision. Same mapping and same reasoning as the TableRule manifest's
	// dataset_rules.ValidateManifestSeverity.
	if cmd.Flags().Changed("treat-as-alert") {
		old, _ := cmd.Flags().GetBool("treat-as-alert")
		replacement := api.RuleSeverityAsDeclared
		if !old {
			replacement = api.RuleSeverityWarning
		}
		return qerrors.NewBadUsagef(
			"--treat-as-alert is no longer a field on a rule suggestion; use `--severity %s` for --treat-as-alert=%t (expected: %s)",
			replacement, old, severityNames())
	}

	if !cmd.Flags().Changed("severity") {
		return nil
	}
	raw, _ := cmd.Flags().GetString("severity")
	severity := api.RuleSeverity(raw)
	if !slices.Contains(dataset_rules.SeverityValues, severity) {
		return qerrors.NewBadUsagef("invalid --severity %q (expected one of: %s)", raw, severityNames())
	}
	f.Severity = &severity
	return nil
}

func splitKV(flag, kv string) (string, string, error) {
	k, v, found := strings.Cut(kv, "=")
	if !found || k == "" {
		return "", "", qerrors.NewBadUsagef("invalid %s %q: expected key=value", flag, kv)
	}
	return k, v, nil
}
