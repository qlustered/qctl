package duplicate_group //nolint:revive // Package name mirrors the hyphenated command.

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/duplicate_groups"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

func newAllowAllCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "allow-all",
		Short: "Allow every repeated key of an upload",
		Long: `Propose, or approve, an allow of the current copies on each repeated_key
group of an upload (--file-id, the top-level file, not one of its sections)
that holds a copy, and apply them.

A group is skipped, and named on its own line, when its own pending decision
differs (pending_differs), it already has an effective decision of its own
(decided_here), or you may not decide it (not_designated, excluded). Allows
that still need approvals are listed as approved with their count.

Prompts unless --yes. -o json|yaml prints the server's {allowed, approved,
skipped, apply}; -o name prints duplicate-group/<uuid> for each group allowed
or approved.`,
		Example:     `  qctl duplicate-group allow-all --table orders --file-id 311 --comment "re-sent export" --yes`,
		Annotations: decisionAnnotations(),
		Args:        cobra.NoArgs,
		RunE:        runAllowAll,
	}
	addCommonFlags(cmd, "Comment recorded with each decision")
	cmdutil.RefInt(cmd.Flags(), "file-id", "file", "The upload's file id (required)")
	_ = cmd.MarkFlagRequired("file-id")
	return cmd
}

func runAllowAll(cmd *cobra.Command, _ []string) error {
	if _, err := preflight(cmd, nil); err != nil {
		return err
	}
	fileID, _, err := cmdutil.PositiveIDFlag(cmd, "file-id")
	if err != nil {
		return err
	}
	if fileID == 0 {
		return qerrors.NewBadUsage("--file-id is required")
	}
	comment, err := commentFlag(cmd)
	if err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	question := fmt.Sprintf("Allow every repeated key of file/%d on table/%d? Each repeated_key group of the upload "+
		"holding a copy gets an allow of its current copies.", fileID, t.tableID)
	if err := confirm(cmd, "allowing an upload's repeated keys", question); err != nil {
		return err
	}
	result, err := t.client.AllowAll(t.token(), t.tableID, fileID, comment)
	if err != nil {
		return mapAllowAllError(err)
	}
	out := cmdutil.NewResultWriter(cmd)
	fmt.Fprintf(out.Human, "file/%d: %d allowed, %d awaiting approval, %d skipped\n",
		fileID, len(result.Allowed), len(result.Approved), len(result.Skipped))
	names := make([]string, 0, len(result.Allowed)+len(result.Approved))
	for _, id := range result.Allowed {
		fmt.Fprintf(out.Human, "  %s allowed\n", groupRef(id))
		names = append(names, groupRef(id))
	}
	for _, p := range result.Approved {
		fmt.Fprintf(out.Human, "  %s approved [d: %s] (approvals: %d/%d)\n",
			groupRef(p.GroupID), p.DecisionID, p.Approvals, p.RequiredApprovals)
		names = append(names, groupRef(p.GroupID))
	}
	for _, s := range result.Skipped {
		fmt.Fprintf(out.Human, "  %s skipped: %s\n", groupRef(s.GroupID), terminal.Sanitize(string(s.Reason)))
	}
	writeApply(out.Human, &result.Apply)
	warnUnapplied(cmd, &result.Apply)
	return out.Write(result, names...)
}

// mapAllowAllError explains allow-all's refusals.
func mapAllowAllError(err error) error {
	if apiErr, ok := asAPIError(err); ok && apiErr.Code == codeDecisionPending {
		return fmt.Errorf("%w\n(a group gained another reviewer's pending decision meanwhile; run allow-all again: "+
			"it skips that group as pending_differs)", err)
	}
	return mapTableError(err)
}

func newReapplyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "reapply",
		Short: "Retry unapplied duplicate group decisions",
		Long: `Re-apply duplicate group decisions to rows that disagree with them, and
retry periods whose latest apply failed or was skipped because the period
was locked. Unlock locked periods before retrying. It records no decision.

Prompts unless --yes. -o json|yaml prints the server's {decision_ids,
periods}.`,
		Example:     `  qctl duplicate-group reapply --table orders --yes`,
		Annotations: cmdutil.WithFormats(catalog.For(resource), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runReapply,
	}
	addCommonFlags(cmd, "")
	return cmd
}

func runReapply(cmd *cobra.Command, _ []string) error {
	if _, err := preflight(cmd, nil); err != nil {
		return err
	}
	t, err := resolveTarget(cmd)
	if err != nil {
		return err
	}
	if err := confirm(cmd, "re-applying duplicate group decisions",
		fmt.Sprintf("Re-apply the duplicate group decisions on table/%d?", t.tableID)); err != nil {
		return err
	}
	result, err := t.client.Reapply(t.token(), t.tableID)
	if err != nil {
		return mapTableError(err)
	}
	out := cmdutil.NewResultWriter(cmd)
	if len(result.Periods) == 0 {
		fmt.Fprintf(out.Human, "table/%d: every duplicate group's rows already agree with its decision\n", t.tableID)
	} else {
		fmt.Fprintf(out.Human, "table/%d: re-applied\n", t.tableID)
		writeApply(out.Human, result)
	}
	warnUnapplied(cmd, result)
	return out.Write(result)
}

func asAPIError(err error) (*duplicate_groups.APIError, bool) {
	var apiErr *duplicate_groups.APIError
	ok := errors.As(err, &apiErr)
	return apiErr, ok
}

// mapTableError adds the permission note to a table route's code-less 403.
func mapTableError(err error) error {
	if apiErr, ok := asAPIError(err); ok && apiErr.StatusCode == http.StatusForbidden && apiErr.Code == "" {
		return fmt.Errorf("%w\n%s", err, approvePermissionNote)
	}
	return err
}
