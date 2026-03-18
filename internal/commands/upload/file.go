package upload

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

const (
	unknownValue = "Unknown"
)

// uploadContext holds resolved values for the upload operation
type uploadContext struct {
	filePath        string
	datasetName     string
	cloudSourceName string
	orgID           string
	orgName         string
	fileSize        int
	datasetID       int
	cloudSourceID   int
}

// NewFileCommand creates the 'upload file' command
func NewFileCommand() *cobra.Command {
	var (
		serverFlag      string
		userFlag        string
		tableID         int
		tableName       string
		cloudSourceID   int
		cloudSourceName string
		yes             bool
	)

	cmd := &cobra.Command{
		Use:   "file PATH",
		Short: "Upload a file for ingestion",
		Long: `Upload a file to a cloud source for ingestion.

The file will be uploaded to backup storage and then published for ingestion into the specified table.

You must specify either:
  - --table-id or --table (dataset)
  - --cloud-source-id or --cloud-source (data source)

If the table has only one cloud source, you can omit the cloud source flags.

Examples:
  # Upload to a specific table and cloud source
  qctl upload file data.csv --table-id 123 --cloud-source-id 456

  # Upload using names (requires quotes if names contain spaces)
  qctl upload file data.csv --table "Customer Data" --cloud-source "S3 Import"

  # Skip confirmation prompt
  qctl upload file data.csv --table-id 123 --cloud-source-id 456 --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath := args[0]

			// Validate file
			fileSize, err := validateFile(filePath)
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

			// Resolve table
			datasetID, datasetName, err := cmdutil.ResolveTable(ctx, tableID, tableName)
			if err != nil {
				return err
			}

			// Resolve cloud source
			csID, csName, err := cmdutil.ResolveCloudSource(ctx, datasetID, datasetName, cloudSourceID, cloudSourceName)
			if err != nil {
				return err
			}

			// Get org context
			orgID, orgName, err := getOrgContext(ctx)
			if err != nil {
				return err
			}

			// Build upload context
			uc := &uploadContext{
				filePath:        filePath,
				fileSize:        fileSize,
				datasetID:       datasetID,
				datasetName:     datasetName,
				cloudSourceID:   csID,
				cloudSourceName: csName,
				orgID:           orgID,
				orgName:         orgName,
			}

			// Display confirmation and prompt
			displayUploadConfirmation(uc)
			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo("Proceed with upload and ingestion?")
				if err != nil {
					return err
				}
				if !confirmed {
					return fmt.Errorf("upload cancelled")
				}
			}

			// Perform upload
			return performUpload(ctx, uc)
		},
	}

	// Add flags
	cmd.Flags().StringVar(&serverFlag, "server", "", "Override server URL")
	cmd.Flags().StringVar(&userFlag, "user", "", "Override user")
	cmd.Flags().IntVar(&tableID, "table-id", 0, "Table ID (dataset ID)")
	cmd.Flags().StringVar(&tableName, "table", "", "Table name (requires quotes if name contains spaces)")
	cmd.Flags().IntVar(&cloudSourceID, "cloud-source-id", 0, "Cloud source ID (data source model ID)")
	cmd.Flags().StringVar(&cloudSourceName, "cloud-source", "", "Cloud source name (requires quotes if name contains spaces)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}

// validateFile checks if the file exists and is not a directory.
// Returns the file size in bytes.
func validateFile(filePath string) (int, error) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("failed to access file: %w", err)
	}
	if fileInfo.IsDir() {
		return 0, fmt.Errorf("path is a directory, not a file: %s", filePath)
	}
	return int(fileInfo.Size()), nil
}

// getOrgContext retrieves the current organization ID and name.
func getOrgContext(ctx *cmdutil.CommandContext) (string, string, error) {
	authClient := auth.NewClient(ctx.ServerURL, ctx.Verbosity)
	userMe, err := authClient.GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get current user info: %w", err)
	}

	orgName := unknownValue
	orgID := unknownValue
	if userMe.MembershipOrgID != nil && len(userMe.ActiveOrganizationNames) > 0 {
		orgID = *userMe.MembershipOrgID
		for i, id := range userMe.ActiveOrganizationIDs {
			if id == orgID && i < len(userMe.ActiveOrganizationNames) {
				orgName = userMe.ActiveOrganizationNames[i]
				break
			}
		}
	}

	return orgID, orgName, nil
}

// displayUploadConfirmation prints the upload summary for user confirmation.
func displayUploadConfirmation(uc *uploadContext) {
	fmt.Printf("Organization : %s (%s)\n", uc.orgName, uc.orgID)
	fmt.Printf("Table        : %s (id=%d)\n", uc.datasetName, uc.datasetID)
	fmt.Printf("Cloud source : %s (id=%d)\n", uc.cloudSourceName, uc.cloudSourceID)
	fmt.Printf("File         : %s, size=%d bytes\n", filepath.Base(uc.filePath), uc.fileSize)
	fmt.Println()
}

// performUpload executes the upload workflow: create URL, upload file, publish.
func performUpload(ctx *cmdutil.CommandContext, uc *uploadContext) error {
	client, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create stored items client: %w", err)
	}
	token := ctx.Credential.AccessToken

	// Generate storage key
	key := stored_items.GenerateStorageKey(uc.orgID, uc.datasetID, uc.cloudSourceID, filepath.Base(uc.filePath))

	// Create stored item and get pre-signed upload URL
	fmt.Println("Creating upload URL...")
	uploadReq := stored_items.StoredItemPutRequest{
		DataSourceModelID: uc.cloudSourceID,
		Key:               key,
		FileSize:          &uc.fileSize,
	}

	urlResp, err := client.CreateStoredItemForUpload(token, uploadReq)
	if err != nil {
		return fmt.Errorf("failed to create upload URL: %w", err)
	}

	storedItemID := urlResp.ID

	// Display any message from backend
	if urlResp.Msg != nil && *urlResp.Msg != "" {
		fmt.Printf("Note: %s\n", *urlResp.Msg)
	}

	// Upload file
	fmt.Printf("Uploading file (stored_item_id=%d)...\n", storedItemID)
	if err := client.UploadFile(urlResp, uc.filePath); err != nil {
		return handleUploadFailure(client, token, storedItemID, ctx.Verbosity, err)
	}

	// Publish for ingestion
	fmt.Println("Publishing for ingestion...")
	if err := client.PublishStoredItemForIngestion(token, storedItemID); err != nil {
		return fmt.Errorf("upload succeeded but ingestion publish failed: %w\nYou can retry later by re-running ingestion for this file", err)
	}

	// Success
	fmt.Println()
	fmt.Println("Uploaded file and queued for ingestion.")
	fmt.Printf("stored_item_id : %d\n", storedItemID)
	fmt.Printf("table          : %s\n", uc.datasetName)
	fmt.Printf("cloud_source   : %s\n", uc.cloudSourceName)

	return nil
}

// handleUploadFailure cleans up the zombie stored item after upload failure.
func handleUploadFailure(client *stored_items.Client, token string, storedItemID int, verbosity int, uploadErr error) error {
	fmt.Fprintf(os.Stderr, "Upload failed: %v\n", uploadErr)
	fmt.Fprintf(os.Stderr, "Cleaning up stored_item %d...\n", storedItemID)

	if cleanupErr := client.DeleteZombieStoredItem(token, storedItemID); cleanupErr != nil {
		if verbosity > 0 {
			fmt.Fprintf(os.Stderr, "Warning: zombie cleanup also failed: %v\n", cleanupErr)
		}
	}

	return fmt.Errorf("failed to upload file to storage; stored_item %d has been cleaned up", storedItemID)
}
