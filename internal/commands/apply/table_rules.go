package apply

import (
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_rules"
	"github.com/qlustered/qctl/internal/pkg/jsonschemautil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// getRulesOrgContext retrieves the current organization ID and name for rule operations.
func getRulesOrgContext(ctx *cmdutil.CommandContext) (string, string, error) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = "Unknown"
	}
	return orgID, orgName, nil
}

// resolveColumnMapping returns the column mapping from the manifest, or nil if not set.
func resolveColumnMapping(manifest *dataset_rules.TableRuleApplyManifest) map[string]string {
	if len(manifest.Spec.ColumnMapping) > 0 {
		return manifest.Spec.ColumnMapping
	}
	return nil
}

// validateParamsAgainstSchema fetches the rule revision's param_schema and validates params.
func validateParamsAgainstSchema(rvClient *rule_versions.Client, token, ruleRevisionID string, params map[string]interface{}) error {
	detail, err := rvClient.GetRuleRevisionDetails(token, ruleRevisionID)
	if err != nil {
		return fmt.Errorf("failed to fetch rule revision details for param validation: %w", err)
	}
	return jsonschemautil.ValidateParams(params, detail.ParamSchema)
}

// applyTableRuleYAML is the entry point for the generic "apply -f" dispatcher.
// It bootstraps auth context and delegates to applyTableRuleManifest.
func applyTableRuleYAML(cmd *cobra.Command, filePath string) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	orgID, orgName, err := getRulesOrgContext(ctx)
	if err != nil {
		return err
	}

	client, err := dataset_rules.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create dataset rules client: %w", err)
	}

	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create rule versions client: %w", err)
	}

	return applyTableRuleManifest(client, rvClient, ctx.Credential.AccessToken, filePath, orgID, orgName, false)
}

// applyTableRuleManifest handles a single table rule YAML manifest file.
func applyTableRuleManifest(client *dataset_rules.Client, rvClient *rule_versions.Client, token, filePath, orgID, orgName string, yes bool) error {
	// Read file
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	// Parse YAML manifest
	var manifest dataset_rules.TableRuleApplyManifest
	if err := yaml.Unmarshal(fileBytes, &manifest); err != nil {
		return fmt.Errorf("failed to parse YAML manifest %s: %w", filePath, err)
	}

	// Validate common fields
	if manifest.APIVersion != "qluster.ai/v1" {
		return fmt.Errorf("unsupported apiVersion %q in %s (expected: qluster.ai/v1)", manifest.APIVersion, filePath)
	}
	if manifest.Kind != "TableRule" {
		return fmt.Errorf("unsupported kind %q in %s (expected: TableRule)", manifest.Kind, filePath)
	}

	// Determine mode
	if manifest.IsPatch() {
		return applyTableRulePatch(client, rvClient, token, filePath, orgID, orgName, &manifest, yes)
	}
	if manifest.IsInstantiate() {
		return applyTableRuleInstantiate(client, rvClient, token, filePath, orgID, orgName, &manifest, yes)
	}

	return fmt.Errorf("cannot determine operation for %s: provide metadata.id for patch or spec.rule_revision_id for instantiate", filePath)
}

