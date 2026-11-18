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

// diffTable previews the create or update plan that apply uses. The plan owns
// both the change list and the typed request body, so neither command has a
// parallel reconstruction that can drift from the wire request.
func diffTable(cmd *cobra.Command, filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	manifest, err := datasets.ParseTableManifest(data)
	if err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to load manifest")
	}
	if errs := manifest.Validate(); len(errs) > 0 {
		return qerrors.NewBadUsagef("manifest validation failed:\n  - %s", joinTableValidationErrors(errs))
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	plan, err := client.PrepareApply(ctx.Credential.AccessToken, manifest)
	if err != nil {
		return fmt.Errorf("failed to prepare table apply: %w", err)
	}
	changes := plan.Changes()
	if len(changes) == 0 {
		return nil
	}

	out := cmd.OutOrStdout()
	fmt.Fprint(out, datasets.FormatTableFieldChanges(changes, ""))
	for _, request := range []struct {
		label string
		body  any
	}{
		{"request body", plan.RequestBody()},
		{"table kinds PUT body", plan.TableKindsBody()},
		{"column standard fields PUT body", plan.ColumnConceptsBody()},
	} {
		if request.body == nil {
			continue
		}
		body, err := json.Marshal(request.body)
		if err != nil {
			return fmt.Errorf("failed to encode %s: %w", request.label, err)
		}
		// json.Marshal leaves C1 control characters raw in this human preview.
		fmt.Fprintf(out, "\n%s:\n%s\n", request.label, terminal.Sanitize(string(body)))
	}
	return qerrors.ErrDiffDifferences
}

func joinTableValidationErrors(errs []datasets.ValidationError) string {
	text := make([]string, len(errs))
	for i := range errs {
		text[i] = errs[i].Error()
	}
	return strings.Join(text, "\n  - ")
}
