package rows

import (
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/stored_items"
	"github.com/spf13/cobra"
)

type scopeKind int

const (
	scopeTable scopeKind = iota
	scopeCloudSource
	scopeFile
)

type scope struct {
	thirdParty         bool
	kind               scopeKind
	tableID            int
	tableName          string
	cloudSourceID      int
	cloudSourceName    string
	cloudSourceTableID *int
	fileID             int

	// hydrate is set ONLY when a refusal message rendered from this scope would
	// otherwise be missing a value, and it is the one place in the group where
	// building an error message may cost a request. See scope.forMessage.
	//
	// It is nil on every other scope, which is what makes "a client-side
	// refusal costs no request" structural rather than a promise: there is
	// nothing to call.
	hydrate func(scope) (scope, bool)
}

// kindName names the scope in every refusal this group prints, including
// unroutableSelector's — the message a fourth scopeKind is MOST likely to
// produce, since the refusing defaults exist for exactly that edit.
//
// So "table" is a named arm and the default is not it. A default that said
// "table" would make `unroutableSelector` report "at table scope" for a scope
// that is not the table, i.e. lie in the one message written to be read during
// the debugging session the bug caused. There is nothing honest to say about an
// unnamed kind, so it says only that, and the number tells the reader which
// constant to go and look at.
//
// scope.label() gets the same treatment, and for a stronger reason: the
// confirmation PROMPT renders label() and runs BEFORE the route dispatch, so
// label() is the first thing an unknown scopeKind would print — a prompt saying
// "Delete every row in table orders" for a scope that is not the table is a
// user acting on a wrong belief, not just a confusing bug report.
func (s scope) kindName() string {
	switch s.kind {
	case scopeTable:
		return "table"
	case scopeCloudSource:
		return "cloud-source"
	case scopeFile:
		return "file"
	default:
		return fmt.Sprintf("unknown-scope-%d", int(s.kind))
	}
}

// tableRef is what a follow-up command line should say: the name when we have
// one, the id otherwise, and a PLACEHOLDER when neither is known.
//
// The placeholder is reachable only from a refusal message, because selector
// refusals are now decided BEFORE the table is resolved (see resolveTarget):
// `qctl rows delete-quarantined --row-id 5` names no table at all, so the recipe in
// the refusal has nothing to name either. It must not render "0" — that is a
// line that RUNS, against whatever dataset 0 is, rather than one the user can
// see they have to complete.
func (s scope) tableRef() string {
	return idRef(s.tableID, s.tableName, "<table>")
}

// tableCommandRef renders the table argument for a runnable command line.
// Most references stay positional, but a name strconv.Atoi accepts would be
// reinterpreted as an ID by the positional resolver. Preserve that identity
// with the standard name-only flag. The commands this package emits lines for
// all accept --table. Shared-table recipes also retain --shared.
func (s scope) tableCommandRef() string {
	ref := s.tableArgument()
	if s.thirdParty {
		ref += " --shared"
	}
	return ref
}

func (s scope) tableArgument() string {
	if s.tableName == "" && s.tableID <= 0 {
		return "<table>"
	}
	if s.tableName != "" {
		if _, err := strconv.Atoi(s.tableName); err == nil {
			return "--table " + shellArg(s.tableName)
		}
	}
	return shellArg(s.tableRef())
}

// idRef renders the reference a pasteable command line should carry: the name
// if there is one, else the id, else the placeholder. Every line qctl prints
// here is meant to be copied, so an unknown value has to LOOK unknown.
func idRef(id int, name, placeholder string) string {
	switch {
	case name != "":
		return name
	case id > 0:
		return strconv.Itoa(id)
	default:
		return placeholder
	}
}

