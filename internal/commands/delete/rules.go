package delete

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/qlustered/qctl/internal/terminal"
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

Under -o json or yaml, the server's answer is printed: {message, deleted,
not_found, skipped}, or one List of them when several files are given. -o name
prints rule/<revision-id> for each deleted revision. The prose goes to stderr.

Examples:
  # Unsubmit rules defined in a file
  qctl delete rules -f rules.py

  # Skip confirmation prompt
  qctl delete rules -f rules.py --yes

  # Print what was removed as JSON
  qctl delete rules -f rules.py --yes -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("rule"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(filePaths) == 0 {
				return qerrors.NewBadUsage("at least one file is required (-f or --filename)")
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
			w := cmdutil.NewResultWriter(cmd)
			var results []any
			var names []string
			var runErr error
			for _, filePath := range filePaths {
				result, err := unsubmitRuleFile(w.Human, cmd.ErrOrStderr(), client, token, filePath, ctx.OrganizationID, orgName, yes, ctx.Verbosity)
				if err != nil {
					runErr = err
					break
				}
				if result != nil {
					results = append(results, result)
					names = append(names, deletedRuleNames(result)...)
				}
			}

			writeErr := w.WriteRun(len(filePaths), results, names...)
			if runErr != nil {
				return runErr
			}
			return writeErr
		},
	}

	cmd.Flags().StringArrayVarP(&filePaths, "filename", "f", nil, "Path to the Python source file (can be specified multiple times)")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")
	cmd.Flags().String("server", "", "Override server URL")
	cmd.Flags().String("user", "", "Override user")

	return cmd
}

// unsubmitRuleFile handles unsubmitting rules from a single file. It returns
// the server's answer, or nil when the prompt was declined.
func unsubmitRuleFile(out, promptOut io.Writer, client *rule_versions.Client, token, filePath, orgID, orgName string, yes bool, verbosity int) (*rule_versions.RuleVersionUnsubmitResponse, error) {
	// Read file
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	fileText := string(fileBytes)

	// Display confirmation
	fmt.Fprintf(out, "Organization : %s (%s)\n", terminal.Sanitize(orgName), terminal.Sanitize(orgID))
	fmt.Fprintf(out, "File         : %s\n", filepath.Base(filePath))
	fmt.Fprintln(out)

	if !yes {
		confirmed, err := cmdutil.ConfirmYesNo(promptOut, "Unsubmit (remove) rules defined in this file from the catalog?")
		if err != nil {
			return nil, err
		}
		if !confirmed {
			fmt.Fprintf(out, "Skipping %s\n\n", filePath)
			return nil, nil
		}
	}

	// Unsubmit
	fmt.Fprintf(out, "Deleting rules from %s...\n", filepath.Base(filePath))
	result, err := client.UnsubmitRuleVersion(token, fileText)
	if err != nil {
		return nil, fmt.Errorf("rule delete failed for %s: %w", filePath, err)
	}

	// Display results
	displayUnsubmitResult(out, result, verbosity)
	return result, nil
}

// deletedRuleNames returns rule/<revision-id> for each deleted item that
// carries a revision id.
func deletedRuleNames(result *rule_versions.RuleVersionUnsubmitResponse) []string {
	var names []string
	for _, item := range result.Deleted {
		if item.RevisionID != nil {
			names = append(names, "rule/"+item.RevisionID.String())
		}
	}
	return names
}

// displayUnsubmitResult prints the unsubmit result to w.
func displayUnsubmitResult(w io.Writer, result *rule_versions.RuleVersionUnsubmitResponse, verbosity int) {
	if result.Message != "" {
		fmt.Fprintf(w, "\n%s\n\n", terminal.Sanitize(result.Message))
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
			fmt.Fprintf(w, "  Reason: %s\n", terminal.Sanitize(group.Reason))
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
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", terminal.Sanitize(item.Name), terminal.Sanitize(item.Release), revID)
		} else {
			fmt.Fprintf(tw, "  %s\t%s\n", terminal.Sanitize(item.Name), terminal.Sanitize(item.Release))
		}
	}
	tw.Flush()
	fmt.Fprintln(w)
}
