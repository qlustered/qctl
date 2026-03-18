package completion

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// shellType represents supported shell types
type shellType string

const (
	shellBash       shellType = "bash"
	shellZsh        shellType = "zsh"
	shellFish       shellType = "fish"
	shellPowershell shellType = "powershell"
	shellUnknown    shellType = "unknown"

	osWindows = "windows"
	osLinux   = "linux"
	osDarwin  = "darwin"
)

// installConfig holds the configuration for shell completion installation
type installConfig struct {
	shell           shellType
	completionDir   string
	completionFile  string
	rcFile          string
	rcSnippet       string
	needsFpathSetup bool
}

func newInstallCmd(rootCmd *cobra.Command) *cobra.Command {
	var (
		shellFlag string
		yes       bool
	)

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Automatically install shell completion",
		Long: fmt.Sprintf(`Automatically install shell completion for %[1]s.

This command detects your current shell and installs the completion script
to the appropriate location. It may also update your shell configuration
file (e.g., ~/.bashrc, ~/.zshrc) if needed.

Supported shells: bash, zsh, fish, powershell

Examples:
    # Auto-detect shell and install
    %[1]s completion install

    # Install for a specific shell
    %[1]s completion install --shell zsh

    # Skip confirmation prompt
    %[1]s completion install --yes

If installation fails, see '%[1]s completion <shell> --help' for manual
installation instructions.
`, cmdName),
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(rootCmd, cmd, shellFlag, yes)
		},
	}

	cmd.Flags().StringVar(&shellFlag, "shell", "", "Shell to install completion for (bash, zsh, fish, powershell)")
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "Skip confirmation prompt")

	return cmd
}

func runInstall(rootCmd *cobra.Command, cmd *cobra.Command, shellFlag string, skipConfirm bool) error {
	shell, err := resolveShell(cmd, shellFlag)
	if err != nil {
		return err
	}

	config, err := getInstallConfig(shell)
	if err != nil {
		printManualInstructions(cmd.ErrOrStderr(), shell)
		return err
	}

	printInstallPlan(cmd, config)

	if !skipConfirm {
		confirmed, err := confirmInstall(cmd)
		if err != nil {
			return err
		}
		if !confirmed {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")
			return nil
		}
	}

	if err := performInstall(rootCmd, cmd, shell, config); err != nil {
		return err
	}

	printPostInstallInstructions(cmd, shell)
	return nil
}

func resolveShell(cmd *cobra.Command, shellFlag string) (shellType, error) {
	if shellFlag != "" {
		shell := shellType(shellFlag)
		if !isValidShell(shell) {
			return shellUnknown, fmt.Errorf("unsupported shell: %s (supported: bash, zsh, fish, powershell)", shellFlag)
		}
		return shell, nil
	}

	shell := detectShell()
	if shell == shellUnknown {
		fmt.Fprintln(cmd.ErrOrStderr(), "Could not detect your shell.")
		fmt.Fprintln(cmd.ErrOrStderr(), "Please specify your shell with --shell flag:")
		fmt.Fprintln(cmd.ErrOrStderr(), "    qctl completion install --shell zsh")
		fmt.Fprintln(cmd.ErrOrStderr(), "")
		fmt.Fprintln(cmd.ErrOrStderr(), "Or see manual instructions:")
		fmt.Fprintln(cmd.ErrOrStderr(), "    qctl completion <shell> --help")
		return shellUnknown, fmt.Errorf("could not detect shell")
	}
	return shell, nil
}

func printInstallPlan(cmd *cobra.Command, config *installConfig) {
	fmt.Fprintf(cmd.OutOrStdout(), "Detected shell: %s\n\n", config.shell)
	fmt.Fprintln(cmd.OutOrStdout(), "This will:")
	fmt.Fprintf(cmd.OutOrStdout(), "  • Create directory: %s\n", config.completionDir)
	fmt.Fprintf(cmd.OutOrStdout(), "  • Write completion script: %s\n", config.completionFile)
	if config.rcSnippet != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  • Add to %s (if not present):\n", config.rcFile)
		for _, line := range strings.Split(config.rcSnippet, "\n") {
			if line != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "      %s\n", line)
			}
		}
	}
	fmt.Fprintln(cmd.OutOrStdout())
}