// getDataLine renders this scope as a 'qctl get data' command line. It is the
// ONE builder behind every such line qctl prints, for EVERY verb in this group
// and not just the delete pair: the "See the current state with" follow-up after a row
// action, and the signature recipe in the --row-id refusal. A verb that wants
// to print one calls this; it does not hand-build a line of its own.
//
// They were built separately once and drifted — the follow-up narrowed to the
// cloud source or file that was acted on while the recipe named the whole
// table. On a CONTENT signature that is not cosmetic: two files can carry the
// same initial_signature_, so a signature read off an un-narrowed listing can
// name a different row than the one on screen, and the row it deletes gets an
// exclusion entry that keeps the line from loading again.
//
// isBadData picks the population, and every caller reads it from the ONE place
// its verb declares it. There are three such places, one per route family:
//
//   - delete-quarantined: support.isBadData, which runDelete also puts on the
//     wire as the delete route's is_bad_data parameter;
//   - the two move verbs: the api.QuarantineHold runMove sends, read at the
//     call site as `hold == api.QuarantineHoldHold` — a parked row (hold) is
//     read back from the quarantine table, a reprocessed one (undecided) from
//     the clean table it returns to;
//   - reprocess-quarantined, hold, release and reset-hold: a literal true,
//     because their routes have no population parameter to read and no
//     population choice to make. Every one of them addresses the bad-rows table
//     at every scope — reprocess through
//     nullify_alert_id_and_mark_bad_rows_for_processing, the other three
//     through _update_keep_in_quarantine_returning's
//     `UPDATE {bad_rows_table}` (cettings/.../data_controller/quarantine.py),
//     which is the same statement whichever of the three values it writes —
//     so there is exactly one answer and the verb's own name is it.
//
// In the first two cases the value that renders the line IS the value that went
// on the wire, or is derived from it in one expression at the call site; in the
// third there is nothing on the wire to disagree with. Either way the line
// always names the rows the verb's own route acts on.
//
// The quarantine STATUS is deliberately NOT carried across. --status
// failed_validation matches rows whose column is NULL while every 'get data'
// filter matches only FALSE, so a status flag here would render a line that
// reads back a different set than the one acted on — the exact mismatch
// 'rows delete-quarantined --help' warns about.
func (s scope) getDataLine(isBadData bool) string {
	// shellArg, not tableRef raw: --table's own usage says names containing
	// spaces are legal and must be quoted, and `qctl get data my table` is two
	// positionals -- cobra refuses it at exit 2. The run that quotes the
	// --replace and --value it added must quote the name it inherited too.
	line := "qctl get data " + s.tableCommandRef()
	switch s.kind {
	case scopeCloudSource:
		// `get data` has no --cloud-source, so a NAME will not do here and the
		// id is the only narrowing this line can carry. When the user named the
		// source and the lookup that would have turned it into an id failed,
		// the placeholder keeps the line NARROWED — see scope.forMessage for
		// why an un-narrowed line is not an acceptable fallback.
		line += " --data-source-model-id " + idRef(s.cloudSourceID, "", "<cloud-source-id>")
	case scopeFile:
		// No placeholder: --file-id is an id by construction and
		// checkScopeFlags has already refused a non-positive one, so file scope
		// always knows its own number without asking anyone.
		line += fmt.Sprintf(" --stored-item-id %d", s.fileID)
	}
	if isBadData {
		line += " --is-bad-data"
	}
	return line
}

// deletedRowsLine is the `get deleted-rows` command line for the scope's
// table, narrowed to its cloud source at cloud-source scope. File scope is not
// narrowed: the file route deletes matching quarantine rows of every source
// (atlas-9po84.5), so a --file-id listing would hide some of them. A shared
// table has none: the listing has no third-party route.
func (s scope) deletedRowsLine() string {
	if s.thirdParty {
		return ""
	}
	line := "qctl get deleted-rows " + s.tableArgument()
	if s.kind == scopeCloudSource {
		line += " --cloud-source-id " + idRef(s.cloudSourceID, "", "<cloud-source-id>")
	}
	return line
}

