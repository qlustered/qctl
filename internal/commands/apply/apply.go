package apply

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewCommand creates the apply command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "apply",
		Short: "Apply configurations",
		Long: `Commands for applying configurations declaratively from files.

If you specify -f with a YAML file, the resource type is inferred from
the 'kind' field in the manifest (like kubectl). Otherwise, use a
subcommand to specify the resource type explicitly.

Examples:
  # Generic apply (infers kind from file)
  qctl apply -f table.yaml
  qctl apply -f destination.yaml

  # Explicit subcommand
  qctl apply table -f table.yaml
  qctl apply destination -f dest.yaml`,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return fmt.Errorf("filename is required (-f or --filename)\n\nUsage:\n  qctl apply -f <file.yaml>\n  qctl apply <resource> -f <file.yaml>")
			}
			return genericApply(cmd, filePath)
		},
	}

	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file")
	cmd.Flags().Bool("fail-fast", false, "Stop processing on first document failure")

	// Add subcommands
	cmd.AddCommand(NewDestinationCommand())
	cmd.AddCommand(NewDatasetCommand())
	cmd.AddCommand(NewCloudSourceCommand())

	return cmd
}
