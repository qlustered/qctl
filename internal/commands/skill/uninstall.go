package skill

import (
	"fmt"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

const nonInteractiveUninstall = "confirmation required but stdin is not an interactive terminal (pass --yes)"

func newUninstallCmd() *cobra.Command {
	var (
		yes   bool
		force bool
	)

	cmd := &cobra.Command{
		Use:   "uninstall TARGET",
		Short: "Remove the installed qctl operating skill",
		Long: `Remove the qctl operating skill from an agent host's skill directory.

TARGET names the host, and neither of the two is the default: codex removes
~/.agents/skills/qctl; --legacy selects $CODEX_HOME/skills/qctl, or
~/.codex/skills/qctl when CODEX_HOME is unset;
claude-code removes $CLAUDE_CONFIG_DIR/skills/qctl, or ~/.claude/skills/qctl
when CLAUDE_CONFIG_DIR is unset. Only that one directory is removed, never a
sibling under the host's skills directory and never the skills directory
itself, and one host at a time: the other host's install is left in place.
The removal is local: nothing is unpublished and no other machine is affected.

An absent installation is a no-op at exit 0. A directory qctl did not install,
or one whose files have changed since it did, is refused untouched -- pass
--force to remove it anyway. --yes answers the prompt; it does not imply
--force.

This command is offline: no server, no credential, no organization.

Examples:
    # remove the skill Codex reads
    qctl skill uninstall codex

    # remove the one Claude Code reads, unattended
    qctl skill uninstall claude-code --yes
`,
		Args:        targetArg,
		ValidArgs:   agentskill.TargetNames(),
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(cmd, args, yes, force)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "Remove a modified or unrecognized directory")
	cmd.Flags().Bool("legacy", false, "Use the deprecated Codex skill location")

	return cmd
}

func runUninstall(cmd *cobra.Command, args []string, yes, force bool) error {
	inst := installerFor(cmd, args)
	plan, err := inst.PlanUninstall(force)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	printDestination(out, args[0], plan.Report)

	if !plan.Writes() {
		fmt.Fprintf(out, "Nothing to remove.\n")
		return nil
	}

	fmt.Fprintf(out, "Action:      %s\n\n", plan.Action)
	prompt := fmt.Sprintf("Remove %s?", plan.Dir)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractiveUninstall, prompt); err != nil {
		return err
	}
	if err := inst.Uninstall(plan); err != nil {
		return err
	}

	fmt.Fprintf(out, "Removed %s from this machine.\n", plan.Dir)
	return nil
}