func confirmInstall(cmd *cobra.Command) (bool, error) {
	fmt.Fprint(cmd.OutOrStdout(), "Proceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes", nil
}

func performInstall(rootCmd *cobra.Command, cmd *cobra.Command, shell shellType, config *installConfig) error {
	// Create completion directory
	if err := os.MkdirAll(config.completionDir, 0755); err != nil {
		printManualInstructions(cmd.ErrOrStderr(), shell)
		return fmt.Errorf("failed to create directory %s: %w", config.completionDir, err)
	}

	// Generate and write completion script
	if err := writeCompletionScript(rootCmd, cmd, shell, config); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "✓ Wrote completion script to %s\n", config.completionFile)

	// Update RC file if needed
	if config.rcSnippet != "" && config.rcFile != "" {
		updateRCFileWithFeedback(cmd, config)
	}

	return nil
}

func writeCompletionScript(rootCmd *cobra.Command, cmd *cobra.Command, shell shellType, config *installConfig) error {
	var buf bytes.Buffer
	var err error

	switch shell {
	case shellBash:
		err = rootCmd.GenBashCompletionV2(&buf, true)
	case shellZsh:
		err = rootCmd.GenZshCompletion(&buf)
	case shellFish:
		err = rootCmd.GenFishCompletion(&buf, true)
	case shellPowershell:
		err = rootCmd.GenPowerShellCompletionWithDesc(&buf)
	}
	if err != nil {
		printManualInstructions(cmd.ErrOrStderr(), shell)
		return fmt.Errorf("failed to generate completion script: %w", err)
	}

	if err := os.WriteFile(config.completionFile, buf.Bytes(), 0644); err != nil {
		printManualInstructions(cmd.ErrOrStderr(), shell)
		return fmt.Errorf("failed to write completion script: %w", err)
	}

	return nil
}

func updateRCFileWithFeedback(cmd *cobra.Command, config *installConfig) {
	updated, err := updateRCFile(config.rcFile, config.rcSnippet)
	switch {
	case err != nil:
		fmt.Fprintf(cmd.ErrOrStderr(), "Warning: Could not update %s: %v\n", config.rcFile, err)
		fmt.Fprintf(cmd.ErrOrStderr(), "Please manually add the following to %s:\n\n", config.rcFile)
		fmt.Fprintln(cmd.ErrOrStderr(), config.rcSnippet)
	case updated:
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Updated %s\n", config.rcFile)
	default:
		fmt.Fprintf(cmd.OutOrStdout(), "✓ %s already configured\n", config.rcFile)
	}
}

func printPostInstallInstructions(cmd *cobra.Command, shell shellType) {
	fmt.Fprintln(cmd.OutOrStdout(), "")
	if shell == shellZsh {
		fmt.Fprintln(cmd.OutOrStdout(), "To activate completions, run:")
		fmt.Fprintln(cmd.OutOrStdout(), "    compinit")
		fmt.Fprintln(cmd.OutOrStdout(), "")
		fmt.Fprintln(cmd.OutOrStdout(), "Or start a new shell.")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Start a new shell to activate completions.")
	}
}

func isValidShell(shell shellType) bool {
	switch shell {
	case shellBash, shellZsh, shellFish, shellPowershell:
		return true
	default:
		return false
	}
}

func detectShell() shellType {
	// Check $SHELL environment variable
	shellEnv := os.Getenv("SHELL")
	if shellEnv != "" {
		base := filepath.Base(shellEnv)
		switch base {
		case "bash":
			return shellBash
		case "zsh":
			return shellZsh
		case "fish":
			return shellFish
		}
	}

	// On Windows, check for PowerShell
	if runtime.GOOS == osWindows {
		// Check if we're running in PowerShell
		if os.Getenv("PSModulePath") != "" {
			return shellPowershell
		}
	}

	// Check parent process name as fallback
	if ppid := os.Getppid(); ppid > 0 {
		if procName := getProcessName(ppid); procName != "" {
			switch {
			case strings.Contains(procName, "bash"):
				return shellBash
			case strings.Contains(procName, "zsh"):
				return shellZsh
			case strings.Contains(procName, "fish"):
				return shellFish
			case strings.Contains(procName, "pwsh"), strings.Contains(procName, "powershell"):
				return shellPowershell
			}
		}
	}

	return shellUnknown
}

