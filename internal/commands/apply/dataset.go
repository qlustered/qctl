package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
)

// NewDatasetCommand creates the apply dataset/table command
func NewDatasetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "table",
		Aliases: []string{"dataset"},
		Short:   "Apply a table configuration from a file",
		Long: `Apply a table (dataset) configuration from a YAML file.

This command creates a new table if it doesn't exist, or updates an existing one
when the name matches. The operation is idempotent - running it multiple times
with the same file produces the same result.

SCHEMA
======
For the complete schema with all field types, descriptions, and constraints, run:

  qctl explain table

Basic structure:

  apiVersion: qluster.ai/v1          # Required. Must be "qluster.ai/v1"
  kind: Table                        # Required. Must be "Table"
  metadata:
    name: <string>                   # Required. Table display name
  spec:
    destination_id: <int>            # Required. Destination to write into
    database_name: <string>          # Required. Destination database name
    schema_name: <string>            # Required. Destination schema/path
    table_name: <string>             # Required. Destination table name
    migration_policy: <string>       # Required. Use 'qctl explain table.migration_policy' for values
    data_loading_process: <string>   # Required. Use 'qctl explain table.data_loading_process' for values
    backup_settings_id: <int>        # Required. Backup configuration ID
    # ... additional optional fields available via 'qctl explain table'

EXAMPLE
=======
# File: table-orders.yaml
apiVersion: qluster.ai/v1
kind: Table
metadata:
  name: orders
spec:
  destination_id: 3
  database_name: analytics
  schema_name: public
  table_name: orders
  migration_policy: apply_asap
  data_loading_process: snapshot
  backup_settings_id: 1

# Apply the configuration:
qctl apply table -f table-orders.yaml

# View full schema documentation:
qctl explain table

# View specific field details:
qctl explain table.migration_policy`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return fmt.Errorf("filename is required (-f or --filename)")
			}
			return applyTable(cmd, filePath)
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// applyTable is the shared apply logic for table manifests, used by both the
// "apply table" subcommand and the generic "apply -f" dispatcher.
func applyTable(cmd *cobra.Command, filePath string) error {
	tableManifest, err := loadDatasetManifest(filePath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	if errs := tableManifest.Validate(); len(errs) > 0 {
		var messages []string
		for _, e := range errs {
			messages = append(messages, e.Error())
		}
		return fmt.Errorf("manifest validation failed:\n  - %s", strings.Join(messages, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := client.Apply(ctx.Credential.AccessToken, tableManifest)
	if err != nil {
		return fmt.Errorf("failed to apply dataset: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "table/%s %s\n", tableManifest.Metadata.Name, result.Action)
	return nil
}

func loadDatasetManifest(path string) (*datasets.TableManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	generic, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}

	if generic.APIVersion != pkgmanifest.APIVersionV1 {
		return nil, fmt.Errorf("expected apiVersion '%s', got '%s'", pkgmanifest.APIVersionV1, generic.APIVersion)
	}
	if generic.Kind != "Table" {
		return nil, fmt.Errorf("expected kind 'Table', got '%s'", generic.Kind)
	}

	var tolerant datasets.TableManifestWithStatus
	if err := pkgmanifest.StrictUnmarshal(data, &tolerant); err != nil {
		return nil, err
	}

	return &tolerant.TableManifest, nil
}
