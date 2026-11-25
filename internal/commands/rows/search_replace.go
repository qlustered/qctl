package rows

import (
	"fmt"
	"io"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/catalog"
	"github.com/qlustered/qctl/internal/cmdutil"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// searchReplaceVerb is the name cobra registers, the name every refusal spells
// and the name the preview and result lines print, declared once so those
// cannot drift — the precedent resetHoldVerb sets.
const searchReplaceVerb = "search-replace"

// noSelectors is the selector list of a verb that has NONE, and it must be
// non-nil.
//
// support{} spells "this scope is not supported at all" as a nil list, and
// checkScopeFlags tests exactly that (`sup.cloud == nil`, `sup.file == nil`).
// A nil here would therefore refuse the two scopes search-replace works at,
// while an empty non-nil list says the true thing: the scope is supported and
// no flag names rows within it.
var noSelectors = []selectorKind{}

// searchReplaceSupport is `rows search-replace`'s row of the group's matrix,
// and it is the first row in the group with no selectors at all. The SEARCH
// TERM is what names the rows here, so `--row-id`, `--signature`, `--status`
// and `--all` are not registered and resolveSelector is never called — see
// runSearchReplace for what stands in its place in the refusal order.
//
// Scope is the table, optionally narrowed to one cloud source: the request body
// is search_term / replace_term / match_mode / field_name /
// data_source_model_id / dry_run (atlas SearchAndReplaceRequestSchema,
// capi/capi/schemas/data_schema.py) and data_source_model_id is the only
// narrowing on it. There is no stored-item form of the route at all, so `file`
// stays nil, addScopeFlags never registers `--file-id`, and cobra's own
// unknown-flag error refuses it at exit 2 before any request — the same answer
// send-to-quarantine gives `--cloud-source`.
//
// rowIDs and all stay undeclared, and that is not an omission: both are read
// only by the flags this verb does not register. The group-wide guards
// (TestEveryRegisteredRowIDFlagDeclaresAPopulation and its --all twin) walk the
// registered tree for exactly that reason.
//
// isBadData likewise stays false, meaning "not applicable" rather than "acts on
// clean rows": this route carries no is_bad_data parameter, because it has no
// clean-table form to choose between. The population is a fact about the route
// —
// `FROM {bad_rows_table} WHERE {match} AND ingest_job_id_ IS NULL AND
// awaiting_processing_ IS NOT TRUE AND is_deleted_ IS NOT TRUE`
// (atlas search_and_replace_in_bad_rows,
// cettings/.../data_controller/row_overwrite.py) — and the
// verb's own follow-up line declares it once, as a literal true.
var searchReplaceSupport = support{
	table: noSelectors,
	cloud: noSelectors,
}

// The two consequences a user must see BEFORE confirming, and which still print
// under --yes: a script's operator is the reader who most needs them.
//
// Re-validation is the one nobody could predict from the verb's name. A real
// run drains reprocess_dispatch after the commit, flipping unclaimed rows to
// awaiting_processing_ and publishing them per data source, while rows still
// claimed by an active ingestion job defer through a QuarantineReprocessIntent
// (grep _drain_reprocess_dispatch, then mark_bad_rows_for_reprocess in
// quarantine_reprocess.py). Those deferred rows were WRITTEN — the UPDATE
// returned them and they are in replaced_rows; only their re-validation waits.
//
// The note must not call them skipped, and must not say a busy row is skipped
// either. A row an ingestion job already holds, and a row already queued, are
// excluded by the candidate SELECT itself (`ingest_job_id_ IS NULL AND
// awaiting_processing_ IS NOT TRUE AND is_deleted_ IS NOT TRUE`): they never
// reach matched_rows, so they cannot reach skipped_rows. skipped_rows has two
// reachable feeders: a replacement that changed nothing, and the narrow race —
// the batched UPDATE re-tests `b.awaiting_processing_ IS NOT TRUE`, so a row
// queued between the SELECT and the UPDATE is not returned and IS counted
// skipped. External callers are scoped to their assigned sources.
//
// The revocation is UNCONDITIONAL; only its consequence depends on the table:
// the batched UPDATE writes `keep_in_quarantine_ =
// NULLIF(b.keep_in_quarantine_, FALSE)` with no guard around it (row_overwrite.py
// search_and_replace_in_bad_rows), the same clause set-cell's path writes — see
// setCellHoldNote.
const (
	searchReplaceReprocessNote = "Replaced rows are queued for re-validation, and rows an ingestion job holds are replayed when it drains. Rows mid-ingest or already queued are never matched at all; 'skipped' counts matched rows the write left alone."
	searchReplaceHoldNote      = "Replacing revokes an approval and leaves a held row held: an approved row becomes undecided, waiting for approval while it keeps an unclosed submitted review and otherwise held for approval again when it is a suspected duplicate or the table holds rows until approved or its data-review policy requires review."
)

func newSearchReplaceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   searchReplaceVerb + " [table-id-or-name]",
		Short: "Find and replace text across a table's quarantined rows",
		Long: `Find and replace text in the QUARANTINED rows of a table.

  qctl rows search-replace orders --search "N/A" --replace "" --dry-run
  qctl rows search-replace orders --search "N/A" --replace "unknown" --field state
  qctl rows search-replace orders --search '^\s+' --replace "" --regex --field city

Quarantined rows only, by construction: the route reads the bad-rows table and
has no clean-table form. Rows an ingestion job has claimed, rows already queued
for processing and deleted rows are never matched.

There is no row selector — the search term IS the selector. Narrow with --field
to one field and with --cloud-source/--cloud-source-id to one source; without
--field every non-deleted field of the table is searched.

THE PREVIEW IS MANDATORY. Without --yes the command first runs the route's own
dry run, prints the match count, and only then asks to confirm; when nothing
matches it stops there and writes nothing. --dry-run stops after the preview
whatever the count. --yes writes in a single request and shows no preview.

Match mode is the 2x2 of --regex and --case-sensitive: neither is a
case-insensitive substring (the backend default), --case-sensitive alone a
case-sensitive substring, --regex alone a case-insensitive regular expression,
and both a case-sensitive one.

The two substring modes take Excel's wildcards: * matches any run of
characters, ? exactly one, and ~ before *, ? or ~ finds that character itself.
Everything else is literal, spaces included. A wildcard replaces the whole span
it matched, so --search "N*A" --replace X turns "Name: N/A" into "X".

--replace is inserted as typed in the substring modes. With --regex it is a
Python re.sub template, so \1 is a group reference and \n a newline, and a
template the server cannot expand is a 422 ("Invalid replacement template:
...") on the preview as well as the write.

Patterns are never checked here. With --regex the server reads the pattern as
Python re and refuses a bad one with a 422 before it reads a row: "Invalid
regex pattern: ..." when Python cannot compile it, "Regex pattern uses ...,
which cannot be translated for PostgreSQL." when the database, which runs a
translation of it, cannot express it, and "Regex pattern cannot be run in
PostgreSQL (...)." when the database cannot compile that translation, as with
a nested repeat such as (?:a{1000}){1000}. A backreference is refused under
--regex alone; add --case-sensitive. All three exit 2. Without --regex every
non-empty term is valid.

--yes writes every matching cell in ONE uncapped transaction: the preview count
is the size of the write, not a sample of it.

The match count is summed PER FIELD when --field is absent: a row matching in
two fields is counted twice. A dry run reports that count and nothing else — it
cannot know what a real run would skip.

'qctl guide edit-data' shows how to read who changed a cell, when and why: the
table's history and each row's own change log.

-o json|yaml prints {fields_affected, matched_rows, replaced_rows,
skipped_rows} — the preview's under --dry-run or when nothing matches, the
write's otherwise — and moves the preview and result lines to stderr.`,
		Annotations: cmdutil.WithArgKinds(cmdutil.WithFormats(catalog.For("data"), "table", "json", "yaml"), "table"),
		Args:        cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchReplace(cmd, firstArg(args))
		},
	}
	addScopeFlags(cmd, searchReplaceSupport)
	cmd.Flags().String("search", "", "Text or pattern to find (required)")
	cmd.Flags().String("replace", "", "Text to replace each match with (required; \"\" deletes the match). Inserted as typed, except that with --regex it is a Python re.sub template, so \\1 is a group reference and \\n a newline")
	cmd.Flags().String("field", "", "Limit to this field (default: every non-deleted field of the table)")
	cmd.Flags().Bool("regex", false, "Read --search as a regular expression instead of a substring")
	cmd.Flags().Bool("case-sensitive", false, "Match case (the default match is case-insensitive)")
	cmd.Flags().Bool("dry-run", false, "Print the match count and make no change")
	addYesFlag(cmd)
	return cmd
}