func getProcessName(pid int) string {
	// Try to read /proc/<pid>/comm on Linux
	if runtime.GOOS == osLinux {
		data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		if err == nil {
			return strings.TrimSpace(string(data))
		}
	}

	// Try ps command on Unix-like systems
	if runtime.GOOS != osWindows {
		out, err := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "comm=").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}

	return ""
}

func getInstallConfig(shell shellType) (*installConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}

	switch shell {
	case shellBash:
		return getBashConfig(home)
	case shellZsh:
		return getZshConfig(home)
	case shellFish:
		return getFishConfig(home)
	case shellPowershell:
		return getPowershellConfig(home)
	default:
		return nil, fmt.Errorf("unsupported shell: %s", shell)
	}
}

func getBashConfig(home string) (*installConfig, error) {
	// Use XDG_DATA_HOME if available, otherwise ~/.local/share
	dataDir := os.Getenv("XDG_DATA_HOME")
	if dataDir == "" {
		dataDir = filepath.Join(home, ".local", "share")
	}

	completionDir := filepath.Join(dataDir, "bash-completion", "completions")

	// Determine RC file
	rcFile := filepath.Join(home, ".bashrc")
	if runtime.GOOS == osDarwin {
		// macOS uses .bash_profile for login shells
		if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
			rcFile = filepath.Join(home, ".bash_profile")
		}
	}

	// Check if bash-completion is likely installed by checking common directories
	// If ~/.local/share/bash-completion/completions exists or can be created, use it
	// Otherwise fall back to sourcing in bashrc
	var rcSnippet string

	// Check if user already has bash-completion loading this directory
	// If not, we'll add a source line
	bcDir := filepath.Join(dataDir, "bash-completion", "completions")
	if !dirInBashCompletionPath(bcDir) {
		rcSnippet = fmt.Sprintf("# qctl shell completion\n[[ -f %s/%s ]] && source %s/%s", bcDir, cmdName, bcDir, cmdName)
	}

	return &installConfig{
		shell:          shellBash,
		completionDir:  completionDir,
		completionFile: filepath.Join(completionDir, cmdName),
		rcFile:         rcFile,
		rcSnippet:      rcSnippet,
	}, nil
}

func dirInBashCompletionPath(dir string) bool {
	// Check XDG_DATA_DIRS which bash-completion uses
	xdgDirs := os.Getenv("XDG_DATA_DIRS")
	if xdgDirs == "" {
		xdgDirs = "/usr/local/share:/usr/share"
	}
	for _, d := range strings.Split(xdgDirs, ":") {
		if filepath.Join(d, "bash-completion", "completions") == dir {
			return true
		}
	}

	// Also check XDG_DATA_HOME
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, _ := os.UserHomeDir()
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "bash-completion", "completions") == dir
}

func getZshConfig(home string) (*installConfig, error) {
	completionDir := filepath.Join(home, ".zsh", "completions")
	rcFile := filepath.Join(home, ".zshrc")

	// We need to add the completions directory to fpath
	rcSnippet := "# qctl shell completion\nfpath=(~/.zsh/completions $fpath)"

	return &installConfig{
		shell:           shellZsh,
		completionDir:   completionDir,
		completionFile:  filepath.Join(completionDir, "_"+cmdName),
		rcFile:          rcFile,
		rcSnippet:       rcSnippet,
		needsFpathSetup: true,
	}, nil
}

func getFishConfig(home string) (*installConfig, error) {
	// Fish has a standard completions directory
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		configDir = filepath.Join(home, ".config")
	}

	completionDir := filepath.Join(configDir, "fish", "completions")

	return &installConfig{
		shell:          shellFish,
		completionDir:  completionDir,
		completionFile: filepath.Join(completionDir, cmdName+".fish"),
		// Fish automatically loads from completions directory, no RC update needed
	}, nil
}

