package delete

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// NewRulesCommand creates the 'delete rules' command
func NewRulesCommand() *cobra.Command {
	var (
		yes       bool
		filePaths []string
	)

	cmd := &cobra.Command{
		Use:   "rules",
		Short: "Unsubmit rule versions from a Python source file",
		Long: `Unsubmit (remove) rule versions that match the definitions in a Python source file.

This is the reverse of 'qctl apply rules -f <file.py>'. Rules defined in the file
will be removed from the catalog if they exist.

Examples:
  # Unsubmit rules defined in a file
  qctl delete rules -f rules.py

  # Skip confirmation prompt
  qctl delete rules -f rules.py --yes`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return fmt.Errorf("at least one file is required (-f or --filename)")
			}

			// Bootstrap auth context
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			orgName := ctx.OrganizationName
			if orgName == "" {
				orgName = unknownValue
			}

			// Create client
			client, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			if err != nil {
				return fmt.Errorf("failed to create client: %w", err)
			}
			token := ctx.Credential.AccessToken

			// Process each file
			for _, filePath := range filePaths {
				if err := unsubmitRuleFile(client, token, filePath, ctx.OrganizationID, orgName, yes, ctx.Verbosity); err != nil {
					return err
				}
			}

			return nil
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Path to the Python source file (can be specified multiple times)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")

	return cmd
}

// unsubmitRuleFile handles unsubmitting rules from a single file.
func unsubmitRuleFile(client *rule_versions.Client, token, filePath, orgID, orgName string, yes bool, verbosity int) error {
	// Read file
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	fileText := string(fileBytes)

	// Display confirmation
	fmt.Printf("Organization : %s (%s)\n", orgName, orgID)
	fmt.Printf("File         : %s\n", filepath.Base(filePath))
	fmt.Println()

	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo("Unsubmit (remove) rules defined in this file from the catalog?")
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Printf("Skipping %s\n\n", filePath)
			return nil
		}
	}

	// Unsubmit
	fmt.Printf("Deleting rules from %s...\n", filepath.Base(filePath))
	result, err := client.UnsubmitRuleVersion(token, fileText)
	if err != nil {
		return fmt.Errorf("rule delete failed for %s: %w", filePath, err)
	}

	// Display results
	displayUnsubmitResult(os.Stdout, result, verbosity)
	return nil
}

// displayUnsubmitResult prints the unsubmit result to w.
func displayUnsubmitResult(w io.Writer, result *rule_versions.RuleVersionUnsubmitResponse, verbosity int) {
	if result.Message != "" {
		fmt.Fprintf(w, "\n%s\n\n", result.Message)
	}

	if len(result.Deleted) > 0 {
		fmt.Fprintln(w, "Deleted:")
		printResultItems(w, result.Deleted, verbosity)
	}

	if len(result.NotFound) > 0 {
		fmt.Fprintln(w, "Not found:")
		printResultItems(w, result.NotFound, verbosity)
	}

	if len(result.Skipped) > 0 {
		fmt.Fprintln(w, "Skipped:")
		for _, group := range result.Skipped {
			fmt.Fprintf(w, "  Reason: %s\n", group.Reason)
			printResultItems(w, group.Rules, verbosity)
		}
	}
}

// hasAnyRevisionID returns true if at least one item has a non-nil RevisionID.
func hasAnyRevisionID(items []rule_versions.UnsubmitResultItem) bool {
	for _, item := range items {
		if item.RevisionID != nil {
			return true
		}
	}
	return false
}

// printResultItems renders a table of UnsubmitResultItem with NAME, VERSION,
// and optionally REVISION ID columns. Shows short IDs by default, full UUIDs with -v.
func printResultItems(w io.Writer, items []rule_versions.UnsubmitResultItem, verbosity int) {
	showRevID := hasAnyRevisionID(items)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if showRevID {
		fmt.Fprintln(tw, "  NAME\tVERSION\tREVISION ID")
	} else {
		fmt.Fprintln(tw, "  NAME\tVERSION")
	}
	for _, item := range items {
		if showRevID {
			revID := ""
			if item.RevisionID != nil {
				if verbosity >= 1 {
					revID = item.RevisionID.String()
				} else {
					revID = rule_versions.ShortID(item.RevisionID.String())
				}
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", item.Name, item.Release, revID)
		} else {
			fmt.Fprintf(tw, "  %s\t%s\n", item.Name, item.Release)
		}
	}
	tw.Flush()
	fmt.Fprintln(w)
}
