package skill

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status TARGET",
		Short: "Report the installed qctl operating skill",
		Long: `Report what is installed in an agent host's qctl skill directory.

TARGET names the host, and neither of the two is the default: codex reports on
~/.agents/skills/qctl; --legacy selects $CODEX_HOME/skills/qctl, or
~/.codex/skills/qctl when CODEX_HOME is unset;
claude-code reports on $CLAUDE_CONFIG_DIR/skills/qctl, or ~/.claude/skills/qctl
when CLAUDE_CONFIG_DIR is unset. One host is reported at a time, and what is
installed for the other does not change the answer.

Without --legacy, Codex also reports an existing legacy copy separately.

The resolved path is printed whether or not anything is installed there, with
one of five states:

    not installed              nothing is at the path
    installed, current         the files match this qctl
    installed, update available a qctl install of different content, unedited
    installed, modified        a qctl install whose files have changed since
    installed, unrecognized    a directory qctl did not install

All five exit 0: the state is the answer, not the exit code. Only an actual
read failure is non-zero. Read-only and offline.

Examples:
    # what is installed for Codex
    qctl skill status codex

    # and for Claude Code
    qctl skill status claude-code

    # the state alone, for a script
    qctl skill status claude-code -o json
`,
		Args:        targetArg,
		ValidArgs:   agentskill.TargetNames(),
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table", "json", "yaml"),
		RunE:        runStatus,
	}
	cmd.Flags().Bool("legacy", false, "Use the deprecated Codex skill location")

	return cmd
}

// statusView is the structured form of a report. It is written by hand rather
// than tagged onto agentskill.Report so the wire names stay a decision of this
// command rather than a side effect of a field rename.
type statusView struct {
	Target               string      `json:"target" yaml:"target"`
	Path                 string      `json:"path" yaml:"path"`
	State                string      `json:"state" yaml:"state"`
	Description          string      `json:"description" yaml:"description"`
	InstalledVersion     string      `json:"installedVersion,omitempty" yaml:"installedVersion,omitempty"`
	InstalledQctlVersion string      `json:"installedQctlVersion,omitempty" yaml:"installedQctlVersion,omitempty"`
	BundledVersion       string      `json:"bundledVersion" yaml:"bundledVersion"`
	Findings             []string    `json:"findings,omitempty" yaml:"findings,omitempty"`
	LegacyInstall        *statusView `json:"legacyInstall,omitempty" yaml:"legacyInstall,omitempty"`
	Legacy               bool        `json:"legacy,omitempty" yaml:"legacy,omitempty"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	inst := installerFor(cmd, args)
	rep, err := inst.Status()
	if err != nil {
		return err
	}

	view := statusView{
		Target:               args[0],
		Path:                 rep.Dir,
		State:                string(rep.State),
		Description:          rep.State.Describe(),
		InstalledVersion:     rep.InstalledVersion,
		InstalledQctlVersion: rep.InstalledQctlVersion,
		BundledVersion:       rep.BundledVersion,
		Findings:             rep.Findings,
		Legacy:               inst.LegacyCodex,
	}
	if inst.Target == agentskill.TargetCodex && !inst.LegacyCodex {
		old := *inst
		old.LegacyCodex = true
		legacy, err := old.Status()
		if err != nil {
			return err
		}
		if legacy.Dir != rep.Dir && legacy.State != agentskill.StateAbsent {
			view.LegacyInstall = &statusView{
				Target: args[0], Path: legacy.Dir, State: string(legacy.State),
				Description: legacy.State.Describe(), InstalledVersion: legacy.InstalledVersion,
				InstalledQctlVersion: legacy.InstalledQctlVersion, BundledVersion: legacy.BundledVersion,
				Findings: legacy.Findings, Legacy: true,
			}
		}
	}

	format, _ := cmd.Flags().GetString("output")
	out := cmd.OutOrStdout()
	switch format {
	case "json":
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	case "yaml":
		enc := jsonnum.NewYAMLEncoder(out)
		enc.SetIndent(2)
		defer func() { _ = enc.Close() }()
		return enc.Encode(view)
	default:
		printStatus(out, view)
		return nil
	}
}

func printStatus(w io.Writer, v statusView) {
	target := v.Target
	if v.Legacy {
		target += " --legacy"
	}
	fmt.Fprintf(w, "Target:            %s\n", v.Target)
	fmt.Fprintf(w, "Path:              %s\n", v.Path)
	fmt.Fprintf(w, "Status:            %s\n", v.Description)
	if v.InstalledVersion != "" {
		fmt.Fprintf(w, "Installed version: %s\n", v.InstalledVersion)
	}
	if v.InstalledQctlVersion != "" {
		fmt.Fprintf(w, "Installed by:      qctl %s\n", v.InstalledQctlVersion)
	}
	fmt.Fprintf(w, "Bundled version:   %s\n", v.BundledVersion)
	if len(v.Findings) > 0 {
		fmt.Fprintln(w)
		for _, f := range v.Findings {
			fmt.Fprintf(w, "  %s\n", f)
		}
	}
	switch agentskill.State(v.State) {
	case agentskill.StateAbsent:
		fmt.Fprintf(w, "\nInstall it with: qctl skill install %s%s\n", target, prompt.YesFlag(false))
	case agentskill.StateUpdateAvailable:
		fmt.Fprintf(w, "\nUpdate it with: qctl skill install %s%s\n", target, prompt.YesFlag(false))
	case agentskill.StateModified, agentskill.StateUnrecognized:
		fmt.Fprintf(w, "\nReplace it with: qctl skill install %s --force%s\n", target, prompt.YesFlag(false))
	}
	if v.LegacyInstall != nil {
		fmt.Fprintln(w, "\nLegacy Codex installation:")
		printStatus(w, *v.LegacyInstall)
		fmt.Fprintf(w, "\nMigrate with: qctl skill install codex%s; then qctl skill uninstall codex --legacy%s\n", prompt.YesFlag(false), prompt.YesFlag(false))
	}
}

func printLegacyReminder(w io.Writer, inst *agentskill.Installer) {
	if inst.Target != agentskill.TargetCodex || inst.LegacyCodex {
		return
	}
	old := *inst
	old.LegacyCodex = true
	rep, err := old.Status()
	if err != nil {
		fmt.Fprintf(w, "Warning: could not check the legacy Codex skill: %s\n", err)
		return
	}
	current, err := inst.Dir()
	if err == nil && rep.Dir != current && rep.State != agentskill.StateAbsent {
		fmt.Fprintf(w, "Legacy Codex skill remains at %q (%s); inspect with qctl skill status codex --legacy and remove with qctl skill uninstall codex --legacy%s.\n", rep.Dir, rep.State.Describe(), prompt.YesFlag(false))
	}
}
