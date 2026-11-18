package apply

import (
	"fmt"
	"io"
	"reflect"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/row_filters"
	"github.com/qlustered/qctl/internal/rule_suggestions"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// checkPendingTableRuleCreates compares a create with pending create suggestions.
func checkPendingTableRuleCreates(warnOut io.Writer, client *rule_suggestions.Client, token string, doc manifestDoc, manifest *dataset_rules.TableRuleApplyManifest, revision *rule_versions.RuleRevisionFull, mapping map[string]string, fills map[string]conceptFill) error {
	predicted, predictable := predictInstance(manifest, revision, mapping, fills)
	name, named := explicitInstanceName(manifest)
	opts := rule_suggestions.ListOptions{States: []string{string(api.SuggestionStatePending)}, Limit: 1000}
	for opts.Page = 1; ; opts.Page++ {
		list, err := client.List(token, manifest.Spec.DatasetID, opts)
		if err != nil {
			return fmt.Errorf("failed to list pending rule suggestions on table %d to check %s for a duplicate: %w", manifest.Spec.DatasetID, doc.Source, err)
		}
		for _, item := range list.Results {
			if item.State != api.SuggestionStatePending || item.Action != api.SuggestionActionCreate {
				continue
			}
			suggestion, err := client.Get(token, manifest.Spec.DatasetID, item.ID.String())
			if err != nil {
				return fmt.Errorf("failed to fetch rule suggestion %s to compare it with the create: %w", item.ID, err)
			}
			if err := checkPendingTableRuleCreate(warnOut, doc, manifest.Spec.DatasetID, suggestion, revision, name, named, predicted, predictable); err != nil {
				return err
			}
		}
		if list.Next == nil || len(list.Results) == 0 {
			return nil
		}
	}
}

// checkPendingTableRuleCreate rejects a conflicting name or a nameless repeat.
func checkPendingTableRuleCreate(warnOut io.Writer, doc manifestDoc, datasetID int, suggestion *rule_suggestions.Suggestion, revision *rule_versions.RuleRevisionFull, name string, named bool, predicted predictedInstance, predictable bool) error {
	if suggestion.State != api.SuggestionStatePending || suggestion.Action != api.SuggestionActionCreate {
		return nil
	}
	if named && suggestion.InstanceName == name {
		return pendingTableRuleCreateError(doc, datasetID, suggestion, "uses an instance name already held by")
	}
	if suggestion.RuleFamilyID != revision.FamilyID {
		return nil
	}
	if !named && predictable && repeatsPendingInstance(suggestion, predicted) {
		return pendingTableRuleCreateError(doc, datasetID, suggestion, "repeats the rule revision, params, severity, row filter and column mapping of")
	}
	fmt.Fprintf(warnOut, "Warning: %s adds another instance of rule family %q to table %d beside pending create suggestion %s (%q). Inspect it with qctl describe rule-suggestion %s --table-id %d.\n",
		doc.Source, terminal.Sanitize(revision.Name), datasetID, suggestion.ID, terminal.Sanitize(suggestion.InstanceName), suggestion.ID, datasetID)
	return nil
}

// repeatsPendingInstance compares a staged create's configuration with predicted.
func repeatsPendingInstance(suggestion *rule_suggestions.Suggestion, predicted predictedInstance) bool {
	if suggestion.RuleRevisionID == nil || *suggestion.RuleRevisionID != predicted.revisionID || suggestion.Severity != predicted.severity {
		return false
	}
	mapping, ok := pendingInstanceMapping(suggestion)
	if !ok || !reflect.DeepEqual(mapping, predicted.mapping) {
		return false
	}
	var params map[string]interface{}
	if suggestion.Params != nil {
		params = *suggestion.Params
	}
	normalized, ok := normalizedParams(params)
	return ok && reflect.DeepEqual(normalized, predicted.params) && row_filters.SameRows(row_filters.FromAPI(suggestion.RowFilter), predicted.rowFilter)
}

// pendingInstanceMapping includes the bindings a staged create's outputs add.
func pendingInstanceMapping(suggestion *rule_suggestions.Suggestion) (map[string]string, bool) {
	mapping := make(map[string]string, len(suggestion.RuleColumnMapping))
	for field, column := range suggestion.RuleColumnMapping {
		mapping[field] = column
	}
	if suggestion.ProducedFields != nil {
		for _, output := range *suggestion.ProducedFields {
			field, fieldOK := output["rule_field_name"].(string)
			column, columnOK := output["dataset_field_name"].(string)
			if !fieldOK || !columnOK {
				return nil, false
			}
			if _, bound := mapping[field]; !bound {
				mapping[field] = column
			}
		}
	}
	return mapping, true
}

// pendingTableRuleCreateError points a refused create at its pending suggestion.
func pendingTableRuleCreateError(doc manifestDoc, datasetID int, suggestion *rule_suggestions.Suggestion, conflict string) error {
	return qerrors.NewBadUsagef("%s %s pending create suggestion %s (%q) on table %d. No table rule exists for that suggestion yet; applying again would create another rule when accepted, even with spec.force. Inspect it with qctl describe rule-suggestion %s --table-id %d; use qctl suggestion edit %s --table-id %d to change it, or qctl suggestion dismiss %s --table-id %d to discard it before re-applying. To add another instance on purpose, choose a different spec.instance_name",
		doc.Source, conflict, suggestion.ID, terminal.Sanitize(suggestion.InstanceName), datasetID, suggestion.ID, datasetID, suggestion.ID, datasetID, suggestion.ID, datasetID)
}
