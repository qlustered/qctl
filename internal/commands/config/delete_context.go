package config

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

func newDeleteContextCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete-context <name>",
		Short: "Delete a context",
		Long: `Delete a context from the configuration. Cannot delete the current context.

Examples:
  # Delete a context (will ask for confirmation)
  qctl config delete-context my-context

  # Skip confirmation prompt
  qctl config delete-context my-context --yes`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			contextName := args[0]

			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Verify the context exists before asking for confirmation
			if _, exists := cfg.Contexts[contextName]; !exists {
				return fmt.Errorf("context %q not found", contextName)
			}

			// Check if trying to delete current context before asking for confirmation
			if cfg.CurrentContext == contextName {
				return fmt.Errorf("cannot delete current context %q", contextName)
			}

			if !yes {
				confirmed, err := cmdutil.ConfirmYesNo(fmt.Sprintf("Delete context %q?", contextName))
				if err != nil {
					return err
				}
				if !confirmed {
					fmt.Println("Delete cancelled")
					return nil
				}
			}

			if err := cfg.DeleteContext(contextName); err != nil {
				return err
			}

			if err := cfg.Save(); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Printf("Deleted context %q\n", contextName)
			return nil
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompt")

	return cmd
}
