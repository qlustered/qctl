// Package ruletemplate previews a TableRuleTemplate on a table and reports the
// plan's open decisions and next steps. diff and apply share it.
package ruletemplate

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/rule_merges"
	"github.com/qlustered/qctl/internal/rule_templates"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// Load reads the file at path and parses it with rule_templates.Parse.
func Load(path string) (*rule_templates.File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}
	return rule_templates.Parse(data, path)
}

// Preview is a fresh preview of a template on a table, with the names to
// render it by.
type Preview struct {
	File *rule_templates.File
	// TableID is the target table; TableName is the --table the command line
	// gave, "" otherwise.
	TableID   int
	TableName string
	Merge     *rule_merges.RuleMerge
	// Body is the preview's response body as received.
	Body       []byte
	Names      rule_merges.RenderNames
	TableRules []dataset_rules.DatasetRuleTiny
	Client     *rule_merges.Client
	// revisions is each rule revision of previewRevisions that could be
	// read, by lower-case id.
	revisions map[string]*rule_versions.RuleRevisionFull
	// migrationReviews is readMigrationReviews of the preview.
	migrationReviews map[int]migrationReview
}

// NewPreview resolves the table --table or --table-id names, has the server
// preview file's template on it, lists the table's rules, reads each rule
// revision of previewRevisions once, and finds the review alert of each draft
// migration a skipped item waits on.
func NewPreview(ctx *cmdutil.CommandContext, cmd *cobra.Command, file *rule_templates.File) (*Preview, error) {
	tableID, err := cmdutil.ResolveTableIDFromFlags(ctx, cmd)
	if err != nil {
		return nil, err
	}
	client, err := rule_merges.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	token := ctx.Credential.AccessToken
	merge, body, err := client.PreviewTemplate(token, tableID, file.Template.ToRequest())
	if err != nil {
		return nil, err
	}
	rulesClient, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}
	rules, err := rulesClient.ListAllDatasetRules(token, tableID)
	if err != nil {
		return nil, fmt.Errorf("failed to list the table's rules: %w", err)
	}
	tableName, _ := cmd.Flags().GetString("table")
	p := &Preview{
		File: file, TableID: tableID, TableName: tableName,
		Merge: merge, Body: body, TableRules: rules, Client: client,
		revisions:        readRevisions(ctx, previewRevisions(merge.Items)),
		migrationReviews: readMigrationReviews(ctx, tableID, merge.Items, rules),
	}
	p.Names = rule_merges.RenderNames{
		TargetTable: tableName,
		TargetRule:  ruleNamer(rules),
		Release:     p.release,
		Builtin:     p.builtin,
	}
	return p, nil
}

// ruleNamer returns the instance name of a rule of rules by UUID, "" for a
// UUID not among them.
func ruleNamer(rules []dataset_rules.DatasetRuleTiny) func(string) string {
	names := make(map[string]string, len(rules))
	for _, rule := range rules {
		names[strings.ToLower(rule.ID.String())] = rule.InstanceName
	}
	return func(id string) string { return names[strings.ToLower(id)] }
}

// readRevisions reads each rule revision of ids; one whose read fails is
// left out.
func readRevisions(ctx *cmdutil.CommandContext, ids []string) map[string]*rule_versions.RuleRevisionFull {
	revisions := map[string]*rule_versions.RuleRevisionFull{}
	client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return revisions
	}
	for _, id := range ids {
		if detail, err := client.GetRuleRevisionDetails(ctx.Credential.AccessToken, id); err == nil {
			revisions[strings.ToLower(id)] = detail
		}
	}
	return revisions
}

// previewRevisions is each distinct rule revision id the rule and target
// snapshots of items name.
func previewRevisions(items []rule_merges.Item) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(id string) {
		if key := strings.ToLower(id); id != "" && !seen[key] {
			seen[key] = true
			ids = append(ids, id)
		}
	}
	for _, item := range items {
		add(item.RuleSnapshot.RuleRevisionID)
		if item.TargetSnapshot != nil {
			add(item.TargetSnapshot.RuleRevisionID)
		}
	}
	return ids
}

// release is the release of rule revision id, "" when it was not read.
func (p *Preview) release(id string) string {
	if revision := p.revisions[strings.ToLower(id)]; revision != nil {
		return revision.Release
	}
	return ""
}

// Enriches reports whether rule revision id enriches field; false when the
// revision was not read.
func (p *Preview) Enriches(id, field string) bool {
	revision := p.revisions[strings.ToLower(id)]
	return revision != nil && slices.Contains(revision.EnrichesColumns, field)
}

// builtin reports a built-in rule revision id; false when it was not read.
func (p *Preview) builtin(id string) bool {
	revision := p.revisions[strings.ToLower(id)]
	return revision != nil && revision.IsBuiltin
}

// Entry returns the template entry whose key is item's template key, or nil.
func (p *Preview) Entry(item rule_merges.Item) *rule_templates.Entry {
	if item.TemplateKey == nil {
		return nil
	}
	for i := range p.File.Template.Spec.Rules {
		if p.File.Template.Spec.Rules[i].Key() == *item.TemplateKey {
			return &p.File.Template.Spec.Rules[i]
		}
	}
	return nil
}

// TableFlag is the flag that names the preview's table on a command line:
// --table with its name when the command line gave one, else --table-id.
func (p *Preview) TableFlag() string {
	if p.TableName != "" {
		return "--table " + cmdutil.ShellArg(p.TableName)
	}
	return fmt.Sprintf("--table-id %d", p.TableID)
}
