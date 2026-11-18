package root

import (
	"fmt"

	"github.com/qlustered/qctl/internal/agentskill"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/client"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/commands/alert"
	"github.com/qlustered/qctl/internal/commands/api_resources"
	"github.com/qlustered/qctl/internal/commands/apply"
	"github.com/qlustered/qctl/internal/commands/auth"
	"github.com/qlustered/qctl/internal/commands/cache"
	"github.com/qlustered/qctl/internal/commands/change_proposal"
	"github.com/qlustered/qctl/internal/commands/comment_thread"
	"github.com/qlustered/qctl/internal/commands/completion"
	"github.com/qlustered/qctl/internal/commands/config"
	copycmd "github.com/qlustered/qctl/internal/commands/copy"
	"github.com/qlustered/qctl/internal/commands/create"
	"github.com/qlustered/qctl/internal/commands/data_review"
	"github.com/qlustered/qctl/internal/commands/delete"
	"github.com/qlustered/qctl/internal/commands/deliverable"
	"github.com/qlustered/qctl/internal/commands/deliverable_run"
	"github.com/qlustered/qctl/internal/commands/describe"
	"github.com/qlustered/qctl/internal/commands/diff"
	"github.com/qlustered/qctl/internal/commands/disable"
	"github.com/qlustered/qctl/internal/commands/docs"
	"github.com/qlustered/qctl/internal/commands/download"
	"github.com/qlustered/qctl/internal/commands/duplicate_group"
	"github.com/qlustered/qctl/internal/commands/email_intake"
	"github.com/qlustered/qctl/internal/commands/email_sender"
	"github.com/qlustered/qctl/internal/commands/enable"
	"github.com/qlustered/qctl/internal/commands/explain"
	"github.com/qlustered/qctl/internal/commands/file"
	"github.com/qlustered/qctl/internal/commands/get"
	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/commands/inspect"
	"github.com/qlustered/qctl/internal/commands/kill"
	"github.com/qlustered/qctl/internal/commands/merge"
	"github.com/qlustered/qctl/internal/commands/notification"
	"github.com/qlustered/qctl/internal/commands/pii_access_request"
	"github.com/qlustered/qctl/internal/commands/pii_grant"
	"github.com/qlustered/qctl/internal/commands/privilege"
	"github.com/qlustered/qctl/internal/commands/queue_message"
	"github.com/qlustered/qctl/internal/commands/rekey"
	"github.com/qlustered/qctl/internal/commands/reveal"
	"github.com/qlustered/qctl/internal/commands/review"
	"github.com/qlustered/qctl/internal/commands/rows"
	"github.com/qlustered/qctl/internal/commands/rule_upgrade"
	"github.com/qlustered/qctl/internal/commands/run"
	"github.com/qlustered/qctl/internal/commands/set_default"
	"github.com/qlustered/qctl/internal/commands/share"
	"github.com/qlustered/qctl/internal/commands/skill"
	"github.com/qlustered/qctl/internal/commands/slack"
	"github.com/qlustered/qctl/internal/commands/submission_status"
	"github.com/qlustered/qctl/internal/commands/submit"
	"github.com/qlustered/qctl/internal/commands/suggestion"
	"github.com/qlustered/qctl/internal/commands/table_member"
	"github.com/qlustered/qctl/internal/commands/table_rule"
	"github.com/qlustered/qctl/internal/commands/table_series"
	"github.com/qlustered/qctl/internal/commands/undelete"
	"github.com/qlustered/qctl/internal/commands/undoredo"
	"github.com/qlustered/qctl/internal/commands/upload"
	"github.com/qlustered/qctl/internal/commands/version"
	"github.com/qlustered/qctl/internal/commands/warning"
	qctlconfig "github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	internalversion "github.com/qlustered/qctl/internal/version"
	"github.com/spf13/cobra"
)

var (
	// cfgFile is the config file path
	cfgFile string
	// contextName is the context --context selects for this invocation
	contextName string
)

