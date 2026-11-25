package catalog_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/commands/apply"
	"github.com/qlustered/qctl/internal/commands/explain"
	"github.com/qlustered/qctl/internal/commands/guide"
	"github.com/qlustered/qctl/internal/resources"
	"github.com/qlustered/qctl/internal/schema"
	"github.com/spf13/cobra"
)

var create = flag.Bool("create", false, "write skeleton pages for resources that have none")

// pagesDir is where the embedded pages live on disk, relative to this package.
const pagesDir = "../resources/pages"

// treeResources is every named resource the command tree references.
func treeResources(root *cobra.Command) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range catalog.Walk(root) {
		switch n.Resource {
		case "", catalog.Dispatch, catalog.Generic:
			continue
		}
		if !seen[n.Resource] {
			seen[n.Resource] = true
			out = append(out, n.Resource)
		}
	}
	sort.Strings(out)
	return out
}

// TestEveryResourceHasPage is the rule that makes a new command impossible to
// add without documenting it. Run with -create to write skeletons for what is
// missing, then fill in the frontmatter by hand.
func TestEveryResourceHasPage(t *testing.T) {
	r := realRoot(t)
	have := map[string]bool{}
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		have[p.Name] = true
	}

	for _, name := range treeResources(r) {
		if have[name] {
			continue
		}
		if *create {
			writeSkeleton(t, r, name)
			continue
		}
		t.Errorf("resource %q is in the command tree with no page.\n"+
			"Create %s/%s.md, or generate a skeleton with:\n"+
			"    go test ./internal/catalog -run TestEveryResourceHasPage -create",
			name, pagesDir, name)
	}
	if *create {
		t.Log("skeletons written; re-run the tests so the embedded pages are rebuilt")
	}
}

func writeSkeleton(t *testing.T, root *cobra.Command, name string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("---\nsummary:\naliases:\nkind:\nschema:\nguide:\nsignature:\n---\n\n## USAGE\n\n")
	for _, line := range catalog.GenerateUsage(root, name) {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	b.WriteString("\n## EXAMPLES\n\n")
	for _, ex := range catalog.SeedExamples(root, name) {
		if ex.Description != "" {
			fmt.Fprintf(&b, "    # %s\n", ex.Description)
		}
		fmt.Fprintf(&b, "    %s\n\n", ex.Command)
	}
	b.WriteString("## GOTCHAS\n\n## SEE ALSO\n")

	path := filepath.Join(pagesDir, name+".md")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// TestEveryPageHasCommands is the reverse rule: a page nobody can reach is a
// lie. Dispatch verbs count — a page whose only verb is `apply` via its kind
// passes.
func TestEveryPageHasCommands(t *testing.T) {
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		if len(r.Verbs) == 0 {
			t.Errorf("page %q is reachable from no command; delete the page or annotate a command with it", r.Name)
		}
	}
}

// TestCatalogHasEveryExpectedResource pins the rows the content plan names, so
// a resource cannot quietly vanish from the index.
//
// The count is deliberately NOT written out here. `want` below is the list and
// `len(want)` is the assertion, so a number transcribed into this comment is a
// second copy that nothing checks: it said "the 32 rows" while `want` already
// held 34, having gone stale the moment composite-run-job and rule-run-replay
// were added. Add the resource to `want`; the count follows.
func TestCatalogHasEveryExpectedResource(t *testing.T) {
	want := []string{
		"alert", "alias", "backup-settings", "change-proposal", "change-review-policy", "cli", "cloud-source", "comment-thread", "composite-dry-run-job", "composite-run-job", "context", "data",
		"data-review", "data-review-group", "data-review-policy", "deliverable", "deliverable-revision", "deliverable-run", "deliverable-template", "destination", "dry-run-job", "duplicate-group", "email-sender", "error-incident",
		"file", "history", "ingestion-job", "latest-alert", "linked-table", "notification", "notification-preference", "org",
		"pii-access-request", "pii-grant", "pii-reveal", "privilege-request", "profiling-job", "rule", "rule-merge", "rule-review", "rule-review-policy", "rule-revision",
		"release", "rule-run-replay", "rule-suggestion", "rule-upgrade", "session", "slack-config", "soft-deleted-tables", "standard-field", "submission-status", "table", "table-config", "table-copy",
		"queue-message", "table-kind", "table-member", "table-rule", "table-rule-template",
		"table-schema", "table-series", "tables-overview", "warning",
	}
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	got := map[string]bool{}
	for _, r := range built {
		got[r.Name] = true
	}
	for _, name := range want {
		if !got[name] {
			t.Errorf("catalog is missing %q", name)
		}
	}
	if len(built) != len(want) {
		t.Errorf("catalog has %d rows, want %d", len(built), len(want))
	}
}

// TestAllPagesParse: a malformed page must be caught in CI, not by a user
// running `qctl explain`.
func TestAllPagesParse(t *testing.T) {
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		if p.Summary == "" {
			t.Errorf("%s: summary is required", p.Name)
		}
		if p.Schema != "" {
			if _, _, ok := p.SchemaRef(); !ok {
				t.Errorf("%s: schema %q must be namespaced %q or %q",
					p.Name, p.Schema,
					resources.SchemaNamespaceOpenAPI+":<component>",
					resources.SchemaNamespaceManifest+":<resource>")
			}
		}
		if p.Signature == "" {
			t.Errorf("%s: signature is required; stamp it with\n"+
				"    go test ./internal/catalog -run TestPageSignatures -update", p.Name)
		}
	}
}

