package submit

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

const maxFileTextLength = 500 * 1024 // 500 KB

// NewRulesCommand creates the 'submit rules' command
func NewRulesCommand() *cobra.Command {
	var (
		force     bool
		yes       bool
		filePaths []string
	)

	cmd := &cobra.Command{
		Use:     "rules",
		Aliases: []string{"rule"},
		Short:   "Submit rule definitions from Python files",
		Long: `Submit rule definitions from Python files into the org rule library.

The Python files are parsed server-side, and only the extracted rule
versions are kept.

File constraints:
  - Maximum file size: 500,000 characters

The --force flag allows submission even if rules with the same version
already exist.

Examples:
  # Submit a single rule file
  qctl submit rules -f rules.py

  # Submit multiple rule files
  qctl submit rules -f rules1.py -f rules2.py -f rules3.py

  # Force submission even if versions exist
  qctl submit rules -f rules.py --force

  # Skip confirmation prompt
  qctl submit rules -f rules.py --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return fmt.Errorf("at least one file is required (-f or --filename)")
			}

			// Validate all files are Python
			for _, fp := range filePaths {
				ext := filepath.Ext(fp)
				if ext != ".py" {
					return fmt.Errorf("unsupported file type %q for %s (only .py files are supported)\n\nTo apply YAML rule manifests, use:\n  qctl apply -f %s", ext, fp, fp)
				}
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			orgID, orgName, err := getRulesOrgContext(ctx)
			if err != nil {
				return err
			}

			// Create rule versions client
			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			// Process each file
			for _, fp := range filePaths {
				if err := importRuleFile(client, token, fp, orgID, orgName, force, yes); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Path to the Python rule file")
	cmd.Flags().BoolVar(&force, "force", false, "Force submission even if rules with the same version already exist")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}

// getRulesOrgContext retrieves the current organization ID and name.
func getRulesOrgContext(ctx *cmdutil.CommandContext) (string, string, error) {
	orgID := ctx.OrganizationID
	orgName := ctx.OrganizationName
	if orgName == "" {
		orgName = "Unknown"
	}
	return orgID, orgName, nil
}

// importRuleFile handles importing a single Python rule file.
func importRuleFile(client *rule_versions.Client, token, filePath, orgID, orgName string, force, yes bool) error {
	fileText, err := readAndValidateRuleFile(filePath)
	if err != nil {
		return err
	}

	displayRulesImportConfirmation(filePath, orgID, orgName, len(fileText))

	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo("Import rules from this file into the org rule library?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Printf("Skipping %s\n\n", filePath)
			return nil
		}
	}

	return submitRuleFile(client, token, filePath, fileText, force)
}

// readAndValidateRuleFile reads a file and validates its size.
func readAndValidateRuleFile(filePath string) (string, error) {
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

// displayRulesImportConfirmation prints the import summary for user confirmation.
func displayRulesImportConfirmation(filePath, orgID, orgName string, fileSize int) {
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("File         : %s\n", filepath.Base(filePath))
	fmt.Printf("Size         : %s (limit %s)\n", formatSize(fileSize), formatSize(maxFileTextLength))
	fmt.Println()
}

// formatSize formats a byte count as a human-readable string.
func formatSize(bytes int) string {
	const (
		kb = 1024
		mb = kb * 1024
	)
	switch {
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(mb))
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(kb))
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}

// submitRuleFile submits a rule file to the server.
func submitRuleFile(client *rule_versions.Client, token, filePath, fileText string, force bool) error {
	forcePtr := &force
	req := rule_versions.RuleVersionSubmitRequest{
		FileText: fileText,
		Force:    forcePtr,
	}

	fmt.Printf("Importing rules from %s...\n", filepath.Base(filePath))
	result, err := client.SubmitRuleVersion(token, req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	displayRulesImportResult(result)
	return nil
}

// displayRulesImportResult prints the import result.
func displayRulesImportResult(result *rule_versions.RuleVersionSubmitResponse) {
	if result.Message != "" {
		fmt.Printf("\n%s\n\n", result.Message)
	}

	if len(result.Added) > 0 {
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION")

		for _, rule := range result.Added {
			if len(rule) == 2 {
				fmt.Fprintf(w, "%s\t%s\n", rule[0], rule[1])
			}
		}

		w.Flush()
	}

	if len(result.NotChanged) > 0 {
		fmt.Printf("Unchanged rule(s):\n")
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tVERSION")

		for _, rule := range result.NotChanged {
			if len(rule) == 2 {
				fmt.Fprintf(w, "%s\t%s\n", rule[0], rule[1])
			}
		}

		w.Flush()
		fmt.Println()
	}
}
