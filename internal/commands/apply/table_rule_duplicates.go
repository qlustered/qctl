package apply

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// checkTableRuleDuplicates checks attached rules and pending create suggestions,
// returning the forced update's target id, or a duplicate or read error.
func checkTableRuleDuplicates(warnOut io.Writer, rules *dataset_rules.Client, suggestions *rule_suggestions.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, revision *rule_versions.RuleRevisionFull, mapping map[string]string, fills map[string]conceptFill, upgradedFromID *uuid.UUID) (*uuid.UUID, error) {
	target, err := checkAttachedTableRuleDuplicates(warnOut, rules, token, doc, manifest, revision, mapping, fills, upgradedFromID)
	if err != nil {
		return nil, err
	}
	if err := checkPendingTableRuleCreates(warnOut, suggestions, token, doc, manifest, revision, mapping, fills); err != nil {
		return nil, err
	}
	return target, nil
}

// checkAttachedTableRuleDuplicates compares a create against the rules already on the
// table. It returns an exit-2 error when spec.instance_name is taken and
// spec.force is not set, and when a nameless create has the same revision,
// params, severity, row filter and column mapping as a live rule. Otherwise it warns on
// warnOut about a forced name collision, or about the live rules of the same
// family. It returns the forced update's target id or a duplicate or read error.
//
// colMapping and conceptFills are instantiateColumnMapping's results.
// upgradedFromID is the resolved spec.upgradedFrom, or nil.
func checkAttachedTableRuleDuplicates(warnOut io.Writer, client *dataset_rules.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull, colMapping map[string]string, conceptFills map[string]conceptFill, upgradedFromID *uuid.UUID) (*uuid.UUID, error) {
	datasetID := manifest.Spec.DatasetID
	rules, err := client.ListAllDatasetRules(token, datasetID)
	if err != nil {
		return nil, fmt.Errorf("failed to list the rules on table %d to check %s for a duplicate: %w", datasetID, doc.Source, err)
	}

	name, named := explicitInstanceName(manifest)
	if named {
		if taken := ruleNamed(rules, name); taken != nil {
			if manifest.Spec.Force == nil || !*manifest.Spec.Force {
				return nil, instanceNameTakenError(doc, datasetID, taken, upgradedFromID)
			}
			warnForcedNameCollision(warnOut, doc, datasetID, taken)
			return &taken.ID, nil
		}
	}

	siblings := liveFamilyInstances(rules, revDetail.FamilyID, upgradedFromID)
	if len(siblings) == 0 {
		return nil, nil
	}
	if !named {
		if predicted, ok := predictInstance(manifest, revDetail, colMapping, conceptFills); ok {
			for i := range siblings {
				same, err := repeatsInstance(client, token, &siblings[i], predicted)
				if err != nil {
					return nil, err
				}
				if same {
					return nil, duplicateInstanceError(doc, datasetID, &siblings[i], predicted.mapping)
				}
			}
		}
	}
	warnSameFamily(warnOut, doc, datasetID, siblings)
	return nil, nil
}

// explicitInstanceName is the manifest's instance name and whether it names
// one. A blank name names none.
func explicitInstanceName(manifest *dataset_rules.TableRuleApplyManifest) (string, bool) {
	if manifest.Spec.InstanceName == nil || strings.TrimSpace(*manifest.Spec.InstanceName) == "" {
		return "", false
	}
	return *manifest.Spec.InstanceName, true
}

// ruleNamed is the rule whose instance name equals name exactly, in any state.
func ruleNamed(rules []dataset_rules.DatasetRuleTiny, name string) *dataset_rules.DatasetRuleTiny {
	for i := range rules {
		if rules[i].InstanceName == name {
			return &rules[i]
		}
	}
	return nil
}

// isRetiredRuleState reports the states in which a rule no longer counts as an
// instance on the table: deleted, and validation_failed.
func isRetiredRuleState(state api.DatasetRuleState) bool {
	return state == api.DatasetRuleStateDeleted || state == api.DatasetRuleStateValidationFailed
}

// liveFamilyInstances is the rules of familyID outside the retired states,
// less the one upgradedFromID names.
func liveFamilyInstances(rules []dataset_rules.DatasetRuleTiny, familyID uuid.UUID, upgradedFromID *uuid.UUID) []dataset_rules.DatasetRuleTiny {
	var siblings []dataset_rules.DatasetRuleTiny
	for _, rule := range rules {
		if rule.FamilyID != familyID || isRetiredRuleState(rule.State) {
			continue
		}
		if upgradedFromID != nil && rule.ID == *upgradedFromID {
			continue
		}
		siblings = append(siblings, rule)
	}
	return siblings
}

