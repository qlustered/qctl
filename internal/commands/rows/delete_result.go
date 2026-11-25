package rows

import (
	"encoding/hex"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/terminal"
	"github.com/spf13/cobra"
)

// deleteResult is a delete route's answer at any scope.
type deleteResult = api.DeleteRowsResponseSchema

// deleteOutputDoc is the -o sentence of delete-quarantined.
const deleteOutputDoc = `

-o json|yaml prints {affected_rows, unmatched_signatures,
not_excluded_signatures} for --signature, and {affected_rows} for --status and
--all, with skipped_repeated_keys added when a group was skipped.
affected_rows is null when no count came back. The result lines move to
stderr.`

// signatureDeleteDocument is the -o json|yaml document of a --signature
// delete. Both lists are always present, [] when empty.
type signatureDeleteDocument struct {
	AffectedRows          *int     `json:"affected_rows"`
	UnmatchedSignatures   []string `json:"unmatched_signatures"`
	NotExcludedSignatures []string `json:"not_excluded_signatures"`
}

// statusDeleteDocument is the -o json|yaml document of a --status or --all
// delete.
type statusDeleteDocument struct {
	AffectedRows        *int                          `json:"affected_rows"`
	SkippedRepeatedKeys []api.RepeatedKeyCopiesSchema `json:"skipped_repeated_keys,omitempty"`
}