// TestFrontmatterReferences: every pointer a page carries must resolve.
func TestFrontmatterReferences(t *testing.T) {
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}

	guides := map[string]bool{}
	for _, name := range guide.TopicNames() {
		guides[name] = true
	}
	manifests := map[string]bool{}
	for _, name := range explain.ManifestSchemaNames() {
		manifests[name] = true
	}
	applyKinds := map[string]bool{}
	for _, k := range strings.Split(apply.NewCommand().Annotations[catalog.AnnotationKinds], ",") {
		applyKinds[k] = true
	}
	generated, err := os.ReadFile(filepath.Join(repositoryRoot, "internal/api/client.gen.go"))
	if err != nil {
		t.Fatalf("read the generated client: %v", err)
	}

	var fields map[string]map[string]string
	for _, p := range pages {
		if p.WriteSchema != "" {
			assertWriteComponent(t, p, string(generated))
		}
		if len(p.CreateOnly) > 0 {
			if fields == nil {
				fields = generatedStructFields(t, generated)
			}
			assertCreateOnlyFields(t, p, fields)
		}
		if p.Guide != "" && !guides[p.Guide] {
			t.Errorf("%s: guide %q does not ship (available: %s)",
				p.Name, p.Guide, strings.Join(guide.TopicNames(), ", "))
		}
		if ns, ref, ok := p.SchemaRef(); ok {
			switch ns {
			case resources.SchemaNamespaceManifest:
				if !manifests[ref] {
					t.Errorf("%s: no static manifest schema named %q", p.Name, ref)
				}
			case resources.SchemaNamespaceOpenAPI:
				assertMappedComponent(t, p, ref)
			}
		}
		if p.Kind != "" && !applyKinds[p.Kind] {
			t.Errorf("%s: kind %q is not accepted by `qctl apply -f`", p.Name, p.Kind)
		}
	}
}

// assertMappedComponent pins which OpenAPI component a page names, not merely
// that it names one that exists somewhere.
//
// `qctl explain <res>` resolves its component from the page's `schema:`
// frontmatter now; it used to resolve it through schema.SchemaMapping, a
// per-resource map. Checking only that the ref is *some* value in that map
// meant alert.md could say `openapi:WarningSchema` and the whole suite still
// passed, with `qctl explain alert` silently serving the warning schema — an
// error that was impossible while the map did the resolving. So: the page and
// the map must agree, resource by resource.
//
// schema.SchemaMapping has no production callers any more. That is the point:
// it is the frozen pre-change mapping, and its only job is to be the thing a
// page is checked against.
func assertMappedComponent(t *testing.T, p resources.Page, ref string) {
	t.Helper()
	mapped := false
	for _, name := range append([]string{p.Name}, p.Aliases...) {
		component, ok := schema.SchemaMapping[name]
		if !ok {
			continue
		}
		mapped = true
		if component != ref {
			t.Errorf("%s.md declares schema openapi:%s, but %q has always resolved to %q.\n"+
				"One of the two is wrong, and `qctl explain %s` would serve the wrong document.",
				p.Name, ref, name, component, p.Name)
		}
	}
	if !mapped {
		t.Errorf("%s.md declares schema openapi:%s, but neither %q nor any of its aliases %v is a key in "+
			"schema.SchemaMapping, so nothing pins the component this page points at.\n"+
			"Add the entry to internal/schema/metadata.go.",
			p.Name, ref, p.Name, p.Aliases)
	}
}

// assertWriteComponent checks a page's writeSchema: it names an openapi
// component the generated client models, and the page's schema is an openapi
// one for explain to merge it into.
func assertWriteComponent(t *testing.T, p resources.Page, generated string) {
	t.Helper()
	write, ok := p.WriteSchemaRef()
	if !ok {
		t.Errorf("%s.md: writeSchema %q is not openapi:<component>", p.Name, p.WriteSchema)
		return
	}
	if ns, _, ok := p.SchemaRef(); !ok || ns != resources.SchemaNamespaceOpenAPI {
		t.Errorf("%s.md: writeSchema %s needs an openapi: schema to merge into, and the page's schema is %q",
			p.Name, write, p.Schema)
	}
	if !strings.Contains(generated, " defines model for "+write+".\n") {
		t.Errorf("%s.md: writeSchema names %s, which internal/api/client.gen.go does not model", p.Name, write)
	}
}