// tableLabel names the table the way a prompt should read it: "orders (id: 42)"
// when a name is known, and the bare id when it is not.
//
// The parenthesis is the whole point of the line — it tells a user who typed a
// NAME which dataset that name resolved to, which is the one fact a
// confirmation prompt can give them that they did not already type. When the
// table was named BY id there is no second fact, and "table 42 (id: 42)" prints
// the same number twice. The two renderings share one function so a scope that
// gains a table reference cannot reintroduce the doubled id.
//
// It reads tableRef, so the placeholder case ("<table>") is inherited rather
// than re-decided: a label built before the table is known says so instead of
// claiming dataset 0.
func (s scope) tableLabel() string {
	if s.tableName == "" {
		return s.tableRef()
	}
	return fmt.Sprintf("%s (id: %d)", s.tableRef(), s.tableID)
}

// label names the scope in a confirmation prompt and a result line.
//
// Named arms and a default that is not the table, for the reason kindName
// records: the prompt runs before any route dispatch, so this is where an
// unknown scopeKind surfaces FIRST, and calling it the table there tells the
// user the action lands somewhere it does not. The default reads the name from
// kindName so the two renderings cannot drift.
func (s scope) label() string {
	label := s.scopeLabel()
	if s.thirdParty {
		return "shared " + label
	}
	return label
}

func (s scope) scopeLabel() string {
	switch s.kind {
	case scopeCloudSource:
		if s.cloudSourceName != "" {
			return fmt.Sprintf("cloud source %s (id: %d) of table %s", s.cloudSourceName, s.cloudSourceID, s.tableRef())
		}
		return fmt.Sprintf("cloud source %d of table %s", s.cloudSourceID, s.tableRef())
	case scopeFile:
		return fmt.Sprintf("file %d of table %s", s.fileID, s.tableRef())
	case scopeTable:
		return "table " + s.tableLabel()
	default:
		return fmt.Sprintf("%s of table %s", s.kindName(), s.tableLabel())
	}
}

// addScopeFlags registers how a verb is pointed at rows.
//
// The table selector pair is unconditional: every verb acts on a table, so it
// is the object being named, and cmdutil.AddTableFlags is the shipped way to
// name one unambiguously (a table can be literally called "42"). The narrowing
// flags are conditional, so a verb that is table-only (send-to-quarantine,
// reprocess-clean) has no --cloud-source at all and cobra's own unknown-flag
// error is the right answer there.
func addScopeFlags(cmd *cobra.Command, sup support) {
	cmdutil.AddTableFlags(cmd, "Table (dataset)")
	cmdutil.AddSharedDataFlag(cmd)
	cmd.Long += "\n\nUse --shared for a table shared from another organization.\nCloud-source narrowing then requires --cloud-source-id; --cloud-source names,\nignore-rule and unignore-rule are not supported for shared tables."
	if sup.cloud != nil {
		cmd.Flags().String("cloud-source", "", "Narrow to this cloud source by NAME (exact, case-sensitive)")
		cmdutil.RefInt(cmd.Flags(), "cloud-source-id", "cloud-source", "Narrow to this cloud source by ID")
	}
	if sup.file != nil {
		cmdutil.RefInt(cmd.Flags(), "file-id", "file", "Narrow to this file (stored item) by ID")
	}
}

func addYesFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("yes", false, "Skip the confirmation prompt")
}

// scopeFlags is the narrowing the user asked for, once every refusal that can
// be decided from the flags alone has already fired. Nothing in it cost a
// request.
type scopeFlags struct {
	cloud  bool
	file   bool
	csID   int
	csName string
	fileID int
}

