// Package resources loads the per-resource documentation pages embedded in the
// binary: one markdown file per resource, frontmatter plus prose, under
// pages/. It is the "semantics" half of the capability catalog; the
// "existence" half is the annotated cobra tree, joined by internal/catalog.
//
// This package must not import other internal/ packages: catalog and explain
// both import it, and a back-edge would create an import cycle.
package resources

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed pages/*.md
var pagesFS embed.FS

// Schema namespaces. A page's schema field names the map its value lives in,
// because data-review-policy and rule-review-policy exist in both the OpenAPI
// mapping and the static manifest map and render different content.
const (
	SchemaNamespaceOpenAPI  = "openapi"
	SchemaNamespaceManifest = "manifest"
)

// Example is one runnable command line from a page's EXAMPLES block.
type Example struct {
	Description string // the "# ..." line above the command, "" if none
	Command     string // e.g. "qctl get data orders --limit 5"
}

// Gotcha is one bullet from a page's GOTCHAS section.
//
// Backend records the trailing "[backend]" marker, which the markdown carries
// inline because that is the ergonomic place to write it and it keeps the
// bullet one physical line. It is an internal maintenance tag for
// tools/gotchassweep and is stripped here so it never reaches a user.
type Gotcha struct {
	Text    string
	Backend bool
}

// CreateOnlyFields maps create-only field names to their accepted create values.
// An empty value list records no value restriction.
type CreateOnlyFields map[string][]string

// UnmarshalYAML accepts a list of field names or a map of names to values.
func (c *CreateOnlyFields) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var names []string
		if err := node.Decode(&names); err != nil {
			return err
		}
		*c = make(CreateOnlyFields, len(names))
		for _, name := range names {
			(*c)[name] = nil
		}
		return nil
	}
	return node.Decode((*map[string][]string)(c))
}

// Page is one resource's documentation: frontmatter metadata plus four body
// sections. It is the only place a resource's semantics are written down.
type Page struct {
	Name        string // file base name; the canonical resource name
	Summary     string
	Aliases     []string
	Kind        string           // apply manifest kind; "" if not apply-managed
	Schema      string           // namespaced, e.g. "openapi:DataSetSchemaFull"; "" if none
	WriteSchema string           // "openapi:<component>" that writes take; "" if none
	CreateOnly  CreateOnlyFields // top-level schema fields apply honors only when it creates the resource
	Guide       string           // guide topic slug; "" if no multi-command task exists
	Signature   string           // see internal/catalog/signature.go
	Usage       []string
	Examples    []Example
	Gotchas     []Gotcha
	SeeAlso     []string
	Raw         string // the file verbatim, for re-stamping
}

type frontmatter struct {
	Summary     string           `yaml:"summary"`
	Aliases     []string         `yaml:"aliases"`
	Kind        string           `yaml:"kind"`
	Schema      string           `yaml:"schema"`
	WriteSchema string           `yaml:"writeSchema"`
	CreateOnly  CreateOnlyFields `yaml:"createOnly"`
	Guide       string           `yaml:"guide"`
	Signature   string           `yaml:"signature"`
}

// SchemaRef splits a namespaced schema value. ok is false for an empty value
// and for a bare (un-namespaced) name.
func (p Page) SchemaRef() (namespace, name string, ok bool) {
	ns, ref, found := strings.Cut(p.Schema, ":")
	if !found || ref == "" {
		return "", "", false
	}
	if ns != SchemaNamespaceOpenAPI && ns != SchemaNamespaceManifest {
		return "", "", false
	}
	return ns, ref, true
}

// WriteSchemaRef returns the component a page's writeSchema names. ok is false
// for an empty value and for any namespace but openapi.
func (p Page) WriteSchemaRef() (name string, ok bool) {
	ns, ref, found := strings.Cut(p.WriteSchema, ":")
	if !found || ref == "" || ns != SchemaNamespaceOpenAPI {
		return "", false
	}
	return ref, true
}

// ParsePage parses one page. name is the canonical resource name (the file's
// base name), not read from the content.
func ParsePage(name string, data []byte) (Page, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Page{}, fmt.Errorf("page %q: must start with a --- frontmatter block", name)
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Page{}, fmt.Errorf("page %q: frontmatter is not terminated by ---", name)
	}
	var fm frontmatter
	dec := yaml.NewDecoder(strings.NewReader(rest[:end+1]))
	dec.KnownFields(true) // catch a typo'd key (e.g. "alias:") instead of silently dropping it
	if err := dec.Decode(&fm); err != nil {
		return Page{}, fmt.Errorf("page %q: frontmatter: %w", name, err)
	}

	p := Page{
		Name:        name,
		Summary:     strings.TrimSpace(fm.Summary),
		Aliases:     fm.Aliases,
		Kind:        strings.TrimSpace(fm.Kind),
		Schema:      strings.TrimSpace(fm.Schema),
		WriteSchema: strings.TrimSpace(fm.WriteSchema),
		CreateOnly:  fm.CreateOnly,
		Guide:       strings.TrimSpace(fm.Guide),
		Signature:   strings.TrimSpace(fm.Signature),
		Raw:         text,
	}
	sections, err := splitSections(name, rest[end+len("\n---\n"):])
	if err != nil {
		return Page{}, err
	}
	p.Usage = commandLines(sections["USAGE"])
	p.Examples = parseExamples(sections["EXAMPLES"])
	p.Gotchas = gotchaBullets(sections["GOTCHAS"])
	p.SeeAlso = bullets(sections["SEE ALSO"])
	return p, nil
}

// canonicalSections is the fixed set of "## " headings a page body may use.
// Keep in sync with the sections ParsePage reads out of the map below.
var canonicalSections = []string{"USAGE", "EXAMPLES", "GOTCHAS", "SEE ALSO"}

// splitSections indexes the body by "## HEADING". name is the page name, used
// only to identify the offending page in the returned error. It returns an
// error for any heading outside canonicalSections, so a typo (e.g.
// "## SEE-ALSO") fails parsing instead of silently discarding the section.
func splitSections(name, body string) (map[string][]string, error) {
	out := map[string][]string{}
	current := ""
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "## ") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			known := false
			for _, s := range canonicalSections {
				if s == current {
					known = true
					break
				}
			}
			if !known {
				return nil, fmt.Errorf("page %q: unknown section heading %q (want one of %s)",
					name, current, strings.Join(canonicalSections, ", "))
			}
			out[current] = nil
			continue
		}
		if current != "" {
			out[current] = append(out[current], line)
		}
	}
	return out, nil
}

// commandLines keeps the non-blank, non-comment lines of an indented block.
func commandLines(lines []string) []string {
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out
}

// parseExamples pairs each command line with the "# ..." line above it.
func parseExamples(lines []string) []Example {
	var out []Example
	desc := ""
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case t == "":
			continue
		case strings.HasPrefix(t, "#"):
			desc = strings.TrimSpace(strings.TrimPrefix(t, "#"))
		default:
			out = append(out, Example{Description: desc, Command: t})
			desc = ""
		}
	}
	return out
}

// bullets keeps "- " list items verbatim (minus the marker).
func bullets(lines []string) []string {
	var out []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "- ") {
			out = append(out, strings.TrimSpace(strings.TrimPrefix(t, "- ")))
		}
	}
	return out
}

// backendMarker tags a gotcha whose truth depends on server behavior, which no
// qctl-side signal can invalidate. See tools/gotchassweep.
const backendMarker = "[backend]"

// gotchaBullets parses the GOTCHAS section, lifting the trailing [backend]
// marker out of each bullet's prose.
func gotchaBullets(lines []string) []Gotcha {
	var out []Gotcha
	for _, text := range bullets(lines) {
		g := Gotcha{Text: text}
		if strings.HasSuffix(text, backendMarker) {
			g.Backend = true
			g.Text = strings.TrimSpace(strings.TrimSuffix(text, backendMarker))
		}
		out = append(out, g)
	}
	return out
}

// Pages returns every embedded page, sorted by name.
func Pages() ([]Page, error) {
	entries, err := fs.ReadDir(pagesFS, "pages")
	if err != nil {
		return nil, err
	}
	var out []Page
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := pagesFS.ReadFile(path.Join("pages", e.Name()))
		if err != nil {
			return nil, err
		}
		p, err := ParsePage(strings.TrimSuffix(e.Name(), ".md"), data)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LookupPage resolves a canonical name or alias, case-insensitively.
func LookupPage(name string) (Page, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	pages, err := Pages()
	if err != nil {
		return Page{}, false
	}
	for _, p := range pages {
		if p.Name == name {
			return p, true
		}
		for _, a := range p.Aliases {
			if strings.ToLower(a) == name {
				return p, true
			}
		}
	}
	return Page{}, false
}
