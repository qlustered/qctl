// Package api_resources implements `qctl api-resources`: a kubectl-style
// cold-start index of every resource, its aliases, every verb that acts on it,
// its apply manifest kind, and its guide topic. It reads the catalog only —
// the annotated cobra tree joined with the resource pages compiled into the
// binary — so it needs no auth, makes no API call, and works offline.
//
//nolint:revive // Package name intentionally matches the api-resources command directory.
package api_resources

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"github.com/spf13/cobra"
)

// emptyCell is what an empty column renders as in table output.
const emptyCell = "—"

// Row is the machine-readable shape emitted by -o json / -o yaml.
//
// Contract notes for consumers:
//   - verb  is the primary verb (the class used for display ordering) and is
//     retained from the pre-catalog shape.
//   - verbs is every verb that acts on the resource, sorted and deduplicated,
//     including verbs conferred by `apply -f` / `diff -f` through the
//     manifest kind.
//   - explain is populated for every resource, because every resource has a
//     page. Use schema == "" — not explain == "" — to ask "is there a schema".
type Row struct {
	Name    string   `json:"name" yaml:"name"`
	Aliases []string `json:"aliases" yaml:"aliases"`
	Verb    string   `json:"verb" yaml:"verb"`
	Verbs   []string `json:"verbs" yaml:"verbs"`
	Kind    string   `json:"kind" yaml:"kind"`
	Explain string   `json:"explain" yaml:"explain"`
	Schema  string   `json:"schema" yaml:"schema"`
	Guide   string   `json:"guide" yaml:"guide"`
	Summary string   `json:"summary" yaml:"summary"`
}

func rows(root *cobra.Command) ([]Row, error) {
	built, err := catalog.Build(root)
	if err != nil {
		return nil, err
	}
	out := make([]Row, 0, len(built))
	for _, r := range built {
		aliases := r.Aliases
		if aliases == nil {
			aliases = []string{} // marshal as [] not null
		}
		verbs := r.Verbs
		if verbs == nil {
			verbs = []string{}
		}
		out = append(out, Row{
			Name:    r.Name,
			Aliases: aliases,
			Verb:    r.PrimaryVerb,
			Verbs:   verbs,
			Kind:    r.Kind,
			Explain: "qctl explain " + r.Name,
			Schema:  r.Schema,
			Guide:   r.Guide,
			Summary: r.Summary,
		})
	}
	return out, nil
}

// NewCommand creates the api-resources command.
func NewCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "api-resources",
		Short: "List all resources and the verbs that act on each",
		Long: `List every qctl resource: its aliases, every verb that acts on it, its
apply manifest kind (if any), and its guide topic. After a verb, a resource's
aliases work in place of its name, in either number: qctl get datasets runs
qctl get tables.

This is the cold-start index — one call, no priors. It is computed from the
command tree and the resource pages shipped in the binary, so it needs no
authentication, works offline, and cannot omit a command that exists.

Examples:
  # Human-readable table
  qctl api-resources

  # Machine-readable (one shot for agents)
  qctl api-resources -o json`,
		// render handles table, json and yaml only.
		Annotations: cmdutil.WithFormats(catalog.For("cli"), "table", "json", "yaml"),
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			format, _ := cmd.Flags().GetString("output")
			return render(cmd.OutOrStdout(), cmd.Root(), format)
		},
	}
}

func render(w io.Writer, root *cobra.Command, format string) error {
	rs, err := rows(root)
	if err != nil {
		return err
	}
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(rs)
	case "yaml":
		enc := jsonnum.NewYAMLEncoder(w)
		enc.SetIndent(2)
		defer enc.Close()
		return enc.Encode(rs)
	default:
		return renderTable(w, rs)
	}
}

func renderTable(w io.Writer, rs []Row) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "NAME\tALIASES\tVERBS\tKIND\tGUIDE")
	for _, r := range rs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.Name,
			cell(strings.Join(r.Aliases, ",")),
			cell(strings.Join(r.Verbs, ",")),
			cell(r.Kind),
			cell(r.Guide),
		)
	}
	return nil
}

func cell(s string) string {
	if s == "" {
		return emptyCell
	}
	return s
}
