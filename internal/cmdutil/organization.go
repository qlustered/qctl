package cmdutil

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/org"
	"github.com/spf13/cobra"
)

// ConfirmOrganizationMatch confirms a fuzzy organization name before mutation.
func ConfirmOrganizationMatch(cmd *cobra.Command, input, id, name string) error {
	if input == name || strings.EqualFold(input, id) ||
		(org.IsUUIDLike(input) && strings.HasPrefix(strings.ToLower(id), strings.ToLower(input))) {
		return nil
	}
	yes, _ := cmd.Flags().GetBool("yes")
	return Confirm(cmd.ErrOrStderr(), yes,
		"organization name needs confirmation; pass the exact name or full UUID, or --yes to confirm the fuzzy match",
		fmt.Sprintf("Did you mean %q?", name))
}