// resolveTarget is the front half of every one of the eleven verbs with a row
// selector, and it exists so the ORDER below is written once instead of eleven
// times.
//
// The order is the contract, not a convenience:
//
//  1. localScope — every scope refusal the flags alone decide.
//  2. resolveSelector — every selector refusal the flags alone decide.
//     2b. localChecks — the verb's OWN flag refusals, when it has any.
//  3. cmdutil.Bootstrap — credentials, --server, -O/--org.
//  4. hydrateScope — the lookups that cost requests.
//
// CLAUDE.md's contract is that a client-side validation failure exits 2, and
// steps 1 and 2 are exactly those failures. With the resolve ahead of them,
// `qctl rows hold orders` — a missing selector, decidable from the command line
// — spent a credential read and a GET /datasets and then exited 1 on whatever
// the network said, while `qctl rows hold 42` exited 2. The same typo had two
// exit codes depending on whether the table was named or numbered.
//
// Bootstrap sits at 3 rather than 1 deliberately: a usage error in the command
// line the user just typed should be reported before a config error in a file
// they did not. Its own refusals still fire before anything is mutated.
//
// localChecks is step 2b: a verb's own flag refusals, for a verb that has flags
// beyond the selector. It is variadic so the eight verbs that have none of its
// own pass
// nothing and read exactly as before. It exists rather than each such verb
// spelling the order out again: runSearchReplace already writes a second copy
// (resolveScopeOnly, its own flags, remoteScope) because there was no slot for
// these, and a third copy is how an ordering drifts. Every check runs BEFORE
// Bootstrap, so a wrong command line still costs no request.
func resolveTarget(cmd *cobra.Command, tableArg string, sup support, verb string, localChecks ...func(*cobra.Command) error) (*cmdutil.CommandContext, scope, selector, error) {
	sc, f, err := localScope(cmd, tableArg, sup, verb)
	if err != nil {
		return nil, scope{}, selector{}, err
	}
	sel, err := resolveSelector(cmd, sc, sup, verb)
	if err != nil {
		return nil, scope{}, selector{}, err
	}
	for _, check := range localChecks {
		if err := check(cmd); err != nil {
			return nil, scope{}, selector{}, err
		}
	}
	ctx, sc, err := remoteScope(cmd, sc, tableArg, f)
	if err != nil {
		return nil, scope{}, selector{}, err
	}
	return ctx, sc, sel, nil
}

// resolveScopeOnly is resolveTarget for a verb that HAS no row selector, and
// `rows search-replace` is the only one: its search term is what names the
// rows, so there is no selector flag to resolve and step 2 of the order above
// is the verb's own flag validation instead.
//
// It is written as the same three shared calls rather than a second copy of
// them, because the ORDER is the contract resolveTarget's doc records and a
// copy is how an ordering drifts. The caller slots its own refusals between
// localScope and remoteScope, which is exactly where resolveSelector sits, so
// the guarantee that survives is the one that matters: every refusal decidable
// from the command line fires at exit 2 before any request is made.
func resolveScopeOnly(cmd *cobra.Command, tableArg string, sup support, verb string) (scope, scopeFlags, error) {
	return localScope(cmd, tableArg, sup, verb)
}

// remoteScope is steps 3 and 4 — the halves that cost requests. Split out so
// resolveTarget and the selector-less verbs share one copy of them.
func remoteScope(cmd *cobra.Command, sc scope, tableArg string, f scopeFlags) (*cmdutil.CommandContext, scope, error) {
	if err := cmdutil.ValidateTableSelector(cmd, tableArg); err != nil {
		return nil, scope{}, err
	}
	ctx, err := cmdutil.Bootstrap(cmd)
	if err != nil {
		return nil, scope{}, err
	}
	sc, err = hydrateScope(cmd, ctx, sc, tableArg, f)
	if err != nil {
		return nil, scope{}, err
	}
	return ctx, sc, nil
}

