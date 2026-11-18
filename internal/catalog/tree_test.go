package catalog

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
)

// fakeTree builds a miniature qctl: a dispatch parent with one leaf, a
// generic -f dispatcher, and a deeply nested leaf.
func fakeTree() *cobra.Command {
	root := &cobra.Command{Use: "qctl", Annotations: Parent()}

	get := &cobra.Command{Use: "get", Annotations: Parent()}
	get.AddCommand(&cobra.Command{Use: "data <table>", Annotations: For("data")})
	root.AddCommand(get)

	root.AddCommand(&cobra.Command{Use: "apply", Annotations: Dispatcher([]string{"TableRule", "Table"})})

	cache := &cobra.Command{Use: "cache", Annotations: Parent()}
	aliases := &cobra.Command{Use: "aliases", Annotations: Parent()}
	aliases.AddCommand(&cobra.Command{Use: "list", Annotations: For("alias")})
	cache.AddCommand(aliases)
	root.AddCommand(cache)

	return root
}

func TestWalkVisitsEveryNodeRootFirst(t *testing.T) {
	var paths []string
	for _, n := range Walk(fakeTree()) {
		paths = append(paths, n.Path)
	}
	want := []string{
		"qctl",
		"qctl apply",
		"qctl cache", "qctl cache aliases", "qctl cache aliases list",
		"qctl get", "qctl get data",
	}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("Walk paths =\n%v\nwant\n%v", paths, want)
	}
}

func TestWalkReadsAnnotations(t *testing.T) {
	byPath := map[string]Node{}
	for _, n := range Walk(fakeTree()) {
		byPath[n.Path] = n
	}
	if got := byPath["qctl get data"].Resource; got != "data" {
		t.Errorf("get data resource = %q, want %q", got, "data")
	}
	if got := byPath["qctl get"].Resource; got != Dispatch {
		t.Errorf("get resource = %q, want %q", got, Dispatch)
	}
	apply := byPath["qctl apply"]
	if apply.Resource != Generic {
		t.Errorf("apply resource = %q, want %q", apply.Resource, Generic)
	}
	// Dispatcher sorts, so the annotation is a stable function of the map.
	if !reflect.DeepEqual(apply.Kinds, []string{"Table", "TableRule"}) {
		t.Errorf("apply kinds = %v, want [Table TableRule]", apply.Kinds)
	}
}

func TestVerbIsFirstSegmentAfterBinary(t *testing.T) {
	cases := map[string]string{
		"qctl get data":           "get",
		"qctl config use-context": "config",
		"qctl cache aliases list": "cache",
		"qctl version":            "version",
		"qctl":                    "",
	}
	for path, want := range cases {
		if got := Verb(path); got != want {
			t.Errorf("Verb(%q) = %q, want %q", path, got, want)
		}
	}
}
