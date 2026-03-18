package download

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

// NewFileCommand creates the 'download file' command
func NewFileCommand() *cobra.Command {
	var (
		output string
		force  bool
	)

	cmd := &cobra.Command{
		Use:   "file <id>",
		Short: "Download the original raw file",
		Long: `Download the original raw file from backup storage.

The file will be downloaded to the current directory using its original filename,
or to a custom path if --output is specified.

Examples:
  # Download to current directory
  qctl download file 123

  # Download to specific path
  qctl download file 123 --output /path/to/file.csv

  # Overwrite existing file
  qctl download file 123 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Parse stored item ID
			storedItemID, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid file ID '%s': must be a number", args[0])
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			// Create client
			storedItemsClient, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}

			// Get download URL
			fmt.Println("Getting download URL...")
			urlResp, err := storedItemsClient.GetDownloadURL(ctx.Credential.AccessToken, storedItemID)
			if err != nil {
				return fmt.Errorf("failed to get download URL: %w", err)
			}

			// Display any message from backend
			if urlResp.Msg != nil && *urlResp.Msg != "" {
				fmt.Printf("Note: %s\n", *urlResp.Msg)
			}

			// Resolve output path
			outputPath, err := resolveOutputPath(storedItemsClient, ctx.Credential.AccessToken, storedItemID, output)
			if err != nil {
				return err
			}

			// Download file
			fmt.Printf("Downloading to %s...\n", outputPath)
			if err := storedItemsClient.DownloadFile(urlResp, outputPath, force); err != nil {
				return fmt.Errorf("failed to download file: %w", err)
			}

			fmt.Printf("Successfully downloaded file to: %s\n", outputPath)

			return nil
		},
	}

	addDownloadFlags(cmd, &output, &force)
	return cmd
}

// resolveOutputPath determines the output file path.
func resolveOutputPath(client *stored_items.Client, token string, storedItemID int, output string) (string, error) {
	outputPath := output
	if outputPath == "" {
		// Fetch file info to get filename
		file, err := client.GetStoredItem(token, storedItemID)
		if err != nil {
			return "", fmt.Errorf("failed to get file info: %w", err)
		}
		outputPath = file.FileName
	}

	// Make sure path is absolute or relative to current directory
	if !filepath.IsAbs(outputPath) {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
		outputPath = filepath.Join(cwd, outputPath)
	}

	return outputPath, nil
}

// addDownloadFlags adds flags for the download command.
func addDownloadFlags(cmd *cobra.Command, output *string, force *bool) {
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")
	cmd.Flags().StringVar(output, "output", "", "Output file path (default: use original filename)")
	cmd.Flags().BoolVar(force, "force", false, "Overwrite existing file")
}
