package create

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewDryRunJobCommand creates the 'create dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	var (
		filePath        string
		tableID         int
		tableName       string
		cloudSourceID   int
		cloudSourceName string
	)

	cmd := &cobra.Command{
		Use:   "dry-run-job",
		Short: "Create a dry-run job from a spec file",
		Long: `Create a dry-run job from a YAML spec file.

The spec file defines the rule_run_specs and optional parameters for the dry run.
You must specify the cloud source (via flags or spec metadata). The table is only
needed when resolving a cloud source by name or when auto-detecting a cloud source.

Position within rule_run_specs is inferred from the array index (first item = 0,
second item = 1). You may specify 1 or 2 rule_run_specs entries.

Spec attributes:
  rule_run_specs          1-2 entries identifying the rule(s) to run.
    dataset_rule_id       UUID of a rule attached to the table.
    rule_revision_id      UUID (or 8-char short ID) of a specific rule revision.
    rule                  Rule name (resolved via qctl get rules). Alternative to rule_revision_id.
    release               Release version (e.g. "1.0.0"). Only valid with 'rule'.
    params                Key-value parameters passed to parameterized rules.
    treat_as_alert        If true, run the rule in alert mode.
    column_mapping        Column name remapping for the rule.

  max_rows                Maximum rows to process (default: 2000).
  nondeterminism_check    Whether to verify rule determinism. Options:
                            "sample" (default) - re-run rules on a sample of rows and
                              compare results to detect nondeterministic behavior.
                            "off" - skip the nondeterminism check entirely.
  nondeterminism_sample_size
                          Number of rows to sample for the nondeterminism check
                          (default: 25). Only used when nondeterminism_check is "sample".

Examples:

  Two rules compared by dataset_rule_id:
  --- spec.yaml ---

  apiVersion: qluster.ai/v1
  kind: DryRunJob
  metadata:
    cloud_source: my_source
  spec:
    rule_run_specs:
      - dataset_rule_id: "550e8400-e29b-41d4-a716-446655440099"
      - dataset_rule_id: "660e8400-e29b-41d4-a716-446655440099"
    max_rows: 1000
  
  qctl create dry-run-job -f spec.yaml --table my_table

  -------------

  Test a rule revision with parameters:

  --- spec.yaml ---
  apiVersion: qluster.ai/v1
  kind: DryRunJob
  metadata:
    cloud_source_id: 456
  spec:
    rule_run_specs:
      - rule_revision_id: "770e8400-e29b-41d4-a716-446655440099"
        params:
          threshold: 0.9
          mode: strict
    nondeterminism_check: "off"

  qctl create dry-run-job -f spec.yaml --cloud-source-id 456

  -------------

  Test a rule by name (resolved automatically):

  --- spec.yaml ---
  apiVersion: qluster.ai/v1
  kind: DryRunJob
  metadata:
    cloud_source_id: 456
  spec:
    rule_run_specs:
      - rule: email_validator
        release: "1.0.0"

  qctl create dry-run-job -f spec.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if filePath == "" {
				return fmt.Errorf("--file is required")
			}

			// Parse spec file
			manifest, err := dry_runs.LoadDryRunJobManifest(filePath)
			if err != nil {
				return err
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Validate conflicting flags
			if err = cmdutil.ValidateConflictingFlags(cmd,
				[]string{"table-id", "table"},
				[]string{"cloud-source-id", "cloud-source"},
			); err != nil {
				return err
			}

			// Resolve cloud source: flag overrides spec metadata
			resolvedCSID := cloudSourceID
			resolvedCSName := cloudSourceName
			if resolvedCSID == 0 && resolvedCSName == "" {
				if manifest.Metadata.CloudSourceID != nil {
					resolvedCSID = *manifest.Metadata.CloudSourceID
				} else if manifest.Metadata.CloudSource != "" {
					resolvedCSName = manifest.Metadata.CloudSource
				}
			}

			var csID int
			if resolvedCSID > 0 {
				// Cloud source ID known directly — no table resolution needed.
				var err2 error
				csID, _, err2 = cmdutil.ResolveCloudSource(ctx, 0, "", resolvedCSID, "")
				if err2 != nil {
					return err2
				}
			} else {
				// Need the table to resolve cloud source by name or auto-detect.
				resolvedTableID := tableID
				resolvedTableName := tableName
				if resolvedTableID == 0 && resolvedTableName == "" {
					if manifest.Metadata.TableID != nil {
						resolvedTableID = *manifest.Metadata.TableID
					} else if manifest.Metadata.Table != "" {
						resolvedTableName = manifest.Metadata.Table
					}
				}

				dsID, _, err2 := cmdutil.ResolveTable(ctx, resolvedTableID, resolvedTableName)
				if err2 != nil {
					return err2
				}

				var err3 error
				csID, _, err3 = cmdutil.ResolveCloudSource(ctx, dsID, strconv.Itoa(dsID), resolvedCSID, resolvedCSName)
				if err3 != nil {
					return err3
				}
			}

			// Resolve rule names and short IDs in rule_run_specs
			if err := resolveRuleSpecs(ctx, manifest); err != nil {
				return err
			}

			// Build launch request
			req, err := buildLaunchRequest(csID, manifest)
			if err != nil {
				return fmt.Errorf("failed to build launch request: %w", err)
			}

			// Launch dry-run job
			client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			resp, err := client.LaunchDryRunJob(ctx.Credential.AccessToken, *req)
			if err != nil {
				return err
			}

			// Output result
			w := cmd.OutOrStdout()
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			return encoder.Encode(map[string]int{"dry_run_job_id": resp.DryRunJobID})
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "Path to the dry-run job spec file (required)")
	cmd.Flags().IntVar(&tableID, "table-id", 0, "Table ID (overrides spec metadata)")
	cmd.Flags().StringVar(&tableName, "table", "", "Table name (overrides spec metadata)")
	cmd.Flags().IntVar(&cloudSourceID, "cloud-source-id", 0, "Cloud source ID (overrides spec metadata)")
	cmd.Flags().StringVar(&cloudSourceName, "cloud-source", "", "Cloud source name (overrides spec metadata)")

	return cmd
}

// buildLaunchRequest converts the parsed manifest to an API launch request
func buildLaunchRequest(cloudSourceID int, manifest *dry_runs.DryRunJobCreateManifest) (*dry_runs.LaunchRequest, error) {
	req := &dry_runs.LaunchRequest{
		DataSourceModelID:        cloudSourceID,
		MaxRows:                  manifest.Spec.MaxRows,
		NondeterminismSampleSize: manifest.Spec.NondeterminismSampleSize,
		PrimaryKeysInBadData:     manifest.Spec.PrimaryKeysInBadData,
		PrimaryKeysInCleanData:   manifest.Spec.PrimaryKeysInCleanData,
		ExecutionContext:         manifest.Spec.ExecutionContext,
	}

	// Set nondeterminism check
	if manifest.Spec.NondeterminismCheck != nil {
		nc := api.LaunchDryJobRunRequestSchemaNondeterminismCheck(*manifest.Spec.NondeterminismCheck)
		req.NondeterminismCheck = &nc
	}

	// Build rule_run_specs as []interface{} for the union type
	ruleSpecs := make([]interface{}, len(manifest.Spec.RuleRunSpecs))
	for i, rs := range manifest.Spec.RuleRunSpecs {
		spec := map[string]interface{}{
			"position": i,
		}
		if rs.DatasetRuleID != nil {
			parsedUUID, err := uuid.Parse(*rs.DatasetRuleID)
			if err != nil {
				return nil, fmt.Errorf("invalid dataset_rule_id %q: %w", *rs.DatasetRuleID, err)
			}
			spec["dataset_rule_id"] = openapi_types.UUID(parsedUUID)
		}
		if rs.RuleRevisionID != nil {
			parsedUUID, err := uuid.Parse(*rs.RuleRevisionID)
			if err != nil {
				return nil, fmt.Errorf("invalid rule_revision_id %q: %w", *rs.RuleRevisionID, err)
			}
			spec["rule_revision_id"] = openapi_types.UUID(parsedUUID)
		}
		if rs.TreatAsAlert != nil {
			spec["treat_as_alert"] = *rs.TreatAsAlert
		}
		if rs.Params != nil {
			spec["params"] = *rs.Params
		}
		if rs.ColumnMapping != nil {
			spec["column_mapping"] = *rs.ColumnMapping
		}
		ruleSpecs[i] = spec
	}

	// Use FromLaunchDryJobRunRequestSchemaRuleRunSpecs0 to set the union
	if err := req.RuleRunSpecs.FromLaunchDryJobRunRequestSchemaRuleRunSpecs0(ruleSpecs); err != nil {
		return nil, fmt.Errorf("failed to set rule_run_specs: %w", err)
	}

	return req, nil
}

// resolveRuleSpecs resolves rule names and short IDs in rule_run_specs to full UUIDs.
// It modifies the manifest in-place, setting RuleRevisionID for specs that use
// the 'rule' field or a non-UUID short ID in 'rule_revision_id'.
func resolveRuleSpecs(ctx *cmdutil.CommandContext, manifest *dry_runs.DryRunJobCreateManifest) error {
	// Check if any resolution is needed
	needsResolution := false
	for _, rs := range manifest.Spec.RuleRunSpecs {
		if rs.Rule != "" {
			needsResolution = true
			break
		}
		if rs.RuleRevisionID != nil {
			if _, err := uuid.Parse(*rs.RuleRevisionID); err != nil {
				needsResolution = true
				break
			}
		}
	}
	if !needsResolution {
		return nil
	}

	// Create rule_versions client lazily (only when resolution is needed)
	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create rule versions client: %w", err)
	}

	for i := range manifest.Spec.RuleRunSpecs {
		rs := &manifest.Spec.RuleRunSpecs[i]

		if rs.Rule != "" {
			// Resolve rule name (+optional release) to a full UUID
			resolvedID, err := rvClient.ResolveRuleID(ctx.Credential.AccessToken, rs.Rule, rs.Release)
			if err != nil {
				return fmt.Errorf("rule_run_specs[%d]: %w", i, err)
			}
			rs.RuleRevisionID = &resolvedID
			rs.Rule = ""
			rs.Release = ""
			continue
		}

		if rs.RuleRevisionID != nil {
			if _, err := uuid.Parse(*rs.RuleRevisionID); err != nil {
				// Not a valid UUID — treat as short ID
				resolvedID, err := rvClient.ResolveRuleID(ctx.Credential.AccessToken, *rs.RuleRevisionID, "")
				if err != nil {
					return fmt.Errorf("rule_run_specs[%d]: %w", i, err)
				}
				rs.RuleRevisionID = &resolvedID
			}
		}
	}

	return nil
}
