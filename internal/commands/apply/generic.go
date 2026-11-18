package apply

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/output"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/prompt"
	"github.com/spf13/cobra"
)

// applyFunc applies one manifest document and returns its result and error.
type applyFunc func(cmd *cobra.Command, doc manifestDoc) (*applyResult, error)

// applyResult carries a result document and its resource reference.
type applyResult struct {
	document any
	name     string
}

func namedApplyResult(document any, resource string, id any) *applyResult {
	return &applyResult{document: document, name: fmt.Sprintf("%s/%v", resource, id)}
}

// humanOut is where a handler writes what it tells a reader: stdout under the
// table view, stderr under -o json, -o yaml and -o name.
func humanOut(cmd *cobra.Command) io.Writer {
	return cmdutil.NewResultWriter(cmd).Human
}

// structuredOutput reports whether stdout carries result documents.
func structuredOutput(cmd *cobra.Command) bool {
	return cmdutil.NewResultWriter(cmd).Machine()
}

// documentOutput reports whether stdout carries JSON or YAML.
func documentOutput(cmd *cobra.Command) bool {
	format, _ := cmd.Flags().GetString("output")
	return format == string(output.FormatJSON) || format == string(output.FormatYAML)
}

// genericKinds is the dispatch table for `qctl apply -f` and the single source
// of truth for the kinds apply accepts. genericApply dispatches through it,
// applyKindsLine() prints its keys in --help, and the command's "kinds"
// annotation is generated from it — so behavior, help text, and the catalog
// cannot disagree.
var genericKinds = map[string]applyFunc{
	"BackupSettings":          applyBackupSettings,
	"ChangeReviewPolicy":      applyChangeReviewPolicyYAML,
	"CloudSource":             applyCloudSource,
	"StandardField":           applyConcept,
	"DataReviewPolicy":        applyDataReviewPolicyYAML,
	"Deliverable":             applyDeliverable,
	"Destination":             applyDestination,
	"LinkedTable":             applyLinkedTable,
	"NotificationPreferences": applyNotificationPreferences,
	"Rule":                    applyRuleYAML,
	"RuleReviewPolicy":        applyRuleReviewPolicyYAML,
	"Table":                   applyTable,
	"TableKind":               applyTableKind,
	"TableRule":               applyTableRuleYAML,
	"TableSchema":             applyTableSchema,
	"TableSeries":             applyTableSeries,
}

// applyKinds returns the accepted kinds, sorted.
func applyKinds() []string {
	kinds := make([]string, 0, len(genericKinds))
	for k := range genericKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	return kinds
}

// genericApply reads a manifest file, inspects each document's kind, and
// dispatches to the appropriate resource-specific apply function.
func genericApply(cmd *cobra.Command, filePath string) error {
	// Reject Python files with a helpful message
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == ".py" {
		yes, _ := cmd.Flags().GetBool("yes")
		//nolint:staticcheck // Preserve the established multi-line CLI guidance verbatim.
		return qerrors.NewBadUsagef("Python rule files must be submitted with:\n  qctl submit rules -f %s%s", cmdutil.ShellArg(filePath), prompt.YesFlag(yes))
	}

	docs, err := readManifestDocuments(filePath)
	if err != nil {
		return err
	}
	if err := refuseValidateOnlyBesideOtherKinds(cmd, filePath, docs); err != nil {
		return err
	}
	return applyDocuments(cmd, docs, resolveGenericKind)
}

// refuseValidateOnlyBesideOtherKinds returns a bad-usage error when
// --validate-only is set and docs hold a document of a kind genericKinds
// dispatches other than LinkedTable, naming each such document.
func refuseValidateOnlyBesideOtherKinds(cmd *cobra.Command, filePath string, docs []manifestDoc) error {
	if validateOnly, _ := cmd.Flags().GetBool("validate-only"); !validateOnly {
		return nil
	}
	var others []string
	for _, doc := range docs {
		kind := pkgmanifest.CanonicalKind(doc.Kind)
		if _, ok := genericKinds[kind]; ok && kind != "LinkedTable" {
			others = append(others, docLabel(doc))
		}
	}
	if len(others) == 0 {
		return nil
	}
	return qerrors.NewBadUsagef("--validate-only validates LinkedTable documents only, and %s holds %s; nothing was applied",
		filePath, strings.Join(others, ", "))
}

// resolveGenericKind maps one document's kind to its handler, resolving a kind
// alias (pkgmanifest.CanonicalKind) on the way. An alias stays out of
// genericKinds so the catalog advertises exactly one name per resource.
func resolveGenericKind(doc manifestDoc) (applyFunc, error) {
	apply, ok := genericKinds[pkgmanifest.CanonicalKind(doc.Kind)]
	if !ok {
		return nil, qerrors.NewBadUsagef("unsupported kind %q in %s (supported: %s)",
			doc.Kind, doc.Source, strings.Join(applyKinds(), ", "))
	}
	return apply, nil
}
