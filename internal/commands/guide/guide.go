package guide

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/markdown"
	"github.com/spf13/cobra"
)

// NewCommand creates the `guide` command: a registry of task-oriented tutorials
// for common qctl workflows, shipped in the binary (usable offline).
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guide [topic]",
		Short: "Task-oriented tutorials for common qctl workflows",
		Long: `Task-oriented tutorials ("guides") for common qctl workflows.

Unlike --help, which documents a single command, a guide walks through a whole
task that may span several commands (for example, finding a rule revision and
then applying a manifest to instantiate it).

Run "qctl guide" with no arguments to list available guides, or
"qctl guide <topic>" to read one. --search finds the guides and sections that
mention a word; --section prints one section of a guide.`,
		Example: `  # List available guides
  qctl guide

  # Read a specific guide
  qctl guide instantiate-rule

  # Which guides mention a word, and in which sections
  qctl guide --search quarantine

  # Read one section of a guide
  qctl guide triage-alerts --section "Act on a warning"`,
		Args:        cobra.MaximumNArgs(1),
		Annotations: catalog.For("cli"),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			query, _ := cmd.Flags().GetString("search")
			heading, _ := cmd.Flags().GetString("section")
			switch {
			case query != "" && (len(args) > 0 || heading != ""):
				return qerrors.NewBadUsage("--search looks across every guide; drop the topic and --section")
			case query != "":
				return printSearch(out, query)
			case len(args) == 0 && heading != "":
				return qerrors.NewBadUsage("--section needs a topic: qctl guide <topic> --section <heading>")
			case len(args) == 0:
				return printIndex(out)
			}
			return printTopic(cmd, out, args[0], heading)
		},
	}
	cmd.Flags().String("search", "", "List the guides and sections that mention every word of this text")
	cmd.Flags().String("section", "", "Print only the sections whose heading contains this text")
	return cmd
}

func printIndex(out io.Writer) error {
	fmt.Fprintln(out, "Available guides:")
	fmt.Fprintln(out)
	for _, t := range topics {
		fmt.Fprintf(out, "  %-22s %s\n", t.Name, t.Summary)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Run "qctl guide <topic>" to read one.`)
	return nil
}

func printTopic(cmd *cobra.Command, out io.Writer, name, heading string) error {
	t, ok := lookup(name)
	if !ok {
		// Nouns live under `explain`, tasks under `guide`. A reader who
		// guessed the wrong surface gets redirected, not rejected.
		if r, found := catalog.Lookup(cmd.Root(), name); found {
			fmt.Fprintf(out, "%q is a resource, not a workflow guide.\n\n", r.Name)
			fmt.Fprintf(out, "  qctl explain %s\n", r.Name)
			if r.Guide != "" {
				fmt.Fprintf(out, "\nIts workflow guide is:\n\n  qctl guide %s\n", r.Guide)
			}
			return nil
		}
		return unknownTopicError(name)
	}

	body, err := t.body()
	if err != nil {
		return err
	}
	if heading != "" {
		found := findSections(body, heading)
		if len(found) == 0 {
			return unknownSectionError(t, body, heading)
		}
		var b strings.Builder
		for i, s := range found {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(s.Body)
		}
		body = b.String()
	}

	// Render prettily on a TTY; emit raw markdown otherwise (ideal for pipes
	// and coding agents).
	renderer, rerr := markdown.New(markdown.Options{Writer: out})
	if rerr == nil && renderer.IsTTY() {
		if rendered, err := renderer.Render(body); err == nil {
			fmt.Fprint(out, rendered)
			return nil
		}
	}
	fmt.Fprint(out, body)
	return nil
}

// unknownTopicError names the topics closest to name, with their summaries,
// or every topic when none is close.
func unknownTopicError(name string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown guide %q", name)
	if near := suggestTopics(name); len(near) > 0 {
		b.WriteString("\n\nClosest guides:\n")
		for _, t := range near {
			fmt.Fprintf(&b, "  %-22s %s\n", t.Name, t.Summary)
		}
	} else {
		fmt.Fprintf(&b, "\n\nAvailable guides: %s\n", strings.Join(topicNames(), ", "))
	}
	fmt.Fprintf(&b, "\nSearch every guide: qctl guide --search %s", searchWord(name))
	return qerrors.NewBadUsage(b.String())
}

// searchWord is the longest word of name, the likeliest to find something.
func searchWord(name string) string {
	best := ""
	for _, w := range words(name) {
		if len(w) > len(best) {
			best = w
		}
	}
	if best == "" {
		return "<word>"
	}
	return best
}

// unknownSectionError lists the headings of t a --section can name.
func unknownSectionError(t topic, body, heading string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "guide %s has no section matching %q\n\nIts sections:\n", t.Name, heading)
	for _, s := range sections(body) {
		if s.Level > 1 {
			fmt.Fprintf(&b, "  %s%s\n", strings.Repeat("  ", s.Level-2), s.Heading)
		}
	}
	return qerrors.NewBadUsage(strings.TrimRight(b.String(), "\n"))
}

func printSearch(out io.Writer, query string) error {
	hits, err := search(query)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		fmt.Fprintf(out, "No guide mentions %q. Run \"qctl guide\" for the index.\n", query)
		return nil
	}
	fmt.Fprintf(out, "Guides mentioning %q:\n\n", query)
	for _, h := range hits {
		fmt.Fprintf(out, "  %-22s %s\n", h.Topic.Name, h.Topic.Summary)
		for _, s := range h.Sections {
			fmt.Fprintf(out, "      § %s\n", s)
		}
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, `Read one section: qctl guide <topic> --section "<heading>"`)
	return nil
}
