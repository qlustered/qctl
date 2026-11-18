package cmdutil

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// APIResourcesHint is the discoverability escape hatch every rejected
// subcommand ends with: whatever the user guessed, this names the one command
// that lists what qctl actually has. Exported so the two parents that reject a
// guess with their own message before reaching SubcommandRequired -- `create`'s
// apply redirect and `delete`'s table refusal -- end identically to it instead
// of carrying a copy that can drift.
const APIResourcesHint = "Run 'qctl api-resources' to see all resources and how to manage them."

const (
	// maxSuggestions caps the "Did you mean this?" block.
	maxSuggestions = 5
	// maxListedSubcommands is the most subcommands a bare parent names inline.
	maxListedSubcommands = 16
)

// SubcommandRequired is the RunE of a parent command that only groups
// subcommands.
//
// With no arguments, the root command prints its help and any other parent
// returns a usage error, naming its subcommands when it has at most
// maxListedSubcommands of them. With an unknown
// subcommand it returns a usage error suggesting up to five runnable command
// lines: the commands of the resource the word names, else the parent's
// near-miss children, else, under the root, the commands of the closest
// resources.
//
// Usage:
//
//	cmd := &cobra.Command{
//	    Use:   "get",
//	    Short: "Display one or many resources",
//	    RunE:  cmdutil.SubcommandRequired,
//	}
func SubcommandRequired(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		if !cmd.HasParent() {
			return cmd.Help()
		}
		return bareParentError(cmd)
	}

	unknown := args[0]
	var msg strings.Builder
	fmt.Fprintf(&msg, "unknown command %q for %q\n\n", unknown, cmd.CommandPath())
	if lines := suggestionLines(cmd, unknown); len(lines) > 0 {
		msg.WriteString("Did you mean this?\n")
		for _, line := range lines {
			fmt.Fprintf(&msg, "  %s\n", line)
		}
		msg.WriteString("\n")
	}
	msg.WriteString(APIResourcesHint)
	return qerrors.NewBadUsage(msg.String())
}

// bareParentError is the usage error for a non-root parent run without a
// subcommand. It lists the available subcommands when there are at most
// maxListedSubcommands of them.
func bareParentError(cmd *cobra.Command) error {
	var names []string
	for _, c := range cmd.Commands() {
		if c.IsAvailableCommand() && c.Name() != "help" {
			names = append(names, c.Name())
		}
	}
	if len(names) > 0 && len(names) <= maxListedSubcommands {
		return qerrors.NewBadUsagef("%s needs a subcommand: %s", cmd.CommandPath(), strings.Join(names, ", "))
	}
	return qerrors.NewBadUsage(cmd.CommandPath() + " needs a subcommand.\n" + APIResourcesHint)
}

// suggestionLines returns at most maxSuggestions runnable command lines for
// an unknown word typed under cmd.
func suggestionLines(cmd *cobra.Command, word string) []string {
	if strings.TrimSpace(word) == "" {
		return nil
	}
	root := cmd.Root()
	var cmds []*cobra.Command
	if r, ok := catalog.ResourceNamed(root, word); ok {
		cmds = resourceSuggestions(cmd, r)
	} else {
		cmds = nearMisses(cmd, word)
		if len(cmds) == 0 && !cmd.HasParent() {
			cmds = closestResources(root, word)
		}
	}
	var lines []string
	for _, c := range cmds {
		if len(lines) == maxSuggestions {
			break
		}
		lines = append(lines, catalog.UsageLine(c))
	}
	return lines
}

// resourceSuggestions returns r's commands. Under a non-root parent, the
// parent's own children that act on r come first.
func resourceSuggestions(parent *cobra.Command, r catalog.Resource) []*cobra.Command {
	var out []*cobra.Command
	if parent.HasParent() {
		nouns := catalog.NounsOf(r.Name, r.Aliases).All()
		for _, c := range parent.Commands() {
			if suggestible(c) && actsOn(c, r.Name, nouns) {
				out = append(out, c)
			}
		}
	}
	for _, c := range resourceCommands(parent.Root(), r) {
		out = appendCommand(out, c)
	}
	return out
}