// assertCreateOnlyFields checks a page's createOnly: the page's schema is an
// openapi one for explain to mark, and each name is a top-level field of that
// component or of the page's writeSchema, with values from its generated enum.
func assertCreateOnlyFields(t *testing.T, p resources.Page, fields map[string]map[string]string) {
	t.Helper()
	ns, read, ok := p.SchemaRef()
	if !ok || ns != resources.SchemaNamespaceOpenAPI {
		t.Errorf("%s.md: createOnly %v needs an openapi: schema to mark, and the page's schema is %q",
			p.Name, p.CreateOnly, p.Schema)
		return
	}
	write, _ := p.WriteSchemaRef()
	for name, values := range p.CreateOnly {
		fieldType, inRead := fields[read][name]
		writeType, inWrite := fields[write][name]
		if !inRead && !inWrite {
			t.Errorf("%s.md: createOnly names %q, which internal/api/client.gen.go models as a top-level field "+
				"of neither %s nor the writeSchema %q, so `qctl explain %s` marks nothing",
				p.Name, name, read, write, p.Name)
			continue
		}
		if inWrite {
			fieldType = writeType
		}
		for _, value := range values {
			if !slices.Contains(api.StringEnumValues[fieldType], value) {
				t.Errorf("%s.md: createOnly field %q lists %q, which is not a generated %s enum value (allowed: %v)",
					p.Name, name, value, fieldType, api.StringEnumValues[fieldType])
			}
		}
	}
}

// TestSchemaMappingIsHonoredByPages is the other direction: every resource the
// frozen mapping can resolve must still resolve, through its page, to the same
// document. Deleting a page's `schema:` line, or renaming the page out from
// under an alias, would otherwise shrink `explain`'s coverage silently.
func TestSchemaMappingIsHonoredByPages(t *testing.T) {
	manifests := map[string]bool{}
	for _, name := range explain.ManifestSchemaNames() {
		manifests[name] = true
	}
	for name, component := range schema.SchemaMapping {
		p, ok := resources.LookupPage(name)
		if !ok {
			t.Errorf("schema.SchemaMapping resolves %q but no page claims it (as a name or an alias)", name)
			continue
		}
		ns, ref, ok := p.SchemaRef()
		switch {
		case !ok:
			t.Errorf("%q resolved to the OpenAPI component %q, but %s.md declares no schema at all: "+
				"`qctl explain %s` no longer has a schema document to serve", name, component, p.Name, name)
		case ns == resources.SchemaNamespaceManifest:
			// Deliberate, and older than the pages: explain has always checked
			// its static manifest schemas before the OpenAPI mapping, so
			// data-review-policy and rule-review-policy were served from the
			// binary and their SchemaMapping entries were already dead. Assert
			// the static schema they name really ships.
			if !manifests[ref] {
				t.Errorf("%s.md overrides the OpenAPI mapping with manifest:%s, but no such static schema ships",
					p.Name, ref)
			}
		case ref != component:
			t.Errorf("%q resolved to %q, but %s.md now points at %q: `qctl explain %s` changed which document it serves",
				name, component, p.Name, ref, name)
		}
	}
}

// TestDispatcherKindsResolve: a kind apply or diff accepts but no page claims
// is a resource with no documentation and no catalog row.
func TestDispatcherKindsResolve(t *testing.T) {
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	owners := map[string][]string{}
	for _, p := range pages {
		if p.Kind != "" {
			owners[p.Kind] = append(owners[p.Kind], p.Name)
		}
	}
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Resource != catalog.Generic {
			continue
		}
		for _, kind := range n.Kinds {
			switch len(owners[kind]) {
			case 1:
			case 0:
				t.Errorf("%s accepts kind %q but no page declares it", n.Path, kind)
			default:
				t.Errorf("kind %q is declared by %d pages (%v); it must be exactly one",
					kind, len(owners[kind]), owners[kind])
			}
		}
	}
}

// TestUsageResolves is an interim safety net: every USAGE line an embedded
// page carries must resolve to a real command, so a missing dispatcher line
// (the class of bug fixed in 68bc68e) fails CI instead of shipping silently.
// This does not compare USAGE against GenerateUsage output — that
// reconciliation (including table.md) is phase 3's job.
func TestUsageResolves(t *testing.T) {
	root := realRoot(t)
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		for _, line := range p.Usage {
			if _, err := catalog.ResolveCommandLine(root, line); err != nil {
				t.Errorf("%s.md: %v", p.Name, err)
			}
		}
	}
}

// TestGeneratedUsageIncludesRequiredFlags pins the six exact generated lines
// the phase-3 task-3 brief specifies: UsageLine must append a command's own
// required flags (shorthand when it has one, long form otherwise, a value
// placeholder after), or the generated line is not runnable.
func TestGeneratedUsageIncludesRequiredFlags(t *testing.T) {
	want := map[string]string{
		"qctl apply table":        "qctl apply table -f <file.yaml>",
		"qctl apply cloud-source": "qctl apply cloud-source -f <file.yaml>",
		"qctl apply destination":  "qctl apply destination -f <file.yaml>",
		"qctl apply linked-table": "qctl apply linked-table -f <file.yaml>",
		"qctl review assign":      "qctl review assign <review-id> --reviewer <reviewer>",
		"qctl review unassign":    "qctl review unassign <review-id> --reviewer <reviewer>",
	}
	found := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		wantLine, ok := want[n.Path]
		if !ok {
			continue
		}
		found[n.Path] = true
		if got := catalog.UsageLine(n.Cmd); got != wantLine {
			t.Errorf("UsageLine(%s) = %q, want %q", n.Path, got, wantLine)
		}
	}
	for path := range want {
		if !found[path] {
			t.Errorf("command %q not found in tree", path)
		}
	}
}