// predictedColumnMapping is the column mapping the create ends with:
// colMapping, then each concept fill and each produced field's binding for a
// field not yet mapped.
func predictedColumnMapping(manifest *dataset_rules.TableRuleApplyManifest, colMapping map[string]string, conceptFills map[string]conceptFill) map[string]string {
	predicted := make(map[string]string, len(colMapping)+len(conceptFills))
	for field, column := range colMapping {
		predicted[field] = column
	}
	for field, fill := range conceptFills {
		if _, ok := predicted[field]; !ok {
			predicted[field] = fill.column
		}
	}
	if manifest.Spec.ProducedFields != nil {
		for _, pf := range *manifest.Spec.ProducedFields {
			if _, ok := predicted[pf.RuleFieldName]; !ok {
				predicted[pf.RuleFieldName] = pf.DatasetFieldName
			}
		}
	}
	return predicted
}

// predictedInstance is what a create sets on the rule it makes.
type predictedInstance struct {
	rowFilter  *row_filters.Filter
	revisionID uuid.UUID
	severity   api.RuleSeverity
	// params is spec.params, or {} when unset, as jsonnum.Remarshal returns
	// it.
	params  interface{}
	mapping map[string]string
}

// predictInstance is the rule the create makes: the revision, spec.severity
// or else the revision's default_severity, spec.params, and
// predictedColumnMapping. It reports false when spec.severity is unset and the
// revision has no default_severity, or when spec.params does not encode as
// JSON.
func predictInstance(manifest *dataset_rules.TableRuleApplyManifest, revDetail *rule_versions.RuleRevisionFull, colMapping map[string]string, conceptFills map[string]conceptFill) (predictedInstance, bool) {
	severity := manifest.Spec.Severity
	if severity == nil {
		severity = revDetail.DefaultSeverity
	}
	if severity == nil {
		return predictedInstance{}, false
	}
	var params map[string]interface{}
	if manifest.Spec.Params != nil {
		params = *manifest.Spec.Params
	}
	normalized, ok := normalizedParams(params)
	if !ok {
		return predictedInstance{}, false
	}
	return predictedInstance{
		rowFilter:  manifest.Spec.RowFilter,
		revisionID: revDetail.ID,
		severity:   *severity,
		params:     normalized,
		mapping:    predictedColumnMapping(manifest, colMapping, conceptFills),
	}, true
}

// normalizedParams is params, or {} when nil, as jsonnum.Remarshal returns it.
func normalizedParams(params map[string]interface{}) (interface{}, bool) {
	if params == nil {
		params = map[string]interface{}{}
	}
	return jsonnum.Remarshal(params)
}

// repeatsInstance reports whether rule has predicted's revision, severity,
// column mapping (field for field) and params. The rule's detail is fetched
// only when its listed revision, severity and column set already match.
func repeatsInstance(client *dataset_rules.Client, token string, rule *dataset_rules.DatasetRuleTiny, predicted predictedInstance) (bool, error) {
	if rule.RuleRevisionID != predicted.revisionID || rule.Severity != predicted.severity || !sameColumnSet(ruleColumns(rule), predicted.mapping) {
		return false, nil
	}
	detail, err := client.GetDatasetRuleDetail(token, rule.ID.String())
	if err != nil {
		return false, fmt.Errorf("failed to fetch table rule %s to compare it with the create: %w", rule.ID, err)
	}
	if len(detail.ColumnMappingDict) != len(predicted.mapping) {
		return false, nil
	}
	for field, column := range predicted.mapping {
		if got, ok := detail.ColumnMappingDict[field]; !ok || got != column {
			return false, nil
		}
	}
	params, ok := normalizedParams(detail.Params)
	return ok && reflect.DeepEqual(params, predicted.params) && row_filters.SameRows(row_filters.FromAPI(detail.RowFilter), predicted.rowFilter), nil
}

// ruleColumns is the table columns a listed rule binds.
func ruleColumns(rule *dataset_rules.DatasetRuleTiny) []string {
	if rule.DatasetColumns == nil {
		return nil
	}
	return *rule.DatasetColumns
}

// sameColumnSet reports whether columns and the values of mapping are the same
// set.
func sameColumnSet(columns []string, mapping map[string]string) bool {
	want := make(map[string]bool, len(mapping))
	for _, column := range mapping {
		want[column] = true
	}
	got := make(map[string]bool, len(columns))
	for _, column := range columns {
		got[column] = true
	}
	if len(got) != len(want) {
		return false
	}
	for column := range got {
		if !want[column] {
			return false
		}
	}
	return true
}

// describeListedRule renders a listed rule as `"name" (id, state: s)`.
func describeListedRule(rule *dataset_rules.DatasetRuleTiny) string {
	return fmt.Sprintf("%q (%s, state: %s)", terminal.Sanitize(rule.InstanceName), rule.ID, terminal.Sanitize(string(rule.State)))
}

