package get

import "github.com/spf13/cobra"

// newTestRootCmd returns a "qctl" root command carrying the persistent flags
// the real root command registers (internal/commands/root/root.go). It is
// the common part of every setupTestCommandFor<X> helper in this package:
// callers still build and attach their own "get" child and subcommand(s), so
// only the boilerplate that was byte-for-byte identical across files lives
// here.
func newTestRootCmd() *cobra.Command {
	rootCmd := &cobra.Command{Use: "qctl"}
	rootCmd.PersistentFlags().String("server", "", "API server URL")
	rootCmd.PersistentFlags().String("user", "", "User email")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers")
	rootCmd.PersistentFlags().Int("max-column-width", 80, "Maximum width for table columns")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http://")
	// The real root uses a count flag, not a bool.
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level")
	return rootCmd
}