// TestApplyKindsComplete: apply must accept every kind a page advertises.
// diff is deliberately partial and is exempt from this direction.
func TestApplyKindsComplete(t *testing.T) {
	accepted := map[string]bool{}
	for _, n := range catalog.Walk(realRoot(t)) {
		if n.Resource == catalog.Generic && n.Path == "qctl apply" {
			for _, k := range n.Kinds {
				accepted[k] = true
			}
		}
	}
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		if p.Kind != "" && !accepted[p.Kind] {
			t.Errorf("%s declares kind %q, which `qctl apply -f` does not accept", p.Name, p.Kind)
		}
	}
}

// TestPageSignatures forces a re-read of a page's hand-written prose at the
// moment its commands' behavior could have changed. It cannot prove the prose
// is true; it can prove nobody looked since the surface moved.
//
// It is also the only writer: -update re-stamps the signature and regenerates
// the USAGE block. The write therefore triggers on a stale signature *or* a
// drifted USAGE, never on the signature alone — a writer whose trigger is
// narrower than what it writes leaves CI red with a remedy that does nothing.
// The CI (non--update) path is unchanged and still fails only on a stale
// signature; TestPageUsageMatchesGenerated remains the test that fails on
// drifted USAGE.
func TestPageSignatures(t *testing.T) {
	root := realRoot(t)
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}

	for _, p := range pages {
		want := catalog.Signature(root, p)
		usage := catalog.GenerateUsageWith(root, p.Name, p.Kind)
		staleSignature := p.Signature != want
		driftedUsage := !reflect.DeepEqual(p.Usage, usage)

		if *update {
			if !staleSignature && !driftedUsage {
				continue
			}
			path := filepath.Join(pagesDir, p.Name+".md")
			stamped := []byte(catalog.Restamp(p.Raw, usage, want))
			// Verify before writing, so a Restamp bug never lands its damage
			// on disk.
			if err := checkRestamp(p, stamped, want, usage); err != nil {
				t.Fatalf("re-stamp of %s would be wrong, nothing written: %v", path, err)
			}
			if err := os.WriteFile(path, stamped, 0o644); err != nil {
				t.Fatalf("re-stamp %s: %v", path, err)
			}
			t.Logf("re-stamped %s (%s → %s)", path, p.Signature, want)
			continue
		}

		if !staleSignature {
			continue
		}
		cmds := strings.Join(commandsFor(root, p), ", ")
		t.Errorf("%s.md is stale: the command surface of %s changed since its GOTCHAS were last verified.\n"+
			"Re-read those commands (docs/commands.md maps each to its file), update GOTCHAS, then re-stamp:\n"+
			"    go test ./internal/catalog -run TestPageSignatures -update",
			p.Name, cmds)
	}
}

// checkRestamp is the round-trip guard on the only code path that rewrites a
// page: re-parse what Restamp produced and insist it changed the signature and
// the USAGE block and nothing else.
//
// GOTCHAS and SEE ALSO are empty on every page today, so a prose-destroying
// Restamp bug would land silently now and only surface once phase 6 has filled
// them in — with the prose already gone. This turns any such bug into a loud
// failure at stamp time instead.
func checkRestamp(before resources.Page, stamped []byte, wantSignature string, wantUsage []string) error {
	after, err := resources.ParsePage(before.Name, stamped)
	if err != nil {
		return fmt.Errorf("the re-stamped page no longer parses: %w", err)
	}
	if after.Signature != wantSignature {
		return fmt.Errorf("signature is %q, want %q (Restamp did not stamp it)", after.Signature, wantSignature)
	}
	if !reflect.DeepEqual(after.Usage, wantUsage) {
		return fmt.Errorf("USAGE is %v, want %v", after.Usage, wantUsage)
	}
	if !reflect.DeepEqual(after.Examples, before.Examples) {
		return fmt.Errorf("EXAMPLES changed: %v → %v", before.Examples, after.Examples)
	}
	if !reflect.DeepEqual(after.Gotchas, before.Gotchas) {
		return fmt.Errorf("GOTCHAS changed: %v → %v", before.Gotchas, after.Gotchas)
	}
	if !reflect.DeepEqual(after.SeeAlso, before.SeeAlso) {
		return fmt.Errorf("SEE ALSO changed: %v → %v", before.SeeAlso, after.SeeAlso)
	}
	return nil
}

// TestPageUsageMatchesGenerated is the reconciliation TestUsageResolves defers:
// every page's USAGE block must be exactly what the generator would produce
// for that page's own kind, same lines and same order. This is the net that
// would have caught table.md's hand-curated USAGE (wrong order, and a bare
// `qctl apply table` where the generator emits the -f form) — TestUsageResolves
// only checks that each line resolves, not that the block matches.
//
// It deliberately uses GenerateUsageWith(root, p.Name, p.Kind) rather than
// GenerateUsage: the page in hand is the authority on its own kind, and
// routing through GenerateUsage would re-add the second lookup through the
// compiled embed FS that Task 3 removed.
func TestPageUsageMatchesGenerated(t *testing.T) {
	root := realRoot(t)
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		want := catalog.GenerateUsageWith(root, p.Name, p.Kind)
		if reflect.DeepEqual(p.Usage, want) {
			continue
		}
		t.Errorf("%s.md USAGE does not match the generator.\n"+
			"got:\n    %s\nwant:\n    %s\n"+
			"Re-stamp it with:\n"+
			"    go test ./internal/catalog -run TestPageSignatures -update",
			p.Name, strings.Join(p.Usage, "\n    "), strings.Join(want, "\n    "))
	}
}

