package copycmd

import "github.com/spf13/cobra"

// bindCloudSourceSetFlag binds the repeatable --set flag to values.
func bindCloudSourceSetFlag(cmd *cobra.Command, values *[]string) {
	cmd.Flags().StringArrayVar(values, "set", nil, "Override a non-credential source field as key=value (repeatable)")
}
