package root

import (
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/spf13/cobra"
)

const helpCommandName = "help"

// newHelpCommand is cobra's stock help command (InitDefaultHelpCmd,
// cobra@v1.10.2/command.go) with one substantive change: it resolves
// the topic through findTopic rather than calling Find directly.
//
// Why it has to. The stock command does `cmd, _, e := c.Root().Find(args)` and
// reports the topic unknown when e != nil. Find only produces that error by
// falling through to legacyArgs, and it only falls through when the command it
// landed on has a nil Args (command.go Find). Root sets `Args:
// cobra.ArbitraryArgs` on purpose (see root.go), so `qctl help bogustopic`
// resolves to root with no error and the stock command prints root's whole
// help as though `bogustopic` had been a real topic. That is a diagnostic this
// branch would otherwise have taken away.
//
// The replacement calls findTopic instead, which restores the nil Args for the
// length of the one Find call. So this decides nothing for itself: behavior is
// whatever qctl printed before root gained an Args, path for path.
// `qctl help get bogus` stays silent (legacyArgs never fired below root
// either), and the exit code stays 0, because it always was 0. Restoring a
// diagnostic is not the same decision as rejecting the input; whether an
// unknown help topic should exit 2 is a product question this branch does not
// answer.
//
// Everything else here is a transcription. The wording, the `%#q` slice
// formatting that renders the topic as [`bogustopic`], the stderr-by-default
// stream, the InitDefaultHelpFlag/InitDefaultVersionFlag calls and the
// completion function are cobra's, so `qctl help`, `qctl help get`, `qctl help
// get rule` and `__complete help …` are byte-identical to the stock command.
// Two things cannot be transcribed, both because the field they read is
// unexported. The completion function's `subCmd == cmd.helpCommand` becomes a
// comparison on the name -- the same set of commands, since the help command is
// the only node in this tree named "help". And `GroupID: c.helpCommandGroupID`
// is dropped entirely: there is no getter for it. Inert while qctl
// declares no command groups, and TestHelpCommandMatchesCobrasStockOne fails
// the day that stops being true.
func newHelpCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long: `Help provides help for any command in the application.
Simply type ` + root.DisplayName() + ` help [path to command] for full details.`,
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table"),
		ValidArgsFunction: func(c *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
			var completions []cobra.Completion
			cmd, e := findTopic(c.Root(), args)
			if e != nil {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			if cmd == nil {
				// Root help command.
				cmd = c.Root()
			}
			for _, subCmd := range cmd.Commands() {
				if subCmd.IsAvailableCommand() || subCmd.Name() == helpCommandName {
					if strings.HasPrefix(subCmd.Name(), toComplete) {
						completions = append(completions, cobra.CompletionWithDesc(subCmd.Name(), subCmd.Short))
					}
				}
			}
			return completions, cobra.ShellCompDirectiveNoFileComp
		},
		Run: func(c *cobra.Command, args []string) {
			cmd, e := findTopic(c.Root(), args)
			if cmd == nil || e != nil {
				c.Printf("Unknown help topic %#q\n", args)
				cobra.CheckErr(c.Root().Usage())
				return
			}
			// Flow the context down to be used in help text.
			if cmd.Context() == nil {
				cmd.SetContext(c.Context())
			}
			cmd.InitDefaultHelpFlag()    // make possible 'help' flag to be shown
			cmd.InitDefaultVersionFlag() // make possible 'version' flag to be shown
			cobra.CheckErr(cmd.Help())
		},
	}
}

// findTopic is Find, asked the way the stock help command needs it asked.
//
// Find reports an unknown topic only by falling through to legacyArgs, and it
// only falls through when the landing command's Args is nil
// (cobra@v1.10.2/command.go Find) -- so root's Args, which this branch added
// for `qctl bogustop`, silences it. Clearing that field for the length of the
// call is what makes the answer cobra's rather than this file's.
//
// The alternative was to re-derive legacyArgs' condition (args.go) from
// Find's leftover slice, which does not work: legacyArgs is handed
// stripFlags of that slice (command.go Find), and stripFlags drops empty and
// `-`-prefixed tokens, stops at `--`, and eats the value of a `-f arg` pair.
// Re-deriving the condition means transcribing stripFlags too, and this file
// already carries one cobra transcription; a second one is a second thing to
// keep in step with cobra. Delegating cannot drift.
//
// Mutating the tree mid-run is safe here and only here: cobra command trees are
// not concurrency-safe to begin with, nothing can run between the assignment
// and the deferred restore, and the only readers of root.Args are Find -- the
// call being made, which wants the nil -- and root's own ValidateArgs, which
// ExecuteC ran long before the help command's Run. Find's one write is
// commandCalledAs.name on the nodes it matches, already set to those same
// values by the Find ExecuteC did over the same argv.
// TestHelpDoesNotLeaveRootWithoutItsArgs holds the restore in place.
//
// Find's leftover slice is dropped because neither caller wants it; the stock
// help command discards it too (command.go InitDefaultHelpCmd).
func findTopic(root *cobra.Command, args []string) (*cobra.Command, error) {
	saved := root.Args
	root.Args = nil
	defer func() { root.Args = saved }()
	cmd, _, err := root.Find(args)
	return cmd, err
}
