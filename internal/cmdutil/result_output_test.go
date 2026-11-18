package cmdutil

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type resultWriterFixture struct {
	ID     int    `json:"id"`
	Secret string `json:"secret"`
}

// runResultWriter executes a leaf that writes one prose line to Human and the
// fixture through Write, and returns what reached each stream.
func runResultWriter(t *testing.T, args ...string) (stdout, stderr string) {
	t.Helper()
	leaf := &cobra.Command{
		Use: "thing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := NewResultWriter(cmd)
			fmt.Fprintln(w.Human, "thing/7 created")
			return w.Write(resultWriterFixture{ID: 7, Secret: "s3cr3t"}, "thing/7")
		},
	}
	root := newValidatingRoot(leaf)
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"thing"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out.String(), errOut.String()
}

func TestResultWriterTableKeepsProseOnStdout(t *testing.T) {
	stdout, stderr := runResultWriter(t)
	if stdout != "thing/7 created\n" {
		t.Errorf("stdout = %q, want the prose line alone", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want nothing", stderr)
	}
}

func TestResultWriterJSONIsTheOnlyThingOnStdout(t *testing.T) {
	stdout, stderr := runResultWriter(t, "-o", "json")
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, stdout)
	}
	if got["id"] != float64(7) {
		t.Errorf("id = %v, want 7", got["id"])
	}
	if stderr != "thing/7 created\n" {
		t.Errorf("stderr = %q, want the prose line", stderr)
	}
}

// Write goes through output.Printer, so a secret-named field is masked unless
// the caller opts out.
func TestResultWriterMasksSecrets(t *testing.T) {
	stdout, _ := runResultWriter(t, "-o", "yaml")
	if strings.Contains(stdout, "s3cr3t") || !strings.Contains(stdout, "secret: '***'") {
		t.Errorf("yaml did not mask the secret:\n%s", stdout)
	}
	stdout, _ = runResultWriter(t, "-o", "json", "--allow-plaintext-secrets")
	if !strings.Contains(stdout, "s3cr3t") {
		t.Errorf("--allow-plaintext-secrets did not unmask:\n%s", stdout)
	}
}

func TestResultWriterNamePrintsTheNames(t *testing.T) {
	stdout, stderr := runResultWriter(t, "-o", "name")
	if stdout != "thing/7\n" {
		t.Errorf("stdout = %q, want %q", stdout, "thing/7\n")
	}
	if stderr != "thing/7 created\n" {
		t.Errorf("stderr = %q, want the prose line", stderr)
	}
}

// runWriteRun executes a leaf whose run over total documents returned docs.
func runWriteRun(t *testing.T, total int, docs []any, names []string, args ...string) string {
	t.Helper()
	leaf := &cobra.Command{
		Use: "thing",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return NewResultWriter(cmd).WriteRun(total, docs, names...)
		},
	}
	root := newValidatingRoot(leaf)
	root.PersistentFlags().Bool("allow-plaintext-secrets", false, "")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetArgs(append([]string{"thing"}, args...))
	if err := root.Execute(); err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return out.String()
}

func TestWriteRunShapesTheDocuments(t *testing.T) {
	one := []any{resultWriterFixture{ID: 7}}
	two := []any{resultWriterFixture{ID: 7}, resultWriterFixture{ID: 8}}

	// One document of one: the bare object.
	var single map[string]any
	if err := json.Unmarshal([]byte(runWriteRun(t, 1, one, nil, "-o", "json")), &single); err != nil {
		t.Fatalf("one document: %v", err)
	}
	if single["id"] != float64(7) {
		t.Errorf("one document = %v, want the bare object", single)
	}

	// Several documents, even with only one returned: a List.
	for _, docs := range [][]any{one, two} {
		var list struct {
			Kind  string           `json:"kind"`
			Items []map[string]any `json:"items"`
		}
		if err := json.Unmarshal([]byte(runWriteRun(t, 3, docs, nil, "-o", "json")), &list); err != nil {
			t.Fatalf("three documents: %v", err)
		}
		if list.Kind != "List" || len(list.Items) != len(docs) {
			t.Errorf("three documents = %+v, want a List of %d", list, len(docs))
		}
	}

	// One document that returned nothing: stdout stays empty.
	if got := runWriteRun(t, 1, nil, nil, "-o", "json"); got != "" {
		t.Errorf("no document = %q, want nothing", got)
	}

	if got := runWriteRun(t, 2, two, []string{"thing/7", "thing/8"}, "-o", "name"); got != "thing/7\nthing/8\n" {
		t.Errorf("-o name = %q, want both names", got)
	}
	if got := runWriteRun(t, 1, nil, []string{"thing/7"}, "-o", "name"); got != "thing/7\n" {
		t.Errorf("name without a document = %q, want thing/7", got)
	}
}
