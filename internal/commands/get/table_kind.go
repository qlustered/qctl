package get

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// standardFieldDisplay is a row in the table-kind standard field listing.
type standardFieldDisplay struct {
	Slug              string `json:"slug"`
	Name              string `json:"name"`
	ExpectedFieldType string `json:"expected_field_type"`
	Description       string `json:"description"`
	Aliases           string `json:"aliases"`
	UpdatedAt         string `json:"updated_at"`
}

// NewTableKindGetCommand creates the get table-kind command.
func NewTableKindGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-kind <slug-or-id>",
		Short: "Display a single table kind with its standard fields",
		Long: `Display a table kind by slug, short ID, or full UUID.

Shows the table kind's details and its standard fields.

Examples:
  qctl get table-kind car-policy-bordereau
  qctl get table-kind 550e8400
  qctl get table-kind car-policy-bordereau -o yaml
  qctl get table-kind car-policy-bordereau -o json`,
		Annotations: cmdutil.WithFormats(catalog.For("table-kind"), "table", "json", "yaml"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]
			if input == "" {
				return qerrors.NewBadUsage("table kind identifier cannot be empty")
			}

			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}

			client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

			kindID, err := client.ResolveDatasetKindID(ctx.Credential.AccessToken, input)
			if err != nil {
				return err
			}

			kind, err := client.GetDatasetKind(ctx.Credential.AccessToken, kindID)
			if err != nil {
				return fmt.Errorf("failed to get table kind: %w", err)
			}

			// Extract standard fields from the response (may be nil)
			var standardFields []dataset_kinds.DatasetConceptFull
			if kind.Concepts != nil {
				standardFields = *kind.Concepts
			}

			outputFormat, _ := cmd.Flags().GetString("output")

			switch outputFormat {
			case "json", "yaml": //nolint:goconst // Keep output format handling local to this command.
				return output.WriteRaw(cmd.OutOrStdout(), outputFormat, dataset_kinds.FromAPI(kind))

			default:
				w := cmd.OutOrStdout()

				// Print kind info as key-value header. The name, slug and
				// description are the server's text, so they are sanitized
				// here; the concept table below goes through tableui,
				// which sanitizes its own cells.
				fmt.Fprintf(w, "Name:       %s\n", terminal.Sanitize(kind.Name))
				fmt.Fprintf(w, "Slug:       %s\n", terminal.Sanitize(kind.Slug))
				fmt.Fprintf(w, "ID:         %s\n", kind.ID.String())
				fmt.Fprintf(w, "Built-in:   %s\n", boolYesNo(kind.IsBuiltin))
				if kind.IsBuiltin {
					fmt.Fprintf(w, "Customized: %s\n", boolYesNo(kind.IsCustomized))
				}
				if ctx.Verbosity >= 2 {
					fmt.Fprintf(w, "Description: %s\n", terminal.Sanitize(derefStr(kind.Description)))
				}
				fmt.Fprintln(w)

				if len(standardFields) == 0 {
					fmt.Fprintln(w, "No standard fields linked to this table kind.")
					return nil
				}

				// Print standard fields as a table with verbosity-dependent columns
				displayResults := make([]standardFieldDisplay, len(standardFields))
				for i, field := range standardFields {
					displayResults[i] = standardFieldDisplay{
						Slug:              field.Slug,
						Name:              field.Name,
						ExpectedFieldType: formatExpectedFieldType(field.ExpectedFieldType),
						Description:       derefStr(field.Description),
						Aliases:           formatAliases(field.Aliases),
						UpdatedAt:         field.UpdatedAt.Format("2006-01-02"),
					}
				}

				defaultCols := "slug,name,expected_field_type"
				if ctx.Verbosity >= 3 {
					defaultCols = "slug,name,expected_field_type,description,aliases"
				} else if ctx.Verbosity >= 2 {
					defaultCols = "slug,name,expected_field_type,description"
				}

				return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
			}
		},
	}

	cmdutil.DeclareColumns(cmd, []standardFieldDisplay(nil))
	return cmd
}

func boolYesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func derefStr(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

func formatAliases(aliases *[]string) string {
	if aliases == nil || len(*aliases) == 0 {
		return "-"
	}
	return strings.Join(*aliases, ", ")
}

func formatExpectedFieldType(fieldType *api.FieldType) string {
	if fieldType == nil {
		return "-"
	}
	return string(*fieldType)
}
