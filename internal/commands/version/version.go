package version

import (
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/config"
	"github.com/qlustered/qctl/internal/output"
	internalversion "github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
)

// NewCommand creates the version command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "version",
		Short:       "Print version information",
		Long:        `Print the version information for qctl client.`,
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE:        runVersion,
	}

	return cmd
}

// versionDocument is the version as -o json and -o yaml print it.
type versionDocument struct {
	Version        string  `json:"version"`
	Commit         string  `json:"commit"`
	CurrentContext *string `json:"current_context"`
}

func runVersion(cmd *cobra.Command, args []string) error {
	// The current context is shown when the config loads and names one.
	var currentContext *string
	if cfg, err := config.Load(); err == nil && cfg.CurrentContext != "" {
		currentContext = &cfg.CurrentContext
	}

	format, _ := cmd.Flags().GetString("output")
	if format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		printer, err := output.NewPrinterFromCmd(cmd)
		if err != nil {
			return err
		}
		return printer.Print(versionDocument{
			Version:        internalversion.Version,
			Commit:         internalversion.Commit,
			CurrentContext: currentContext,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "qctl version %s (commit: %s)\n", internalversion.Version, internalversion.Commit)
	if currentContext != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "Current context: %s\n", *currentContext)
	}
	return nil
}
