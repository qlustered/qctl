package get

import (
	"fmt"

	"github.com/qlustered/qctl/internal/auth"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/terminal"
)

// noVisibleRowsHint reads the current role to explain NO_TABLE_MESSAGE.
func noVisibleRowsHint(ctx *cmdutil.CommandContext) func() string {
	return func() string {
		me, err := auth.NewClient(ctx.ServerURL, ctx.Verbosity).GetMe(ctx.Credential.AccessToken, ctx.OrganizationID)
		if err == nil && me.Role != "" && me.Role != "ops" && me.Role != "admin" {
			return fmt.Sprintf("No table in org %q is shared with your account (role: %s).\n"+
				"Ask a table owner or admin to add you, then re-run the same command.\n",
				orgLabel(ctx), terminal.Sanitize(me.Role))
		}
		return fmt.Sprintf("No tables are visible to your account in org %q.\n"+
			"An empty list does not establish whether this organization has tables.\n"+
			"If you expect access, ask a table owner or admin, then re-run the same command.\n", orgLabel(ctx))
	}
}
