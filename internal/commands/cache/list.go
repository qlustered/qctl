package cache

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/aliasstore"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

func newListCommand() *cobra.Command {
	return &cobra.Command{
		Use:         "list",
		Short:       "List persisted UUID aliases for the current server",
		Args:        cobra.NoArgs,
		Annotations: cmdutil.WithFormats(catalog.For("alias"), "table", "json", "yaml"),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, err := cmdutil.BootstrapWithoutAuth(cmd)
			if err != nil {
				return err
			}
			serverKey, err := config.NormalizeEndpointKey(ctx.ServerURL)
			if err != nil {
				return err
			}

			entries, err := listAliases(serverKey)
			if err != nil {
				return err
			}

			format, _ := cmd.Flags().GetString("output")
			if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
				printer, err := output.NewPrinterFromCmd(cmd)
				if err != nil {
					return err
				}
				return printer.Print(aliasRows(entries))
			}

			if len(entries) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No aliases for the current server.")
				return nil
			}

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ALIAS\tRESOURCE\tUUID")
			for _, e := range entries {
				fmt.Fprintf(w, "%s\t%s\t%s\n", e.Alias, e.ResourceType, e.UUID)
			}
			return w.Flush()
		},
	}
}

// listAliases returns the store's entries for serverKey, and none when the
// store file does not exist yet.
func listAliases(serverKey string) ([]aliasstore.Entry, error) {
	store, err := aliasstore.OpenReadOnly()
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer store.Close()
	return store.List(serverKey)
}

// aliasRow is one alias as -o json and -o yaml print it.
type aliasRow struct {
	Alias    string  `json:"alias"`
	Resource *string `json:"resource"`
	UUID     string  `json:"uuid"`
}

// catalogResources names each alias resource type by its catalog resource.
var catalogResources = map[aliasstore.ResourceType]string{
	aliasstore.ResourceDatasetRule: "table-rule",
}

// aliasRows returns a row for each entry, with the resource null for an alias
// whose prefix no resource type registers.
func aliasRows(entries []aliasstore.Entry) []aliasRow {
	rows := make([]aliasRow, 0, len(entries))
	for _, e := range entries {
		row := aliasRow{Alias: e.Alias, UUID: e.UUID}
		if e.ResourceType != "" {
			resource := string(e.ResourceType)
			if name, ok := catalogResources[e.ResourceType]; ok {
				resource = name
			}
			row.Resource = &resource
		}
		rows = append(rows, row)
	}
	return rows
}
