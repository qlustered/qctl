package diff

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// diffTableSchema previews a TableSchema apply: one "property: old → new" line
// per changed field, then the exact request body apply would send.
//
// It shares datasets.BuildSchemaOverrides and datasets.SchemaPatchBody with
// apply — not a parallel implementation — so the preview of a document cannot
// drift from the request apply would send for that document.
//
// That guarantee is per DOCUMENT. It reaches the whole FILE only because
// genericDiff refuses a multi-document manifest: this function reads filePath
// and strict-unmarshals it, and pkgmanifest.StrictUnmarshal decodes exactly
// one document, so without that refusal a five-document file would preview
// one change and apply five.
//
// Returns ErrDiffDifferences (silent exit 1) when the override set is
// non-empty, nil (exit 0, no output) when it is empty.
func diffTableSchema(cmd *cobra.Command, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	manifest, err := datasets.ParseTableSchemaManifest(data, filePath)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)

	datasetID, err := client.ResolveID(ctx.Credential.AccessToken, manifest.ManifestTableRef())
	if err != nil {
		return err
	}
	detail, err := client.GetSchemaDetails(ctx.Credential.AccessToken, datasetID)
	if err != nil {
		return fmt.Errorf("failed to get table schema: %w", err)
	}

	overrides, changes, err := datasets.BuildSchemaOverrides(detail, manifest.Spec, filePath)
	if err != nil {
		return err
	}
	if len(overrides) == 0 {
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprint(out, datasets.FormatSchemaFieldChanges(changes, ""))
	// The warnings go to stdout with the rest of the diff -- stderr is
	// contractually empty here, and callers gate on the exit code, not on the
	// text.
	for _, warn := range []string{
		cmdutil.ProducedColumnWarning(ctx, detail, changes),
		cmdutil.SchemaColumnRuleWarning(ctx, datasetID, datasets.DeletedColumns(changes), datasets.RetypedColumns(changes)),
	} {
		if warn != "" {
			fmt.Fprintf(out, "\n%s", strings.TrimSuffix(warn, "\n"))
		}
	}
	body, err := json.Marshal(datasets.SchemaPatchBody(detail, overrides))
	if err != nil {
		return fmt.Errorf("failed to encode request body: %w", err)
	}
	// json.Marshal escapes C0 but passes C1 code points through raw, so the
	// encoded body is not self-protecting: U+009B survives it as a live CSI
	// introducer. This preview is read by a human, not piped.
	fmt.Fprintf(out, "\nrequest body:\n%s\n", terminal.Sanitize(string(body)))
	return qerrors.ErrDiffDifferences
}
