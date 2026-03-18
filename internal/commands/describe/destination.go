package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// DestinationManifestWithStatus extends DestinationManifest with status info
type DestinationManifestWithStatus struct {
	Status     *DestinationStatus               `yaml:"status,omitempty" json:"status,omitempty"`
	Spec       destinations.DestinationSpec     `yaml:"spec" json:"spec"`
	Metadata   destinations.DestinationMetadata `yaml:"metadata" json:"metadata"`
	APIVersion string                           `yaml:"apiVersion" json:"apiVersion"`
	Kind       string                           `yaml:"kind" json:"kind"`
}

// DestinationStatus holds runtime status information
type DestinationStatus struct {
	AvailableDatabases     *[]string `yaml:"available_databases,omitempty" json:"available_databases,omitempty"`
	*DestinationTimestamps `yaml:",inline" json:",inline"`
	ID                     int32 `yaml:"id" json:"id"`
}

// DestinationTimestamps groups optional timestamp fields.
type DestinationTimestamps struct {
	CreatedAt *string `yaml:"created_at,omitempty" json:"created_at,omitempty"`
	UpdatedAt *string `yaml:"updated_at,omitempty" json:"updated_at,omitempty"`
}

func destTypePtr(t destinations.DestinationType) *destinations.DestinationType {
	return &t
}

// NewDestinationCommand creates the describe destination command
func NewDestinationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destination <id>",
		Short: "Show details of a specific destination",
		Long: `Show details of a specific destination including its configuration and available databases.

For YAML output (default), the destination is converted to a manifest format that can be
used with 'qctl destinations apply -f'. The password field is always set to null for security.

For JSON output, the raw API response is returned.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: Destination
  metadata:
    name: analytics-db
  spec:
    type: postgresql
    host: db.example.com
    port: 5432
    database_name: analytics
    user: analytics
    password: null
    connect_timeout: 30
  status:
    id: 42
    available_databases:
      - analytics
      - staging`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse destination ID from arg
			destinationID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid destination ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create destinations client
			client, err := destinations.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get destination details
			result, err := client.GetDestination(ctx.Credential.AccessToken, destinationID)
			if err != nil {
				return fmt.Errorf("failed to get destination: %w", err)
			}

			// Side-load available database names
			skipDatabases, _ := cmd.Flags().GetBool("skip-databases")
			var databases []string
			if !skipDatabases {
				databases, err = client.GetDestinationDatabaseNames(ctx.Credential.AccessToken, destinationID)
				if err != nil {
					// Log warning but don't fail - databases might not be accessible
					if ctx.Verbosity > 0 {
						fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to fetch database names: %v\n", err)
					}
				}
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = "yaml"
			}

			// For JSON output, return raw API response with databases
			if outputFormat == "json" {
				type rawResponse struct {
					*destinations.DestinationFull
					AvailableDatabases []string `json:"available_databases,omitempty"`
				}
				raw := rawResponse{
					DestinationFull:    result,
					AvailableDatabases: databases,
				}
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(raw)
			}

			// For YAML output, convert to manifest format
			manifest := apiResponseToManifest(result, databases)

			// For table format, use the printer
			if outputFormat == "table" {
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifest)
			}

			// YAML output (default)
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer encoder.Close()
			return encoder.Encode(manifest)
		},
	}

	// Add flags
	cmd.Flags().Bool("skip-databases", false, "Skip fetching available database names")

	return cmd
}

// apiResponseToManifest converts an API response to a DestinationManifest
// Password is always set to nil for security
func apiResponseToManifest(resp *destinations.DestinationFull, databases []string) *DestinationManifestWithStatus {
	var availableDatabases *[]string
	if len(databases) > 0 {
		availableDatabases = &databases
	}
	var createdAt, updatedAt *string
	if ts := resp.CreatedAt.Format("2006-01-02T15:04:05Z07:00"); ts != "" {
		createdAt = &ts
	}
	if ts := resp.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"); ts != "" {
		updatedAt = &ts
	}
	timestamps := &DestinationTimestamps{
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}

	return &DestinationManifestWithStatus{
		APIVersion: manifest.APIVersionV1,
		Kind:       "Destination",
		Metadata: destinations.DestinationMetadata{
			Name: resp.Name,
		},
		Spec: destinations.DestinationSpec{
			Type:           destTypePtr(destinations.DestinationType(resp.DestinationType)),
			Host:           &resp.Host,
			Port:           int32(resp.Port),
			DatabaseName:   &resp.DatabaseName,
			User:           &resp.User,
			Password:       nil, // Always nil for security
			ConnectTimeout: &resp.ConnectTimeout,
		},
		Status: &DestinationStatus{
			ID:                    int32(resp.ID),
			AvailableDatabases:    availableDatabases,
			DestinationTimestamps: timestamps,
		},
	}
}
