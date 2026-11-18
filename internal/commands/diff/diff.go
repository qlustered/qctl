// Package diff implements `qctl diff -f` — preview what `apply -f` would
// change, without applying (the kubectl diff analog).
package diff

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
)

// diffFunc previews one manifest kind from filePath.
type diffFunc func(cmd *cobra.Command, filePath string) error

// diffKinds is diff's dispatch table — deliberately a subset of apply's.
var diffKinds = map[string]diffFunc{
	"Deliverable":   diffDeliverable,
	"StandardField": diffConcept,
	"LinkedTable":   diffLinkedTable,
	"Table":         diffTable,
	"TableKind":     diffTableKind,
	"TableRule":     diffTableRule,
	"TableSchema":   diffTableSchema,
	"TableSeries":   diffTableSeries,
}

func diffKindNames() []string {
	names := make([]string, 0, len(diffKinds))
	for k := range diffKinds {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// NewCommand creates the diff command.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:         "diff",
		Short:       "Preview what apply -f would change without applying",
		Annotations: cmdutil.WithFormats(catalog.Dispatcher(diffKindNames()), "table"),
		Long: `Preview the changes 'qctl apply -f' would make, without applying them.

Reads the manifest, fetches the live resource, and prints what apply would
send. A kind apply writes in one request prints a field-level old -> new diff
followed by the exact request body; TableSeries and Deliverable, which apply
writes in several, print each request in order, as described below.
Read-only: nothing is applied and no confirmation is asked.

Currently supports kind: Deliverable, StandardField, Table, TableKind, TableRule
in patch mode (metadata.id set), TableSchema, TableSeries, and LinkedTable (kind:
CompositeDataset is an alias). Other kinds, and instantiate-mode TableRule
manifests, are rejected.

TableSeries prints the ordered series, membership and deadline schedule
requests. A planned create uses <created-series-id> for the UUID the server
will assign. A deadline schedule PATCH is first sent to the server's preview,
which writes nothing, for the period of the stored schedule; after the requests,
each value, switch and date it changes prints as "<step> <column>: old → new".
A create or a frequency change prints a note instead of a preview.

Deliverable prints every request apply would send, in order, then any notes;
when nothing would change it prints the notes on stderr and exits 0.
A deliverable's basic fields are not versioned: the PATCH that changes them
takes effect as soon as apply sends it. The sheet and column binding writes
spec.sheets plans come last. Ids the server will assign print as
<created-deliverable-id>, <uploaded-template-id>, <created-revision-id> and
<created-sheet-binding-id>, and those of the bindings a created release
copies as <copied-sheet-binding-id> and <copied-column-binding-id>. diff
never uploads the template.

A LinkedTable manifest is first sent to the server with validate_only=true,
which persists nothing; a manifest that fails exits 2 with its errors, as
'qctl apply linked-table --validate-only' does. A valid one is compared with
the latest spec revision of the table named metadata.name, or, when no table
has that name, listed field by field as additions.

The file must hold exactly one document. Unlike 'qctl apply -f', which applies
every document in a multi-document file, diff previews one at a time and
rejects a multi-document file with exit 2 rather than silently previewing only
the first.

Exit codes (kubectl diff contract):
  0  no differences (nothing is printed on stdout)
  1  differences found (the diff is on stdout; stderr stays empty)
Real failures use qctl's typed exit codes (2 bad usage, 3 not found,
4 unauthorized, 5 API incompatible). Unlike kubectl, a generic runtime
failure also exits 1 — distinguishable by its stderr output.

Examples:
  qctl diff -f deliverable.yaml
  qctl diff -f table.yaml
  qctl diff -f table-kind.yaml
  qctl diff -f table-rule.yaml
  qctl diff -f table-schema.yaml
  qctl diff -f table-series.yaml
  qctl diff -f linked-table.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			filePath, _ := cmd.Flags().GetString("filename")
			if filePath == "" {
				return qerrors.NewBadUsage("filename is required (-f or --filename)\n\nUsage:\n  qctl diff -f <file.yaml>")
			}
			return genericDiff(cmd, filePath)
		},
	}
	cmd.Flags().StringP("filename", "f", "", "Path to the YAML manifest file (required)")
	return cmd
}

// genericDiff dispatches by manifest kind, mirroring apply's genericApply.
// diffKinds is the extension point for future kinds.
func genericDiff(cmd *cobra.Command, filePath string) error {
	if strings.ToLower(filepath.Ext(filePath)) == ".py" {
		return qerrors.NewBadUsagef("diff takes YAML manifests, not Python rule files: %s", filePath)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	// Count the documents before dispatching. Every diffFunc re-reads the file
	// and hands it to a strict unmarshaler, and pkgmanifest.StrictUnmarshal
	// calls yaml.Decoder.Decode ONCE -- so a multi-document file previews
	// document 1 and silently discards the rest, while `apply -f` on the same
	// file applies every one of them. For TableSchema each applied document
	// kills the table's in-flight training jobs, queues a migrate job and
	// raises an alert, so the gap is not cosmetic.
	//
	// Refusing is the whole fix: multi-document diff is deliberately not
	// implemented. This is what makes "the preview cannot drift from the
	// request" true of a FILE and not only of a document.
	docs, splitErr := pkgmanifest.SplitDocuments(data)
	if splitErr == nil && len(docs) > 1 {
		return qerrors.NewBadUsagef(
			"diff previews a single-document manifest; %s has %d documents — split it or preview each separately",
			filePath, len(docs))
	}
	manifest, err := pkgmanifest.LoadBytes(data)
	if err != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, err, "failed to parse manifest")
	}
	// A split failure cannot simply be handed to LoadBytes, because LoadBytes
	// decodes ONE document: a file whose document 1 is valid and whose document
	// 3 is malformed fails the split, parses clean here, and would be previewed
	// -- the guard skipped on the strength of the very count it needed. So if
	// the split failed and document 1 nonetheless parsed, the stream broke
	// AFTER document 1, which means the file holds more than one document and
	// diff refuses it like any other. Wrapping splitErr keeps the yaml line
	// number, which is the only thing that says where.
	//
	// A single malformed document fails LoadBytes above instead, in the wording
	// every existing test pins.
	if splitErr != nil {
		return qerrors.Wrap(qerrors.ExitBadUsage, splitErr, fmt.Sprintf(
			"diff previews a single-document manifest; %s holds more than one, and a document after the first will not parse",
			filePath))
	}
	preview, ok := diffKinds[pkgmanifest.CanonicalKind(manifest.Kind)]
	if !ok {
		return qerrors.NewBadUsagef("diff is not yet supported for kind %q (supported: %s; TableRule in patch mode only)",
			manifest.Kind, strings.Join(diffKindNames(), ", "))
	}
	return preview(cmd, filePath)
}
