package create

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dry_runs"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

type dryRunJobOptions struct {
	filePath        string
	tableID         int
	tableName       string
	cloudSourceID   int
	cloudSourceName string
}

// NewDryRunJobCommand creates the 'create dry-run-job' command
func NewDryRunJobCommand() *cobra.Command {
	opts := dryRunJobOptions{}

	cmd := &cobra.Command{
		Use:   "dry-run-job",
		Short: "Create a dry-run job from a spec file",
		Long: `Create a dry-run job from a YAML spec file.

The spec file defines the rule_run_specs and optional parameters for the dry run.
Name a cloud source or a table, via flags or spec metadata (cloud_source_id,
cloud_source, table_id, table); a flag wins over its metadata key. A cloud
source ID needs no table. A cloud source name, or none, needs the table: with
none, the table's only cloud source is used, and a table with several exits 2.

The named table is the table the run goes on; with none, it runs on the cloud
source's own table. For a series cloud source, naming a period table of its
series runs the dry run on that period table instead. A cloud source ID given
with a table must name the source's own table or a period table of its series,
or the command exits 2. Launching a dry run kills your own queued or running
exploratory dry run of the same cloud source, on any of its period tables.

Position within rule_run_specs is inferred from the array index (first item = 0,
second item = 1). You may specify 1 or 2 rule_run_specs entries.

Spec attributes:
  rule_run_specs          1-2 entries identifying the rule(s) to run.
    dataset_rule_id       UUID of a rule attached to the table.
    rule_revision_id      UUID (or 8-char short ID) of a specific rule revision.
    rule                  Rule name (resolved via qctl get rules). Alternative to rule_revision_id.
    release               Release version (e.g. "1.0.0"). Only valid with 'rule'.
    params                Key-value parameters passed to parameterized rules.
    severity              What this run does with the severity its findings declare:
                            "as_declared" (default) - each finding keeps the severity
                              the rule gave it.
                            "warning" - every finding of this rule is a warning.
                            "blocker" - every finding of this rule is a blocker, the
                              class that quarantines a row in a real run.
                          Omitting it is as_declared: a dry run does not inherit the
                          severity of the live rule named by dataset_rule_id, nor the
                          rule revision's declared default.
    column_mapping        Column name remapping for the rule.

  max_rows                Maximum rows to process (default: 1000).
  nondeterminism_check    Whether to verify rule determinism. Options:
                            "sample" (default) - re-run rules on a sample of rows and
                              compare results to detect nondeterministic behavior.
                            "off" - skip the nondeterminism check entirely.
  nondeterminism_sample_size
                          Number of rows to sample for the nondeterminism check
                          (default: 25). Only used when nondeterminism_check is "sample".

Output: {"dry_run_job_id": N} as JSON, the default, or as YAML under -o yaml;
-o name prints dry-run-job/<id>.

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
        severity: blocker
    nondeterminism_check: "off"

  qctl create dry-run-job -f spec.yaml --cloud-source-id 456

  Run the same spec on a period table of a series cloud source:

  qctl create dry-run-job -f spec.yaml --cloud-source-id 456 --table sales_2026_09

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
		Annotations: cmdutil.WithFormats(catalog.For("dry-run-job"), "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE:        func(cmd *cobra.Command, args []string) error { return runCreateDryRunJob(cmd, opts) },
	}

	cmd.Flags().StringVarP(&opts.filePath, "file", "f", "", "Path to the dry-run job spec file (required)")
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.RefIntVar(cmd.Flags(), &opts.cloudSourceID, "cloud-source-id", "cloud-source", "Cloud source ID (overrides spec metadata)")
	cmd.Flags().StringVar(&opts.cloudSourceName, "cloud-source", "", "Cloud source name (overrides spec metadata)")

	return cmd
}

func runCreateDryRunJob(cmd *cobra.Command, opts dryRunJobOptions) error {
	if opts.filePath == "" {
		return qerrors.NewBadUsage("--file is required")
	}
	manifest, err := dry_runs.LoadDryRunJobManifest(opts.filePath)
	if err != nil {
		return err
	}
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	if err := validateCloudSourceFlags(cmd); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	csID, datasetID, err := resolveDryRunCloudSource(cmd, ctx, manifest, opts)
	if err != nil {
		return err
	}
	if err := resolveRuleSpecs(ctx, manifest); err != nil {
		return err
	}
	req, err := buildLaunchRequest(csID, manifest)
	if err != nil {
		return fmt.Errorf("failed to build launch request: %w", err)
	}
	req.DatasetID = datasetID
	client := dry_runs.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	// The backend caps a user at 3 dry runs EXECUTING per organization and
	// answers the next launch with 429 (checked by the check_dry_run_rate_limit
	// call in atlas capi/capi/controllers/dry_run_controller.py
	// create_and_run_dry_run, raised as dry_run_limit_api_error). Queued jobs
	// are free -- check_dry_run_rate_limit in
	// cettings/cettings/controllers/dry_run_helpers.py,
	// "Counts only executing states; queued work is free" -- and rule
	// validations do not count at all. Waiting and re-sending the identical
	// request is exactly what the error message asks the user to do by hand,
	// and a refused launch is rolled back whole, so it is safe.
	// The progress line goes to stderr, not cmd.OutOrStdout(): stdout here is a
	// JSON document a script parses, and RetryOnThrottle leaves that choice to
	// the caller.
	resp, err := cmdutil.RetryOnThrottle(cmd.ErrOrStderr(), "dry runs", func() (*dry_runs.LaunchResponse, error) {
		return client.LaunchDryRunJob(ctx.Credential.AccessToken, *req)
	})
	if err != nil {
		return err
	}
	doc := map[string]int{"dry_run_job_id": resp.DryRunJobID}
	if w := cmdutil.NewResultWriter(cmd); w.Machine() {
		return w.Write(doc, fmt.Sprintf("dry-run-job/%d", resp.DryRunJobID))
	}
	// No -o: the --output flag still holds its table default, which this
	// command does not take, so the document is JSON.
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	return encoder.Encode(doc)
}

// validateCloudSourceFlags rejects --cloud-source-id together with
// --cloud-source, and a --cloud-source-id that is not positive.
func validateCloudSourceFlags(cmd *cobra.Command) error {
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"cloud-source-id", "cloud-source"}); err != nil {
		return err
	}
	_, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
	return err
}

// resolveDryRunCloudSource resolves the cloud source the dry run launches on,
// and the dataset_id to send: the named table's ID when it is not the source's
// own table, nil otherwise.
func resolveDryRunCloudSource(cmd *cobra.Command, ctx *cmdutil.CommandContext, manifest *dry_runs.DryRunJobCreateManifest, opts dryRunJobOptions) (int, *int, error) {
	csID, csName := opts.cloudSourceID, opts.cloudSourceName
	if csID == 0 && csName == "" {
		if manifest.Metadata.CloudSourceID != nil {
			csID = *manifest.Metadata.CloudSourceID
		} else if manifest.Metadata.CloudSource != "" {
			csName = manifest.Metadata.CloudSource
		}
	}
	tableID, tableName := dryRunTableSelector(cmd, manifest, opts)
	var table cmdutil.ResolvedTable
	if csID == 0 || tableID != 0 || tableName != "" {
		var err error
		table, err = cmdutil.ResolveTableRecord(ctx, tableID, tableName)
		if err != nil {
			return 0, nil, err
		}
	}
	cs, err := cmdutil.ResolveCloudSourceRecord(ctx, table, csID, csName)
	if err != nil {
		return 0, nil, err
	}
	if table.ID > 0 && table.ID != cs.DatasetID {
		datasetID := table.ID
		return cs.ID, &datasetID, nil
	}
	return cs.ID, nil, nil
}

// dryRunTableSelector returns the table ID or name from --table-id/--table,
// else from the spec metadata's table_id/table.
func dryRunTableSelector(cmd *cobra.Command, manifest *dry_runs.DryRunJobCreateManifest, opts dryRunJobOptions) (int, string) {
	// AddTableFlags owns --table-id/--table's registration, so opts.tableID/
	// opts.tableName are populated here, at their point of use, instead of at
	// flag-registration time.
	opts.tableID, _ = cmd.Flags().GetInt("table-id")
	opts.tableName, _ = cmd.Flags().GetString("table")
	if opts.tableID != 0 || opts.tableName != "" {
		return opts.tableID, opts.tableName
	}
	if manifest.Metadata.TableID != nil {
		return *manifest.Metadata.TableID, ""
	}
	return 0, manifest.Metadata.Table
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
				return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("invalid dataset_rule_id %q", *rs.DatasetRuleID))
			}
			spec["dataset_rule_id"] = parsedUUID
		}
		if rs.RuleRevisionID != nil {
			parsedUUID, err := uuid.Parse(*rs.RuleRevisionID)
			if err != nil {
				return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, fmt.Sprintf("invalid rule_revision_id %q", *rs.RuleRevisionID))
			}
			spec["rule_revision_id"] = parsedUUID
		}
		// Omitted stays omitted: the backend resolves an absent severity to
		// as_declared when it loads the rule, and sending a value here would
		// also write it into the record describe reads back.
		if rs.Severity != nil {
			spec["severity"] = *rs.Severity
		}
		if rs.Params != nil {
			spec["params"] = *rs.Params
		}
		if rs.ColumnMapping != nil {
			spec["column_mapping"] = *rs.ColumnMapping
		}
		if rs.RowFilterSet || rs.RowFilter != nil {
			spec["row_filter"] = rs.RowFilter
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
