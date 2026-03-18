package version

import (
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/config"
	internalversion "github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
)

// NewCommand creates the version command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Print the version information for qctl client.`,
		Run:   runVersion,
	}

	return cmd
}

func runVersion(cmd *cobra.Command, args []string) {
	// Print client version
	fmt.Fprintf(os.Stdout, "qctl version %s (commit: %s)\n", internalversion.Version, internalversion.Commit)

	// Try to load config and print current context if available
	cfg, err := config.Load()
	if err == nil && cfg.CurrentContext != "" {
		fmt.Fprintf(os.Stdout, "Current context: %s\n", cfg.CurrentContext)
	}
}

// fetchUserEmail makes an API call to retrieve the current user's email
// This will be implemented once the API client is ready
// func fetchUserEmail(endpoint, token string) (string, error) {
// 	// Implementation will go here
// 	return "", nil
// }
