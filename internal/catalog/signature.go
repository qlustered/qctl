package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/qlustered/qctl/internal/resources"
)

// PageNodes selects the command nodes a page is responsible for documenting:
// every command mapped to the resource by name, plus every generic dispatcher
// that reaches it through its manifest kind (a page whose kind `apply -f`
// accepts is managed by apply, so a change to apply's flags can invalidate
// that page's gotchas).
//
// Signature hashes exactly these nodes, and any message that tells a developer
// *which* commands changed must list exactly these nodes — otherwise the
// message names commands that visibly did not change, the developer concludes
// "spurious", and reflexively runs -update. Both callers go through this one
// function so the two selections cannot drift apart.
func PageNodes(root *cobra.Command, page resources.Page) []Node {
	var out []Node
	for _, n := range Walk(root) {
		switch {
		case n.Resource == page.Name:
		case n.Resource == Generic && page.Kind != "" && containsString(n.Kinds, page.Kind):
		default:
			continue
		}
		out = append(out, n)
	}
	return out
}

// Signature fingerprints the behavior-bearing command surface behind a page:
// for every command PageNodes selects, in walk order, its path, its argument
// spec, and its own local flags with their names, shorthands, defaults and
// required-ness.
//
// Shorthand and required-ness are hashed because UsageLine renders both (see
// requiredFlagsSuffix): a page's generated USAGE line is `qctl apply table -f
// <file.yaml>` precisely because -f is a required flag with that shorthand.
// A field the generator renders but the hash cannot see is a page whose USAGE
// moves with no re-read demanded — and, because the -update writer only runs
// for pages whose signature is stale, a page CI can never repair.
//
// Deliberately excluded: Long text, Short text, and flag usage strings. The
// rule resource maps to nine commands; hashing their help prose would fail CI
// on any typo fix and demand a human re-verify unrelated gotchas. The
// predictable response is a reflexive -update, which destroys the mechanism's
// value. Hashing only behavior-bearing surface means a firing signature almost
// always means behavior actually moved.
//
// LocalFlags() excludes inherited PersistentFlags by pointer identity (cobra v1),
// so a new root persistent flag does not re-stamp all 26 pages. A command's own
// local flag shadowing a root persistent flag's name *does* count: it is behavior
// that page documents.
func Signature(root *cobra.Command, page resources.Page) string {
	h := sha256.New()

	for _, n := range PageNodes(root, page) {
		fmt.Fprintf(h, "path:%s\n", n.Path)
		fmt.Fprintf(h, "args:%s\n", strings.TrimSpace(strings.TrimPrefix(n.Cmd.Use, n.Cmd.Name())))

		var flags []string
		n.Cmd.LocalFlags().VisitAll(func(f *pflag.Flag) {
			// Cobra adds --help to a command's local flags lazily, the first
			// time anything renders its help or usage (InitDefaultHelpFlag),
			// and LocalFlags memoizes the result. Whether it is present
			// therefore depends on what else ran in this test binary, so
			// hashing it would make the same page signature differ between
			// `go test -run TestPageSignatures` and a full `make test` —
			// leaving the gate permanently unfixable. --help is also not
			// surface any page documents.
			if f.Name == "help" {
				return
			}
			flags = append(flags, fmt.Sprintf("flag:%s|-%s=%s|required=%t",
				f.Name, f.Shorthand, f.DefValue, isRequiredFlag(f)))
		})
		sort.Strings(flags)
		for _, line := range flags {
			fmt.Fprintln(h, line)
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:7]
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