// searchReplaceTerms is the verb's flag set once every refusal decidable from
// the command line has already fired. Nothing in it cost a request.
type searchReplaceTerms struct {
	search  string
	replace string
	field   string
	mode    api.SearchMatchMode
	dryRun  bool
	yes     bool
}

// searchReplaceFlags is every refusal this verb decides from the flags alone,
// and it stands where resolveSelector stands for the other eleven verbs: after
// the scope refusals, before Bootstrap. Every one is exit 2 through
// qerrors.NewBadUsage and none costs a request.
//
// --search is required AND non-empty (the schema's min_length=1 would be a 422
// round trip), while --replace is required by PRESENCE only: "" is a legal and
// useful value there — it deletes the match — so testing the value would make
// the documented way to delete text unreachable.
func searchReplaceFlags(cmd *cobra.Command) (searchReplaceTerms, error) {
	var t searchReplaceTerms

	if !cmd.Flags().Changed("search") {
		return t, qerrors.NewBadUsagef(
			"'qctl rows %s' needs --search <term>: the search term is what names the rows", searchReplaceVerb)
	}
	t.search, _ = cmd.Flags().GetString("search")
	if t.search == "" {
		return t, qerrors.NewBadUsage(
			"--search cannot be empty: it names the rows to act on, and an empty term would name every row")
	}

	if !cmd.Flags().Changed("replace") {
		return t, qerrors.NewBadUsagef(
			"'qctl rows %s' needs --replace <text>: pass --replace \"\" to delete the matched text", searchReplaceVerb)
	}
	t.replace, _ = cmd.Flags().GetString("replace")

	t.dryRun, _ = cmd.Flags().GetBool("dry-run")
	t.yes, _ = cmd.Flags().GetBool("yes")
	if t.dryRun && t.yes {
		// Refused rather than ignored, the rule `download data` sets: --dry-run
		// never writes, so the prompt --yes skips was never going to be shown
		// and the flag would change nothing while reading as though it had.
		return t, qerrors.NewBadUsage(
			"--dry-run and --yes cannot be combined: --dry-run never writes, so there is no prompt for --yes to skip")
	}

	t.field, _ = cmd.Flags().GetString("field")
	if cmd.Flags().Changed("field") && t.field == "" {
		// `--field ""` marks the flag Changed while naming nothing. Left
		// unchecked it reaches the wire as field_name:"" — not the null that
		// means "every field" — and earns a 422 for a field that does not
		// exist. Same guard, same reason, as checkScopeFlags' `--cloud-source ""`.
		return t, qerrors.NewBadUsage(
			"--field cannot be empty: name the field, or omit --field to search every field")
	}

	regex, _ := cmd.Flags().GetBool("regex")
	caseSensitive, _ := cmd.Flags().GetBool("case-sensitive")
	t.mode = matchMode(regex, caseSensitive)
	return t, nil
}

