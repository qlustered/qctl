package cmdutil

import (
	"strings"

	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// AnnotationColumns is the cobra annotation key DeclareColumns records a
// command's columns under, comma-separated.
const AnnotationColumns = "columns"

// DeclareColumns records on cmd the union of output.AvailableColumns for each
// sample, in order and without duplicates, and returns it.
//
// A sample is a value of the type the command hands its printer, such as
// []orgs.OrgDisplay(nil). A command with more than one display passes one
// sample per display.
func DeclareColumns(cmd *cobra.Command, samples ...interface{}) []string {
	var columns []string
	seen := map[string]bool{}
	for _, sample := range samples {
		for _, c := range output.AvailableColumns(sample, nil) {
			if !seen[c] {
				seen[c] = true
				columns = append(columns, c)
			}
		}
	}
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[AnnotationColumns] = strings.Join(columns, ",")
	return columns
}

// DeclaredColumns returns the columns DeclareColumns recorded on cmd, or nil.
func DeclaredColumns(cmd *cobra.Command) []string {
	if raw := cmd.Annotations[AnnotationColumns]; raw != "" {
		return strings.Split(raw, ",")
	}
	return nil
}

// ValidateDeclaredColumns rejects, with exit 2, a --columns name that cmd's
// declared columns do not include, whatever --output is. A command that
// declared no columns is not checked.
func ValidateDeclaredColumns(cmd *cobra.Command) error {
	declared := DeclaredColumns(cmd)
	if len(declared) == 0 {
		return nil
	}
	raw, _ := cmd.Flags().GetString("columns")
	return output.ValidateColumns(output.ParseColumns(raw), declared)
}
