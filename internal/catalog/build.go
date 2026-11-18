package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/resources"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Class is how a resource comes into existence. It decides display order and
// the primary verb reported to machine consumers.
type Class int

const (
	ClassApply    Class = iota // the page declares an apply manifest kind
	ClassCreate                // the tree has a `create` verb for it
	ClassReadOnly              // everything else
	verbApply     = "apply"
	verbCreate    = "create"
)

func (c Class) String() string {
	switch c {
	case ClassApply:
		return verbApply
	case ClassCreate:
		return verbCreate
	default:
		return "read-only"
	}
}

// Resource is one catalog row: a page plus everything the tree knows.
type Resource struct {
	resources.Page
	Verbs       []string // sorted, deduplicated: tree verbs ∪ dispatch verbs
	Class       Class
	PrimaryVerb string   // the token machine consumers switch on
	Commands    []string // command paths mapped here, in walk order
}

// Build computes the catalog from the annotated tree and the embedded pages.
func Build(root *cobra.Command) ([]Resource, error) {
	pages, err := resources.Pages()
	if err != nil {
		return nil, err
	}
	return BuildWith(root, pages), nil
}

// BuildWith is Build over an explicit page set, so tests can join a synthetic
// tree with fixture pages instead of the 26 embedded ones.
//
// Resources present in the tree but missing a page are still returned, with
// usage derived from the tree, so a missing page never blocks a user; the
// drift tests fail instead.
func BuildWith(root *cobra.Command, pages []resources.Page) []Resource {
	byName := map[string]*Resource{}
	byKind := map[string]string{}
	// synthesize records the resources whose USAGE must come from the tree
	// because no page supplied one.
	synthesize := map[string]bool{}
	var order []string
	for _, p := range pages {
		byName[p.Name] = &Resource{Page: p}
		order = append(order, p.Name)
		synthesize[p.Name] = len(p.Usage) == 0
		if p.Kind != "" {
			byKind[p.Kind] = p.Name
		}
	}

	verbs := map[string]map[string]bool{}
	addVerb := func(name, verb string) {
		if verb == "" {
			return
		}
		if verbs[name] == nil {
			verbs[name] = map[string]bool{}
		}
		verbs[name][verb] = true
	}
	ensure := func(name string) *Resource {
		r, ok := byName[name]
		if !ok {
			r = &Resource{Page: resources.Page{Name: name}}
			byName[name] = r
			order = append(order, name)
			synthesize[name] = true
		}
		return r
	}

	for _, n := range Walk(root) {
		switch n.Resource {
		case "", Dispatch:
			continue
		case Generic:
			// A dispatcher confers its verb on every resource whose page
			// declares one of its kinds. This is the only route in for the
			// resources with no apply subcommand at all.
			for _, kind := range n.Kinds {
				if name, ok := byKind[kind]; ok {
					addVerb(name, Verb(n.Path))
				}
			}
		default:
			r := ensure(n.Resource)
			addVerb(n.Resource, Verb(n.Path))
			r.Commands = append(r.Commands, n.Path)
			if synthesize[n.Resource] {
				r.Usage = append(r.Usage, UsageLine(n.Cmd))
			}
		}
	}

	out := make([]Resource, 0, len(order))
	for _, name := range order {
		r := byName[name]
		for v := range verbs[name] {
			r.Verbs = append(r.Verbs, v)
		}
		sort.Strings(r.Verbs)
		r.Class = classify(*r)
		r.PrimaryVerb = primaryVerb(r.Class, r.Verbs)
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Class != out[j].Class {
			return out[i].Class < out[j].Class
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// classify keys the apply class off the page's kind, not off tree verbs: the
// four -f-only resources have no apply subcommand and would otherwise be
// demoted to read-only.
func classify(r Resource) Class {
	if r.Kind != "" {
		return ClassApply
	}
	for _, v := range r.Verbs {
		if v == verbCreate {
			return ClassCreate
		}
	}
	return ClassReadOnly
}

// primaryVerb keeps the pre-catalog `verb` field honest. Read-only resources
// report "get" when they have it (every resource that had a row before this
// design does), and their first verb otherwise — never a fictional "get".
func primaryVerb(c Class, verbs []string) string {
	switch c {
	case ClassApply:
		return verbApply
	case ClassCreate:
		return verbCreate
	}
	for _, v := range verbs {
		if v == "get" {
			return "get"
		}
	}
	if len(verbs) > 0 {
		return verbs[0]
	}
	return ""
}

// Lookup resolves a canonical name or alias against the built catalog.
func Lookup(root *cobra.Command, name string) (Resource, bool) {
	all, err := Build(root)
	if err != nil {
		return Resource{}, false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, r := range all {
		if r.Name == name {
			return r, true
		}
		for _, a := range r.Aliases {
			if strings.ToLower(a) == name {
				return r, true
			}
		}
	}
	return Resource{}, false
}

// UsageLine renders a command as a copy-pasteable line: its full path, the
// positional spec from Use (without cobra's "[flags]" suffix), and finally
// its own required flags — a required flag is not optional, so a line that
// dropped it would not be runnable. BuildWith and GenerateUsageWith both funnel
// through this one function, so the two can never disagree on rendering.
func UsageLine(c *cobra.Command) string {
	args := strings.TrimSpace(strings.TrimPrefix(c.Use, c.Name()))
	line := c.CommandPath()
	if args != "" {
		line += " " + args
	}
	return line + requiredFlagsSuffix(c)
}

// requiredFlagPlaceholders overrides the generic <flag-name> value
// placeholder for flags whose spelling must match another hardcoded string.
// "filename" must render as <file.yaml> because GenerateUsageWith's generic
// dispatcher line (internal/catalog/generate.go: n.Path+" -f <file.yaml>
// # kind: "+kind) already hardcodes that spelling, and a page must never
// show two different placeholders for the same flag.
var requiredFlagPlaceholders = map[string]string{
	"filename": "<file.yaml>",
}

// isRequiredFlag reports whether cobra's MarkFlagRequired was called on f,
// which it records as the BashCompOneRequiredFlag annotation.
//
// One helper, two callers on purpose: requiredFlagsSuffix renders required
// flags into a page's USAGE and Signature hashes them, and a difference the
// renderer sees but the hash does not is a page whose USAGE drifts without any
// human being asked to re-read its GOTCHAS.
func isRequiredFlag(f *pflag.Flag) bool {
	required := f.Annotations[cobra.BashCompOneRequiredFlag]
	return len(required) > 0 && required[0] == "true"
}

// requiredFlagsSuffix renders a command's own required flags — never flags
// inherited from a parent — in deterministic (lexical, via VisitAll) order,
// as a string to append to a usage line. LocalFlags (unlike Flags) is
// guaranteed to exclude a parent's persistent flags even after cobra has
// merged them for parsing, which is what keeps inherited flags out here.
func requiredFlagsSuffix(c *cobra.Command) string {
	var parts []string
	c.LocalFlags().VisitAll(func(f *pflag.Flag) {
		if !isRequiredFlag(f) {
			return
		}
		placeholder, ok := requiredFlagPlaceholders[f.Name]
		if !ok {
			placeholder = "<" + f.Name + ">"
		}
		if f.Shorthand != "" {
			parts = append(parts, "-"+f.Shorthand+" "+placeholder)
		} else {
			parts = append(parts, "--"+f.Name+" "+placeholder)
		}
	})
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

// ResolveCommandLine resolves an example or guide command line against the
// tree. The command path must exist and resolve to a runnable command, and
// every flag must be registered on it. Positional arguments are not checked —
// they are placeholders or sample values by design.
//
// Accepted form: "qctl <subcommand> [<subcommand> ...] [flags and args]" —
// flags must come after the full subcommand path. Known limitation: a legal
// cobra line with flags interleaved before the subcommand path is complete
// (e.g. "qctl -o json get tables") stops walking at the first "-" token, so
// cmd never advances past root and the call fails with `"-o" is not a qctl
// command` even though the line is valid input to cobra. No page or guide
// uses that form today; a caller validating free-form command lines (e.g. a
// guide body) rather than the page EXAMPLES convention above must account for
// this before relying on a resolve failure to mean "invalid command".
func ResolveCommandLine(root *cobra.Command, line string) (*cobra.Command, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || fields[0] != root.Name() {
		return nil, fmt.Errorf("%q: must start with %q", line, root.Name())
	}
	if len(fields) < 2 {
		return nil, fmt.Errorf("%q: names no command", line)
	}

	cmd := root
	rest := fields[1:]
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		child := childNamed(cmd, rest[0])
		if child == nil {
			break
		}
		cmd, rest = child, rest[1:]
	}
	if cmd == root {
		return nil, fmt.Errorf("%q: %q is not a qctl command", line, fields[1])
	}
	// Runnable() alone cannot say this: a parent routed through
	// cmdutil.SubcommandRequired has a RunE, so without the annotation check a
	// subcommand that does not exist ("qctl review frobnicate 1") stopped the
	// walk at its parent, and the unknown name passed as a positional.
	if !cmd.Runnable() || cmd.Annotations[AnnotationResource] == Dispatch {
		if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
			return nil, fmt.Errorf("%q: %q is not a subcommand of %q", line, rest[0], cmd.CommandPath())
		}
		return nil, fmt.Errorf("%q: %q is a dispatch parent, not a runnable command", line, cmd.CommandPath())
	}
	if err := checkFlags(cmd, line, fields[1:]); err != nil {
		return nil, err
	}
	return cmd, nil
}

func childNamed(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
		for _, a := range c.Aliases {
			if a == name {
				return c
			}
		}
	}
	return nil
}

func checkFlags(cmd *cobra.Command, line string, tokens []string) error {
	for _, tok := range tokens {
		name, long, ok := flagToken(tok)
		if !ok {
			continue
		}
		if long {
			// cmd.PersistentFlags() is checked directly, not just cmd.Flags()
			// and cmd.InheritedFlags(): cobra only merges a command's own
			// persistent flags into Flags() lazily (as a side effect of a
			// prior InheritedFlags()/LocalFlags() call), and InheritedFlags()
			// itself only reports ancestors' flags, never the command's own.
			// Without this, a flag a command registers on itself via
			// PersistentFlags() (e.g. apply's --yes) is invisible on the
			// first check in a process and visible on the second, depending
			// on merge side effects instead of the flag actually existing.
			if !hasLongFlag(cmd, name) {
				return fmt.Errorf("%q: %s has no --%s flag", line, cmd.CommandPath(), name)
			}
			continue
		}
		if err := checkShortFlags(cmd, line, name); err != nil {
			return err
		}
	}
	return nil
}

func flagToken(tok string) (name string, long bool, ok bool) {
	if !strings.HasPrefix(tok, "-") || tok == "-" || tok == "--" {
		return "", false, false
	}
	trimmed := strings.TrimLeft(tok, "-")
	if trimmed == "" || (trimmed[0] >= '0' && trimmed[0] <= '9') {
		return "", false, false // a negative number argument, not a flag
	}
	name, _, _ = strings.Cut(trimmed, "=")
	return name, strings.HasPrefix(tok, "--"), true
}

func hasLongFlag(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Lookup(name) != nil ||
		cmd.PersistentFlags().Lookup(name) != nil ||
		cmd.InheritedFlags().Lookup(name) != nil
}

// A single dash may carry a cluster of shorthands (-vvv for the count flag),
// and pflag's ShorthandLookup panics on anything longer than one character, so
// check them one at a time. A shorthand's value must be a separate token:
// write "-o json", never "-ojson".
func checkShortFlags(cmd *cobra.Command, line, name string) error {
	for _, c := range name {
		short := string(c)
		if !hasShortFlag(cmd, short) {
			return fmt.Errorf("%q: %s has no -%s flag", line, cmd.CommandPath(), short)
		}
	}
	return nil
}

func hasShortFlag(cmd *cobra.Command, short string) bool {
	return cmd.Flags().ShorthandLookup(short) != nil ||
		cmd.PersistentFlags().ShorthandLookup(short) != nil ||
		cmd.InheritedFlags().ShorthandLookup(short) != nil
}