// commandsFor names the commands a stale-page message must send the developer
// back to. It goes through catalog.PageNodes — the same selection Signature
// hashes — so the message can never again list a set narrower than what moved
// the signature. Matching only on n.Resource omitted the generic dispatchers,
// so adding one flag to `qctl apply` reported eight stale pages while naming
// only their get/delete commands, none of which had changed: a message that
// reads as spurious and trains the reflexive -update the mechanism exists to
// prevent.
func commandsFor(root *cobra.Command, page resources.Page) []string {
	var out []string
	for _, n := range catalog.PageNodes(root, page) {
		out = append(out, `"`+n.Path+`"`)
	}
	return out
}

// TestCommandsForNamesTheDispatchersThatMovedTheSignature: on the real tree,
// a stale-page message for a kind-bearing page must name `qctl apply` and
// every other dispatcher that reaches the page by kind. Adding a flag to
// `qctl apply` moves eight page signatures; before this, the message for each
// listed only that page's own get/delete commands — none of which had changed.
func TestCommandsForNamesTheDispatchersThatMovedTheSignature(t *testing.T) {
	root := realRoot(t)
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}

	kinded := 0
	for _, p := range pages {
		if p.Kind == "" {
			continue
		}
		kinded++
		named := map[string]bool{}
		for _, c := range commandsFor(root, p) {
			named[c] = true
		}
		if !named[`"qctl apply"`] {
			t.Errorf("%s.md declares kind %q, so `qctl apply` moves its signature, but the stale message names only %v",
				p.Name, p.Kind, commandsFor(root, p))
		}
		for _, n := range catalog.Walk(root) {
			if n.Resource != catalog.Generic {
				continue
			}
			for _, k := range n.Kinds {
				if k == p.Kind && !named[`"`+n.Path+`"`] {
					t.Errorf("%s.md: %q accepts kind %q and is hashed into the signature, but the stale message omits it",
						p.Name, n.Path, p.Kind)
				}
			}
		}
	}
	if kinded == 0 {
		t.Fatal("fixture assumption broken: no embedded page declares a kind")
	}
}

// TestExplainKeysHavePages replaces the deleted TestRegistrySupersetOfExplain:
// every name explain can resolve must have a page, or `api-resources` would
// omit something `explain` answers for.
func TestExplainKeysHavePages(t *testing.T) {
	names := append([]string{}, explain.ManifestSchemaNames()...)
	for key := range schema.SchemaMapping {
		names = append(names, key)
	}
	for _, name := range names {
		if _, ok := resources.LookupPage(name); !ok {
			t.Errorf("explain resolves %q but no page claims it (as a name or an alias)", name)
		}
	}
}

// TestManifestAndOpenAPINamespacesDoNotOverlap: a page that declares
// `openapi:` for a resource explain would serve from a static manifest would
// make -o table and -o json disagree. Namespacing exists to prevent exactly
// that, so assert it.
func TestManifestAndOpenAPINamespacesDoNotOverlap(t *testing.T) {
	manifests := map[string]bool{}
	for _, n := range explain.ManifestSchemaNames() {
		manifests[n] = true
	}
	pages, err := resources.Pages()
	if err != nil {
		t.Fatalf("Pages: %v", err)
	}
	for _, p := range pages {
		ns, _, ok := p.SchemaRef()
		if ok && ns == resources.SchemaNamespaceOpenAPI && manifests[p.Name] {
			t.Errorf("%s declares an openapi schema but explain serves it from a static manifest", p.Name)
		}
	}
}

// stampFixture is a page in the state a real one is in: hand-written EXAMPLES,
// GOTCHAS and SEE ALSO that no stamp may touch.
const stampFixture = `---
summary: rows of a table
aliases:
kind:
schema:
guide:
signature: 0000000
---

## USAGE

    qctl get data <old>

## EXAMPLES

    # curated by hand
    qctl get data orders

## GOTCHAS

- hand-written, must survive

## SEE ALSO

- ` + "`qctl guide inspect-data`" + `
`

func parseFixture(t *testing.T) resources.Page {
	t.Helper()
	p, err := resources.ParsePage("data", []byte(stampFixture))
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	return p
}

// TestCheckRestampAcceptsAGoodStamp: the guard must not fire on what Restamp
// actually produces, or -update becomes unusable.
func TestCheckRestampAcceptsAGoodStamp(t *testing.T) {
	p := parseFixture(t)
	usage := []string{"qctl get data <table-id-or-name>"}
	stamped := []byte(catalog.Restamp(p.Raw, usage, "abc1234"))
	if err := checkRestamp(p, stamped, "abc1234", usage); err != nil {
		t.Errorf("guard fired on a correct stamp: %v", err)
	}
}