// deleteConsequences are the lines the confirmation prompt prints for a
// delete with selector sel at scope sc; exclude is the
// --exclude-from-future-uploads value, which a --signature delete always has.
func deleteConsequences(verb string, sel selector, sc scope, exclude *bool) ([]string, error) {
	switch sel.kind {
	case selSignatures:
		if exclude == nil {
			return nil, unroutableSelector(verb, sel, sc)
		}
		lines := []string{
			"--signature writes an exclusion entry for each deleted line, scoped to the line's source and identity, to " +
				filesInView(sc) + ", so a reprocess of those files does not bring the line back.",
		}
		if *exclude {
			lines = append(lines, fmt.Sprintf("--exclude-from-future-uploads also writes them to %s, so a new upload "+
				"drops a line with the same identity.", futureUploadsList(sc)))
		} else {
			lines = append(lines, "--exclude-from-future-uploads=false: a new upload of these lines loads them again.")
		}
		return append(lines,
			"A line whose entry would drop a live clean row is deleted but not excluded, and the result lists it."), nil
	case selStatus, selAll:
		return []string{
			fmt.Sprintf("%s excludes nothing: the rows are deleted with your reason and load again through the rules "+
				"if the sender re-sends them.", sel.kind),
			"To keep specific lines from loading again, delete them with --signature.",
		}, nil
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}

// filesInView names the files whose exclusion lists a --signature delete at
// sc writes.
func filesInView(sc scope) string {
	if sc.kind == scopeFile {
		return "the file's exclusion list"
	}
	return "the exclusion list of each file a deleted row came from"
}

// futureUploadsList names the exclusion list a --signature delete at sc
// writes with --exclude-from-future-uploads.
func futureUploadsList(sc scope) string {
	switch sc.kind {
	case scopeTable:
		return "the table's exclusion list"
	case scopeCloudSource:
		return "the cloud source's exclusion list (the table's, for a table-series source)"
	case scopeFile:
		return "the exclusion list of the file's cloud source (the table's, for a table-series source)"
	default:
		return fmt.Sprintf("the %s's exclusion list", sc.kindName())
	}
}

// reportDeleteResult prints a delete's outcome and writes its -o document.
//
// A --signature delete that deleted nothing and names unmatched signatures
// returns a not-found error instead, as reportMoveResult does for a wholly
// missing --row-id request.
func reportDeleteResult(cmd *cobra.Command, sc scope, sel selector, sup support, act action, res *deleteResult) error {
	if sel.kind == selSignatures && allSignaturesUnmatched(res) {
		unmatched := *res.UnmatchedSignatures
		return qerrors.NewNotFound(apierror.WithScopeNote(fmt.Sprintf(
			"%s: no quarantined row in %s has %s; 0 rows deleted:\n%s",
			act.verb, sc.label(), signatureCount(len(unmatched)), indentedList(unmatched))))
	}
	doc, err := deleteDocument(act.verb, sel, sc, res)
	if err != nil {
		return err
	}

	w := cmdutil.NewResultWriter(cmd)
	var counted *bulkResult
	if res != nil {
		counted = &bulkResult{AffectedRows: res.AffectedRows}
	}
	printResultLine(w.Human, sc, sel, sup, act, counted, "")
	warnDeleteOutcome(cmd.ErrOrStderr(), sc, res)
	printFollowUp(w.Human, sc.getDataLine(sup.isBadData), deletedRowsFollowUp(w, sc, res))
	return w.Write(doc)
}

// deletedRowsFollowUp is the `get deleted-rows` line for a delete that removed
// rows, under table output only; otherwise "".
func deletedRowsFollowUp(w cmdutil.ResultWriter, sc scope, res *deleteResult) string {
	if w.Machine() || res == nil || res.AffectedRows == nil || *res.AffectedRows <= 0 {
		return ""
	}
	return sc.deletedRowsLine()
}

// allSignaturesUnmatched reports whether res deleted no row and names at least
// one unmatched signature. A delete that matches no row answers every
// requested signature as unmatched (delete_data_rows, atlas quarantine.py).
func allSignaturesUnmatched(res *deleteResult) bool {
	return res != nil && res.AffectedRows != nil && *res.AffectedRows == 0 &&
		res.UnmatchedSignatures != nil && len(*res.UnmatchedSignatures) > 0
}

// deleteDocument is the -o json|yaml document for a delete with selector sel.
func deleteDocument(verb string, sel selector, sc scope, res *deleteResult) (interface{}, error) {
	if res == nil {
		res = &deleteResult{}
	}
	switch sel.kind {
	case selSignatures:
		return signatureDeleteDocument{
			AffectedRows:          res.AffectedRows,
			UnmatchedSignatures:   nonNilStrings(res.UnmatchedSignatures),
			NotExcludedSignatures: nonNilStrings(res.NotExcludedSignatures),
		}, nil
	case selStatus, selAll:
		doc := statusDeleteDocument{AffectedRows: res.AffectedRows}
		if res.SkippedRepeatedKeys != nil {
			doc.SkippedRepeatedKeys = *res.SkippedRepeatedKeys
		}
		return doc, nil
	default:
		return nil, unroutableSelector(verb, sel, sc)
	}
}

func nonNilStrings(p *[]string) []string {
	if p == nil || *p == nil {
		return []string{}
	}
	return *p
}

// warnDeleteOutcome prints one stderr block per non-empty list in res.
func warnDeleteOutcome(out io.Writer, sc scope, res *deleteResult) {
	if res == nil {
		return
	}
	if res.UnmatchedSignatures != nil && len(*res.UnmatchedSignatures) > 0 {
		fmt.Fprintf(out, "Warning: %s matched no quarantined row in %s; nothing was deleted for them:\n%s\n",
			signatureCount(len(*res.UnmatchedSignatures)), sc.label(), indentedList(*res.UnmatchedSignatures))
	}
	if res.NotExcludedSignatures != nil && len(*res.NotExcludedSignatures) > 0 {
		fmt.Fprintf(out, "Warning: %s deleted but not excluded: a live clean row of the same source shares the line's "+
			"identity, or the row is a duplicate copy a Keep one decision did not load, so the line comes back if re-sent:\n%s\n",
			signatureCount(len(*res.NotExcludedSignatures)), indentedList(*res.NotExcludedSignatures))
	}
	if res.SkippedRepeatedKeys != nil && len(*res.SkippedRepeatedKeys) > 0 {
		groups := *res.SkippedRepeatedKeys
		fmt.Fprintf(out, "Warning: %d repeated-key group%s left in place: the selection held some but not all "+
			"copies of the key, so none of the group's rows were deleted:\n%s",
			len(groups), groupPlural(len(groups)), repeatedKeyCopiesBlock(groups))
		fmt.Fprintf(out, "To delete a group, send its clean copies to quarantine, then delete every copy:\n%s",
			repeatedKeyRecipe(sc, groups))
	}
}

func signatureCount(n int) string {
	if n == 1 {
		return "1 signature"
	}
	return fmt.Sprintf("%d signatures", n)
}

func groupPlural(n int) string {
	if n == 1 {
		return " was"
	}
	return "s were"
}

// indentedList renders values one per line, indented, with no trailing
// newline.
func indentedList(values []string) string {
	lines := make([]string, len(values))
	for i, v := range values {
		lines[i] = "  " + terminal.Sanitize(v)
	}
	return strings.Join(lines, "\n")
}

// repeatedKeyPartialDelete is the error_code of the 409 refusing a delete that
// names some but not all live copies of a repeated key.
const repeatedKeyPartialDelete = "REPEATED_KEY_PARTIAL_DELETE"

// explainDeleteError appends the copies and the recipe to a
// REPEATED_KEY_PARTIAL_DELETE refusal, keeping err in the chain. Any other
// error is returned unchanged.
func explainDeleteError(err error, sc scope) error {
	var apiErr *datasets.APIError
	if !stderrors.As(err, &apiErr) || apiErr.Code != repeatedKeyPartialDelete {
		return err
	}
	groups, ok := repeatedKeyCopies(apiErr.Context)
	if !ok || len(groups) == 0 {
		return fmt.Errorf("%w\n\nSend every clean copy of the key to quarantine with 'qctl rows send-to-quarantine', "+
			"then delete all of its copies in one 'qctl rows delete-quarantined'", err)
	}
	return fmt.Errorf("%w\n\nNothing was deleted. Every live copy of the key must be deleted together:\n%s"+
		"Send the clean copies to quarantine, then delete every copy in one command:\n%s",
		err, repeatedKeyCopiesBlock(groups), strings.TrimSuffix(repeatedKeyRecipe(sc, groups), "\n"))
}

// repeatedKeyCopies decodes context.repeated_key_copies.
func repeatedKeyCopies(errContext map[string]interface{}) ([]api.RepeatedKeyCopiesSchema, bool) {
	raw, ok := errContext["repeated_key_copies"]
	if !ok {
		return nil, false
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, false
	}
	var groups []api.RepeatedKeyCopiesSchema
	if err := json.Unmarshal(b, &groups); err != nil {
		return nil, false
	}
	return groups, true
}

// repeatedKeyCopiesBlock lists each group's key and every copy's table,
// signature and row id, one copy per line, ending in a newline.
func repeatedKeyCopiesBlock(groups []api.RepeatedKeyCopiesSchema) string {
	var b strings.Builder
	for _, g := range groups {
		fmt.Fprintf(&b, "  key %s (group %s):\n", keyValuesPhrase(g.KeyValues), g.GroupID)
		for _, m := range g.Members {
			sig := terminal.Sanitize(m.InitialSignature)
			if sig == "" {
				sig = "(no signature)"
			}
			fmt.Fprintf(&b, "    %-10s %s  row %d\n", terminal.Sanitize(string(m.Table)), sig, m.RowID)
		}
	}
	return b.String()
}

// keyValuesPhrase renders a group's key as column=value pairs sorted by
// column, a null value as null.
func keyValuesPhrase(kv map[string]*string) string {
	cols := make([]string, 0, len(kv))
	for c := range kv {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	pairs := make([]string, len(cols))
	for i, c := range cols {
		v := "null"
		if kv[c] != nil {
			v = *kv[c]
		}
		pairs[i] = terminal.Sanitize(c) + "=" + terminal.Sanitize(v)
	}
	return strings.Join(pairs, ", ")
}

// repeatedKeyRecipe is the send-to-quarantine line for the groups' clean
// copies, when any, and the delete-quarantined line naming every copy's
// signature, each indented and ending in a newline. Both name the table, the
// one scope where both verbs reach every copy.
func repeatedKeyRecipe(sc scope, groups []api.RepeatedKeyCopiesSchema) string {
	table := scope{kind: scopeTable, thirdParty: sc.thirdParty, tableID: sc.tableID, tableName: sc.tableName}
	var clean, sigs []string
	for _, g := range groups {
		for _, m := range g.Members {
			if m.Table == api.RepeatedKeyCopySchemaTableClean {
				clean = append(clean, fmt.Sprintf("--row-id %d", m.RowID))
			}
			if _, err := hex.DecodeString(m.InitialSignature); err == nil && m.InitialSignature != "" {
				sigs = append(sigs, "--signature "+m.InitialSignature)
			}
		}
	}
	var b strings.Builder
	if len(clean) > 0 {
		fmt.Fprintf(&b, "  qctl rows %s %s %s\n", sendToQuarantineVerb, table.tableCommandRef(), strings.Join(clean, " "))
	}
	fmt.Fprintf(&b, "  qctl rows delete-quarantined %s %s --exclude-from-future-uploads=<true|false> --reason <reason>\n", table.tableCommandRef(), strings.Join(sigs, " "))
	return b.String()
}
