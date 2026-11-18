// Package catalog_test here guards a specific ResolveCommandLine blind spot:
// a flag a command registers on itself via PersistentFlags(), rather than
// one it inherits from a parent. It needs the real tree (not a synthetic
// fixture) because the bug is in cobra's own lazy flag-merge behavior on the
// real `apply` command, not in anything the test could fabricate.
package catalog_test

import (
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
)

// TestResolveCommandLineFindsSelfRegisteredPersistentFlag: `apply` registers
// --yes/-y via cmd.PersistentFlags() on itself (internal/commands/apply/
// apply.go), so its subcommands inherit it — but "qctl apply -f x.yaml --yes"
// resolves to `apply` itself, not a child. On the first such check in a
// process, cmd.Flags() has not yet had cobra's lazy persistent-flag merge run
// for this command, and cmd.InheritedFlags() deliberately excludes a
// command's own local flags (it only reports ancestors'), so a naive
// Flags()-then-InheritedFlags() check finds neither and rejects a flag that
// is genuinely registered. Both the long and short forms must resolve.
func TestResolveCommandLineFindsSelfRegisteredPersistentFlag(t *testing.T) {
	root := realRoot(t)
	if _, err := catalog.ResolveCommandLine(root, "qctl apply -f x.yaml --yes"); err != nil {
		t.Errorf("apply's own --yes flag rejected: %v", err)
	}
	if _, err := catalog.ResolveCommandLine(root, "qctl apply -f x.yaml -y"); err != nil {
		t.Errorf("apply's own -y shorthand rejected: %v", err)
	}
}
