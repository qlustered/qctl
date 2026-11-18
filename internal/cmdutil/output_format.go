package cmdutil

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// AnnotationOutputFormats is the cobra annotation key a command uses to declare
// the complete set of --output values it honors, comma-separated, in the order
// an error message should list them.
//
// Declare one only when the command's real vocabulary differs from
// DefaultOutputFormats: `qctl describe dataset` renders YAML for -o name, and
// `qctl get rule` prints Python source for -o code -- a value that appears in
// no Format constant at all. A command with no annotation gets the four the
// root flag advertises.
//
//	Annotations: cmdutil.WithFormats(catalog.For("rule"), "table", "json", "yaml", "code"),
//
// This key rides alongside catalog's "resource" key in the same map; catalog
// ignores keys it does not know, and catalog.Signature does not hash
// annotations at all, so declaring one re-stamps no resource page.
const AnnotationOutputFormats = "output_formats"

// DefaultOutputFormats is what a command supports unless it declares otherwise.
func DefaultOutputFormats() []string { return output.FormatNames() }

// WithFormats declares the complete set of --output values a command honors,
// merged into the annotation map catalog.For/Parent/Dispatcher built.
func WithFormats(base map[string]string, formats ...string) map[string]string {
	merged := make(map[string]string, len(base)+1)
	for k, v := range base {
		merged[k] = v
	}
	merged[AnnotationOutputFormats] = strings.Join(formats, ",")
	return merged
}

// SupportedOutputFormats returns the --output values cmd honors.
func SupportedOutputFormats(cmd *cobra.Command) []string {
	if raw := cmd.Annotations[AnnotationOutputFormats]; raw != "" {
		return strings.Split(raw, ",")
	}
	return DefaultOutputFormats()
}

// ValidateOutputFormat rejects an --output value the command cannot render.
//
// The root command runs this from PersistentPreRunE, which is the only place
// that covers all ~50 read sites at once: the value is read back out of the
// flag by every printer, every bespoke switch, and every default-column
// selector, and each of those used to have its own idea of what an unknown
// value meant (table here, YAML there, Markdown in `get data`).
func ValidateOutputFormat(cmd *cobra.Command) error {
	if !ownsRootOutputFlag(cmd) {
		return nil
	}
	// Only what the user typed. The flag's default is "table", which a command
	// that renders JSON by default (inspect dry-run-job) does not support --
	// validating an unpassed flag would break it with no flag in sight.
	if !cmd.Flags().Changed("output") {
		return nil
	}
	value, _ := cmd.Flags().GetString("output")
	return ValidateOutputFormatValue(value, SupportedOutputFormats(cmd))
}

// ValidateOutputFormatValue is ValidateOutputFormat for a value that did not
// come from this command's flag -- the one persisted by `config set-context`.
//
// Exit 2, like every other flag-validation failure: a script that cannot tell
// "you asked for a format that does not exist" from "the API fell over" is
// back to acting on a wrong answer.
func ValidateOutputFormatValue(value string, supported []string) error {
	for _, f := range supported {
		if value == f {
			return nil
		}
	}
	return qerrors.NewBadUsagef("unknown output format %q\n  valid: %s", value, strings.Join(supported, ", "))
}

// ownsRootOutputFlag reports whether cmd's --output is the root's persistent
// format flag rather than a local flag of the same name.
//
// `qctl download file --output report.csv` is a file path, and pflag's
// AddFlagSet skips a flag whose name is already registered, so that local flag
// shadows the persistent one wholesale (the -o shorthand included). Pointer
// identity is the only reliable way to tell them apart; a command that
// registers its own --output owns its own validation.
func ownsRootOutputFlag(cmd *cobra.Command) bool {
	f := cmd.Flags().Lookup("output")
	if f == nil {
		return false
	}
	root := cmd.Root()
	return root != nil && root.PersistentFlags().Lookup("output") == f
}

// ApplyContextOutputFormat fills --output from the current context's `output`
// key when the user did not pass the flag. Precedence is flag > context, the
// same order every other setting uses.
//
// Until now that key was written by `config set-context --output` and read by
// nothing but the display line in `config current-context` -- the per-context
// default documented in internal/config/config.go did not exist. Setting the
// flag (rather than passing the value around) is deliberate: ~50 sites read
// the flag, and the commands that branch on Flags().Changed("output") should
// treat a configured default exactly like a typed one.
//
// Best-effort on purpose:
//   - a config that fails to load is ignored, so `qctl version --config /bad`
//     keeps exiting 0 as it does today; every command that needs the config
//     loads it again and reports the error itself.
//   - a stored value this command cannot render is skipped, leaving the
//     command's own default. The user configured a preference, not this
//     invocation.
//   - a stored value no command can render warns and is skipped rather than
//     failing: erroring here would break `config set-context`, the one command
//     that can repair it.
func ApplyContextOutputFormat(cmd *cobra.Command) {
	if !ownsRootOutputFlag(cmd) || cmd.Flags().Changed("output") {
		return
	}
	cfg, err := config.Load()
	if err != nil || cfg == nil {
		return
	}
	ctx, err := cfg.GetCurrentContext()
	if err != nil || ctx == nil || ctx.Output == "" {
		return
	}
	if ValidateOutputFormatValue(ctx.Output, DefaultOutputFormats()) != nil {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"warning: context %q sets an invalid output format %q; ignoring it\n",
			cfg.CurrentContext, ctx.Output)
		return
	}
	if ValidateOutputFormatValue(ctx.Output, SupportedOutputFormats(cmd)) != nil {
		return
	}
	_ = cmd.Flags().Set("output", ctx.Output)
}
