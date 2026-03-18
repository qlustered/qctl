package root

import (
	"fmt"

	"github.com/qlustered/qctl/internal/commands/apply"
	"github.com/qlustered/qctl/internal/commands/auth"
	"github.com/qlustered/qctl/internal/commands/submit"
	"github.com/qlustered/qctl/internal/commands/completion"
	"github.com/qlustered/qctl/internal/commands/config"
	"github.com/qlustered/qctl/internal/commands/create"
	"github.com/qlustered/qctl/internal/commands/delete"
	"github.com/qlustered/qctl/internal/commands/disable"
	"github.com/qlustered/qctl/internal/commands/describe"
	"github.com/qlustered/qctl/internal/commands/docs"
	"github.com/qlustered/qctl/internal/commands/download"
	"github.com/qlustered/qctl/internal/commands/enable"
	"github.com/qlustered/qctl/internal/commands/explain"
	"github.com/qlustered/qctl/internal/commands/get"
	"github.com/qlustered/qctl/internal/commands/inspect"
	"github.com/qlustered/qctl/internal/commands/kill"
	"github.com/qlustered/qctl/internal/commands/run"
	"github.com/qlustered/qctl/internal/commands/set_default"
	"github.com/qlustered/qctl/internal/commands/undelete"
	"github.com/qlustered/qctl/internal/commands/upload"
	"github.com/qlustered/qctl/internal/commands/version"
	internalversion "github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
)

var (
	// cfgFile is the config file path
	cfgFile string
)

var rootCmd = &cobra.Command{
	Use:   "qctl",
	Short: "qctl - CLI for Qluster platform",
	Long: `qctl is a command-line interface for managing Qluster resources.

It provides commands for managing tables, ingestion pipelines, storage,
rules, queues, training jobs, and more.`,
	Version:       internalversion.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Disable default completion command - we provide our own with better help text
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.SetVersionTemplate(fmt.Sprintf("qctl version %s (commit: %s)\n", internalversion.Version, internalversion.Commit))

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.qctl/config)")
	rootCmd.PersistentFlags().String("server", "", "API server URL (overrides context config)")
	rootCmd.PersistentFlags().String("user", "", "User email (overrides context config)")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers (table format only)")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display (table format only)")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns (0 = no limit)")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext (json/yaml formats)")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http:// endpoints")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level (-v=7 structured HTTP logging, -v=8 curl with redacted tokens, -v=9 curl with full tokens)")
	rootCmd.PersistentFlags().Duration("request-timeout", 0, "Request timeout (0 = default)")
	rootCmd.PersistentFlags().Int("retries", 0, "Number of retries for failed requests (0 = default)")
	rootCmd.PersistentFlags().StringP("org", "O", "", "Organization ID or name (overrides QCTL_ORG env and context config)")

	// Add subcommands
	rootCmd.AddCommand(version.NewCommand())
	rootCmd.AddCommand(config.NewCommand())
	rootCmd.AddCommand(auth.NewCommand())
	rootCmd.AddCommand(get.NewCommand())
	rootCmd.AddCommand(describe.NewCommand())
	rootCmd.AddCommand(explain.NewCommand())
	rootCmd.AddCommand(docs.NewCommand(rootCmd))
	rootCmd.AddCommand(completion.NewCommand(rootCmd))

	// Verb commands (kubectl-style)
	rootCmd.AddCommand(run.NewCommand())
	rootCmd.AddCommand(kill.NewCommand())
	rootCmd.AddCommand(upload.NewCommand())
	rootCmd.AddCommand(download.NewCommand())
	rootCmd.AddCommand(delete.NewCommand())
	rootCmd.AddCommand(undelete.NewCommand())
	rootCmd.AddCommand(enable.NewCommand())
	rootCmd.AddCommand(disable.NewCommand())
	rootCmd.AddCommand(set_default.NewCommand())
	rootCmd.AddCommand(apply.NewCommand())
	rootCmd.AddCommand(submit.NewCommand())
	rootCmd.AddCommand(create.NewCommand())
	rootCmd.AddCommand(inspect.NewCommand())
}

// initConfig is called before running any command
// Config loading is now handled by individual commands using the internal/config package
func initConfig() {
	// This is intentionally minimal - config is loaded on-demand by commands
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

// GetRootCmd returns the root command (useful for adding subcommands)
func GetRootCmd() *cobra.Command {
	return rootCmd
}
