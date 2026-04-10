package get

import (
	"encoding/json"
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/output"
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
				// Print kind info as key-value header
				fmt.Fprintf(cmd.OutOrStdout(), "Name:       %s\n", kind.Name)
				fmt.Fprintf(cmd.OutOrStdout(), "Slug:       %s\n", kind.Slug)
				fmt.Fprintf(cmd.OutOrStdout(), "ID:         %s\n", kind.ID.String())
				fmt.Fprintf(cmd.OutOrStdout(), "Built-in:   %s\n", boolYesNo(kind.IsBuiltin))
				fmt.Fprintf(cmd.OutOrStdout(), "\n")

				if len(fieldKinds) == 0 {
					fmt.Fprintln(cmd.OutOrStdout(), "No field kinds found.")
					return nil
				}

				// Print field kinds as a table
				type fieldKindDisplay struct {
					Slug      string `json:"slug"`
					Name      string `json:"name"`
					UpdatedAt string `json:"updated_at"`
				}

				displayResults := make([]fieldKindDisplay, len(fieldKinds))
				for i, fk := range fieldKinds {
					displayResults[i] = fieldKindDisplay{
						Slug:      fk.Slug,
						Name:      fk.Name,
						UpdatedAt: fk.UpdatedAt.Format("2006-01-02"),
					}
				}

				setDefaultColumns(cmd, "slug,name,updated_at")
				printer, err := output.NewPrinterFromCmd(cmd)
				if err != nil {
					return fmt.Errorf("failed to create output printer: %w", err)
				}
				return printer.Print(displayResults)
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
