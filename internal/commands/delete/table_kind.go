package delete

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewTableKindCommand creates the delete table-kind command.
func NewTableKindCommand() *cobra.Command {
	var yes bool
	var untagTables bool
	cmd := &cobra.Command{
		Use:   "table-kind <slug-or-name-or-id>",
		Short: "Delete a table kind, optionally untagging its tables first",
		Long: `Delete a table kind and its standard field links. The standard fields themselves are kept.

Built-in table kinds cannot be deleted. Untag a table kind from active and
disabled tables before deleting it, or pass --untag-tables. That flag lists
tables visible to you, removes the kind from them, then deletes it. Untags
already made are kept if a later step fails. Other table kinds are kept.

Examples:
  qctl delete table-kind car-policy-bordereau
  qctl delete table-kind car-policy-bordereau --yes
  qctl delete table-kind car-policy-bordereau --untag-tables`,
		Annotations: cmdutil.WithFormats(catalog.For("table-kind"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return executeDeleteTableKind(cmd, args[0], yes, untagTables)
		},
	}
	addDeleteFlags(cmd, &yes)
	cmd.Flags().BoolVar(&untagTables, "untag-tables", false, "Untag this kind from visible active and disabled tables before deleting it")
	return cmd
}

func executeDeleteTableKind(cmd *cobra.Command, ref string, yes, untagTables bool) error {
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	token := ctx.Credential.AccessToken
	kindID, err := client.ResolveDatasetKindID(token, ref)
	if err != nil {
		return err
	}
	kind, err := client.GetDatasetKind(token, kindID)
	if err != nil {
		return fmt.Errorf("failed to get table kind: %w", err)
	}
	name, slug := terminal.Sanitize(kind.Name), terminal.Sanitize(kind.Slug)
	if kind.IsBuiltin {
		return qerrors.NewBadUsagef("built-in table kind %q cannot be deleted; submit table-kinds can still edit it", slug)
	}
	var tables []taggedTable
	if untagTables {
		tables, err = listTaggedTables(ctx.ServerURL, ctx.OrganizationID, token, ctx.Verbosity, kindID)
		if err != nil {
			return fmt.Errorf("failed to list tagged tables: %w", err)
		}
		if ctx.Verbosity > 0 {
			for _, table := range tables {
				fmt.Fprintf(cmd.ErrOrStderr(), "  %s (%d)\n", terminal.Sanitize(table.Name), table.ID)
			}
		}
	}

	prompt := fmt.Sprintf("Delete table kind %q (%s)? Its %d standard field link(s) will be removed; the standard fields themselves are kept. This cannot be undone.",
		name, slug, lenConcepts(kind))
	if untagTables {
		prompt = fmt.Sprintf("Untag table kind %q (%s) from %d visible table(s), then delete it and its %d standard field link(s)? Untags already made remain if a later step fails. This cannot be undone.",
			name, slug, len(tables), lenConcepts(kind))
	}
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
		fmt.Sprintf("confirmation required but stdin is not an interactive terminal -- pass --yes to delete table kind %q (%s)", name, slug), prompt,
	); err != nil {
		return err
	}
	untagged := 0
	if untagTables {
		untagged, err = untagTaggedTables(client, token, kindID, tables)
		if err != nil {
			return err
		}
	}
	if err := client.DeleteDatasetKind(token, kindID); err != nil {
		return tableKindDeleteError(err, slug, untagged, untagTables)
	}
	if untagTables {
		fmt.Fprintf(cmd.OutOrStdout(), "Table kind %q (%s) deleted; %d table(s) untagged.\n", name, slug, untagged)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Table kind %q (%s) deleted.\n", name, slug)
	}
	return nil
}

func tableKindDeleteError(err error, slug string, untagged int, untagTables bool) error {
	var apiErr *dataset_kinds.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
		switch apiErr.Code {
		case "ERR_STILL_REFERENCED":
			if untagTables {
				return qerrors.NewBadUsagef("%s table(s) are still tagged with table kind %q (active or disabled); %d visible table(s) were untagged and remain so. Other tagged tables may be hidden from your listing, or a table was tagged during deletion; retry with --untag-tables", taggedTableCount(apiErr), slug, untagged)
			}
			return qerrors.NewBadUsagef("%s table(s) are still tagged with table kind %q (active or disabled); remove its slug from spec.table_kinds in each kind: Table manifest and apply it first", taggedTableCount(apiErr), slug)
		case "ERR_DB_DEPENDENCY_VIOLATION":
			if untagTables {
				return qerrors.NewBadUsagef("a table was tagged while deleting; the kind was not deleted; %d table(s) were untagged and remain so; retry with --untag-tables", untagged)
			}
			return qerrors.NewBadUsage("a table was tagged while deleting; nothing was deleted; retry")
		}
	}
	if untagTables {
		return fmt.Errorf("%d table(s) were untagged and remain so; failed to delete table kind: %w", untagged, err)
	}
	return err
}

func lenConcepts(kind *dataset_kinds.DatasetKindWithConcepts) int {
	if kind.Concepts == nil {
		return 0
	}
	return len(*kind.Concepts)
}

func taggedTableCount(err *dataset_kinds.APIError) string {
	usage, ok := err.Context["usage"].(map[string]interface{})
	if !ok {
		return "Some"
	}
	if count, ok := usage["datasets"]; ok {
		return fmt.Sprint(count)
	}
	return "Some"
}