// localScope is the scope as far as the command line alone can decide it, and
// it makes NO request. What it returns is enough for every selector refusal:
// resolveSelector and everything it calls read sc.kind (support.at,
// scope.kindName, scopesSupporting) plus, on the one refusal that prints a
// recipe, the references scope.getDataLine renders.
//
// The narrowing branch is entered only when the user actually passed one of the
// flags — see checkScopeFlags for why that matters.
func localScope(cmd *cobra.Command, tableArg string, sup support, verb string) (scope, scopeFlags, error) {
	thirdParty, err := cmdutil.SharedData(cmd, "cloud-source")
	if err != nil {
		return scope{}, scopeFlags{}, err
	}
	f, err := checkScopeFlags(cmd, sup, verb)
	if err != nil {
		return scope{}, scopeFlags{}, err
	}
	if err := validateSharedScope(thirdParty, verb); err != nil {
		return scope{}, scopeFlags{}, err
	}

	// --table-id is read with the shared validator rather than left to
	// cmdutil.ResolveTableID in hydrateScope, so a non-positive id is still
	// refused for free. Same message, same exit code — this is the call, not a
	// copy of it.
	tableID, _, err := cmdutil.PositiveIDFlag(cmd, "table-id")
	if err != nil {
		return scope{}, scopeFlags{}, err
	}

	sc := scope{thirdParty: thirdParty, kind: scopeTable, tableID: tableID, tableName: tableNameFrom(cmd, tableArg)}
	if sc.tableID == 0 {
		// A numeric positional is an id too, and knowing it here costs nothing:
		// datasets.Client.ResolveID strconv.Atoi's the positional before it
		// ever calls the API (internal/datasets/client.go), so this reads
		// the same answer the resolve would have returned.
		if id, err := strconv.Atoi(tableArg); err == nil && id > 0 {
			sc.tableID = id
		}
	}

	switch {
	case f.file:
		sc.kind, sc.fileID = scopeFile, f.fileID
	case f.cloud:
		// f.csID is 0 when the user named the source instead of numbering it,
		// and that is the ONE value a refusal message cannot get for free.
		sc.kind, sc.cloudSourceID = scopeCloudSource, f.csID
		if f.csID == 0 {
			sc.hydrate = func(s scope) (scope, bool) {
				ctx, err := cmdutil.Bootstrap(cmd)
				if err != nil {
					return s, false
				}
				out, err := hydrateScope(cmd, ctx, s, tableArg, f)
				if err != nil {
					return s, false
				}
				return out, true
			}
		}
	}
	return sc, f, nil
}

func validateSharedScope(shared bool, verb string) error {
	if !shared {
		return nil
	}
	if verb == ignoreRuleVerb || verb == unignoreRuleVerb {
		return qerrors.NewBadUsagef("rows %s does not support --shared", verb)
	}
	return nil
}

// hydrateScope resolves table and source identifiers and validates file membership.
func hydrateScope(cmd *cobra.Command, ctx *cmdutil.CommandContext, sc scope, tableArg string, f scopeFlags) (scope, error) {
	tableID, err := cmdutil.ResolveDataTableID(ctx, cmd, tableArg, sc.thirdParty)
	if err != nil {
		return scope{}, err
	}
	sc.tableID = tableID
	if f.file {
		if err := validateFileTable(ctx, sc); err != nil {
			return scope{}, err
		}
	}

	if f.cloud && sc.thirdParty {
		sc.cloudSourceID = f.csID
		sc.cloudSourceTableID = &tableID
	} else if f.cloud {
		cs, err := cmdutil.ResolveCloudSourceRecord(ctx, cmdutil.ResolvedTable{ID: sc.tableID, Name: sc.tableName}, f.csID, f.csName)
		if err != nil {
			return scope{}, err
		}
		sc.cloudSourceID, sc.cloudSourceName = cs.ID, cs.Name
		if cs.TableSeriesID != nil {
			sc.cloudSourceTableID = &tableID
		}
	}
	// Nothing a message can want is missing any more, so drop the hook: a
	// second call to forMessage on a hydrated scope must not pay for the same
	// lookups again.
	sc.hydrate = nil
	return sc, nil
}

