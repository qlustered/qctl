package privilege

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/privilege_requests"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// appliedImmediately is the second result line of a request the server
// applied inline (the sole-eligible-ops exemption in create_request_async).
const appliedImmediately = "Applied immediately: the server found you to be the sole eligible ops user, " +
	"so the two-person rule did not apply."

type requestOptions struct {
	targetUser        string
	action            string
	justification     string
	justificationFile string
}

func newRequestCommand() *cobra.Command {
	opts := requestOptions{}

	cmd := &cobra.Command{
		Use:   "request",
		Short: "Request an ops privilege change (escalate or demote) for another user",
		Long: `Open a request to grant (escalate) or revoke (demote) another user's ops
eligibility, is_ops, and hand its id to an approver.

--action takes escalate or demote, in any case. --target-user takes an email, a
"First Last" name, or a UUID. A name resolves against the CURRENT org's
members, while an escalation target must be an active admin of the HQ org, so
from a customer org pass the UUID. You cannot target yourself.

The justification comes from --justification or from a file named by
--justification-file ("-" reads stdin); give exactly one. The server wants 10
to 4000 characters and answers anything else with a 422 (exit 2) naming the
bound.

A request normally comes back Pending, for another ops user to approve. When
you are the sole eligible ops user the server applies it at once instead, and
the result line reads "created and applied". A target that already has a
Pending request is refused (exit 2) with a message naming that request's id.

-o json and -o yaml emit the created request as the server sent it; -o name
prints privilege-request/<id>, to hand to the approver.

Examples:
  qctl privilege request --target-user tom@example.com --action escalate --justification "INC-4411: on-call needs ops access to replay the stuck queue"
  qctl privilege request --target-user 0190bbbb-1111-2222-3333-444444444444 --action demote --justification-file why.md
  cat why.md | qctl privilege request --target-user tom@example.com --action demote --justification-file -
  id=$(qctl privilege request --target-user tom@example.com --action escalate --justification-file why.md -o name)`,
		Annotations: cmdutil.WithFormats(catalog.For("privilege-request"), "table", "json", "yaml", "name"),
		Args:        cobra.NoArgs,
		RunE:        func(cmd *cobra.Command, _ []string) error { return runRequest(cmd, opts) },
	}

	cmd.Flags().StringVar(&opts.targetUser, "target-user", "",
		`The user whose ops privilege changes (email, "First Last", or UUID)`)
	cmd.Flags().StringVar(&opts.action, "action", "",
		"What to do to the target's is_ops: escalate (grant) or demote (revoke)")
	cmd.Flags().StringVar(&opts.justification, "justification", "",
		"Why the change is needed (10-4000 characters)")
	cmd.Flags().StringVar(&opts.justificationFile, "justification-file", "",
		"Read the justification from a file (\"-\" = stdin)")
	_ = cmd.MarkFlagRequired("target-user")
	_ = cmd.MarkFlagRequired("action")
	return cmd
}

func runRequest(cmd *cobra.Command, opts requestOptions) error {
	// Everything decidable locally runs before Bootstrap, so a bad value costs
	// no credential read and no request. The justification's length is NOT among
	// them: the server's 422 names its bounds, and a local copy would drift.
	action, err := privilege_requests.ParseAction(opts.action)
	if err != nil {
		return err
	}
	if err := privilege_requests.CheckTargetUser(opts.targetUser); err != nil {
		return err
	}
	justification, err := readJustification(cmd, opts)
	if err != nil {
		return err
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	client := privilege_requests.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	target, _, err := client.ResolveTargetUser(ctx.Credential.AccessToken, opts.targetUser)
	if err != nil {
		return err
	}
	// A 400 passes through as-is. The duplicate-Pending refusal carries the
	// existing request's id in its text only; it is shown, never parsed.
	created, err := client.Create(ctx.Credential.AccessToken, target, action, justification)
	if err != nil {
		return err
	}
	return printCreated(cmd, created, opts.targetUser)
}

// readJustification resolves the --justification/--justification-file pair:
// exactly one, and "-" reads stdin.
func readJustification(cmd *cobra.Command, opts requestOptions) (string, error) {
	pair := []string{"justification", "justification-file"}
	if err := cmdutil.ValidateConflictingFlags(cmd, pair); err != nil {
		return "", err
	}
	if err := cmdutil.RequireOneOfFlags(cmd, pair); err != nil {
		return "", err
	}
	if !cmd.Flags().Changed("justification-file") {
		return opts.justification, nil
	}
	return cmdutil.ReadTextFile(cmd, opts.justificationFile, "justification")
}

// printCreated reports the new request. The status is the server's, never
// assumed: a 201 can already be Approved.
func printCreated(cmd *cobra.Command, created *privilege_requests.Request, targetAsTyped string) error {
	applied := created.Status == api.PrivilegeRequestStatusApproved
	verb := "created"
	if applied {
		verb = "created and applied"
	}
	// created.ID is a UUID and cannot carry a control byte; the status, the
	// values behind the action, and the target as typed can.
	lines := []string{fmt.Sprintf("privilege-request/%s %s (status: %s, action: %s, target: %s)",
		created.ID, verb,
		terminal.Sanitize(string(created.Status)),
		terminal.Sanitize(privilege_requests.ActionOf(created.OldValue, created.NewValue)),
		terminal.Sanitize(targetAsTyped))}
	if applied {
		lines = append(lines, appliedImmediately)
	}
	return printResult(cmd, created.ID, created, lines...)
}
