package catalog_test

import (
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
)

// destructiveWords mark a command path that probably removes something, as
// whole words of it: "skill" is not "kill".
var destructiveWords = []string{"delete", "kill", "retire", "discard", "purge", "drop", "remove", "destroy", "wipe", "truncate"}

// notDestructive are the commands whose name says otherwise, each with why.
var notDestructive = map[string]string{
	"config delete-context": "removes a local context, nothing on a server",
}

// TestEveryDestructiveLookingCommandIsClassified fails on a command whose
// path says it removes something but which the protected-context gate does
// not cover. Add it to cmdutil's destructiveCommands, or to notDestructive
// with the reason.
func TestEveryDestructiveLookingCommandIsClassified(t *testing.T) {
	for _, n := range catalog.Walk(realRoot(t)) {
		path := strings.TrimPrefix(n.Path, "qctl ")
		if n.Cmd.HasSubCommands() || !looksDestructive(path) {
			continue
		}
		if cmdutil.IsDestructive(n.Cmd) || exempt(path) {
			continue
		}
		t.Errorf("qctl %s looks destructive but is not gated on a protected context", path)
	}
}

// TestDestructiveCommandsExist fails on a destructiveCommands entry that names
// no command, which would leave its replacement ungated.
func TestDestructiveCommandsExist(t *testing.T) {
	paths := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		paths[strings.TrimPrefix(n.Path, "qctl ")] = true
	}
	for _, d := range cmdutil.DestructiveCommands() {
		if !paths[d] {
			t.Errorf("destructiveCommands lists %q, which is not a command", d)
		}
	}
	for p := range notDestructive {
		if !paths[p] {
			t.Errorf("notDestructive lists %q, which is not a command", p)
		}
	}
}

func looksDestructive(path string) bool {
	for _, word := range strings.FieldsFunc(path, func(r rune) bool { return r == ' ' || r == '-' }) {
		for _, w := range destructiveWords {
			if word == w {
				return true
			}
		}
	}
	return false
}

func exempt(path string) bool {
	for p := range notDestructive {
		if path == p || strings.HasPrefix(path, p+" ") {
			return true
		}
	}
	return false
}