var rootCmd = &cobra.Command{
	Use:   "qctl",
	Short: "qctl - CLI for Qluster platform",
	Long: `qctl is a command-line interface for managing Qluster resources.

It provides commands for managing tables, ingestion pipelines, storage,
rules, queues, training jobs, and more.

START HERE
  qctl <command> --help          one command: its flags and examples
  qctl guide                     task walkthroughs; --search <word> finds the section
  qctl explain <res>[.<field>]   what a resource is and how to drive it; --grep <word> its gotchas
  qctl api-resources             every resource, and every verb that acts on it

All four work offline and need no authentication. qctl docs prints the whole
command reference, several hundred KB at once: search it, don't read it.

AGENT SKILL
  qctl skill install TARGET   write qctl's operating skill into an agent
                              host's skill directory: ` + agentskill.JoinTargets() + `

  Optional, and never a prerequisite: every qctl command is available
  whether the skill is installed or not. Offline too.

ORGANIZATION SCOPE
  Tables, rules, jobs and the rest live inside an organization. A command
  that acts on one of them resolves the organization first and stays there
  for the whole invocation: --org, else QCTL_ORG, else the context's
  default. An empty list, a 403 or a 404 means the resource is unavailable
  in that organization -- qctl never retries in another one, and neither should
  you. Change it deliberately: ` + "`--org`" + `, ` + "`qctl auth\n  switch-org`" + `, or ` + "`qctl config set-context --current --org <name-or-id>`" + `.`,
	Annotations: catalog.Parent(),
	// Both fields are load-bearing, for different reasons. Without Args,
	// cobra's Find rejects an unknown name itself through legacyArgs
	// (cobra@v1.10.2/command.go Find, args.go) and returns from
	// inside Execute before execute() ever runs, so nothing here can type it.
	// Without RunE, execute() bails at !Runnable() and returns
	// flag.ErrHelp before it reaches ValidateArgs, printing help and exiting
	// 0. With both, `qctl bogustop` lands in the same SubcommandRequired the
	// other 23 nodes routing through it use (22 assign it as their RunE;
	// `qctl create` calls it from a wrapper) -- exit 2, and the api-resources
	// pointer root was the only one of them to lack. Bare `qctl` still prints
	// help and exits 0: SubcommandRequired calls cmd.Help() when there are no
	// arguments.
	//
	// Side effect, deliberate: a Runnable root now reaches PersistentPreRunE,
	// so `qctl -o bogus` is rejected instead of quietly printing help.
	//
	// One side effect had to be undone rather than accepted. cobra's stock
	// help command decides a topic is unknown from the error Find returns,
	// and a non-nil Args is exactly what stops Find producing one -- so
	// `qctl help bogustopic` would have printed root's whole help in silence.
	// help.go registers a help command that resolves the topic itself.
	//
	// `qctl ""` exits 2 in the decorated Args validator. `qctl -- bogustop`
	// reaches RunE and exits 2 like `qctl get -- bogusresource`.
	//
	// The help and version flags never reach RunE: cobra short-circuits both
	// before it. `qctl bogustop --help` and `-h` exit 2 all the same, because
	// root's HelpFunc (help_flag.go) rejects an unknown name on every dispatch
	// parent, root included. `qctl bogustop --version` prints the version and
	// exits 0. Root is the only command that sets Version, so `qctl get
	// bogusresource --version` is exit 2, "unknown flag".
	//
	// flag_errors_exit_code_test.go pins the empty name and the help flag.
	Args:          cobra.ArbitraryArgs,
	RunE:          cmdutil.SubcommandRequired,
	Version:       internalversion.Version,
	SilenceUsage:  true,
	SilenceErrors: true,
	// Disable default completion command - we provide our own with better help text
	CompletionOptions: cobra.CompletionOptions{
		DisableDefaultCmd: true,
	},
}