// matchMode is the full 2x2 of --regex x --case-sensitive, and all four
// generated values are reachable.
//
// The mapping is the backend's own table, not a qctl invention:
// MATCH_MODE_TO_FLAGS maps each mode to (use_regex, case_sensitive) as
// contains->(F,T), contains_case_insensitive->(F,F), regex->(T,T),
// regex_case_insensitive->(T,F) (atlas common/common/normalization.py).
// Neither flag is therefore the schema's own default,
// contains_case_insensitive (SearchAndReplaceRequestSchema.match_mode,
// capi/capi/schemas/data_schema.py).
func matchMode(regex, caseSensitive bool) api.SearchMatchMode {
	switch {
	case regex && caseSensitive:
		return api.Regex
	case regex:
		return api.RegexCaseInsensitive
	case caseSensitive:
		return api.Contains
	default:
		return api.ContainsCaseInsensitive
	}
}

// request is the domain-client request for one call. dryRun is the caller's,
// never t.dryRun: the preview and the write differ in that one field and
// reading it from the flag would make the write inherit the preview's value.
func (t searchReplaceTerms) request(sc scope, dryRun bool) datasets.SearchReplaceRequest {
	req := datasets.SearchReplaceRequest{
		SearchTerm:  t.search,
		ReplaceTerm: t.replace,
		MatchMode:   t.mode,
		DryRun:      dryRun,
	}
	if t.field != "" {
		field := t.field
		req.FieldName = &field
	}
	if sc.kind == scopeCloudSource {
		id := sc.cloudSourceID
		req.DataSourceModelID = &id
	}
	return req
}

