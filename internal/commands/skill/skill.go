// Package skill installs the agent operating skill qctl ships, into the
// skill directory of the agent host that is asked for.
//
// Nothing here bootstraps a client: the whole group is offline, needs no qctl
// context, no credential and no organization, and the target argument is
// validated before a single file is read.
package skill

import (
	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// NewCommand creates the skill command group.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill [command]",
		Short: "Install the qctl operating skill for an agent host",
		Long: `Install the agent operating skill that qctl ships.

The skill teaches an agent how to drive qctl: run the command the request
names before exploring, stay in the organization already selected, trust
qctl's own diagnostics, and reach for narrow help rather than dumping the
whole reference. qctl itself stays authoritative for its commands and
resources -- the skill does not copy them.

Every verb names the host to act on. Two are accepted, and neither is the
default:

    codex         ~/.agents/skills/qctl
    claude-code   $CLAUDE_CONFIG_DIR/skills/qctl, or ~/.claude/skills/qctl

Both hosts are served the same operating policy, and the two installs are
independent: installing, updating or removing one leaves the other exactly
as it was.

For older Codex hosts, --legacy selects $CODEX_HOME/skills/qctl, or
~/.codex/skills/qctl when CODEX_HOME is unset. To migrate, install codex
without --legacy, then uninstall codex --legacy. Old copies are left in
place until explicitly removed.

    qctl skill install TARGET     write it into that host's skill directory
    qctl skill status TARGET      report what is installed there
    qctl skill uninstall TARGET   remove it again

Installing it is optional, for either host. All qctl commands are available
either way.

Every one of them is offline: no server, no credential, no organization.`,
		Annotations: catalog.Parent(),
		RunE:        cmdutil.SubcommandRequired,
	}

	cmd.AddCommand(newInstallCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newUninstallCmd())

	return cmd
}

// targetArg is the Args validator every leaf shares: exactly one target, from
// the fixed list. Both rejections are bad usage -- the count through
// cmdutil.TypeArgsErrors, the name here -- and cobra runs Args before any
// PersistentPreRunE, so a wrong target exits 2 before the destination is
// resolved or a byte is read.
//
// cobra.OnlyValidArgs would do the same job off ValidArgs, but its message
// ("invalid argument ... for ...") never names what the accepted values are.
var targetArg = cobra.MatchAll(cobra.ExactArgs(1), validTarget)

func validTarget(cmd *cobra.Command, args []string) error {
	// ExactArgs has already reported any other count; re-reporting it here
	// would print two messages for one mistake.
	if len(args) != 1 {
		return nil
	}
	if _, ok := agentskill.ParseTarget(args[0]); !ok {
		return qerrors.NewBadUsagef("unknown skill target %q\n  accepted: %s", args[0], agentskill.JoinTargets())
	}
	if legacy, _ := cmd.Flags().GetBool("legacy"); legacy && args[0] != string(agentskill.TargetCodex) {
		return qerrors.NewBadUsage("--legacy is only supported for the codex skill target")
	}
	return nil
}

// installerFor builds an installer over the process environment for the
// target cobra validated in Args.
//
// The name is converted rather than re-parsed. agentskill keys every host fact
// off the target's own name, so a name no host rule answers to resolves no
// directory and is reported by agentskill itself, naming the spelling the user
// typed -- one phrasing for that rejection instead of a copy of it here. It is
// unreachable through cobra either way.
func installerFor(cmd *cobra.Command, args []string) *agentskill.Installer {
	i := agentskill.New(agentskill.Target(args[0]))
	i.LegacyCodex, _ = cmd.Flags().GetBool("legacy")
	return i
}