func init() {
	// cobra runs only the closest PersistentPreRunE. There are zero other
	// hooks in the tree today, so this changes nothing now -- it exists so
	// that a subcommand adding its own hook later cannot silently switch
	// --output validation off for its whole subtree.
	cobra.EnableTraverseRunHooks = true

	// cobra hands ParseFlags' failures -- unknown flag, unparseable value --
	// straight back to Execute, where GetExitCode finds no *qerrors.Error and
	// falls through to 1. Every other malformed input in qctl exits 2.
	// FlagError keeps cobra's wording at the start; subcommands inherit it
	// because FlagErrorFunc() walks up to the parent.
	rootCmd.SetFlagErrorFunc(cmdutil.FlagError)

	// The one place that covers every --output read site. Runs after cobra's
	// initializers and ValidateArgs, before any RunE, so no command reaches
	// the network with a format it cannot render.
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if err := cmdutil.ValidateOutputFormat(cmd); err != nil {
			return err
		}
		// Before any RunE, so a bad --columns name costs no config read or request.
		if err := cmdutil.ValidateDeclaredColumns(cmd); err != nil {
			return err
		}
		cmdutil.ApplyContextOutputFormat(cmd)
		// The raw backend text behind a Replace code is gated on -v, and
		// error rendering happens far from any cobra command. This hook is
		// the one place that runs for every command in the tree. The error is
		// discarded, matching every other GetCount("verbose") call site in
		// this repo: a hypothetical flag-shadowing failure here should degrade
		// the -v gate, not fail the whole command.
		verbosity, _ := cmd.Flags().GetCount("verbose")
		apierror.SetVerbosity(verbosity)
		// Same hook for the same reason: --request-timeout and --retries are
		// global, and the ~195 places that build a domain client are far from
		// any cobra command. Resolving both here is what makes a client added
		// later honour them without being told to.
		if err := applyRequestPolicy(cmd); err != nil {
			return err
		}
		// Same class, different cobra path: ValidateRequiredFlags' error is
		// returned directly rather than through FlagErrorFunc
		// (cobra command.go execute), so it would reach main bare and exit 1.
		// cobra runs it just after this hook, so running it here first is the
		// only way to type it. Last in the hook, so no other error's ordering
		// moves: `qctl apply table -o bogus` still reports the output format.
		if err := cmd.ValidateRequiredFlags(); err != nil {
			return cmdutil.RequiredFlagsError(cmd, err)
		}
		notifyInstalledSkills(cmd)
		// Last, because it can prompt: every usage error above wins first.
		return cmdutil.GuardProtectedContext(cmd)
	}

	cobra.OnInitialize(initConfig)

	rootCmd.SetVersionTemplate(fmt.Sprintf("qctl version %s (commit: %s)\n", internalversion.Version, internalversion.Commit))

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.qctl/config)")
	rootCmd.PersistentFlags().StringVar(&contextName, "context", "", "Context to use for this command, without switching (required by destructive commands on a protected context)")
	rootCmd.PersistentFlags().String("server", "", "API server URL (overrides context config)")
	rootCmd.PersistentFlags().String("user", "", "User email (overrides context config)")
	rootCmd.PersistentFlags().StringP("output", "o", "table", "output format (table|json|yaml|name)")
	rootCmd.PersistentFlags().Bool("no-headers", false, "Don't print column headers (table format only)")
	rootCmd.PersistentFlags().String("columns", "", "Comma-separated list of columns to display (table format only)")
	rootCmd.PersistentFlags().Int("max-column-width", 0, "Maximum width for table columns, 0 = no limit (default 80 on a terminal, no limit otherwise)")
	rootCmd.PersistentFlags().Bool("allow-plaintext-secrets", false, "Show secret fields in plaintext (json/yaml formats)")
	rootCmd.PersistentFlags().Bool("allow-insecure-http", false, "Allow non-localhost http:// endpoints")
	rootCmd.PersistentFlags().CountP("verbose", "v", "Verbosity level (-v=7 structured HTTP with redacted bodies, -v=8 curl with redacted credentials, -v=9 curl full)")
	rootCmd.PersistentFlags().Duration("request-timeout", 0, "Deadline for a single HTTP request attempt (0 = 30s default)")
	rootCmd.PersistentFlags().Int("retries", 0, "Retry a failed read-only request this many times (0 = never)")
	cmdutil.RefStringP(rootCmd.PersistentFlags(), "org", "O", "org", "Organization ID or name (overrides QCTL_ORG env and context config)")

	// Without this the shell completes --output with *filenames*. The values
	// come from the executing command, so a command that declares a narrower
	// set offers only what it can render. download file's local --output keeps
	// filename completion, which is right -- it is a path.
	cobra.CheckErr(rootCmd.RegisterFlagCompletionFunc("output",
		func(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return cmdutil.SupportedOutputFormats(cmd), cobra.ShellCompDirectiveNoFileComp
		}))

	// Add subcommands
	rootCmd.AddCommand(version.NewCommand())
	rootCmd.AddCommand(config.NewCommand())
	rootCmd.AddCommand(config.NewInfoCommand())
	rootCmd.AddCommand(auth.NewCommand())
	rootCmd.AddCommand(cache.NewCommand())
	rootCmd.AddCommand(get.NewCommand())
	rootCmd.AddCommand(describe.NewCommand())
	rootCmd.AddCommand(explain.NewCommand())
	rootCmd.AddCommand(api_resources.NewCommand())
	rootCmd.AddCommand(docs.NewCommand(rootCmd))
	// guide declares its format set here because cmdutil's own tests import
	// the guide package, so guide cannot import cmdutil.
	guideCmd := guide.NewCommand()
	guideCmd.Annotations = cmdutil.WithFormats(guideCmd.Annotations, "table")
	rootCmd.AddCommand(guideCmd)
	rootCmd.AddCommand(completion.NewCommand(rootCmd))
	rootCmd.AddCommand(skill.NewCommand())

	// Verb commands (kubectl-style)
	rootCmd.AddCommand(run.NewCommand())
	rootCmd.AddCommand(kill.NewCommand())
	rootCmd.AddCommand(upload.NewCommand())
	rootCmd.AddCommand(file.NewCommand())
	rootCmd.AddCommand(table_series.NewCommand())
	rootCmd.AddCommand(submission_status.NewCommand())
	rootCmd.AddCommand(download.NewCommand())
	rootCmd.AddCommand(delete.NewCommand())
	rootCmd.AddCommand(undelete.NewCommand())
	rootCmd.AddCommand(undoredo.NewUndoCommand())
	rootCmd.AddCommand(undoredo.NewRedoCommand())
	rootCmd.AddCommand(share.NewCommand())
	rootCmd.AddCommand(copycmd.NewCommand())
	rootCmd.AddCommand(merge.NewCommand())
	rootCmd.AddCommand(enable.NewCommand())
	rootCmd.AddCommand(disable.NewCommand())
	rootCmd.AddCommand(set_default.NewCommand())
	rootCmd.AddCommand(apply.NewCommand())
	rootCmd.AddCommand(diff.NewCommand())
	rootCmd.AddCommand(submit.NewCommand())
	rootCmd.AddCommand(create.NewCommand())
	rootCmd.AddCommand(inspect.NewCommand())
	rootCmd.AddCommand(review.NewCommand())
	rootCmd.AddCommand(data_review.NewCommand())
	rootCmd.AddCommand(data_review.NewGroupCommand())
	rootCmd.AddCommand(deliverable.NewCommand())
	rootCmd.AddCommand(deliverable_run.NewCommand())
	rootCmd.AddCommand(comment_thread.NewCommand())
	rootCmd.AddCommand(suggestion.NewCommand())
	rootCmd.AddCommand(alert.NewCommand())
	rootCmd.AddCommand(warning.NewCommand())
	rootCmd.AddCommand(notification.NewCommand())
	rootCmd.AddCommand(slack.NewCommand())
	rootCmd.AddCommand(rows.NewCommand())
	rootCmd.AddCommand(rule_upgrade.NewCommand())
	rootCmd.AddCommand(rekey.NewCommand())
	rootCmd.AddCommand(table_rule.NewCommand())
	rootCmd.AddCommand(table_member.NewCommand())
	rootCmd.AddCommand(queue_message.NewCommand())
	rootCmd.AddCommand(privilege.NewCommand())
	rootCmd.AddCommand(pii_grant.NewCommand())
	rootCmd.AddCommand(email_intake.NewCommand())
	rootCmd.AddCommand(email_sender.NewCommand())
	rootCmd.AddCommand(pii_access_request.NewCommand())
	rootCmd.AddCommand(duplicate_group.NewCommand())
	rootCmd.AddCommand(reveal.NewCommand())
	rootCmd.AddCommand(change_proposal.NewCommand())

	// `help` is a command node like any other, so it must carry an annotation
	// too; newHelpCommand sets it. Registering our own is what keeps `qctl
	// help bogustopic` saying so -- cobra's stock one reads the unknown topic
	// off Find's error, which root's Args suppresses (see help.go).
	// InitDefaultHelpCmd adopts whatever SetHelpCommand left and is idempotent
	// (cobra calls it again during Execute); calling it here means the walk
	// below sees `help` without anyone having run a command first.
	rootCmd.SetHelpCommand(newHelpCommand(rootCmd))
	rootCmd.InitDefaultHelpCmd()

	// cobra answers the help flag with HelpFunc instead of RunE, so this is
	// where `qctl get bogus --help` is turned into SubcommandRequired's exit 2
	// rather than get's help. Every command without its own HelpFunc inherits
	// it.
	rootCmd.SetHelpFunc(rejectUnknownUnderHelp(rootCmd.HelpFunc()))

	// After every AddCommand, so each noun command is in the tree to receive
	// its resource's aliases.
	cobra.CheckErr(catalog.WireAliases(rootCmd))

	// Last in init(), after every AddCommand and after InitDefaultHelpCmd, so
	// the walk sees the whole tree. cobra validates Args before any hook this
	// file installs could run, so decorating the validators here is the only
	// way to give a wrong argument count the exit 2 that every other malformed
	// input gets. TestEveryArgsValidatorExitsBadUsage pins it.
	cmdutil.TypeArgsErrors(rootCmd)
	// Same walk, same reason to sit here: every positional reference takes the
	// kind/<id> that -o name prints. TestPositionalsTakeTheirOwnNameOutput pins it.
	cmdutil.StripKindRefs(rootCmd)
}