// applyTableRulePatch patches an existing table rule.
func applyTableRulePatch(client *dataset_rules.Client, rvClient *rule_versions.Client, token, filePath, orgID, orgName string, manifest *dataset_rules.TableRuleApplyManifest, yes bool) error {
	if manifest.Spec.DatasetID == 0 {
		return fmt.Errorf("spec.dataset_id is required for patch in %s", filePath)
	}

	// Resolve column mapping
	colMapping := resolveColumnMapping(manifest)

	// Build patch request
	patchReq := api.PatchDatasetRuleJSONRequestBody{
		InstanceName: manifest.Spec.InstanceName,
		State:        manifest.Spec.State,
		TreatAsAlert: manifest.Spec.TreatAsAlert,
		Params:       manifest.Spec.Params,
	}
	if colMapping != nil {
		patchReq.ColumnMappingDict = &colMapping
	}

	// Check if anything is being patched
	if patchReq.InstanceName == nil && patchReq.State == nil && patchReq.TreatAsAlert == nil && patchReq.Params == nil && patchReq.ColumnMappingDict == nil {
		return fmt.Errorf("no patchable fields found in %s (supported: spec.instance_name, spec.state, spec.treat_as_alert, spec.params, spec.column_mapping)", filePath)
	}

	// Validate params against schema if provided
	if patchReq.Params != nil {
		detail, err := client.GetDatasetRuleDetail(token, manifest.Metadata.ID)
		if err != nil {
			return fmt.Errorf("failed to fetch existing table rule for param validation: %w", err)
		}
		if err := validateParamsAgainstSchema(rvClient, token, detail.RuleRevision.ID.String(), *patchReq.Params); err != nil {
			return err
		}
	}

	// Display confirmation
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("Table Rule ID: %s\n", manifest.Metadata.ID)
	fmt.Printf("Dataset ID   : %d\n", manifest.Spec.DatasetID)
	fmt.Printf("File         : %s\n", filePath)
	fmt.Println()
	fmt.Println("Changes to apply:")
	if patchReq.InstanceName != nil {
		fmt.Printf("  instance_name    : %s\n", *patchReq.InstanceName)
	}
	if patchReq.State != nil {
		fmt.Printf("  state            : %s\n", *patchReq.State)
	}
	if patchReq.TreatAsAlert != nil {
		fmt.Printf("  treat_as_alert   : %v\n", *patchReq.TreatAsAlert)
	}
	if patchReq.Params != nil {
		fmt.Printf("  params           : %v\n", *patchReq.Params)
	}
	if patchReq.ColumnMappingDict != nil {
		fmt.Printf("  column_mapping   : %v\n", *patchReq.ColumnMappingDict)
	}
	fmt.Println()

	// Prompt for confirmation
	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo("Apply these changes to the table rule?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Printf("Skipping %s\n\n", filePath)
			return nil
		}
	}

	// Patch rule
	fmt.Printf("Patching table rule %s...\n", manifest.Metadata.ID)
	result, err := client.PatchDatasetRule(token, manifest.Spec.DatasetID, manifest.Metadata.ID, patchReq)
	if err != nil {
		return fmt.Errorf("table rule patch failed for %s: %w", filePath, err)
	}

	fmt.Printf("Table rule updated: %s (%s)\n\n", result.InstanceName, result.ID.String())
	return nil
}

// applyTableRuleInstantiate instantiates a new table rule from a rule revision.
func applyTableRuleInstantiate(client *dataset_rules.Client, rvClient *rule_versions.Client, token, filePath, orgID, orgName string, manifest *dataset_rules.TableRuleApplyManifest, yes bool) error {
	if manifest.Spec.DatasetID == 0 {
		return fmt.Errorf("spec.dataset_id is required for instantiate in %s", filePath)
	}
	if manifest.Spec.InstanceName == nil || *manifest.Spec.InstanceName == "" {
		return fmt.Errorf("spec.instance_name is required for instantiate in %s", filePath)
	}

	// Resolve column mapping
	colMapping := resolveColumnMapping(manifest)

	// Validate params against schema if provided
	if manifest.Spec.Params != nil {
		if err := validateParamsAgainstSchema(rvClient, token, manifest.Spec.RuleRevisionID, *manifest.Spec.Params); err != nil {
			return err
		}
	}

	// Build instantiate request
	req := api.InstantiateRuleJSONRequestBody{
		DatasetID:         manifest.Spec.DatasetID,
		InstanceName:      *manifest.Spec.InstanceName,
		RuleColumnMapping: colMapping,
		Params:            manifest.Spec.Params,
		TreatAsAlert:      manifest.Spec.TreatAsAlert,
		Force:             manifest.Spec.Force,
	}

	// Display confirmation
	fmt.Printf("Organization     : %s (%s)\n", orgName, orgID)
	fmt.Printf("Rule Revision ID : %s\n", manifest.Spec.RuleRevisionID)
	fmt.Printf("Dataset ID       : %d\n", manifest.Spec.DatasetID)
	fmt.Printf("Instance Name    : %s\n", *manifest.Spec.InstanceName)
	fmt.Printf("File             : %s\n", filePath)
	fmt.Println()

	// Prompt for confirmation
	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo("Instantiate this rule onto the table?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Printf("Skipping %s\n\n", filePath)
			return nil
		}
	}

	// Instantiate rule
	fmt.Printf("Instantiating rule %s...\n", manifest.Spec.RuleRevisionID)
	result, err := client.InstantiateRule(token, manifest.Spec.RuleRevisionID, req)
	if err != nil {
		return fmt.Errorf("rule instantiation failed for %s: %w", filePath, err)
	}

	fmt.Printf("Table rule created: %s (%s)\n\n", result.InstanceName, result.ID.String())
	return nil
}
