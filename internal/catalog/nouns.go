package catalog

import (
	"strings"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
)

// uncountable lists the resource nouns whose plural is the noun itself.
var uncountable = map[string]bool{
	"backup-settings":     true,
	"data":                true,
	"history":             true,
	"soft-deleted-tables": true,
	"table-series":        true,
	"tables-overview":     true,
}

// Plural returns the plural of a resource noun: "table" -> "tables",
// "inbox" -> "inboxes", "dataset-copy" -> "dataset-copies". An uncountable
// noun is returned unchanged.
func Plural(noun string) string {
	if uncountable[noun] {
		return noun
	}
	for _, suffix := range []string{"s", "x", "z", "ch", "sh"} {
		if strings.HasSuffix(noun, suffix) {
			return noun + "es"
		}
	}
	if stem, ok := strings.CutSuffix(noun, "y"); ok && !endsInVowel(stem) {
		return stem + "ies"
	}
	return noun + "s"
}

// singular reverses Plural for a noun that ends in "s".
func singular(noun string) string {
	if stem, ok := strings.CutSuffix(noun, "ies"); ok {
		return stem + "y"
	}
	for _, suffix := range []string{"sses", "xes", "zes", "ches", "shes"} {
		if strings.HasSuffix(noun, suffix) {
			return strings.TrimSuffix(noun, "es")
		}
	}
	return strings.TrimSuffix(noun, "s")
}

func endsInVowel(s string) bool {
	return s != "" && strings.ContainsRune("aeiou", rune(s[len(s)-1]))
}

// Nouns are the words that name one resource on the command line, split by
// grammatical number. An uncountable noun appears in both lists.
type Nouns struct {
	Singular []string
	Plural   []string
}

// NounsOf returns a resource's name and page aliases in both numbers. A page
// alias ending in "s" is read as a plural.
func NounsOf(name string, aliases []string) Nouns {
	var n Nouns
	add := func(sing, plur string) {
		n.Singular = appendNew(n.Singular, sing)
		n.Plural = appendNew(n.Plural, plur)
	}
	add(name, Plural(name))
	for _, a := range aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if strings.HasSuffix(a, "s") && !uncountable[a] {
			add(singular(a), a)
		} else {
			add(a, Plural(a))
		}
	}
	return n
}

// All returns every noun, singular forms first.
func (n Nouns) All() []string {
	out := append([]string(nil), n.Singular...)
	for _, p := range n.Plural {
		out = appendNew(out, p)
	}
	return out
}