func notifyInstalledSkills(cmd *cobra.Command) {
	for node := cmd; node != nil; node = node.Parent() {
		switch node.Name() {
		case "skill", "completion", "__complete", "__completeNoDesc", helpCommandName, "version":
			return
		}
	}
	agentskill.Notify(cmd.ErrOrStderr())
}

// applyRequestPolicy resolves --request-timeout and --retries into the
// process-wide client.RequestPolicy, once per invocation.
//
// Both flags are read through cmd.Flags() rather than
// cmd.Root().PersistentFlags() so that a subcommand which one day registers a
// narrower --request-timeout of its own is the one that wins, which is how
// --verbose is read two lines above.
//
// Negative values are a client-side usage failure, so exit 2 like every other
// one: a Duration flag happily parses "-5s", and an http.Client given a
// negative timeout does not fail -- it treats it as expired and every request
// dies instantly with a deadline error that names no flag.
func applyRequestPolicy(cmd *cobra.Command) error {
	timeout, _ := cmd.Flags().GetDuration("request-timeout")
	retries, _ := cmd.Flags().GetInt("retries")

	if timeout < 0 {
		return qerrors.NewBadUsagef(
			"--request-timeout must not be negative (got %s); 0 means the built-in default of %s",
			timeout, client.DefaultTimeout)
	}
	if retries < 0 {
		return qerrors.NewBadUsagef("--retries must not be negative (got %d); 0 means never retry", retries)
	}

	client.SetRequestPolicy(client.RequestPolicy{Timeout: timeout, Retries: retries})
	return nil
}

// initConfig is called before running any command
// Config loading is now handled by individual commands using the internal/config package
func initConfig() {
	// Config itself is loaded on-demand by commands. All this has to do is
	// publish --config before any of them get there: cobra runs its
	// initializers after flag parsing and before Run, so cfgFile is set by
	// now, and every later config.Load/Save picks up the override.
	qctlconfig.SetPathOverride(cfgFile)
	qctlconfig.SetContextOverride(contextName)
}

// Execute runs the root command
func Execute() error {
	_, err := executeC()
	return apierror.FinalizeScopeNote(err)
}

// GetRootCmd returns the root command (useful for adding subcommands)
func GetRootCmd() *cobra.Command {
	return rootCmd
}