// formatColumnList renders a column list sorted and de-duplicated.
func formatColumnList(columns []string) string {
	seen := make(map[string]bool, len(columns))
	unique := make([]string, 0, len(columns))
	for _, column := range columns {
		if !seen[column] {
			seen[column] = true
			unique = append(unique, column)
		}
	}
	if len(unique) == 0 {
		return "no columns"
	}
	sort.Strings(unique)
	return terminal.Sanitize(strings.Join(unique, ", "))
}

// instanceNameTakenError is the refusal for a create whose spec.instance_name
// another rule on the table already holds.
func instanceNameTakenError(doc manifestDoc, datasetID int, taken *dataset_rules.DatasetRuleTiny, upgradedFromID *uuid.UUID) error {
	prefix := fmt.Sprintf("spec.instance_name in %s is taken: table %d already has a rule named %s, and an instance name is unique per table",
		doc.Source, datasetID, describeListedRule(taken))
	switch {
	case taken.State == api.DatasetRuleStateDeleted:
		return qerrors.NewBadUsagef("%s; a deleted rule keeps its name and accepts no change, so the name stays taken: choose another spec.instance_name, or omit it for a generated one", prefix)
	case taken.State == api.DatasetRuleStateValidationFailed:
		return qerrors.NewBadUsagef("%s; a rule that failed validation keeps its name, so the name stays taken: give the retry another spec.instance_name, or omit it for a generated one", prefix)
	case upgradedFromID != nil && taken.ID == *upgradedFromID:
		return qerrors.NewBadUsagef("%s; spec.upgradedFrom names that rule, and its replacement cannot reuse the name: choose another spec.instance_name, or omit it for a generated one", prefix)
	case taken.State == api.DatasetRuleStateError:
		return qerrors.NewBadUsagef("%s; to repair that rule, apply configuration changes with metadata.id: %s and omit spec.state so the repair is validated; to unblock the table, apply metadata.id: %s with spec.state: disabled or deleted. To create another rule, choose another spec.instance_name, or omit it for a generated one", prefix, taken.ID, taken.ID)
	}
	return qerrors.NewBadUsagef("%s. To change that rule, apply it with metadata.id: %s; to replace it, set spec.upgradedFrom: %s and give the replacement another spec.instance_name (or omit it); to add another rule, choose another spec.instance_name",
		prefix, taken.ID, taken.ID)
}

// warnForcedNameCollision says what spec.force does with a taken name. It
// writes nothing for a rule in the error state.
func warnForcedNameCollision(warnOut io.Writer, doc manifestDoc, datasetID int, taken *dataset_rules.DatasetRuleTiny) {
	if taken.State == api.DatasetRuleStateError {
		return
	}
	fmt.Fprintf(warnOut, "Warning: %s sets spec.force and table %d already has a rule named %s: the server updates that rule in place and tries to enable it, with no validation dry run, instead of creating a new rule; a create staged for review is instead renamed when it is accepted\n",
		doc.Source, datasetID, describeListedRule(taken))
}

// duplicateInstanceError is the refusal for a nameless create that repeats
// rule: same revision, params, severity, row filter and column mapping.
func duplicateInstanceError(doc manifestDoc, datasetID int, rule *dataset_rules.DatasetRuleTiny, predicted map[string]string) error {
	mapping := formatColumnMapping(predicted)
	if len(predicted) > 0 {
		mapping = "(" + mapping + ")"
	}
	return qerrors.NewBadUsagef("%s repeats a rule already on table %d: %s has the same rule revision, params, severity, row filter and column mapping %s. Applying a create manifest again does not update that rule; without spec.instance_name the server adds a second instance under a generated name. To change that rule, apply it with metadata.id: %s; to replace it, set spec.upgradedFrom: %s; to add a second instance on purpose, set spec.instance_name",
		doc.Source, datasetID, describeListedRule(rule), terminal.Sanitize(mapping), rule.ID, rule.ID)
}

// warnSameFamily lists the other live instances of the rule's family that the
// create will sit beside.
func warnSameFamily(warnOut io.Writer, doc manifestDoc, datasetID int, siblings []dataset_rules.DatasetRuleTiny) {
	fmt.Fprintf(warnOut, "Warning: %s adds another instance of rule family %q to table %d, which already has:\n",
		doc.Source, terminal.Sanitize(siblings[0].FamilyName), datasetID)
	for i := range siblings {
		fmt.Fprintf(warnOut, "  %s on %s\n", describeListedRule(&siblings[i]), formatColumnList(ruleColumns(&siblings[i])))
	}
	fmt.Fprintln(warnOut, "  To change one of these instead, apply it with metadata.id: <its id>; to replace one, set spec.upgradedFrom: <its id>.")
}