func getPowershellConfig(home string) (*installConfig, error) {
	var completionDir, rcFile string

	if runtime.GOOS == osWindows {
		// Windows PowerShell profile location
		documentsDir := filepath.Join(home, "Documents")
		completionDir = filepath.Join(documentsDir, "WindowsPowerShell", "Completions")
		rcFile = filepath.Join(documentsDir, "WindowsPowerShell", "Microsoft.PowerShell_profile.ps1")
	} else {
		// PowerShell Core on Linux/macOS
		configDir := os.Getenv("XDG_CONFIG_HOME")
		if configDir == "" {
			configDir = filepath.Join(home, ".config")
		}
		completionDir = filepath.Join(configDir, "powershell", "Completions")
		rcFile = filepath.Join(configDir, "powershell", "Microsoft.PowerShell_profile.ps1")
	}

	completionFile := filepath.Join(completionDir, cmdName+".ps1")
	rcSnippet := fmt.Sprintf("# qctl shell completion\n. %s", completionFile)

	return &installConfig{
		shell:          shellPowershell,
		completionDir:  completionDir,
		completionFile: completionFile,
		rcFile:         rcFile,
		rcSnippet:      rcSnippet,
	}, nil
}

func updateRCFile(rcFile, snippet string) (bool, error) {
	// Read existing content
	content, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}

	contentStr := string(content)

	// Check if qctl completion is already configured
	if strings.Contains(contentStr, "qctl") && strings.Contains(contentStr, "completion") {
		return false, nil // Already configured
	}

	// Check for the specific snippet content (excluding comments)
	snippetLines := strings.Split(snippet, "\n")
	for _, line := range snippetLines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			if strings.Contains(contentStr, line) {
				return false, nil // Already has this configuration
			}
		}
	}

	// For zsh, we need to insert fpath BEFORE oh-my-zsh or compinit is called
	if strings.HasSuffix(rcFile, ".zshrc") {
		return insertZshCompletion(rcFile, contentStr, snippet)
	}

	// For other shells, append to the end
	return appendToFile(rcFile, content, snippet)
}

// insertZshCompletion inserts the fpath line before oh-my-zsh or compinit in .zshrc
func insertZshCompletion(rcFile, content, snippet string) (bool, error) {
	lines := strings.Split(content, "\n")
	insertIdx := -1

	// Find the line where oh-my-zsh is sourced or compinit is called
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Match: source $ZSH/oh-my-zsh.sh or source ~/.oh-my-zsh/oh-my-zsh.sh or variations
		if strings.Contains(trimmed, "oh-my-zsh.sh") && (strings.HasPrefix(trimmed, "source") || strings.HasPrefix(trimmed, ".")) {
			insertIdx = i
			break
		}
		// Match: compinit (but not commented out)
		if !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "compinit") {
			insertIdx = i
			break
		}
	}

	if insertIdx >= 0 {
		// Insert before the found line
		newLines := make([]string, 0, len(lines)+3)
		newLines = append(newLines, lines[:insertIdx]...)
		newLines = append(newLines, "") // blank line before
		newLines = append(newLines, strings.Split(snippet, "\n")...)
		newLines = append(newLines, "") // blank line after
		newLines = append(newLines, lines[insertIdx:]...)

		newContent := strings.Join(newLines, "\n")
		if err := os.WriteFile(rcFile, []byte(newContent), 0644); err != nil {
			return false, err
		}
		return true, nil
	}

	// No oh-my-zsh or compinit found, append to end
	return appendToFile(rcFile, []byte(content), snippet)
}

// appendToFile appends the snippet to the end of the file
func appendToFile(rcFile string, content []byte, snippet string) (bool, error) {
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Add newlines before snippet if file is not empty and doesn't end with newline
	prefix := ""
	if len(content) > 0 && content[len(content)-1] != '\n' {
		prefix = "\n\n"
	} else if len(content) > 0 {
		prefix = "\n"
	}

	if _, err := f.WriteString(prefix + snippet + "\n"); err != nil {
		return false, err
	}

	return true, nil
}

func printManualInstructions(w io.Writer, shell shellType) {
	fmt.Fprintf(w, "\nFor manual installation instructions, run:\n")
	fmt.Fprintf(w, "    %s completion %s --help\n", cmdName, shell)
}