// validateFileTable reads the file's table and refuses a file of any table
// other than sc.tableID. A shared scope reads the third-party file route; any
// failed read is returned as an error.
func validateFileTable(ctx *cmdutil.CommandContext, sc scope) error {
	siClient, err := stored_items.NewClient(ctx.ServerURL, ctx.OrganizationID, ctx.Verbosity)
	if err != nil {
		return fmt.Errorf("failed to create files client: %w", err)
	}
	datasetID, err := fileTableID(ctx, siClient, sc)
	if err != nil {
		return err
	}
	if datasetID <= 0 {
		return fmt.Errorf("cannot verify table for file %d: file metadata has no valid table ID", sc.fileID)
	}
	if datasetID != sc.tableID {
		return qerrors.NewBadUsagef("file %d belongs to table %d, not selected table %d", sc.fileID, datasetID, sc.tableID)
	}
	return nil
}

func fileTableID(ctx *cmdutil.CommandContext, siClient *stored_items.Client, sc scope) (int, error) {
	token := ctx.Credential.AccessToken
	if !sc.thirdParty {
		file, err := siClient.GetStoredItem(token, sc.fileID)
		if err != nil {
			return 0, fmt.Errorf("cannot verify table for file %d: %w", sc.fileID, err)
		}
		return file.DatasetID, nil
	}
	file, err := siClient.GetThirdPartyStoredItem(token, sc.fileID)
	if err != nil {
		if qerrors.GetExitCode(err) == qerrors.ExitNotFound {
			return 0, fmt.Errorf("cannot verify table for file %d: %w\n\nA server without the shared file read also answers 404; deleting rows of a shared table's file needs a server that has it", sc.fileID, err)
		}
		return 0, fmt.Errorf("cannot verify table for file %d: %w", sc.fileID, err)
	}
	return file.DatasetID, nil
}

// forMessage is the scope a REFUSAL MESSAGE renders from, and the one place in
// this group where building an error may cost a request.
//
// It exists for a single case: `--cloud-source <name>` together with a refused
// `--row-id` on a delete verb. That refusal's whole value is the recipe it
// prints, the recipe narrows with --data-source-model-id, and `qctl get data`
// has no --cloud-source flag — so a name will not do and only a lookup yields
// the id. Printing an UN-NARROWED recipe instead is not an option:
// initial_signature_ is a content hash, so a signature read off the whole
// table's listing can name a row of a different source, which a cloud-source
// delete skips while still answering ok.
//
// The lookup's error is deliberately DISCARDED rather than returned. The caller
// is already returning a bad-usage error; letting a network failure here
// replace it would turn a client-side exit 2 into an exit 1, which is the exact
// defect resolveTarget's ordering exists to remove. The fallback is
// getDataLine's <cloud-source-id> placeholder: still narrowed, and visibly
// incomplete rather than quietly wrong.
func (s scope) forMessage() scope {
	if s.hydrate == nil {
		return s
	}
	if out, ok := s.hydrate(s); ok {
		return out
	}
	return s
}

