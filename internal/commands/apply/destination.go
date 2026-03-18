package apply

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/destinations"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
)

// NewDestinationCommand creates the apply destination command
func NewDestinationCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "destination",
		Short: "Apply a destination configuration from a file",
		Long: `Apply a destination configuration from a YAML file.

This command creates a new destination if it doesn't exist, or updates an existing
destination if one with the same name already exists. This makes the operation
idempotent - running it multiple times with the same file produces the same result.

SCHEMA
======

  apiVersion: qluster.ai/v1          # Required. Must be "qluster.ai/v1"
  kind: Destination                  # Required. Must be "Destination"
  metadata:
    name: <string>                   # Required. Unique identifier for the destination
    labels:                          # Optional. Key-value pairs for organization
      <key>: <value>
    annotations:                     # Optional. Key-value pairs for metadata
      <key>: <value>
  spec:
    type: <string>                   # Required. Destination type: "postgresql"
    host: <string>                   # Required. Database server hostname or IP address
    port: <integer>                  # Required. Database server port (e.g., 5432)
    database_name: <string>          # Required. Name of the database to connect to
    user: <string>                   # Required. Database username
    password: <string>               # Optional. Database password (supports ${ENV_VAR})
    connect_timeout: <integer>       # Optional. Connection timeout in seconds

ENVIRONMENT VARIABLES
=====================
Sensitive fields support ${VAR} syntax for environment variable substitution:
  - password: ${DB_PASSWORD}
  - user: ${DB_USER}
  - host: ${DB_HOST}
  - database_name: ${DB_NAME}

EXAMPLE
=======
# File: production-db.yaml
apiVersion: qluster.ai/v1
kind: Destination
metadata:
  name: production-db
  labels:
    environment: production
    team: backend
spec:
  type: postgresql
  host: db.example.com
  port: 5432
  database_name: myapp
  user: app_user
  password: ${DB_PASSWORD}
  connect_timeout: 30

# Apply the configuration:
export DB_PASSWORD='your-secret-password'
qctl apply destination -f production-db.yaml

VALIDATION
==========
The manifest is validated before applying. Common validation errors:
  - "apiVersion: required field is missing"
  - "spec.type: invalid destination type, must be one of: postgresql"
  - "spec.port: must be a positive integer"
  - Unknown fields are rejected (e.g., "unknown field 'usernmae'" for typos)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return fmt.Errorf("filename is required (-f or --filename)")
			}
			return applyDestination(cmd, filePath)
		},
	}

	// Add flags
	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	_ = cmd.MarkFlagRequired("filename")

	return cmd
}

// destinationManifestTolerant wraps DestinationManifest to accept (and discard)
// the status section that "qctl describe destination" emits.
type destinationManifestTolerant struct {
	destinations.DestinationManifest `yaml:",inline"`
	Status                           interface{} `yaml:"status,omitempty"`
}

// applyDestination is the shared apply logic for destination manifests, used by
// both the "apply destination" subcommand and the generic "apply -f" dispatcher.
func applyDestination(cmd *cobra.Command, filePath string) error {
	destManifest, err := loadDestinationManifest(filePath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	processEnvironmentVariables(destManifest)

	validationErrors := destManifest.Validate()
	if len(validationErrors) > 0 {
		var errMsgs []string
		for _, e := range validationErrors {
			errMsgs = append(errMsgs, e.Error())
		}
		return fmt.Errorf("manifest validation failed:\n  - %s", strings.Join(errMsgs, "\n  - "))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}

	client, err := destinations.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}

	result, err := client.Apply(ctx.Credential.AccessToken, destManifest)
	if err != nil {
		return fmt.Errorf("failed to apply destination: %w", err)
	}

	outputFormat, _ := cmd.Flags().GetString("output")
	if outputFormat == "json" {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "destination/%s %s\n", result.Name, result.Action)
	return nil
}

// loadDestinationManifest loads and parses a destination manifest from a file
func loadDestinationManifest(path string) (*destinations.DestinationManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// First, load into generic manifest to check kind
	genericManifest, err := manifest.LoadBytes(data)
	if err != nil {
		return nil, err
	}

	// Verify kind is Destination
	if genericManifest.Kind != "Destination" {
		return nil, fmt.Errorf("expected kind 'Destination', got '%s'", genericManifest.Kind)
	}

	// Now load into typed struct with strict validation (tolerating status section)
	var tolerant destinationManifestTolerant
	if err := manifest.StrictUnmarshal(data, &tolerant); err != nil {
		return nil, err
	}

	return &tolerant.DestinationManifest, nil
}

// processEnvironmentVariables substitutes ${VAR} patterns with environment variable values
func processEnvironmentVariables(m *destinations.DestinationManifest) {
	// Process password field
	if m.Spec.Password != nil && *m.Spec.Password != "" {
		processed := expandEnvVars(*m.Spec.Password)
		m.Spec.Password = &processed
	}

	// Process user field (less common but supported)
	if m.Spec.User != nil && *m.Spec.User != "" {
		processed := expandEnvVars(*m.Spec.User)
		m.Spec.User = &processed
	}

	// Process host field
	if m.Spec.Host != nil && *m.Spec.Host != "" {
		processed := expandEnvVars(*m.Spec.Host)
		m.Spec.Host = &processed
	}

	// Process database name field
	if m.Spec.DatabaseName != nil && *m.Spec.DatabaseName != "" {
		processed := expandEnvVars(*m.Spec.DatabaseName)
		m.Spec.DatabaseName = &processed
	}
}
