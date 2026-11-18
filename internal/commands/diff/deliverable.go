package diff

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// diffDeliverable prints the requests applying the kind: Deliverable manifest
// at path would send, reading spec.template relative to path's directory.
// When there are none it prints only the plan's notes, on stderr.
func diffDeliverable(cmd *cobra.Command, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Deliverable manifest: %w", err)
	}
	m, err := deliverables.ParseManifest(data)
	if err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m, filepath.Dir(path))
	if err != nil {
		return err
	}
	if len(p.Requests) == 0 {
		return p.WriteRequests(cmd.ErrOrStderr())
	}
	if err := p.WriteRequests(cmd.OutOrStdout()); err != nil {
		return err
	}
	return qerrors.ErrDiffDifferences
}