// checkScopeFlags is every scope refusal decidable from the flags alone. All of
// them run before the table resolve, so a wrong command line never costs a
// request — including the two cloud-source ones, which is why the flags are
// read here rather than at the point of use.
func checkScopeFlags(cmd *cobra.Command, sup support, verb string) (scopeFlags, error) {
	// --cloud-source and --cloud-source-id are one selector, as they are on
	// `get files` and `upload file`. Without this the id wins silently and the
	// name the user also typed is never checked against it.
	if err := cmdutil.ValidateConflictingFlags(cmd, []string{"cloud-source-id", "cloud-source"}); err != nil {
		return scopeFlags{}, err
	}

	f := scopeFlags{
		cloud: hasFlag(cmd, "cloud-source") || hasFlag(cmd, "cloud-source-id"),
		file:  hasFlag(cmd, "file-id"),
	}

	switch {
	case f.cloud && f.file:
		return scopeFlags{}, qerrors.NewBadUsage(
			"cannot use both --cloud-source/--cloud-source-id and --file-id: a row action has one scope")
	case f.cloud && sup.cloud == nil:
		return scopeFlags{}, qerrors.NewBadUsagef("'qctl rows %s' does not support cloud-source scope", verb)
	case f.file && sup.file == nil:
		return scopeFlags{}, qerrors.NewBadUsagef("'qctl rows %s' does not support file scope", verb)
	}

	if f.file {
		fileID, _, err := cmdutil.PositiveIDFlag(cmd, "file-id")
		if err != nil {
			return scopeFlags{}, err
		}
		f.fileID = fileID
	}
	if f.cloud {
		csID, _, err := cmdutil.PositiveIDFlag(cmd, "cloud-source-id")
		if err != nil {
			return scopeFlags{}, err
		}
		csName, _ := cmd.Flags().GetString("cloud-source")
		if csID == 0 && csName == "" {
			// `--cloud-source ""` marks the flag Changed while leaving both
			// arguments empty. cmdutil.ResolveCloudSource with neither falls
			// through to autoDetectCloudSource, which silently picks the single
			// source a table happens to have — acceptable for a read, wrong for
			// a delete. It must not have a back door.
			return scopeFlags{}, qerrors.NewBadUsage(
				"--cloud-source cannot be empty: name the cloud source, or use --cloud-source-id <id>")
		}
		f.csID, f.csName = csID, csName
	}
	return f, nil
}

func hasFlag(cmd *cobra.Command, name string) bool {
	return cmd.Flags().Lookup(name) != nil && cmd.Flags().Changed(name)
}

// tableNameFrom keeps the table's name for messages and for the follow-up
// command lines qctl prints. There is no extra lookup: this is only the name
// the user already supplied.
//
// --table is a NAME by construction (cmdutil.AddTableFlags), so it wins
// outright. The positional is the id-or-name convenience path, and a bare
// integer there is an id, not a name — `get table`, `describe table` and `get
// data` all resolve the positional id-first, so a table literally named "42"
// is unreachable through the positional on those three too. Naming it with
// --table is what reaches it, on those commands and on this one.
func tableNameFrom(cmd *cobra.Command, tableArg string) string {
	if name, _ := cmd.Flags().GetString("table"); name != "" {
		return name
	}
	if tableArg == "" {
		return ""
	}
	if _, err := strconv.Atoi(tableArg); err == nil {
		return ""
	}
	return tableArg
}

// shellArg makes a value safe to paste back into a shell.
//
// Every command line this group prints is meant to be copied, and a replacement
// term is arbitrary user text — spaces, quotes and `$` all reach it. POSIX
// single quotes are the only form that quotes everything, and an embedded
// quote is spliced by closing the quoted run, emitting a backslash-escaped
// quote, and reopening. The shared cmdutil implementation owns that literal.
//
// It is deliberately NOT spelled out here, and the earlier attempt to spell it
// out is why this note exists: gofmt applies godoc's typographic convention to
// DOC comments, turning a doubled apostrophe into a right curly quote and a
// doubled backtick into a left one. A doc comment quoting this splice is
// rewritten by the next `make fmt` into a character sequence that no longer
// describes the code, with no diagnostic. Inline comments inside a function
// body are left alone, so that is where such a literal can live.
func shellArg(s string) string {
	return cmdutil.ShellArg(s)
}

// shellQuote is shellArg's always-quoting twin, for PROSE rather than for a
// command line: a prompt and a refusal name user text mid-sentence, where the
// delimiters carry meaning shellArg deliberately drops. "replace N/A with
// unknown" loses the boundary that "replace 'N/A' with 'unknown'" keeps, and
// an empty replacement has nothing to show at all without them.
//
// Same escaping as shellArg -- both delegate to cmdutil's single
// implementation -- so the value still reads the way the shell would see it,
// which is the whole reason neither of these is %q.
func shellQuote(s string) string {
	return cmdutil.ShellQuote(s)
}
