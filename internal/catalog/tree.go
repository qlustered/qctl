// Package catalog computes the qctl capability catalog by walking the cobra
// command tree. The tree — not a hand-written table — is the single source of
// truth for what exists: every command node carries a "resource" annotation,
// and the catalog joins those annotations with the per-resource pages under
// internal/resources.
//
// catalog must not import any internal/commands/... package. Command packages
// import it for the annotation helpers below, and a back-edge would create an
// import cycle. Commands that need the tree at runtime take it from
// cmd.Root(); tests that need the real tree live in package catalog_test.
package catalog

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const (
	// AnnotationResource is the cobra annotation key every command carries.
	AnnotationResource = "resource"
	// AnnotationKinds lists the manifest kinds a Generic dispatcher accepts,
	// comma-separated and sorted.
	AnnotationKinds = "kinds"

	// Dispatch marks a pure dispatch parent (get, describe, auth, ...): it
	// owns no resource, its children carry the truth.
	Dispatch = "-"
	// Generic marks a manifest dispatcher (apply -f, diff -f): it acts on
	// every resource whose page declares one of its kinds.
	Generic = "*"
)

// For annotates a command that acts on exactly one resource.
func For(resource string) map[string]string {
	return map[string]string{AnnotationResource: resource}
}

// Parent annotates a pure dispatch parent.
func Parent() map[string]string {
	return map[string]string{AnnotationResource: Dispatch}
}

// Dispatcher annotates a generic -f dispatcher. Kinds are sorted so the
// annotation is a stable function of the dispatch table it was built from.
func Dispatcher(kinds []string) map[string]string {
	sorted := append([]string(nil), kinds...)
	sort.Strings(sorted)
	return map[string]string{
		AnnotationResource: Generic,
		AnnotationKinds:    strings.Join(sorted, ","),
	}
}

// Node is one command in the walk.
type Node struct {
	Cmd      *cobra.Command
	Path     string   // full command path, e.g. "qctl get data"
	Resource string   // annotation value: a resource name, Dispatch, or Generic
	Kinds    []string // manifest kinds; non-empty only for Generic nodes
}

// Walk returns every node in the tree: root first, then each subtree in
// cobra's own (alphabetical) child order.
func Walk(root *cobra.Command) []Node {
	var out []Node
	var visit func(*cobra.Command)
	visit = func(c *cobra.Command) {
		n := Node{Cmd: c, Path: c.CommandPath(), Resource: c.Annotations[AnnotationResource]}
		if raw := c.Annotations[AnnotationKinds]; raw != "" {
			n.Kinds = strings.Split(raw, ",")
		}
		out = append(out, n)
		for _, sub := range c.Commands() {
			visit(sub)
		}
	}
	visit(root)
	return out
}

// Verb is the first path segment after the binary name — the verb a command
// contributes to its resource, regardless of nesting depth. "qctl cache
// aliases list" contributes "cache"; "qctl version" contributes "version".
// The root itself contributes nothing.
func Verb(path string) string {
	fields := strings.Fields(path)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}
