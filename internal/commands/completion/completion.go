// Package completion provides shell completion generation and installation commands.
package completion

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

const cmdName = "qctl"

// NewCommand creates the completion command with subcommands for each shell.
func NewCommand(rootCmd *cobra.Command) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [command]",
		Short: "Generate or install shell completion scripts",
		Long: `Generate or install autocompletion scripts for qctl.

Shell completion enables tab-completion of commands, flags, and arguments.

To automatically install completion for your shell:

    qctl completion install

To manually generate a completion script, use one of the shell-specific
subcommands (bash, zsh, fish, powershell).`,
		Args: cobra.NoArgs,
	}

	cmd.AddCommand(newBashCmd(rootCmd))
	cmd.AddCommand(newZshCmd(rootCmd))
	cmd.AddCommand(newFishCmd(rootCmd))
	cmd.AddCommand(newPowershellCmd(rootCmd))
	cmd.AddCommand(newInstallCmd(rootCmd))

	return cmd
}

func newBashCmd(rootCmd *cobra.Command) *cobra.Command {
	var noDescriptions bool

	cmd := &cobra.Command{
		Use:   "bash",
		Short: "Generate completion script for bash",
		Long: fmt.Sprintf(`Generate the autocompletion script for bash.

This script depends on the 'bash-completion' package.
If it is not installed already, you can install it via your OS's package manager.

To load completions in your current shell session:

    source <(%[1]s completion bash)

To load completions for every new session, execute once:

### Linux:

    # Create completions directory if needed
    mkdir -p ~/.local/share/bash-completion/completions

    # Generate completion script
    %[1]s completion bash > ~/.local/share/bash-completion/completions/%[1]s

### macOS (with Homebrew):

    # Install bash-completion if needed
    brew install bash-completion@2

    # Generate completion script
    %[1]s completion bash > $(brew --prefix)/etc/bash_completion.d/%[1]s

### Alternative (any system):

    # Add to your ~/.bashrc:
    source <(%[1]s completion bash)

You will need to start a new shell for this setup to take effect.

TIP: Run '%[1]s completion install' to automatically install completions.
`, cmdName),
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rootCmd.GenBashCompletionV2(os.Stdout, !noDescriptions)
		},
	}

	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")

	return cmd
}

func newZshCmd(rootCmd *cobra.Command) *cobra.Command {
	var noDescriptions bool

	cmd := &cobra.Command{
		Use:   "zsh",
		Short: "Generate completion script for zsh",
		Long: fmt.Sprintf(`Generate the autocompletion script for zsh.

If shell completion is not already enabled in your environment you will need
to enable it. You can execute the following once:

    echo "autoload -U compinit; compinit" >> ~/.zshrc

To load completions in your current shell session:

    source <(%[1]s completion zsh)

To load completions for every new session, execute once:

### Recommended (works on Linux and macOS):

    # Create a completions directory
    mkdir -p ~/.zsh/completions

    # Add to your ~/.zshrc (before compinit):
    fpath=(~/.zsh/completions $fpath)

    # Generate completion script
    %[1]s completion zsh > ~/.zsh/completions/_%[1]s

    # Load new completions
    compinit

### macOS (with Homebrew):

    %[1]s completion zsh > $(brew --prefix)/share/zsh/site-functions/_%[1]s

You will need to start a new shell for this setup to take effect.

TIP: Run '%[1]s completion install' to automatically install completions.
`, cmdName),
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noDescriptions {
				return rootCmd.GenZshCompletionNoDesc(os.Stdout)
			}
			return rootCmd.GenZshCompletion(os.Stdout)
		},
	}

	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")

	return cmd
}

func newFishCmd(rootCmd *cobra.Command) *cobra.Command {
	var noDescriptions bool

	cmd := &cobra.Command{
		Use:   "fish",
		Short: "Generate completion script for fish",
		Long: fmt.Sprintf(`Generate the autocompletion script for fish.

To load completions in your current shell session:

    %[1]s completion fish | source

To load completions for every new session, execute once:

    mkdir -p ~/.config/fish/completions
    %[1]s completion fish > ~/.config/fish/completions/%[1]s.fish

You will need to start a new shell for this setup to take effect.

TIP: Run '%[1]s completion install' to automatically install completions.
`, cmdName),
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return rootCmd.GenFishCompletion(os.Stdout, !noDescriptions)
		},
	}

	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")

	return cmd
}

func newPowershellCmd(rootCmd *cobra.Command) *cobra.Command {
	var noDescriptions bool

	cmd := &cobra.Command{
		Use:   "powershell",
		Short: "Generate completion script for PowerShell",
		Long: fmt.Sprintf(`Generate the autocompletion script for PowerShell.

To load completions in your current shell session:

    %[1]s completion powershell | Out-String | Invoke-Expression

To load completions for every new session, add the output of the above command
to your PowerShell profile.

### Windows:

    # Create profile directory if needed
    New-Item -Type Directory -Path (Split-Path $PROFILE) -Force

    # Add completion to profile
    %[1]s completion powershell >> $PROFILE

### Cross-platform (PowerShell Core):

    # Find your profile location
    echo $PROFILE

    # Add completion to profile
    %[1]s completion powershell >> $PROFILE

You will need to start a new shell for this setup to take effect.

TIP: Run '%[1]s completion install' to automatically install completions.
`, cmdName),
		Args:                  cobra.NoArgs,
		DisableFlagsInUseLine: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if noDescriptions {
				return rootCmd.GenPowerShellCompletion(os.Stdout)
			}
			return rootCmd.GenPowerShellCompletionWithDesc(os.Stdout)
		},
	}

	cmd.Flags().BoolVar(&noDescriptions, "no-descriptions", false, "Disable completion descriptions")

	return cmd
}