// TestCheckRestampCatchesAStampThatWroteNothing is finding 3 as a test: when
// Restamp cannot find a signature line to replace it used to return the page
// unchanged, and -update logged a re-stamp and exited 0. The guard turns that
// into a failure at stamp time.
func TestCheckRestampCatchesAStampThatWroteNothing(t *testing.T) {
	p := parseFixture(t)
	usage := []string{"qctl get data <table-id-or-name>"}
	if err := checkRestamp(p, []byte(p.Raw), "abc1234", usage); err == nil {
		t.Error("guard accepted a stamp that changed nothing")
	}
}

// TestCheckRestampCatchesDestroyedProse is the phase-6 insurance policy: a
// stamp that drops hand-written GOTCHAS or SEE ALSO must fail loudly. Those
// sections are empty on every page today, so without this a prose-destroying
// bug would land invisibly and only show up once phase 6 filled them in.
func TestCheckRestampCatchesDestroyedProse(t *testing.T) {
	p := parseFixture(t)
	usage := []string{"qctl get data <table-id-or-name>"}
	good := catalog.Restamp(p.Raw, usage, "abc1234")

	cases := map[string]string{
		"dropped GOTCHAS":  strings.Replace(good, "- hand-written, must survive\n", "", 1),
		"dropped SEE ALSO": strings.Replace(good, "- `qctl guide inspect-data`\n", "", 1),
		"dropped EXAMPLES": strings.Replace(good, "    qctl get data orders\n", "", 1),
	}
	for name, mangled := range cases {
		if mangled == good {
			t.Fatalf("%s: fixture assumption broken, nothing was removed", name)
		}
		if err := checkRestamp(p, []byte(mangled), "abc1234", usage); err == nil {
			t.Errorf("%s: guard accepted a stamp that destroyed hand-written prose", name)
		}
	}
}

// TestCheckRestampCatchesUnwrittenUsage: the guard also covers the other half
// of what a stamp writes.
func TestCheckRestampCatchesUnwrittenUsage(t *testing.T) {
	p := parseFixture(t)
	stamped := []byte(catalog.Restamp(p.Raw, []string{"qctl get data <old>"}, "abc1234"))
	if err := checkRestamp(p, stamped, "abc1234", []string{"qctl get data <new>"}); err == nil {
		t.Error("guard accepted a stamp whose USAGE is not the generated block")
	}
}

// TestEveryResourceHasAGuideOrAReason: workflow coverage is a goal of this
// design, so a resource without a guide must be a deliberate exception rather
// than an oversight. The exception list is the one recorded in the spec's
// content plan: these resources have no multi-command task of their own.
func TestEveryResourceHasAGuideOrAReason(t *testing.T) {
	// "warning" was on this list ("covered inside triage-alerts") until the
	// user-submitted verbs shipped: triage-alerts now documents
	// `qctl warning submit`, so warning.md points at it directly.
	//
	// "composite-run-job" left it when `qctl run composite-run-job` shipped:
	// link-tables now walks find-the-failure, fix, rebuild, follow the run.
	//
	// "history" stayed on the list when `qctl undo`/`qctl redo` shipped, and
	// its reason was rewritten rather than removed: the undo/redo half now
	// exists, but it did not turn the resource into a multi-command task.
	// Every one of the six write leaves is a single self-contained command --
	// the mandatory step call and the confirmation happen inside it -- so
	// there is no sequence of commands for a topic to teach.
	noGuide := map[string]string{
		"backup-settings": "a single declarative apply with readback; no multi-command task",
		"destination":     "a single apply; covered by onboard-table",
		"rule-revision":   "covered inside rule-lifecycle",
		"org":             "covered inside authenticate",
		"cli":             "the guides are themselves the cli surface",
		"email-sender":    "one listing plus three self-contained verbs on one cloud source; no multi-command task",
		"comment-thread":  "covered inside inspect-data",
		"history":         "one listing plus six one-shot undo/redo leaves, each self-contained; no multi-command task",
		// A launch and one read of what it produced. The launch already
		// prints the describe line for the replay it created, so a topic
		// would restate that one line and nothing else.
		"rule-run-replay": "a launch whose own output names the single follow-up read; no multi-command task",
		// An ops surface, not a user task flow: the routes are gated on the
		// ops role, so a guide would teach a sequence almost no reader of the
		// guides can run. The page's own EXAMPLES carry the find-then-repair
		// order.
		"queue-message": "an ops-role-only surface with no user task flow; the page's examples carry the order",
		// The same reasoning as queue-message: every privilege route is gated
		// on the ops role, and the page's EXAMPLES carry the
		// request -> approve -> verify order.
		"privilege-request": "an ops-role-only surface with no user task flow; the page's examples carry the order",
		// One ops-only read with nothing to do next in the same surface.
		"soft-deleted-tables": "an ops-role-only report, one self-contained read; no multi-command task",
		// Gated on pii:grant (admin and ops), and each verb is one
		// self-contained command; the page's EXAMPLES carry the
		// grant -> list -> revoke order.
		"pii-grant": "an admin-only surface of single self-contained commands; the page's examples carry the order",
		// Each verb is one self-contained request for one value; there is
		// no order between them to teach.
		"pii-reveal": "single self-contained commands with no task flow between them",
		// Each is one command that reads its own plan first and prints what it
		// did: copy table's dry run is the plan, and merge table-rules'
		// dry run prints the fill-in resolution file its apply takes.
		"table-copy": "one command whose dry run is its plan and whose result names the new table; no multi-command task",
		"rule-merge": "one command that previews, applies and waits; its dry run prints the resolution file it takes",
		// One read and two ways to write it, each a single self-contained
		// command; the page's EXAMPLES carry the read-then-write order.
		"notification-preference": "one read plus a single apply or set-preference write; no multi-command task",
		"slack-config":            "one read plus one self-contained set-alert-type write; no multi-command task",
		// Local upkeep of what the launcher builds: one listing, a preview-
		// then-collect gc and a pin toggle, each self-contained. Building and
		// selecting a release is switch-context's task, which the page points at.
		"release": "local cache upkeep in self-contained commands; the page's examples carry the preview-then-collect order",
	}
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		if r.Guide != "" {
			if _, exempt := noGuide[r.Name]; exempt {
				t.Errorf("%s is on the no-guide list but points at %q; remove it from the list", r.Name, r.Guide)
			}
			continue
		}
		if _, exempt := noGuide[r.Name]; !exempt {
			t.Errorf("%s has no guide and is not a recorded exception.\n"+
				"Either write a `qctl guide` topic and set `guide:` in %s.md, "+
				"or add it to this test's exception list with the reason.", r.Name, r.Name)
		}
	}
}

