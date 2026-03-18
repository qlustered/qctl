package describe

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/errorincidents"
	"github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/printer"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewErrorIncidentCommand creates the describe error-incident command
func NewErrorIncidentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "error-incident <id>",
		Aliases: []string{"error"},
		Short:   "Show details of a specific error incident",
		Long: `Show details of a specific error incident including its configuration and metadata.

For YAML output (default), the error incident is converted to a manifest format.

For JSON output, the raw API response is returned.

Example manifest returned by this command:

  apiVersion: qluster.ai/v1
  kind: ErrorIncident
  metadata: {}
  spec:
    error: AccessDeniedError
    msg: "Access denied to resource"
    module: sensor
    count: 5
    job_name: sensor-1
    dataset_id: 42
  status:
    id: 123
    deleted: false
    created_at: "2024-01-15T10:30:00Z"`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse error incident ID from arg
			errorID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid error incident ID: %s", args[0])
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create error incidents client
			client, err := errorincidents.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get error incident details
			result, err := client.GetErrorIncident(ctx.Credential.AccessToken, errorID)
			if err != nil {
				return fmt.Errorf("failed to get error incident: %w", err)
			}

			// Get output format
			outputFormat, _ := cmd.Flags().GetString("output")
			if !cmd.Flags().Changed("output") {
				outputFormat = "yaml"
			}

			// For JSON output, return raw API response
			if outputFormat == "json" {
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(result)
			}

			// For YAML output, convert to manifest format
			manifestResult := apiResponseToErrorIncidentManifest(result)

			// For table format, use the printer
			if outputFormat == "table" {
				p, err := printer.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return p.Print(manifestResult)
			}

			// YAML output (default)
			encoder := yaml.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent(2)
			defer encoder.Close()
			return encoder.Encode(manifestResult)
		},
	}

	return cmd
}

// apiResponseToErrorIncidentManifest converts an API response to an ErrorIncidentManifest
func apiResponseToErrorIncidentManifest(resp *errorincidents.ErrorIncidentFull) *errorincidents.ErrorIncidentManifest {
	var createdAt *string
	if resp.CreatedAt != nil {
		ts := resp.CreatedAt.Format("2006-01-02T15:04:05Z07:00")
		createdAt = &ts
	}

	var jobType *string
	if resp.JobType != nil {
		jt := string(*resp.JobType)
		jobType = &jt
	}

	// Convert meta_data to map[string]interface{}
	var metaData map[string]interface{}
	if resp.MetaData != nil {
		metaData = make(map[string]interface{})
		for k, v := range *resp.MetaData {
			metaData[k] = convertMetaDataValue(v)
		}
	}

	return &errorincidents.ErrorIncidentManifest{
		APIVersion: manifest.APIVersionV1,
		Kind:       "ErrorIncident",
		Metadata:   errorincidents.ErrorIncidentMetadata{},
		Spec: errorincidents.ErrorIncidentSpec{
			Error:             resp.Error,
			Msg:               resp.Msg,
			Module:            resp.Module,
			Count:             resp.Count,
			JobName:           resp.JobName,
			JobType:           jobType,
			StackTrace:        resp.StackTrace,
			DatasetID:         resp.DatasetID,
			StoredItemID:      resp.StoredItemID,
			AlertItemID:       resp.AlertItemID,
			DataSourceModelID: resp.DataSourceModelID,
			SettingsModelID:   resp.SettingsModelID,
			MetaData:          metaData,
		},
		Status: &errorincidents.ErrorIncidentStatus{
			ID:        resp.ID,
			Deleted:   resp.Deleted,
			CreatedAt: createdAt,
		},
	}
}

// convertMetaDataValue converts the union type to a plain interface{}
func convertMetaDataValue(v api.ErrorIncidentSchema_MetaData_AdditionalProperties) interface{} {
	// Try each type in order
	if s, err := v.AsErrorIncidentSchemaMetaData0(); err == nil {
		return s
	}
	if i, err := v.AsErrorIncidentSchemaMetaData1(); err == nil {
		return i
	}
	if arr, err := v.AsErrorIncidentSchemaMetaData2(); err == nil {
		return arr
	}
	if arr, err := v.AsErrorIncidentSchemaMetaData3(); err == nil {
		return arr
	}
	if b, err := v.AsErrorIncidentSchemaMetaData4(); err == nil {
		return b
	}
	return nil
}
