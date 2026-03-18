package cmdutil

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/datasets"
)

// ResolveTable resolves a table by ID or name.
// Returns (datasetID, datasetName, error).
func ResolveTable(ctx *CommandContext, tableID int, tableName string) (int, string, error) {
	datasetsClient := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken

	switch {
	case tableID > 0:
		dataset, err := datasetsClient.GetDataset(token, tableID)
		if err != nil {
			return 0, "", fmt.Errorf("failed to fetch table %d: %w", tableID, err)
		}
		return tableID, dataset.Name, nil

	case tableName != "":
		params := datasets.GetDatasetsParams{Name: &tableName, Limit: 10}
		resp, err := datasetsClient.GetDatasets(token, params)
		if err != nil {
			return 0, "", fmt.Errorf("failed to search for table: %w", err)
		}
		if len(resp.Results) == 0 {
			return 0, "", fmt.Errorf("no table found with name '%s'", tableName)
		}
		if len(resp.Results) > 1 {
			return 0, "", fmt.Errorf("table name '%s' is ambiguous across destinations; use --table-id instead", tableName)
		}
		return resp.Results[0].ID, resp.Results[0].Name, nil

	default:
		return 0, "", fmt.Errorf("must specify either --table-id or --table")
	}
}

// ResolveCloudSource resolves a cloud source by ID, name, or auto-detection.
// Returns (cloudSourceID, cloudSourceName, error).
func ResolveCloudSource(ctx *CommandContext, datasetID int, datasetName string, cloudSourceID int, cloudSourceName string) (int, string, error) {
	client, err := cloud_sources.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return 0, "", fmt.Errorf("failed to create cloud sources client: %w", err)
	}
	token := ctx.Credential.AccessToken

	switch {
	case cloudSourceID > 0:
		cs, err := client.GetCloudSource(token, cloudSourceID)
		if err != nil {
			return 0, "", fmt.Errorf("failed to fetch cloud source %d: %w", cloudSourceID, err)
		}
		name := ""
		if cs.Name != nil {
			name = *cs.Name
		}
		return cloudSourceID, name, nil

	case cloudSourceName != "":
		return resolveCloudSourceByName(client, token, datasetID, datasetName, cloudSourceName)

	default:
		return autoDetectCloudSource(client, token, datasetID, datasetName)
	}
}

// resolveCloudSourceByName finds a cloud source by exact name match.
func resolveCloudSourceByName(client *cloud_sources.Client, token string, datasetID int, datasetName, cloudSourceName string) (int, string, error) {
	params := cloud_sources.GetCloudSourcesParams{
		DatasetID:   &datasetID,
		SearchQuery: &cloudSourceName,
	}
	results, err := client.GetAllCloudSources(token, params, 10)
	if err != nil {
		return 0, "", fmt.Errorf("failed to search for cloud source: %w", err)
	}

	// Filter for exact name match
	var matches []cloud_sources.CloudSourceTiny
	for _, cs := range results {
		if cs.Name == cloudSourceName {
			matches = append(matches, cs)
		}
	}

	if len(matches) == 0 {
		return 0, "", fmt.Errorf("cloud source '%s' not found for table '%s'; use --cloud-source-id", cloudSourceName, datasetName)
	}
	if len(matches) > 1 {
		return 0, "", fmt.Errorf("cloud source '%s' is ambiguous for table '%s'; use --cloud-source-id", cloudSourceName, datasetName)
	}

	return matches[0].ID, matches[0].Name, nil
}

// autoDetectCloudSource finds the cloud source when the table has only one.
func autoDetectCloudSource(client *cloud_sources.Client, token string, datasetID int, datasetName string) (int, string, error) {
	params := cloud_sources.GetCloudSourcesParams{DatasetID: &datasetID}
	results, err := client.GetAllCloudSources(token, params, 10)
	if err != nil {
		return 0, "", fmt.Errorf("failed to list cloud sources: %w", err)
	}

	if len(results) == 0 {
		return 0, "", fmt.Errorf("table '%s' has no cloud sources", datasetName)
	}
	if len(results) > 1 {
		return 0, "", fmt.Errorf("table '%s' has multiple cloud sources; specify one with --cloud-source or --cloud-source-id", datasetName)
	}

	return results[0].ID, results[0].Name, nil
}
