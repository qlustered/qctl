package guide

import (
	"embed"
	"fmt"

	"github.com/qlustered/qctl/internal/catalog"
)

//go:embed topics/*.md
var topicsFS embed.FS

// topic is one shipped tutorial. File is the path within topicsFS.
type topic struct {
	Name    string
	Summary string
	File    string
	// Aliases are extra names `qctl guide <name>` accepts. They are deliberately
	// absent from the index: an alias exists so a reasonable guess resolves, not
	// to grow the menu a reader has to scan.
	Aliases []string
}

// topics is the ordered registry of shipped guides. Registry order is the
// order shown in the `qctl guide` index. To add a guide: drop a markdown file
// under topics/ and add one entry here.
var topics = []topic{
	{
		Name:    "discover-capabilities",
		Summary: "Start here: find the command, resource or guide for a task",
		File:    "topics/discover-capabilities.md",
	},
	{
		Name:    "check-inbox",
		Summary: "Read and clear your notification inbox",
		File:    "topics/check-inbox.md",
	},
	{
		Name:    "onboard-table",
		Summary: "Create or update a table (dataset) from a manifest",
		File:    "topics/onboard-table.md",
	},
	{
		Name:    "standard-field-vocabulary",
		Summary: "Define standard fields, tag tables and columns, find matching rules",
		File:    "topics/standard-field-vocabulary.md",
		Aliases: []string{"concept-vocabulary"},
	},
	{
		Name:    "manage-files",
		Summary: "Upload a file, follow its first ingest, download or ignore it",
		File:    "topics/manage-files.md",
	},
	{
		Name:    "connect-sources",
		Summary: "Connect a cloud storage bucket as a data source and ingest from it",
		File:    "topics/connect-sources.md",
	},
	{
		Name:    "link-tables",
		Summary: "Build a linked table that joins several tables on a spine",
		File:    "topics/link-tables.md",
	},
	{
		Name:    "deliverables",
		Summary: "Author a spreadsheet setup, produce a run, sign off and record delivery",
		File:    "topics/deliverables.md",
	},
	{
		Name:    "inspect-data",
		Summary: "Read a table's rows, clean or quarantined, and export them all",
		File:    "topics/inspect-data.md",
	},
	{
		Name:    "edit-data",
		Summary: "Fix bad values in quarantined rows: search-replace, set-cell, undo",
		File:    "topics/edit-data.md",
	},
	{
		Name:    "rule-lifecycle",
		Summary: "A rule end to end: submit, dry run, review, enable, attach",
		File:    "topics/rule-lifecycle.md",
	},
	{
		Name:    "submit-rules",
		Summary: "Submit Python rule files to the org catalog; what a file may contain",
		File:    "topics/submit-rules.md",
	},
	{
		Name:    "instantiate-rule",
		Summary: "Attach a rule to a table or column (create a table rule)",
		File:    "topics/instantiate-rule.md",
		Aliases: []string{"add-rule-to-column"},
	},
	{
		Name:    "manage-suggestions",
		Summary: "Inspect, accept, edit or dismiss proposed new rules and rule changes",
		File:    "topics/manage-suggestions.md",
	},
	{
		Name:    "run-jobs",
		Summary: "Run, kill and wait for ingestion and profiling jobs; job states",
		File:    "topics/run-jobs.md",
	},
	{
		Name:    "dry-run-jobs",
		Summary: "Dry-run a rule against real rows; which job is which",
		File:    "topics/dry-run-jobs.md",
	},
	{
		Name:    "triage-alerts",
		Summary: "Resolve alerts and warnings: what fired, on which rows, which action",
		File:    "topics/triage-alerts.md",
	},
	{
		Name:    "propose-changes",
		Summary: "Propose an alert or warning resolution for an approver, and decide one",
		File:    "topics/propose-changes.md",
		Aliases: []string{"change-proposals", "approve-changes"},
	},
	{
		Name:    "unblock-table",
		Summary: "Find what blocks a table (draft schema, alert, suggestion) and clear it",
		File:    "topics/unblock-table.md",
	},
	{
		Name:    "handle-incidents",
		Summary: "Diagnose a failed ingestion or processing job, then retry it",
		File:    "topics/handle-incidents.md",
	},
	{
		Name:    "request-review",
		Summary: "Rule review, author side: request one and drive it to enable",
		File:    "topics/request-review.md",
	},
	{
		Name:    "review-rules",
		Summary: "Rule review, reviewer side: threads, evidence, approve or reject",
		File:    "topics/review-rules.md",
	},
	{
		Name:    "inspect-schema",
		Summary: "Read a table's schema (columns, types) and its pending draft",
		File:    "topics/inspect-schema.md",
	},
	{
		Name:    "data-review-policy",
		Summary: "Data review policy: which quarantined rows need approval",
		File:    "topics/data-review-policy.md",
	},
	{
		Name:    "review-data",
		Summary: "Data review: submit quarantined rows for approval, and decide them",
		File:    "topics/review-data.md",
	},
	{
		Name:    "authenticate",
		Summary: "Log in, check who you are, and switch organizations",
		File:    "topics/authenticate.md",
	},
	{
		Name:    "switch-context",
		Summary: "Point qctl at another server or context; protected contexts",
		File:    "topics/switch-context.md",
	},
	{
		Name:    "use-aliases",
		Summary: "Use qctl's short aliases instead of raw UUIDs",
		File:    "topics/use-aliases.md",
	},
	{
		Name:    "output-and-scripting",
		Summary: "Exit codes, output formats, --yes and timeouts for scripts and agents",
		File:    "topics/output-and-scripting.md",
	},
}

// body reads the embedded markdown for this topic.
func (t topic) body() (string, error) {
	b, err := topicsFS.ReadFile(t.File)
	if err != nil {
		return "", fmt.Errorf("guide %q: %w", t.Name, err)
	}
	return string(b), nil
}

// lookup finds a topic by exact name, or by one of its aliases.
func lookup(name string) (topic, bool) {
	for _, t := range topics {
		if t.Name == name {
			return t, true
		}
		for _, a := range t.Aliases {
			if a == name {
				return t, true
			}
		}
	}
	return topic{}, false
}

// TopicNames returns the registered guide slugs in registry order. Exported so
// the catalog drift tests can prove no page points at a guide that does not
// ship.
func TopicNames() []string {
	names := make([]string, len(topics))
	for i, t := range topics {
		names[i] = t.Name
	}
	return names
}

// Index returns the registry as name/summary pairs in registry order — the
// same order and the same text `qctl guide` prints. Exported so
// internal/catalog's drift test can generate docs/patterns.md's guide list
// instead of a human transcribing it; TopicNames stays as it is because its
// callers only ever need the slugs.
func Index() []catalog.GuideTopic {
	out := make([]catalog.GuideTopic, len(topics))
	for i, t := range topics {
		out[i] = catalog.GuideTopic{Name: t.Name, Summary: t.Summary}
	}
	return out
}

func topicNames() []string { return TopicNames() }

// TopicBody returns the embedded markdown for a topic. Exported so the guide
// resolution test can prove every command line in it is real.
func TopicBody(name string) (string, error) {
	t, ok := lookup(name)
	if !ok {
		return "", fmt.Errorf("unknown guide %q", name)
	}
	return t.body()
}
