package root

import (
	"testing"

	"github.com/spf13/cobra"
)

// help.go is a transcription of cobra's stock help command, and a transcription
// goes stale silently: cobra changes a word or adds a field, `go get -u` picks
// it up, and qctl keeps printing the old thing with nothing to say so. This
// pins the parts that are supposed to be identical against a stock help command
// built from the same cobra in the module cache, so an upgrade that moves any
// of them fails here instead of shipping.
//
// It deliberately does not pin the two documented divergences -- the Find call
// (findTopic) and the completion function's help-command test -- because those
// live inside function bodies, which no test can compare.
func TestHelpCommandMatchesCobrasStockOne(t *testing.T) {
	// Cobra only builds a help command for a root that has children, and its
	// Long text interpolates DisplayName(), so the stand-in needs qctl's Use.
	stand := &cobra.Command{Use: "qctl"}
	stand.AddCommand(&cobra.Command{Use: "anything"})
	stand.InitDefaultHelpCmd()

	var stock *cobra.Command
	for _, c := range stand.Commands() {
		if c.Name() == "help" {
			stock = c
		}
	}
	if stock == nil {
		t.Fatal("cobra's InitDefaultHelpCmd did not add a help command; the transcription's premise is gone")
	}

	ours := newHelpCommand(&cobra.Command{Use: "qctl"})

	for _, f := range []struct {
		field     string
		got, want string
	}{
		{"Use", ours.Use, stock.Use},
		{"Short", ours.Short, stock.Short},
		{"Long", ours.Long, stock.Long},
		// Both empty, and not by coincidence: cobra fills GroupID from
		// c.helpCommandGroupID (command.go InitDefaultHelpCmd), an unexported field
		// with no getter, so a replacement help command cannot read it. Harmless
		// while qctl uses no command groups. If it ever does, this fires -- and the
		// fix is to pass the same id to SetHelpCommandGroupID and to this
		// command's GroupID, since SetHelpCommand means cobra never gets to.
		{"GroupID", ours.GroupID, stock.GroupID},
	} {
		if f.got != f.want {
			t.Errorf("%s = %q, cobra's stock help command has %q", f.field, f.got, f.want)
		}
	}

	if (ours.Run == nil) != (stock.Run == nil) {
		t.Error("Run is set on one help command and not the other")
	}
	if (ours.ValidArgsFunction == nil) != (stock.ValidArgsFunction == nil) {
		t.Error("ValidArgsFunction is set on one help command and not the other")
	}
	// Not cobra's; qctl's. TestEveryCommandIsAnnotated covers the tree that
	// root builds, but newHelpCommand is also called directly here.
	if ours.Annotations == nil {
		t.Error("the help command carries no catalog annotation")
	}
}