// TestGuideCountMatchesThePlan pins the number of shipped guides.
func TestGuideCountMatchesThePlan(t *testing.T) {
	if got := len(guide.TopicNames()); got != 29 {
		t.Errorf("%d guide topics, want 29", got)
	}
}

// TestSeeAlsoResolves: a SEE ALSO bullet is a promise that the target exists.
// Three shapes are allowed, and each is checked against its own registry.
func TestSeeAlsoResolves(t *testing.T) {
	root := realRoot(t)
	built, err := catalog.Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	names := map[string]bool{}
	for _, r := range built {
		names[r.Name] = true
		for _, a := range r.Aliases {
			names[a] = true
		}
	}
	topics := map[string]bool{}
	for _, n := range guide.TopicNames() {
		topics[n] = true
	}

	for _, r := range built {
		for _, bullet := range r.SeeAlso {
			line := strings.Trim(strings.TrimSpace(bullet), "`")
			fields := strings.Fields(line)
			if len(fields) < 2 || fields[0] != "qctl" {
				t.Errorf("%s.md SEE ALSO: %q must be a backticked qctl command", r.Name, bullet)
				continue
			}
			switch fields[1] {
			case "explain":
				if len(fields) != 3 || !names[fields[2]] {
					t.Errorf("%s.md SEE ALSO: %q names no resource", r.Name, bullet)
				}
			case "guide":
				if len(fields) != 3 || !topics[fields[2]] {
					t.Errorf("%s.md SEE ALSO: %q names no guide topic (available: %s)",
						r.Name, bullet, strings.Join(guide.TopicNames(), ", "))
				}
			default:
				if _, err := catalog.ResolveCommandLine(root, line); err != nil {
					t.Errorf("%s.md SEE ALSO: %v", r.Name, err)
				}
			}
		}
	}
}

// TestGotchasAreNonEmpty: the gotchas tier is the highest-value part of a page
// and the only untested one, so at minimum assert nobody shipped a blank
// section by accident.
func TestGotchasAreNonEmpty(t *testing.T) {
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		if len(r.Gotchas) == 0 {
			t.Errorf("%s.md has no GOTCHAS. If the resource genuinely has none, "+
				"write one bullet saying what a reader might wrongly assume.", r.Name)
		}
		if len(r.SeeAlso) == 0 {
			t.Errorf("%s.md has no SEE ALSO; every resource neighbours at least one other", r.Name)
		}
	}
}

// TestBackendMarkerNeverLeaksIntoTheText: "[backend]" is an internal
// maintenance tag for tools/gotchassweep, split out of the prose into
// Gotcha.Backend. resources.bullets() strips it as a trailing suffix only, so a
// bullet written "... [backend]." would leak the tag into `qctl explain` and
// into the claim's sidecar id, with nothing else to notice.
func TestBackendMarkerNeverLeaksIntoTheText(t *testing.T) {
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		for _, g := range r.Gotchas {
			if strings.Contains(g.Text, "[backend]") {
				t.Errorf("%s.md: the [backend] marker must be the last thing on the "+
					"bullet, with nothing after it:\n    %s", r.Name, g.Text)
			}
		}
	}
}

// nearMissBackendMarker matches a GOTCHAS bullet that ENDS in something shaped
// like the [backend] marker but is not spelled exactly "[backend]": wrong case
// ([Backend]), a stray inner space ([backend ], [ backend]), the wrong
// brackets ((backend), {backend}, `backend`), or an unclosed one ([backend.
//
// Two properties do the work, and both were chosen against the real corpus:
//
//   - It is anchored at end-of-text. "backend" is ordinary prose in these
//     pages -- 13 untagged bullets use the word mid-sentence today -- so a
//     substring search anywhere in the bullet would fire on all thirteen.
//
//   - An OPENING delimiter is required. That is what distinguishes a marker
//     from prose, and it is what keeps "...is decided by the backend" and
//     "...(by the backend)" silent. The price is that a closer-only "backend]"
//     is not matched; that shape is indistinguishable from the parenthesised
//     prose above, and buying it would cost a false positive on real writing.
//     The closer is optional, so a truncated "[backend" IS caught.
//
// Run against all 288 gotcha bullets on the 32 current pages (177 tagged, 111
// plain): zero matches.
var nearMissBackendMarker = regexp.MustCompile("(?i)[`\\[({]\\s*backend\\s*[`\\])}]?[.,;:!]*\\s*$")