func appendNew(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// nounIndex maps every noun to the resource it names.
func nounIndex(pages []resources.Page) (map[string]Nouns, map[string]string) {
	byResource := make(map[string]Nouns, len(pages))
	owner := map[string]string{}
	for _, p := range pages {
		n := NounsOf(p.Name, p.Aliases)
		byResource[p.Name] = n
		for _, word := range n.All() {
			if _, taken := owner[word]; !taken {
				owner[word] = p.Name
			}
		}
	}
	return byResource, owner
}

// WireAliases registers every resource noun as a cobra alias on the commands
// named after that resource, below the top level: `qctl get dataset 2` reaches
// `qctl get table 2` and `qctl get datasets` reaches `qctl get tables`.
//
// A command named in the singular takes the resource's singular nouns, and one
// named in the plural takes the plural nouns. A command with no sibling named
// in the other number takes both. A noun that is already a sibling's name or
// alias is skipped. Top-level commands are verbs and groups, never nouns, and
// gain nothing. Calling it twice adds nothing the first call did not.
func WireAliases(root *cobra.Command) error {
	pages, err := resources.Pages()
	if err != nil {
		return err
	}
	wireAliasesWith(root, pages)
	return nil
}

func wireAliasesWith(root *cobra.Command, pages []resources.Page) {
	nouns, owner := nounIndex(pages)
	var visit func(parent *cobra.Command)
	visit = func(parent *cobra.Command) {
		for _, c := range parent.Commands() {
			if r, ok := owner[c.Name()]; ok {
				for _, alias := range nounAliases(parent, c, r, nouns[r], owner) {
					if !nameTaken(parent, alias) {
						c.Aliases = append(c.Aliases, alias)
					}
				}
			}
			visit(c)
		}
	}
	for _, top := range root.Commands() {
		visit(top)
	}
}

// nounAliases is the set of nouns c should answer to, before collisions.
func nounAliases(parent, c *cobra.Command, resource string, n Nouns, owner map[string]string) []string {
	isSingular := contains(n.Singular, c.Name())
	isPlural := contains(n.Plural, c.Name())
	if isSingular != isPlural {
		other := n.Plural
		if isPlural {
			other = n.Singular
		}
		for _, sib := range parent.Commands() {
			if sib != c && owner[sib.Name()] == resource && contains(other, sib.Name()) {
				if isSingular {
					return n.Singular
				}
				return n.Plural
			}
		}
	}
	return n.All()
}

// nameTaken reports whether word already names a child of parent.
func nameTaken(parent *cobra.Command, word string) bool {
	for _, c := range parent.Commands() {
		if c.Name() == word || c.HasAlias(word) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, have := range list {
		if have == s {
			return true
		}
	}
	return false
}

// ResourceNamed resolves a command-line word to the resource it names: its
// name, its plural, a page alias, or an alias's plural. Case and surrounding
// space are ignored.
func ResourceNamed(root *cobra.Command, word string) (Resource, bool) {
	built, err := Build(root)
	if err != nil {
		return Resource{}, false
	}
	return resourceNamedIn(built, word)
}

func resourceNamedIn(built []Resource, word string) (Resource, bool) {
	word = strings.ToLower(strings.TrimSpace(word))
	for _, r := range built {
		if contains(NounsOf(r.Name, r.Aliases).All(), word) {
			return r, true
		}
	}
	return Resource{}, false
}

// ListCommand returns `qctl get <plural>` for the resource cmd is named after,
// or nil when cmd is not named after a resource or no such command exists or
// accepts zero positional arguments. cmd itself is never returned.
func ListCommand(cmd *cobra.Command) *cobra.Command {
	list := getSibling(cmd, func(n Nouns, name string) bool {
		return contains(n.Plural, name)
	})
	if list == nil || (list.Args != nil && list.Args(list, nil) != nil) {
		return nil
	}
	return list
}

// ShowCommand returns the command that shows one item of the resource cmd is
// named after: `qctl get <singular>` when it exists, else `qctl describe
// <singular>`. It returns nil when cmd is not named after a resource or
// neither command exists. cmd itself is never returned.
func ShowCommand(cmd *cobra.Command) *cobra.Command {
	singularOnly := func(n Nouns, name string) bool {
		return contains(n.Singular, name) && !contains(n.Plural, name)
	}
	if c := getSibling(cmd, singularOnly); c != nil {
		return c
	}
	return verbChild(cmd, "describe", func(n Nouns, name string) bool {
		return contains(n.Singular, name)
	})
}

func getSibling(cmd *cobra.Command, match func(Nouns, string) bool) *cobra.Command {
	return verbChild(cmd, "get", match)
}

// verbChild finds the child of the top-level command verb that is named after
// cmd's resource and satisfies match.
func verbChild(cmd *cobra.Command, verb string, match func(Nouns, string) bool) *cobra.Command {
	pages, err := resources.Pages()
	if err != nil {
		return nil
	}
	nouns, owner := nounIndex(pages)
	resource, ok := owner[cmd.Name()]
	if !ok {
		return nil
	}
	for _, top := range cmd.Root().Commands() {
		if top.Name() != verb {
			continue
		}
		for _, c := range top.Commands() {
			if c != cmd && owner[c.Name()] == resource && match(nouns[resource], c.Name()) {
				return c
			}
		}
	}
	return nil
}
