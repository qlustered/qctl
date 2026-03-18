package docs

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	cmdHelp       = "help"
	cmdDocs       = "docs"
	cmdCompletion = "completion"
)

// NewCommand creates the docs command
func NewCommand(rootCmd *cobra.Command) *cobra.Command {
	var outputFile string

	cmd := &cobra.Command{
		Use:   "docs",
		Short: "Generate comprehensive documentation",
		Long: `Generate a comprehensive documentation page for all qctl commands.

This command produces a single document containing documentation for all
commands and subcommands, including their usage, descriptions, flags,
and examples.

The output is in Markdown format by default and can be redirected to a file.`,
		Example: `  # Print documentation to stdout
  qctl docs

  # Save documentation to a file
  qctl docs -o qctl-manual.md

  # Pipe to a pager
  qctl docs | less`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var w io.Writer = os.Stdout
			if outputFile != "" {
				f, err := os.Create(outputFile)
				if err != nil {
					return fmt.Errorf("failed to create output file: %w", err)
				}
				defer f.Close()
				w = f
			}
			generateDocs(w, rootCmd)
			return nil
		},
	}

	cmd.Flags().StringVarP(&outputFile, "output-file", "f", "", "write output to file instead of stdout")

	return cmd
}

func generateDocs(w io.Writer, rootCmd *cobra.Command) {
	// Title
	fmt.Fprintf(w, "# QCTL Manual\n\n")
	fmt.Fprintf(w, "Comprehensive documentation for the qctl command-line interface.\n\n")

	// Table of Contents
	fmt.Fprintf(w, "## Table of Contents\n\n")
	generateTOC(w, rootCmd, 0)
	fmt.Fprintf(w, "\n---\n\n")

	// Global flags section
	fmt.Fprintf(w, "## Global Flags\n\n")
	fmt.Fprintf(w, "These flags are available for all commands:\n\n")
	generateFlagsTable(w, rootCmd.PersistentFlags())
	fmt.Fprintf(w, "\n---\n\n")

	// Document root command
	generateCommandDoc(w, rootCmd, 0)

	// Document all subcommands
	for _, cmd := range getSortedCommands(rootCmd) {
		if cmd.Name() == cmdDocs || cmd.Name() == cmdHelp || cmd.Name() == cmdCompletion {
			continue // Skip docs, help, and completion commands
		}
		generateCommandTree(w, cmd, 1)
	}
}

func generateTOC(w io.Writer, cmd *cobra.Command, depth int) {
	if depth > 0 {
		indent := strings.Repeat("  ", depth-1)
		anchor := strings.ReplaceAll(cmd.CommandPath(), " ", "-")
		fmt.Fprintf(w, "%s- [%s](#%s)\n", indent, cmd.CommandPath(), anchor)
	}

	for _, subCmd := range getSortedCommands(cmd) {
		if subCmd.Name() == cmdDocs || subCmd.Name() == cmdHelp || subCmd.Name() == cmdCompletion {
			continue
		}
		generateTOC(w, subCmd, depth+1)
	}
}

func generateCommandTree(w io.Writer, cmd *cobra.Command, depth int) {
	generateCommandDoc(w, cmd, depth)

	for _, subCmd := range getSortedCommands(cmd) {
		if subCmd.Name() == cmdHelp {
			continue
		}
		generateCommandTree(w, subCmd, depth+1)
	}
}

func generateCommandDoc(w io.Writer, cmd *cobra.Command, depth int) {
	// Skip root command as it's handled separately in the header
	if depth == 0 {
		return
	}

	// Header with appropriate level (max h4)
	headerLevel := min(depth+1, 4)
	headerPrefix := strings.Repeat("#", headerLevel)

	// Create anchor-friendly name
	anchor := strings.ReplaceAll(cmd.CommandPath(), " ", "-")
	fmt.Fprintf(w, "%s %s {#%s}\n\n", headerPrefix, cmd.CommandPath(), anchor)

	// Short description
	if cmd.Short != "" {
		fmt.Fprintf(w, "%s\n\n", cmd.Short)
	}

	// Long description
	if cmd.Long != "" && cmd.Long != cmd.Short {
		fmt.Fprintf(w, "### Description\n\n")
		fmt.Fprintf(w, "%s\n\n", cmd.Long)
	}

	// Usage
	fmt.Fprintf(w, "### Usage\n\n")
	fmt.Fprintf(w, "```\n%s\n```\n\n", cmd.UseLine())

	// Aliases
	if len(cmd.Aliases) > 0 {
		fmt.Fprintf(w, "**Aliases:** %s\n\n", strings.Join(cmd.Aliases, ", "))
	}

	// Examples
	if cmd.Example != "" {
		fmt.Fprintf(w, "### Examples\n\n")
		fmt.Fprintf(w, "```bash\n%s\n```\n\n", cmd.Example)
	}

	// Local flags
	localFlags := cmd.LocalFlags()
	if localFlags.HasFlags() {
		fmt.Fprintf(w, "### Flags\n\n")
		generateFlagsTable(w, localFlags)
		fmt.Fprintf(w, "\n")
	}

	// Subcommands list
	subCmds := getSortedCommands(cmd)
	if len(subCmds) > 0 {
		fmt.Fprintf(w, "### Subcommands\n\n")
		for _, subCmd := range subCmds {
			if subCmd.Name() == cmdHelp {
				continue
			}
			fmt.Fprintf(w, "- **%s** - %s\n", subCmd.Name(), subCmd.Short)
		}
		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "---\n\n")
}

func generateFlagsTable(w io.Writer, flags *pflag.FlagSet) {
	fmt.Fprintf(w, "| Flag | Shorthand | Type | Default | Description |\n")
	fmt.Fprintf(w, "|------|-----------|------|---------|-------------|\n")

	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			return
		}

		shorthand := ""
		if f.Shorthand != "" {
			shorthand = fmt.Sprintf("-%s", f.Shorthand)
		}

		defaultVal := f.DefValue
		switch defaultVal {
		case "", "[]":
			defaultVal = "-"
		}

		// Escape pipes in description
		desc := strings.ReplaceAll(f.Usage, "|", "\\|")

		fmt.Fprintf(w, "| --%s | %s | %s | %s | %s |\n",
			f.Name, shorthand, f.Value.Type(), defaultVal, desc)
	})
}

func getSortedCommands(cmd *cobra.Command) []*cobra.Command {
	commands := cmd.Commands()
	sort.Slice(commands, func(i, j int) bool {
		return commands[i].Name() < commands[j].Name()
	})
	return commands
}
