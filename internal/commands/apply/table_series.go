package apply

import (
	"fmt"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/table_series"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func applyTableSeries(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := table_series.ParseManifest(doc.Data)
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	c := table_series.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(ctx.Credential.AccessToken, m)
	if err != nil {
		return nil, fmt.Errorf("prepare table series apply: %w", err)
	}
	label := "table-series/" + terminal.Sanitize(p.Name)
	if len(p.Requests) == 0 {
		fmt.Fprintf(humanOut(cmd), "%s unchanged\n", label)
		return namedApplyResult(table_series.FromAPI(p.Current, p.Schedule), "table-series", p.Current.ID), nil
	}
	if len(p.Removals) > 0 {
		if err := confirmSeriesRemovals(cmd, doc, p); err != nil {
			return nil, err
		}
	}
	for _, member := range p.RenamedByName {
		fmt.Fprintf(cmd.ErrOrStderr(), "Note: spec.members[%d] names table %q by name; this apply renames it after the series, "+
			"so the next apply of this file will not find it. Name it by table ID %d instead.\n", member.Index, member.Table, member.DatasetID)
	}
	item, err := c.Apply(ctx.Credential.AccessToken, p)
	if err != nil {
		if item != nil {
			return namedApplyResult(table_series.FromAPI(item, p.Schedule), "table-series", item.ID), err
		}
		return nil, err
	}
	action := actionUpdated
	if p.Create {
		action = actionCreated
	}
	fmt.Fprintf(humanOut(cmd), "%s %s\n", label, action)
	return namedApplyResult(table_series.FromAPI(item, p.Schedule), "table-series", item.ID), nil
}

func confirmSeriesRemovals(cmd *cobra.Command, doc manifestDoc, p *table_series.Plan) error {
	if err := cmdutil.RequireNamedContext(cmd, "remove table series members"); err != nil {
		return err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := refuseUnconfirmedStructuredOutput(cmd, doc, yes); err != nil {
		return err
	}
	if yes {
		return nil
	}
	if !cmdutil.CanPrompt() {
		return qerrors.NewBadUsage("removing table series members requires confirmation; re-run with --yes")
	}
	fmt.Fprintf(humanOut(cmd), "Table series %q: remove table IDs %v\n", terminal.Sanitize(p.Name), p.Removals)
	if err := p.WriteRequests(humanOut(cmd)); err != nil {
		return err
	}
	ok, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Apply these table series changes?")
	if err != nil {
		return err
	}
	if !ok {
		return errDocumentDeclined
	}
	return nil
}
