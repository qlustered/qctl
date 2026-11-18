package create

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// createRedirect returns a hint pointing at the declarative `qctl apply` path
// when a user guesses an apply-managed resource under `qctl create`. It reads
// the catalog, so it covers the four resources that have no apply subcommand
// and can never drift from what `qctl apply -f` accepts.
//
// The fold is applied HERE even though catalog.Lookup applies the same one
// itself (build.go), because the plural retry below transforms this local
// value rather than asking Lookup twice. Neither half of it is redundant:
// TrimSuffix(" tables ", "s") is a no-op, since the string ends in a space,
// and TrimSuffix("TABLES", "s") is a no-op too, since it ends in a capital.
// Without both folds a padded or shouted plural reaches Lookup unchanged and
// misses. Verified by removing each in turn: with no ToLower,
// `qctl create TABLES` falls through to the bare api-resources hint, and with
// no TrimSpace (which is the half this adds) so does `qctl create " tables "`.
// namesTheTable and namesTheRows (internal/commands/delete/delete.go) have
// carried both all along, which is why a padded plural resolved under
// `qctl delete` and not here.
func createRedirect(root *cobra.Command, arg string) (string, bool) {
	name := strings.ToLower(strings.TrimSpace(arg))
	r, ok := catalog.Lookup(root, name)
	if !ok {
		// Tolerate a plural guess (tables, datasets), like the old map did.
		r, ok = catalog.Lookup(root, strings.TrimSuffix(name, "s"))
	}
	if !ok || r.Class != catalog.ClassApply {
		return "", false
	}

	line := "qctl apply -f <file.yaml>" + prompt.YesFlag(false) + "   # kind: " + r.Kind
	for _, u := range r.Usage {
		if strings.HasPrefix(u, "qctl apply "+r.Name+" ") {
			line = u + prompt.YesFlag(false) // a typed subcommand exists; name it
			break
		}
	}
	return fmt.Sprintf("%s is managed declaratively with 'qctl apply':\n  %s\n\n%s",
		r.Name, line, cmdutil.APIResourcesHint), true
}

const (
	managedLabel  = "Managed this way: "
	managedWidth  = 72
	managedIndent = "  "
)

// wrapList renders "<label>a, b, c" wrapped to width, continuation lines
// prefixed with indent. Items are never split: an item wider than the budget
// gets its own overlong line rather than being broken across two, because a
// resource name broken mid-word is not copy-pasteable and none of the eight
// names comes close to the budget today.
func wrapList(label string, items []string, width int, indent string) string {
	if len(items) == 0 {
		return ""
	}
	var lines []string
	line := label
	lineHasItem := false
	for i, item := range items {
		piece := item
		if i < len(items)-1 {
			piece += ","
		}
		switch {
		case !lineHasItem:
			line += piece
			lineHasItem = true
		case len(line)+1+len(piece) <= width:
			line += " " + piece
		default:
			lines = append(lines, line)
			line = indent + piece
		}
	}
	return strings.Join(append(lines, line), "\n")
}

// buildLong assembles `create`'s help text. It takes the names as a parameter
// rather than calling applyManagedNames itself so that it stays pure — no
// tree, no embed, no I/O — which is what lets a test pin the exact rendering
// and reach the empty-names branch that no live path can produce.
func buildLong(names []string) string {
	header := `Create a resource imperatively. Valid resource types: dry-run-job,
composite-dry-run-job, deliverable-run

Most other resources are managed declaratively from a YAML manifest with
'qctl apply', which reads the kind from the file:

  qctl apply -f <file.yaml>        # kind is read from the manifest
  qctl apply table -f table.yaml   # or name the resource explicitly`

	footer := `

Run 'qctl api-resources' for every resource and how to manage it.

New to tables? Run: qctl guide onboard-table`

	list := ""
	if len(names) > 0 {
		list = "\n\n" + wrapList(managedLabel, names, managedWidth, managedIndent)
	}

	return header + list + footer
}

// applyManagedNames returns the resources `qctl apply -f` manages: every
// embedded page declaring a manifest kind, each followed by its aliases in
// parentheses. Sorted, because resources.Pages() already is.
//
// It reads the pages rather than the catalog because catalog.classify keys the
// apply class off page.Kind alone, so no cobra tree is required — and
// NewCommand() has no tree to consult, being called while the root is still
// assembling. This is the applyKindsLine() precedent
// (internal/commands/apply/apply.go): generate the help text from the same
// fact the behavior reads, so --help can never advertise a set the create
// redirect does not recognise.
func applyManagedNames() []string {
	pages, err := resources.Pages()
	if err != nil {
		// Unreachable in a shipped binary: the pages are a compiled embed that
		// TestEveryResourceHasPage keeps parseable. buildLong drops the whole
		// paragraph rather than printing a partial list.
		return nil
	}
	var out []string
	for _, p := range pages {
		if p.Kind == "" {
			continue
		}
		name := p.Name
		if len(p.Aliases) > 0 {
			name += " (" + strings.Join(p.Aliases, ", ") + ")"
		}
		out = append(out, name)
	}
	return out
}

// helpForCreate answers `qctl create table --help` with the apply redirect,
// on stdout with exit 0, as `qctl delete table --help` answers with its
// refusal. Only group itself is intercepted, and only on the --help path,
// where cobra passes the command line as args. Every other call goes to
// cmdutil.InheritedHelp.
func helpForCreate(group *cobra.Command) func(*cobra.Command, []string) {
	return func(cmd *cobra.Command, args []string) {
		if tail := cmd.Flags().Args(); cmd == group && len(args) > 0 && len(tail) > 0 {
			if hint, ok := createRedirect(cmd.Root(), tail[0]); ok {
				fmt.Fprintln(cmd.OutOrStdout(), hint)
				return
			}
		}
		cmdutil.InheritedHelp(group)(cmd, args)
	}
}

// NewCommand creates the create command
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "create",
		Short:       "Create a resource",
		Long:        buildLong(applyManagedNames()),
		Annotations: catalog.Parent(),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				if hint, ok := createRedirect(cmd.Root(), args[0]); ok {
					// Exit 2, like the SubcommandRequired fallthrough below:
					// both reject what the user typed.
					return qerrors.NewBadUsagef("unknown command %q for %q\n\n%s", args[0], cmd.CommandPath(), hint)
				}
			}
			if len(args) == 0 {
				return qerrors.NewBadUsagef("%s\n\nMost resources are created from a manifest: qctl apply -f <file.yaml>%s",
					cmdutil.SubcommandRequired(cmd, args), prompt.YesFlag(false))
			}
			return cmdutil.SubcommandRequired(cmd, args)
		},
	}
	cmd.SetHelpFunc(helpForCreate(cmd))

	cmd.AddCommand(NewDryRunJobCommand())
	cmd.AddCommand(NewCompositeDryRunJobCommand())
	cmd.AddCommand(NewDeliverableRunCommand())

	return cmd
}
