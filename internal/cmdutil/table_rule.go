package cmdutil

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
)

// FindTableRule resolves a table-rule reference a WRITE was given against the
// table's full rule list: an r-* alias, an exact instance name, a SHORT-ID or a
// full UUID, through dataset_rules.FindRuleInList.
//
// It is the one resolver behind `table-rule move` (the rule and its
// --before/--after target) and `rows ignore-rule`/`unignore-rule` (--rule), so
// every write that names a table rule accepts the same forms and refuses the
// same ones. An alias for a rule on ANOTHER table resolves to a UUID the list
// does not carry, so it is not found here rather than acted on elsewhere.
//
// flag prefixes the error when the reference came from a flag ("--rule"), and
// is empty for a positional.
func FindTableRule(ctx *CommandContext, rules []dataset_rules.DatasetRuleTiny, input, flag string) (dataset_rules.DatasetRuleTiny, error) {
	wrap := func(err error) error {
		if flag == "" {
			return err
		}
		return fmt.Errorf("%s: %w", flag, err)
	}
	id, wasAlias, err := ResolveTableRuleAlias(ctx, input)
	if err != nil {
		return dataset_rules.DatasetRuleTiny{}, wrap(err)
	}
	ref := input
	if wasAlias {
		ref = id
	}
	rule, err := dataset_rules.FindRuleInList(rules, ref)
	if err != nil {
		if wasAlias {
			// The no-match text names the UUID the alias stood for, which the
			// user never typed.
			err = fmt.Errorf("alias %s is table-rule %s, which is not on this table: %w", input, id, err)
		}
		return dataset_rules.DatasetRuleTiny{}, wrap(err)
	}
	return rule, nil
}

// ProducedColumnWarning is datasets.FormatProducedColumnWarning over the
// produced columns changes edits in detail, with one rule-detail GET per
// distinct producing rule to name it. A rule that GET cannot name is printed
// by ID. It returns "" without a request when no produced column is edited.
func ProducedColumnWarning(ctx *CommandContext, detail *datasets.TableSchemaDetail, changes []datasets.SchemaFieldChange) string {
	edits := datasets.ProducedColumnEdits(detail, changes)
	if len(edits) == 0 {
		return ""
	}
	names := map[string]string{}
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err == nil {
		fetched := map[string]bool{}
		for _, e := range edits {
			if fetched[e.RuleID] {
				continue
			}
			fetched[e.RuleID] = true
			if rule, err := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, e.RuleID); err == nil {
				names[e.RuleID] = rule.InstanceName
			}
		}
	}
	return datasets.FormatProducedColumnWarning(edits, names, detail.DatasetID)
}

// DeletedColumnRuleWarning reports rules whose mappings or row filters read
// columns in deleted. It returns "" without a request when deleted is empty.
func DeletedColumnRuleWarning(ctx *CommandContext, datasetID int, deleted []string) string {
	return SchemaColumnRuleWarning(ctx, datasetID, deleted, nil)
}

// SchemaColumnRuleWarning reports rule readers of deleted or retyped columns.
func SchemaColumnRuleWarning(ctx *CommandContext, datasetID int, deleted, retyped []string) string {
	if len(deleted) == 0 && len(retyped) == 0 {
		return ""
	}
	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return schemaColumnRuleListFailure(err, deleted, retyped)
	}
	rules, err := client.ListAllDatasetRules(ctx.Credential.AccessToken, datasetID)
	if err != nil {
		return schemaColumnRuleListFailure(err, deleted, retyped)
	}
	readers := datasets.FilterColumnReaders(rules, deleted)
	mappers := datasets.MappedColumnReaders(rules, deleted)
	details := map[string]*api.DatasetRuleDetailSchema{}
	readDetail := func(id string, state api.DatasetRuleState) *api.DatasetRuleDetailSchema {
		if state != api.DatasetRuleStateEnabled {
			return nil
		}
		if detail, fetched := details[id]; fetched {
			return detail
		}
		detail, _ := client.GetDatasetRuleDetail(ctx.Credential.AccessToken, id)
		details[id] = detail
		return detail
	}
	filterReaders := map[string]bool{}
	for i := range readers {
		filterReaders[readers[i].RuleID] = true
		if rule := readDetail(readers[i].RuleID, readers[i].State); rule != nil {
			builtin := rule.IsBuiltin
			readers[i].Builtin = &builtin
		}
	}
	for i := range mappers {
		r := &mappers[i]
		r.Detail = readDetail(r.RuleID, r.State)
		r.FilterReadsDeleted = filterReaders[r.RuleID]
	}
	populateCAFOptionalColumns(ctx, mappers)
	return datasets.FormatRowFilterColumnWarning(readers, datasetID) +
		datasets.FormatMappedColumnWarning(mappers, datasetID) +
		datasets.FormatRowFilterTypeWarning(datasets.FilterColumnReaders(rules, retyped), datasetID)
}

func populateCAFOptionalColumns(ctx *CommandContext, rules []datasets.MappedColumnRule) {
	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return
	}
	revisions := map[string]*api.RuleRevisionFullSchema{}
	for i := range rules {
		r := &rules[i]
		if r.Detail == nil || !r.Detail.IsBuiltin || !r.Detail.IsCaf || r.FilterReadsDeleted {
			continue
		}
		id := r.Detail.RuleRevision.ID.String()
		revision, fetched := revisions[id]
		if !fetched {
			revision, _ = client.GetRuleRevisionDetails(ctx.Credential.AccessToken, id)
			revisions[id] = revision
		}
		if revision != nil {
			r.OptionalColumns = revision.OptionalColumns
		}
	}
}

// schemaColumnRuleListFailure renders the first line of err as a warning.
func schemaColumnRuleListFailure(err error, deleted, retyped []string) string {
	reason, _, _ := strings.Cut(err.Error(), "\n")
	action := "deletes column(s)"
	if len(retyped) > 0 {
		action = "changes column types"
		if len(deleted) > 0 {
			action = "deletes column(s) or changes column types"
		}
	}
	return terminal.Sanitize("Warning: this migration "+action+", and qctl could not list the table's rules to check their column mappings or row filters: "+
		strings.TrimSpace(reason)) + "\n\n"
}
