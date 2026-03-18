package auth

import (
	"fmt"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/config"
	"github.com/spf13/cobra"
)

// NewLogoutCommand creates the auth logout command
func NewLogoutCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Logout and remove stored credentials",
		Long: `Logout from Qluster and remove stored credentials.

This command deletes the locally stored access token. Bearer tokens
are self-expiring and do not require a server-side logout call.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load config
			cfg, err := config.Load()
			if err != nil {
				return fmt.Errorf("failed to load config: %w", err)
			}

			// Resolve server
			serverFlag, _ := cmd.Flags().GetString("server")
			allowInsecure := config.IsInsecureHTTPAllowed()
			if f, _ := cmd.Flags().GetBool("allow-insecure-http"); f {
				allowInsecure = true
			}

			serverURL, err := config.ResolveServer(cfg, serverFlag)
			if err != nil {
				return err
			}

			// Validate server URL
			if err := config.ValidateServer(serverURL, allowInsecure); err != nil {
				return err
			}

			// Resolve organization
			orgFlag, _ := cmd.Root().PersistentFlags().GetString("org")
			orgID, err := config.ResolveOrganization(cfg, orgFlag)
			if err != nil {
				return err
			}

			// Get endpoint key
			endpointKey, err := config.NormalizeEndpointKey(serverURL)
			if err != nil {
				return fmt.Errorf("failed to normalize endpoint: %w", err)
			}

			// Check if plaintext storage is allowed
			allowPlaintext := auth.IsPlaintextAllowed()
			credStore := auth.NewCredentialStore(allowPlaintext)

			// Delete stored credential
			if err := credStore.Delete(endpointKey, orgID); err != nil {
				return fmt.Errorf("failed to delete stored credential: %w", err)
			}

			fmt.Printf("Successfully logged out from organization %s\n", orgID)
			return nil
		},
	}

	return cmd
}