// TestBackendMarkerNearMissPredicate pins nearMissBackendMarker against the
// shapes it must and must not match. The corpus check that uses it
// (TestBackendMarkerIsNeverNearlyRight) passes vacuously on a clean tree, so
// this table is the only place the predicate's behaviour is actually asserted.
func TestBackendMarkerNearMissPredicate(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// The four shapes the ticket names, plus the neighbours that fall in
		// the same hole. None of these leaves "[backend]" in Gotcha.Text, so
		// TestBackendMarkerNeverLeaksIntoTheText cannot see any of them.
		{"the cap is enforced server-side [Backend]", true},
		{"the cap is enforced server-side [BACKEND]", true},
		{"the cap is enforced server-side [backend ]", true},
		{"the cap is enforced server-side [ backend]", true},
		{"the cap is enforced server-side (backend)", true},
		{"the cap is enforced server-side {backend}", true},
		{"the cap is enforced server-side `backend`", true},
		{"the cap is enforced server-side [backend", true},

		// Also a near miss, and also a leak. Both tests fire; either fix
		// satisfies both. The overlap is deliberate -- neither test is allowed
		// to depend on the other still existing.
		{"the cap is enforced server-side [backend].", true},

		// A correctly tagged bullet. gotchaBullets has already lifted the
		// marker out, so what this check sees is ordinary prose.
		{"the cap is enforced server-side", false},

		// The word "backend" as prose. Thirteen bullets do this today; a
		// substring search anywhere in the bullet would fire on all thirteen.
		{"the backend cannot search by id.", false},
		{"clearing is safe -- nothing on the backend knows about aliases", false},
		{"`--label` is what tells the backend the two runs apart.", false},
		{"a backend-shaped payload is not a marker", false},

		// The one false positive the trailing-token anchor would still admit
		// if the opening delimiter were not required. Requiring it is also why
		// a closer-only "backend]" is knowingly out of scope: the two are
		// indistinguishable by shape.
		{"the id is assigned server-side (by the backend)", false},
		{"the value is decided by the backend", false},
		{"", false},
	}
	for _, c := range cases {
		if got := nearMissBackendMarker.MatchString(c.text); got != c.want {
			t.Errorf("nearMissBackendMarker.MatchString(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

// TestBackendMarkerIsNeverNearlyRight is the other half of the invariant
// TestBackendMarkerNeverLeaksIntoTheText states: a GOTCHAS bullet's marker is
// spelled exactly "[backend]", or it is absent -- never nearly right.
//
// The two split the failure space cleanly. The leak test fires whenever the
// literal token survives into Gotcha.Text, so it owns every PLACEMENT error:
// a `[backend]` prefix, a backticked `[backend]`, a trailing "[backend].".
// It structurally cannot see a SPELLING error -- "[Backend]", "[backend ]",
// "[ backend]", "(backend)" all parse as an ordinary gotcha with nothing left
// behind, which is exactly why this test exists.
//
// The consequence of a near miss is worth spelling out, because it is silent
// and permanent. The bullet gets no entry in tools/gotchassweep/baseline.yaml,
// it is never listed by `make gotchas-sweep`, and it is never re-checked
// against ../atlas after `make generate-client` moves the spec -- while the
// sweep goes on reporting that every claim is verified. The author believes
// they filed a backend claim; the sweep has never heard of it.
//
// This check cannot live in tools/gotchassweep: that tool loads the world
// through resources.Pages(), by which point a near-miss bullet is
// indistinguishable from a bullet nobody ever meant to tag. A tool cannot fail
// on what it cannot see, and that asymmetry is what let this hide.
//
// Deliberately no -update: whether a bullet is a backend claim is a human
// judgment, and a mechanical rewrite would happily promote a prose sentence
// into a tracked claim.
func TestBackendMarkerIsNeverNearlyRight(t *testing.T) {
	built, err := catalog.Build(realRoot(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, r := range built {
		for _, g := range r.Gotchas {
			if g.Backend {
				continue // parsed correctly; gotchaBullets already stripped the marker
			}
			if !nearMissBackendMarker.MatchString(g.Text) {
				continue
			}
			t.Errorf("%s.md: this GOTCHAS bullet ends in something shaped like the "+
				"backend marker but spelled differently, so it parsed as an ORDINARY "+
				"gotcha -- no tools/gotchassweep/baseline.yaml entry, no `make "+
				"gotchas-sweep` coverage, and nobody re-checks it after `make "+
				"generate-client`:\n"+
				"    %s\n"+
				"The marker is the exact lowercase token [backend], trailing, unquoted, "+
				"one space before it and nothing after it:\n"+
				"    - ...prose. [backend]\n"+
				"If this bullet is NOT a claim about server behaviour, reword the ending "+
				"so it no longer looks like one.", r.Name, g.Text)
		}
	}
}
