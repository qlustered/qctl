package apply

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/dataset_kinds"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func applyTableKind(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := dataset_kinds.ParseManifest(doc.Data)
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	c := dataset_kinds.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	cc := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m, cc)
	if err != nil {
		return nil, fmt.Errorf("prepare table kind apply: %w", err)
	}
	label := "table-kind/" + terminal.Sanitize(m.Metadata.Slug)
	if len(p.Changes) == 0 {
		fmt.Fprintf(humanOut(cmd), "%s unchanged\n", label)
		return namedApplyResult(p.Current, "table-kind", p.Current.ID), nil
	}
	item, err := c.Apply(ctx.Credential.AccessToken, p)
	if err != nil {
		return nil, err
	}
	action := actionUpdated
	if p.Create {
		action = actionCreated
	}
	fmt.Fprintf(humanOut(cmd), "%s %s\n", label, action)
	return namedApplyResult(item, "table-kind", item.ID), nil
}