// fieldPhrase names what the run searches, and is the ONE place the "every
// field" wording lives: the prompt, the preview line's per-field caveat and the
// result line all read it, so they cannot disagree about the scope of the
// search.
func (t searchReplaceTerms) fieldPhrase() string {
	if t.field == "" {
		return "every field"
	}
	return fmt.Sprintf("field %s", shellQuote(t.field))
}

// actionPhrase is the sentence the prompt asks and the non-interactive refusal
// tells the user to pass --yes to. One builder, two leads — the shape
// confirmAction uses for the other eleven verbs.
//
// shellQuote, not %q, and the difference is not cosmetic: %q is GO quoting, so
// the documented regex example `^\s+` is shown back as "^\\s+" — a pattern
// that means a literal backslash followed by an s, which is not the one about
// to run. The prompt is the last place to catch a wrong pattern, so it must
// render the term the way the user typed it.
func (t searchReplaceTerms) actionPhrase(sc scope) string {
	return fmt.Sprintf("replace %s with %s in %s of %s, matching with %s",
		shellQuote(t.search), shellQuote(t.replace), t.fieldPhrase(), sc.label(), t.mode)
}

func runSearchReplace(cmd *cobra.Command, tableArg string) error {
	// The group's refusal order, with searchReplaceFlags standing where
	// resolveSelector stands for the other eleven verbs: scope refusals, then
	// the verb's own flag refusals, then Bootstrap, then the lookups that cost
	// requests. See resolveTarget for why the order is the contract.
	sc, f, err := resolveScopeOnly(cmd, tableArg, searchReplaceSupport, searchReplaceVerb)
	if err != nil {
		return err
	}
	terms, err := searchReplaceFlags(cmd)
	if err != nil {
		return err
	}
	ctx, sc, err := remoteScope(cmd, sc, tableArg, f)
	if err != nil {
		return err
	}

	client := datasets.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity).WithThirdParty(sc.thirdParty)
	token := ctx.Credential.AccessToken
	w := cmdutil.NewResultWriter(cmd)

	if !terms.yes {
		preview, err := client.SearchAndReplace(token, sc.tableID, terms.request(sc, true))
		if err != nil {
			return err
		}
		reportSearchReplacePreview(w.Human, sc, terms, preview)
		if terms.dryRun {
			return w.Write(preview)
		}
		if preview.MatchedRows == 0 {
			// The UI's own rule (Replace is enabled only when the dry run
			// counted something), and here it also saves the write: a run with
			// nothing to match would answer 0/0/0 at exit 0 after a second
			// round trip, which reads like a failure rather than a no-op.
			fmt.Fprintf(w.Human,
				"Nothing to replace: no quarantined row in %s matches.\n", sc.label())
			return w.Write(preview)
		}
	}

	// The consequences print on BOTH paths, the group's rule: under --yes they
	// are the only warning a script gets.
	out := cmd.ErrOrStderr()
	fmt.Fprintf(out, "%s\n%s\n", searchReplaceReprocessNote, searchReplaceHoldNote)

	// One phrase, two leads — the shape confirmAction gives the other nine
	// verbs, so the question and the refusal cannot describe different writes.
	phrase := terms.actionPhrase(sc)
	nonInteractive := fmt.Sprintf(
		"confirmation required but stdin is not an interactive terminal — pass --yes to %s", phrase)
	prompt := strings.ToUpper(phrase[:1]) + phrase[1:] + "?"
	if err := cmdutil.Confirm(cmd.ErrOrStderr(), terms.yes, nonInteractive, prompt); err != nil {
		return err
	}

	res, err := client.SearchAndReplace(token, sc.tableID, terms.request(sc, false))
	if err != nil {
		return err
	}
	reportSearchReplaceResult(w.Human, sc, terms, res)
	return w.Write(res)
}

