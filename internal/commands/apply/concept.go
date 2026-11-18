package apply

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/concepts"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func applyConcept(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := concepts.ParseManifest(doc.Data)
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	c := concepts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m)
	if err != nil {
		return nil, fmt.Errorf("prepare standard field apply: %w", err)
	}
	label := "standard-field/" + terminal.Sanitize(m.Metadata.Slug)
	if len(p.Changes) == 0 {
		fmt.Fprintf(humanOut(cmd), "%s unchanged\n", label)
		return namedApplyResult(p.Current, "standard-field", p.Current.ID), nil
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
	return namedApplyResult(item, "standard-field", item.ID), nil
}