// actsOn reports whether c is annotated with resource or is named, by its
// name or an alias, with one of nouns.
func actsOn(c *cobra.Command, resource string, nouns []string) bool {
	if c.Annotations[catalog.AnnotationResource] == resource {
		return true
	}
	for _, noun := range nouns {
		if c.Name() == noun || c.HasAlias(noun) {
			return true
		}
	}
	return false
}

// resourceCommands returns r's suggestible commands anywhere under root:
// those under `get` first, then `describe`, then the rest in walk order.
func resourceCommands(root *cobra.Command, r catalog.Resource) []*cobra.Command {
	byPath := map[string]*cobra.Command{}
	for _, n := range catalog.Walk(root) {
		byPath[n.Path] = n.Cmd
	}
	var get, describe, rest []*cobra.Command
	for _, path := range r.Commands {
		c := byPath[path]
		if c == nil || !suggestible(c) {
			continue
		}
		switch catalog.Verb(path) {
		case "get":
			get = append(get, c)
		case "describe":
			describe = append(describe, c)
		default:
			rest = append(rest, c)
		}
	}
	return append(append(get, describe...), rest...)
}

// nearMisses returns the children of cmd that cobra suggests for word: those
// matched by name, then those matched only by an alias, each in cmd's child
// order.
func nearMisses(cmd *cobra.Command, word string) []*cobra.Command {
	// SuggestionsFor compares against cmd.SuggestionsMinimumDistance
	// (cobra@v1.10.2/command.go), which is 0 on every qctl command: cobra
	// only defaults it to 2 inside findSuggestions, a path this
	// code never takes. Left at 0, "levenshteinDistance <= 0" matches an exact
	// name only and prefixes are the sole suggestions anyone ever sees. Set it
	// the way cobra's own default does -- writing the field back, as cobra
	// does -- so root and its 23 sibling parents suggest identically.
	if cmd.SuggestionsMinimumDistance <= 0 {
		cmd.SuggestionsMinimumDistance = 2
	}
	byName := map[string]bool{}
	for _, name := range cmd.SuggestionsFor(word) {
		byName[name] = true
	}
	// SuggestionsFor reads names only. A probe parent with one child per
	// alias applies the same matching to aliases.
	probe := &cobra.Command{SuggestionsMinimumDistance: cmd.SuggestionsMinimumDistance}
	aliasOwner := map[string]string{}
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		for _, alias := range c.Aliases {
			if _, seen := aliasOwner[alias]; !seen {
				aliasOwner[alias] = c.Name()
				probe.AddCommand(&cobra.Command{Use: alias, Run: func(*cobra.Command, []string) {}})
			}
		}
	}
	byAlias := map[string]bool{}
	for _, alias := range probe.SuggestionsFor(word) {
		byAlias[aliasOwner[alias]] = true
	}
	var named, aliased []*cobra.Command
	for _, c := range cmd.Commands() {
		switch {
		case byName[c.Name()]:
			named = append(named, c)
		case byAlias[c.Name()]:
			aliased = append(aliased, c)
		}
	}
	return append(named, aliased...)
}

// closestResources returns one command for each resource catalog.Suggest
// finds near word: its first command in resourceCommands order.
func closestResources(root *cobra.Command, word string) []*cobra.Command {
	names := catalog.Suggest(root, word)
	if len(names) == 0 {
		return nil
	}
	built, err := catalog.Build(root)
	if err != nil {
		return nil
	}
	var out []*cobra.Command
	for _, name := range names {
		for _, r := range built {
			if r.Name != name {
				continue
			}
			if cmds := resourceCommands(root, r); len(cmds) > 0 {
				out = appendCommand(out, cmds[0])
			}
		}
	}
	return out
}

// suggestible reports whether c is worth suggesting: available, and not a
// pure dispatch parent.
func suggestible(c *cobra.Command) bool {
	return c.IsAvailableCommand() && c.Annotations[catalog.AnnotationResource] != catalog.Dispatch
}

func appendCommand(list []*cobra.Command, c *cobra.Command) []*cobra.Command {
	for _, have := range list {
		if have == c {
			return list
		}
	}
	return append(list, c)
}