// reportSearchReplacePreview prints the one line a dry run produces.
//
// Only MatchedRows is read, and the per-field caveat is attached when --field
// is absent, because the dry run computes nothing else: the controller adds
// `total_matched += len(rows)` once per field and then `continue`s before a
// single row is examined (atlas search_and_replace_in_bad_rows,
// row_overwrite.py). ReplacedRows, SkippedRows and FieldsAffected are therefore
// zero on a preview and say nothing about what a real run would do — printing
// them would invent three facts out of a response that carries none.
func reportSearchReplacePreview(out io.Writer, sc scope, terms searchReplaceTerms, res *datasets.SearchReplaceResult) {
	line := fmt.Sprintf("%s (dry run): %d matching row(s) in %s",
		searchReplaceVerb, res.MatchedRows, sc.label())
	if terms.field == "" {
		line += ", counted once per field a row matches in"
	}
	fmt.Fprintln(out, line)
}

// reportSearchReplaceResult prints the write's own three counts and the fields
// it touched, then the line that reads the change back.
//
// fields_affected is appended only where it is meaningful: an empty list after
// a real run means no field had a replacement applied, which is a different
// statement from "the server did not say", and the two renderings keep them
// apart.
func reportSearchReplaceResult(out io.Writer, sc scope, terms searchReplaceTerms, res *datasets.SearchReplaceResult) {
	line := fmt.Sprintf("%s: %d row(s) replaced, %d skipped (%d matched) in %s",
		searchReplaceVerb, res.ReplacedRows, res.SkippedRows, res.MatchedRows, sc.label())
	if len(res.FieldsAffected) == 0 {
		line += "; no field changed"
	} else {
		line += "; fields: " + strings.Join(res.FieldsAffected, ", ")
	}
	fmt.Fprintln(out, line)

	printFollowUp(out, searchReplaceFollowUp(sc, terms))
}

// searchReplaceFollowUp is the 'qctl get data' line that reads the change back,
// built on scope.getDataLine so it carries the cloud-source narrowing this run
// acted within — the group's one builder, never a hand-built line.
//
// The literal true is this verb's single declaration of its population, and it
// has nothing on the wire to disagree with: the route takes no is_bad_data
// parameter because it addresses the quarantine table by construction.
//
// It searches for the REPLACEMENT rather than the original term, because after
// the write the original is what is no longer there. An EMPTY replacement has
// nothing to search for — `--search-query ""` is not a narrowing — so the whole
// recipe is omitted rather than degraded into a line that reads back the entire
// quarantine.
func searchReplaceFollowUp(sc scope, terms searchReplaceTerms) string {
	if terms.replace == "" {
		return ""
	}
	line := sc.getDataLine(true) + " --search-query " + shellArg(terms.replace)
	if terms.field != "" {
		line += " --search-by-field-name " + shellArg(terms.field)
	}
	return line
}
