package get

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/pkg/tableui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewTableKindGetCommand creates the get table-kind command for fetching a single table kind
func NewTableKindGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "table-kind <slug-or-id>",
		Short: "Display a single table kind with its field kinds",
		Long: `Display a table kind by slug, short ID, or full UUID.

Shows the table kind's details and its field kinds.

Examples:
  qctl get table-kind car-policy-bordereau
  qctl get table-kind 550e8400
  qctl get table-kind car-policy-bordereau -o yaml
  qctl get table-kind car-policy-bordereau -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			input := args[0]

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

			// Extract field kinds from the response (may be nil)
			var fieldKinds []dataset_kinds.DatasetFieldKindFull
			if kind.FieldKinds != nil {
				fieldKinds = *kind.FieldKinds
			}

			outputFormat, _ := cmd.Flags().GetString("output")

			switch outputFormat {
			case "json":
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(kind)

			case "yaml":
				encoder := yaml.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent(2)
				defer encoder.Close()
				return encoder.Encode(kind)

			default:
				w := cmd.OutOrStdout()

				// Print kind info as key-value header
				fmt.Fprintf(w, "Name:       %s\n", kind.Name)
				fmt.Fprintf(w, "Slug:       %s\n", kind.Slug)
				fmt.Fprintf(w, "ID:         %s\n", kind.ID.String())
				fmt.Fprintf(w, "Built-in:   %s\n", boolYesNo(kind.IsBuiltin))
				if ctx.Verbosity >= 2 {
					fmt.Fprintf(w, "Description: %s\n", derefStr(kind.Description))
				}
				fmt.Fprintln(w)

				if len(fieldKinds) == 0 {
					fmt.Fprintln(w, "No field kinds found.")
					return nil
				}

				// Print field kinds as a table with verbosity-dependent columns
				type fieldKindDisplay struct {
					Slug        string `json:"slug"`
					Name        string `json:"name"`
					Description string `json:"description"`
					Aliases     string `json:"aliases"`
					UpdatedAt   string `json:"updated_at"`
				}

				displayResults := make([]fieldKindDisplay, len(fieldKinds))
				for i, fk := range fieldKinds {
					displayResults[i] = fieldKindDisplay{
						Slug:        fk.Slug,
						Name:        fk.Name,
						Description: derefStr(fk.Description),
						Aliases:     formatAliases(fk.Aliases),
						UpdatedAt:   fk.UpdatedAt.Format("2006-01-02"),
					}
				}

				defaultCols := "slug,name,updated_at"
				if ctx.Verbosity >= 3 {
					defaultCols = "slug,name,description,aliases,updated_at"
				} else if ctx.Verbosity >= 2 {
					defaultCols = "slug,name,description,updated_at"
				}

				return tableui.PrintFromCmd(cmd, displayResults, defaultCols)
			}
		},
	}

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
