package reveal

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/alerts"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pii_reveals"
	"github.com/qlustered/qctl/internal/warnings"
	"github.com/spf13/cobra"
)

// issueKind is what tells reveal alert and reveal warning apart.
type issueKind struct {
	name   string // the subcommand, the kind/ prefix and the body's kind
	target func(id int) any
	// tableOf reads the issue for the id of the table it is on.
	tableOf func(ctx *cmdutil.CommandContext, id int) (int, error)
}

var issueAlert = issueKind{
	name:   pii_reveals.KindAlert,
	target: pii_reveals.AlertTarget,
	tableOf: func(ctx *cmdutil.CommandContext, id int) (int, error) {
		alert, err := alerts.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
			GetAlert(ctx.Credential.AccessToken, id)
		if err != nil {
			return 0, fmt.Errorf("failed to find the table of alert/%d: %w", id, err)
		}
		return alert.DatasetID, nil
	},
}

var issueWarning = issueKind{
	name:   pii_reveals.KindWarning,
	target: pii_reveals.WarningTarget,
	tableOf: func(ctx *cmdutil.CommandContext, id int) (int, error) {
		warning, err := warnings.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).
			GetWarning(ctx.Credential.AccessToken, id)
		if err != nil {
			return 0, fmt.Errorf("failed to find the table of warning/%d: %w", id, err)
		}
		return warning.DatasetID, nil
	},
}

func newIssueCommand(kind issueKind) *cobra.Command {
	n := kind.name
	cmd := &cobra.Command{
		Use:   n + " <" + n + "-id>",
		Short: "Show one " + n + "'s message with its personal data unmasked",
		Long: strings.ReplaceAll(`Show one KIND's message with the personal-data values in it unmasked.

The KIND is named by its id, or as KIND/<id>. Its table is read from the KIND
itself (one extra request); --table <name> or --table-id <id> names it
instead, and one that is not on that table is refused (404, exit 3).

The message is printed alone on stdout, rendered as qctl describe KIND
renders it; the line saying the reveal was recorded goes to stderr. -o json
and -o yaml print the server's answer, {value, event_id}, with the message
markup intact.

Examples:
  qctl reveal KIND 123
  qctl reveal KIND KIND/123 --table orders -o json`, "KIND", n),
		Annotations: annotations(n),
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIssue(cmd, kind, args[0])
		},
	}
	cmdutil.AddTableFlags(cmd, "Table the "+n+" is on (default: read from the "+n+")")
	return cmd
}

func runIssue(cmd *cobra.Command, kind issueKind, arg string) error {
	id, err := cmdutil.ParseIntArgs([]string{arg}, kind.name+" ID")
	if err != nil {
		return err
	}
	if id[0] <= 0 {
		return qerrors.NewBadUsagef("invalid %s ID: %s", kind.name, arg)
	}
	if err := cmdutil.ValidateOptionalTableSelector(cmd, ""); err != nil {
		return err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return err
	}
	tableID, given, err := cmdutil.OptionalTableID(ctx, cmd, "")
	if err != nil {
		return err
	}
	if !given {
		if tableID, err = kind.tableOf(ctx, id[0]); err != nil {
			return err
		}
	}
	what := fmt.Sprintf("%s/%d's message", kind.name, id[0])
	return send(cmd, ctx, tableID, kind.target(id[0]), what, markdown.PlainText)
}
