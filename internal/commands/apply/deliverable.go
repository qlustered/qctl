package apply

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/deliverables"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// applyDeliverable applies a Deliverable document and prints partial results
// before returning a write error.
func applyDeliverable(cmd *cobra.Command, doc manifestDoc) (*applyResult, error) {
	m, err := deliverables.ParseManifest(doc.Data)
	if err != nil {
		return nil, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, err
	}
	token := ctx.Credential.AccessToken
	c := deliverables.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	p, err := c.Prepare(token, m, filepath.Dir(doc.Path))
	if err != nil {
		return nil, err
	}
	label := "deliverable/" + terminal.Sanitize(p.Slug)
	if len(p.Requests) == 0 {
		fmt.Fprintf(humanOut(cmd), "%s unchanged\n", label)
		if err := p.WriteRowFilterCounts(humanOut(cmd)); err != nil {
			return nil, err
		}
		fmt.Fprint(humanOut(cmd), deliverables.FormatApplyResult(p, &deliverables.ApplyResult{}))
		if !documentOutput(cmd) {
			return namedApplyResult(deliverables.FromAPI(p.Current, p.Revision), "deliverable", p.Current.ID), nil
		}
		document, err := c.ExportManifest(token, p.Current, p.Revision)
		if err != nil {
			return nil, err
		}
		return namedApplyResult(document, "deliverable", p.Current.ID), nil
	}
	if err := announceDeliverableWrites(cmd, doc, p, label); err != nil {
		return nil, err
	}
	r, err := c.Apply(token, p)
	if err != nil {
		if r != nil {
			fmt.Fprint(humanOut(cmd), deliverables.FormatApplyResult(p, r))
			return namedApplyResult(r.Manifest, "deliverable", r.Manifest.Status.ID), err
		}
		return nil, err
	}
	action := actionUpdated
	if r.Created {
		action = actionCreated
	}
	fmt.Fprintf(humanOut(cmd), "%s %s\n", label, action)
	fmt.Fprint(humanOut(cmd), deliverables.FormatApplyResult(p, r))
	return namedApplyResult(r.Manifest, "deliverable", r.Manifest.Status.ID), nil
}

// announceDeliverableWrites gates p's binding removals through
// confirmBindingRemovals when it plans any, and writes p's row filter counts
// unless that confirmation listed p's requests.
func announceDeliverableWrites(cmd *cobra.Command, doc manifestDoc, p *deliverables.Plan, label string) error {
	if len(p.Removals) > 0 || p.MayRemove || p.MayRemoveCells {
		listed, err := confirmBindingRemovals(cmd, doc, p, label)
		if err != nil || listed {
			return err
		}
	}
	return p.WriteRowFilterCounts(humanOut(cmd))
}

// confirmBindingRemovals gates p's binding removals: RequireNamedContext,
// then refuseUnconfirmedStructuredOutput, then --yes or a y/N prompt that
// lists the removals and p's requests. Without --yes on a stdin that cannot
// prompt it is a bad-usage error, and declining that prompt is
// errDocumentDeclined. It reports whether it wrote p's requests.
func confirmBindingRemovals(cmd *cobra.Command, doc manifestDoc, p *deliverables.Plan, label string) (bool, error) {
	if err := cmdutil.RequireNamedContext(cmd, "remove deliverable sheet, column or cell bindings"); err != nil {
		return false, err
	}
	yes, _ := cmd.Flags().GetBool("yes")
	if err := refuseUnconfirmedStructuredOutput(cmd, doc, yes); err != nil {
		return false, err
	}
	if yes {
		return false, nil
	}
	if !cmdutil.CanPrompt() {
		return false, qerrors.NewBadUsage("removing deliverable sheet, column or cell bindings requires confirmation; re-run with --yes")
	}
	out := humanOut(cmd)
	if len(p.Removals) > 0 {
		fmt.Fprintf(out, "%s: remove %s\n", label, strings.Join(p.Removals, ", "))
	}
	if p.MayRemove {
		fmt.Fprintf(out, "%s: the server may move column bindings when it re-anchors a sheet, and apply then removes "+
			"any binding left outside the sheet's listed columns\n", label)
	}
	if p.MayRemoveCells {
		fmt.Fprintf(out, "%s: replacing the copied template may move cell bindings, and apply then removes any cell outside spec.cells\n", label)
	}
	if err := p.WriteRequests(out); err != nil {
		return true, err
	}
	ok, err := cmdutil.ConfirmYesNo(cmd.ErrOrStderr(), "Apply these deliverable changes?")
	if err != nil {
		return true, err
	}
	if !ok {
		return true, errDocumentDeclined
	}
	return true, nil
}
