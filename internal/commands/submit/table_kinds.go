package submit

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/spf13/cobra"
)

// NewTableKindsCommand creates the 'submit table-kinds' command
func NewTableKindsCommand() *cobra.Command {
	var (
		yes       bool
		filePaths []string
	)

	cmd := &cobra.Command{
		Use:     "table-kinds",
		Aliases: []string{"table-kind"},
		Short:   "Submit table kind definitions from TOML or YAML files",
		Long: `Submit table kind definitions from TOML or YAML config files.

The files are parsed server-side. Each file defines a table kind and
optionally its field kinds.

Supported file types: .toml, .yaml, .yml

File constraints:
  - Maximum file size: 500,000 characters

Examples:
  # Submit a single TOML file
  qctl submit table-kinds -f car-policy-bordereau.toml

  # Submit multiple files
  qctl submit table-kinds -f kinds1.toml -f kinds2.yaml

  # Skip confirmation prompt
  qctl submit table-kinds -f car-policy-bordereau.toml --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return fmt.Errorf("at least one file is required (-f or --filename)")
			}

			// Validate all file extensions
			for _, fp := range filePaths {
				if _, err := dataset_kinds.FormatForFile(fp); err != nil {
					return err
				}
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			orgID, orgName, err := getTableKindsOrgContext(ctx)
			if err != nil {
				return err
			}

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken

			for _, fp := range filePaths {
				if err := importTableKindFile(client, token, fp, orgID, orgName, yes); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Path to the TOML or YAML config file")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}

func getTableKindsOrgContext(ctx *cmdutil.CommandContext) (string, string, error) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = "Unknown"
	}
	return orgID, orgName, nil
}

func importTableKindFile(client *dataset_kinds.Client, token, filePath, orgID, orgName string, yes bool) error {
	fileText, err := readAndValidateTableKindFile(filePath)
	if err != nil {
		return err
	}

	format, err := dataset_kinds.FormatForFile(filePath)
	if err != nil {
		return err
	}

	displayTableKindImportConfirmation(filePath, orgID, orgName, len(fileText))

	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo("Import table kind from this file?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Printf("Skipping %s\n\n", filePath)
			return nil
		}
	}

	fmt.Printf("Importing table kind from %s...\n", filepath.Base(filePath))
	result, err := client.ImportFromConfig(token, fileText, format)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	displayTableKindImportResult(result)
	return nil
}

func readAndValidateTableKindFile(filePath string) (string, error) {
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	fileText := string(fileBytes)
	if len(fileText) > maxFileTextLength {
		return "", fmt.Errorf("file %s exceeds maximum size: %d characters (limit: %d)",
			filePath, len(fileText), maxFileTextLength)
	}

	return fileText, nil
}

func displayTableKindImportConfirmation(filePath, orgID, orgName string, fileSize int) {
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("File         : %s\n", filepath.Base(filePath))
	fmt.Printf("Size         : %s (limit %s)\n", formatSize(fileSize), formatSize(maxFileTextLength))
	fmt.Println()
}

func displayTableKindImportResult(result *dataset_kinds.DatasetKindWithFieldKinds) {
	fmt.Printf("\nImported table kind: %s (%s)\n", result.Name, result.Slug)
	if result.FieldKinds != nil {
		fmt.Printf("Field kinds: %d\n", len(*result.FieldKinds))
	}
	fmt.Println()
}
