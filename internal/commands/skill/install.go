package skill

import (
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

const nonInteractiveInstall = "confirmation required but stdin is not an interactive terminal (pass --yes)"

func newInstallCmd() *cobra.Command {
	var (
		yes   bool
		force bool
	)

	cmd := &cobra.Command{
		Use:   "install TARGET",
		Short: "Install the qctl operating skill for an agent host",
		Long: `Install the qctl operating skill into an agent host's skill directory.

TARGET names the host, and neither of the two is the default. codex installs
into ~/.agents/skills/qctl; --legacy selects $CODEX_HOME/skills/qctl, or
~/.codex/skills/qctl when CODEX_HOME is unset. claude-code installs into
$CLAUDE_CONFIG_DIR/skills/qctl, or
~/.claude/skills/qctl when CLAUDE_CONFIG_DIR is unset. The resolved path is
always printed, and nothing outside that one directory is written or removed.

Both hosts get the same operating policy. Each install is versioned by its
own files, so installing for one host leaves the other's exactly as it was.

Re-running the command is safe. An installation that already matches this
binary is left alone, and one qctl installed and nobody has edited since is
updated in place. A directory qctl did not install, or one whose files have
changed since it did, is refused without a byte touched -- pass --force to
replace it. --yes answers the prompt; it does not imply --force.

To migrate an old Codex install, install codex without --legacy, then run
qctl skill uninstall codex --legacy. Installing in the new location leaves
the legacy copy in place until explicitly removed.

This command is offline: no server, no credential, no organization.

Examples:
    # install for Codex, answering the prompt
    qctl skill install codex

    # install for Claude Code, answering the prompt
    qctl skill install claude-code

    # install unattended
    qctl skill install claude-code --yes

    # replace a directory qctl does not own
    qctl skill install codex --yes --force
`,
		Args:        targetArg,
		ValidArgs:   agentskill.TargetNames(),
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, yes, force)
		},
	}

	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip the confirmation prompt")
	cmd.Flags().BoolVar(&force, "force", false, "Replace a modified or unrecognized directory")
	cmd.Flags().Bool("legacy", false, "Use the deprecated Codex skill location")

	return cmd
}

func runInstall(cmd *cobra.Command, args []string, yes, force bool) error {
	inst := installerFor(cmd, args)
	plan, err := inst.PlanInstall(force)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()
	printDestination(out, args[0], plan.Report)

	if !plan.Writes() {
		fmt.Fprintf(out, "Already current; nothing to do.\n")
		printLegacyReminder(cmd.ErrOrStderr(), inst)
		return nil
	}

	printIntent(out, inst.Target, plan)
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), yes, nonInteractiveInstall, installPrompt(plan)); err != nil {
		return err
	}
	if err := inst.Install(plan); err != nil {
		return err
	}

	fmt.Fprintf(out, "%s the qctl skill (version %s) in %s\n",
		pastTense(plan.Action), plan.BundledVersion, plan.Dir)
	fmt.Fprintf(out, "Start a new %s session to pick it up.\n", agentskill.HostName(inst.Target))
	printLegacyReminder(cmd.ErrOrStderr(), inst)
	return nil
}

// printDestination prints the three lines every verb in this group leads with,
// so the resolved path is on screen before anything else is said about it.
func printDestination(w io.Writer, target string, rep agentskill.Report) {
	fmt.Fprintf(w, "Target:      %s\n", target)
	fmt.Fprintf(w, "Destination: %s\n", rep.Dir)
	fmt.Fprintf(w, "Status:      %s\n", rep.State.Describe())
}

// printIntent names the operation and the files it writes. The file list is
// the target's own: the two hosts do not carry the same tree.
func printIntent(w io.Writer, target agentskill.Target, plan agentskill.Plan) {
	fmt.Fprintf(w, "Action:      %s\n\n", plan.Action)
	files, err := agentskill.Bundled(target)
	if err != nil {
		return
	}
	fmt.Fprintln(w, "This will write:")
	for _, f := range files {
		fmt.Fprintf(w, "  %s\n", f.Path)
	}
	fmt.Fprintf(w, "  %s\n\n", agentskill.SidecarName)
}

func installPrompt(plan agentskill.Plan) string {
	if plan.Action == agentskill.ActionReplace {
		return fmt.Sprintf("Replace %s?", plan.Dir)
	}
	return fmt.Sprintf("Write the qctl skill to %s?", plan.Dir)
}

func pastTense(a agentskill.Action) string {
	switch a {
	case agentskill.ActionInstall:
		return "Installed"
	case agentskill.ActionUpdate:
		return "Updated"
	case agentskill.ActionReplace:
		return "Replaced"
	default:
		return string(a)
	}
}
