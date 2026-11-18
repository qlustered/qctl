package diff

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func diffTableKind(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read TableKind manifest: %w", err)
	}
	m, err := dataset_kinds.ParseManifest(data)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	cc := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m, cc)
	if err != nil {
		return fmt.Errorf("prepare table kind diff: %w", err)
	}
	if len(p.Changes) == 0 {
		return nil
	}
	for _, change := range p.Changes {
		oldValue, _ := json.Marshal(change.Old)
		newValue, _ := json.Marshal(change.New)
		fmt.Fprintf(cmd.OutOrStdout(), "%s : %s -> %s\n", change.Field, terminal.Sanitize(string(oldValue)), terminal.Sanitize(string(newValue)))
	}
	body, err := p.RequestJSON()
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "\nrequest body:\n%s\n", terminal.Sanitize(string(body)))
	return qerrors.ErrDiffDifferences
}
