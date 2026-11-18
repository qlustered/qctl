package delete

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// NewStandardFieldCommand creates the delete standard-field command.
func NewStandardFieldCommand() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:         "standard-field <ref>",
		Short:       "Delete an unreferenced standard field",
		Annotations: cmdutil.WithFormats(catalog.For("standard-field"), "table"),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := cmdutil.Bootstrap(cmd)
			if err != nil {
				return err
			}
			client := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
			token := ctx.Credential.AccessToken
			id, err := client.ResolveID(token, args[0])
			if err != nil {
				return err
			}
			concept, err := client.Get(token, id)
			if err != nil {
				return fmt.Errorf("failed to get concept: %w", err)
			}
			name, slug := terminal.Sanitize(concept.Name), terminal.Sanitize(concept.Slug)
			if concept.IsBuiltin {
				return qerrors.NewBadUsagef("built-in standard field %q cannot be deleted", slug)
			}
			if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes,
				fmt.Sprintf("confirmation required but stdin is not an interactive terminal -- pass --yes to delete standard field %q (%s)", name, slug),
				fmt.Sprintf("Delete standard field %q (%s)? This cannot be undone.", name, slug)); err != nil {
				return err
			}
			if err := client.Delete(token, id); err != nil {
				var apiErr *concepts.APIError
				if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
					switch apiErr.Code {
					case "ERR_STILL_REFERENCED":
						return standardFieldStillReferenced(slug, apiErr)
					case "ERR_DB_DEPENDENCY_VIOLATION":
						return qerrors.NewBadUsage("a reference changed while deleting; nothing was deleted; retry")
					}
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Standard field %q (%s) deleted.\n", name, slug)
			return nil
		},
	}
	addDeleteFlags(cmd, &yes)
	return cmd
}

func standardFieldStillReferenced(slug string, err *concepts.APIError) error {
	usage, _ := err.Context["usage"].(map[string]interface{})
	count := func(key string) string {
		if value, ok := usage[key]; ok {
			return fmt.Sprint(value)
		}
		return "unknown"
	}
	return qerrors.NewBadUsagef("standard field %q is still referenced:\n  table kinds: %s (unlink it with `apply -f` using kind: TableKind)\n  table columns: %s (remove visible columns from spec.column_standard_fields in their kind: Table manifests and apply them; dropped columns on active or disabled tables still count)\n  rule revisions: %s (revisions that have run usually cannot be cleared)",
		slug, count("problem_domains"), count("dataset_columns"), count("rule_revisions"))
}
