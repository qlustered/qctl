//nolint:revive // Package name intentionally matches the rule-upgrade command directory.
package rule_upgrade

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/rule_upgrades"
	"github.com/qlustered/qctl/internal/rule_versions"
	"github.com/spf13/cobra"
)

// nonInteractiveNotify names --yes, because that is the fix.
const nonInteractiveNotify = "re-sending an upgrade notice needs confirmation but stdin is " +
	"not an interactive terminal; pass --yes"

func newNotifyCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "notify <rule-name-or-id>",
		Short: "Re-send a rule's upgrade notice to everyone who dismissed it",
		Long: `Re-send a rule's upgrade notice to the users who dismissed it.

This writes to other people's inboxes, so it asks for confirmation first.

Who it reaches: only users who DISMISSED the notice. Someone who has simply
not read it yet is untouched -- they already have it. The notice comes back
unread as well as undismissed, and its repeat count goes up.

Which revision: the family's DEFAULT revision, which is the only one the
backend will re-notify for. --release is for saying so explicitly; naming any
other release is rejected before anything is sent, and --release "" is
rejected too rather than falling back to the default. The positional argument
addresses the FAMILY, so naming one of its non-default revisions by id lands
on the default -- reported on stderr rather than substituted in silence.

Who may do it: only the user who created the revision.

--message replaces the text of every notice it re-sends. Omit it and each
notice keeps the text it already had.

A count of 0 is not a failure -- it means nobody had dismissed this notice.

Examples:
  qctl rule-upgrade notify phone_format
  qctl rule-upgrade notify phone_format --release 3.0.0
  qctl rule-upgrade notify phone_format --message "please upgrade before Friday"
  qctl rule-upgrade notify phone_format --yes

-o json|yaml prints the server's {message, reactivated_count} and moves the
prose to stderr.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("rule-upgrade"), "table", "json", "yaml"), "rule"),
		Args:        cobra.ExactArgs(1),
		RunE:        runNotify,
	}
	cmd.Flags().String("release", "", "Which release to re-notify for (default: the family's default release)")
	cmd.Flags().String("message", "", "Replace the text of every notice this re-sends")
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt (for scripted/non-interactive use)")
	return cmd
}

func runNotify(cmd *cobra.Command, args []string) error {
	// Changed(), not emptiness: --release "" is a script whose variable did not
	// expand, and treating it as "use the default" would blast whatever the
	// family's default happens to be for a release the script never named.
	// --message is read the same way, for the same reason.
	var release *string
	if cmd.Flags().Changed("release") {
		r, _ := cmd.Flags().GetString("release")
		release = &r
	}

	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	rvClient, err := rule_versions.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	family, err := rule_upgrades.ResolveFamily(rvClient, ctx.Credential.AccessToken, args[0])
	if err != nil {
		return err
	}

	target, err := selectRevision(family, release)
	if err != nil {
		return err
	}
	// The positional argument addresses a FAMILY, so naming one of its
	// non-default revisions resolves to the family and lands on the default.
	// Say so: `get rule-upgrades -o json` prints each item's
	// current_revision_id, and pasting one here under --yes would otherwise
	// blast a different revision in silence.
	//
	// Gated on IsUUIDPrefix (a full UUID or a short id) because only those
	// name ONE revision. A family NAME is the ordinary case and resolves to
	// whichever release matches first -- ResolveRuleIDAny "picks the first
	// match" -- which is routinely not the default, so an ungated note would
	// fire on nearly every invocation and mean nothing.
	if rule_versions.IsUUIDPrefix(args[0]) && family.RevisionID != target.ID.String() {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Note: %s names one revision, but the notice is per family: notifying for %s %s, the default.\n",
			args[0], family.Name, target.Release)
	}

	yes, _ := cmd.Flags().GetBool("yes")
	prompt := fmt.Sprintf(
		"Re-send the %s %s upgrade notice to everyone who dismissed it? It comes back unread in their inbox.",
		family.Name, target.Release)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractiveNotify, prompt); err != nil {
		return err
	}

	var msg *string
	if cmd.Flags().Changed("message") {
		m, _ := cmd.Flags().GetString("message")
		msg = &m
	}

	client := rule_upgrades.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	result, err := client.Blast(ctx.Credential.AccessToken, target.ID.String(), msg)
	if err != nil {
		return explainBlastError(err, family.Name, target.Release)
	}

	w := cmdutil.NewResultWriter(cmd)
	fmt.Fprintf(w.Human, "rule-upgrade/%s notified (%d dismissed notification(s) reactivated)\n",
		target.ID, result.ReactivatedCount)
	if result.ReactivatedCount == 0 {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"Nobody had dismissed this notice, so there was nothing to re-send.\n"+
				"Users who have not read it yet still have it in their inbox.\n")
	}
	return w.Write(result)
}

// selectRevision picks the revision to blast and refuses a non-default one
// CLIENT-SIDE.
//
// The backend refuses it too, but as a ValueError the view turns into a 404
// (blast_rule_upgrade_notifications_async,
// ../atlas/cettings/cettings/controllers/notifications_controller.py),
// so an unguarded call reports "not found" for a revision that plainly exists.
func selectRevision(family *rule_upgrades.ResolvedFamily, release *string) (*api.RuleRevisionTinySchema, error) {
	def := family.Default()
	if release == nil {
		if def == nil {
			return nil, qerrors.NewBadUsagef(
				"%s has no default revision, so there is no upgrade notice to re-send", family.Name)
		}
		return def, nil
	}
	named := family.ByRelease(*release)
	if named == nil {
		return nil, qerrors.NewBadUsagef("%s has no release %q", family.Name, *release)
	}
	if !named.IsDefault {
		defaultRelease := "none"
		if def != nil {
			defaultRelease = def.Release
		}
		return nil, qerrors.NewBadUsagef(
			"%s is not the default release of %s (%s is) — only the default revision's upgrade notice can be re-sent",
			*release, family.Name, defaultRelease)
	}
	return named, nil
}

// explainBlastError adds the reason behind a 403. The server says "Only the
// revision creator can blast notifications" without naming which revision or
// which release, which is exactly what the user needs to know.
//
// "The usual cause" is not hedging. The route carries THREE independent 403
// sources and the creator check is the LAST of them: it is gated on
// AppPermission.RULE_INSTANTIATE (blast_upgrade_notifications,
// ../atlas/capi/capi/views/notification_view.py) and then on
// check_organization_access_async, both of which run first. Stating the creator
// rule as the cause would contradict the server's own message and apierror's
// org-scope note, printed directly above this, and send the reader after the
// wrong person.
//
// The hint names `describe rule`, not `get rule-revisions`: the creator is not
// in the latter's default or verbose column sets, while describe prints a
// "Created by" line.
func explainBlastError(err error, familyName, release string) error {
	var apiErr *rule_upgrades.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden {
		return fmt.Errorf(
			"%w\n\nThe usual cause is that only the user who created revision %s of %s "+
				"can re-send its upgrade notice.\n"+
				"See who that is with: qctl describe rule %s --release %s",
			err, release, familyName, familyName, release)
	}
	// No "failed to ..." prefix: rule_upgrades.Blast already passes "request
	// failed" as its context message, and buildHTTPError renders that as a
	// prefix, so adding one here reads as "failed to X: request failed: ...".
	return err
}
