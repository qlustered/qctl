package dataview

// Reserved-column handling for the Markdown payload.
//
// get_data returns the table's reserved (bookkeeping) columns alongside the
// data, and re-adds them even when --fields narrows the request — atlas
// capi/capi/views/data_view_core.py get_limited_fields() unions
// RECORDS_RESERVED_COLUMNS_SET / BAD_RECORDS_RESERVED_COLUMNS_SET back onto
// whatever the caller asked for. Only signature_, ingest_job_id_, is_deleted_
// and history_ids_ (plus dataset_rule_ids_ on quarantined rows, and row_logs_
// unless show_row_logs) are dropped server-side, so a clean page arrives with
// eleven of them and a quarantine page with nineteen. Rendered raw, they take
// most of the payload's width and none of them is the data the reader asked
// for.
//
// So the Markdown view classifies each one: shown, folded into a compact form,
// or hidden. -o json and -o yaml are the raw backend response and are not
// touched by any of this.
//
// The names below mirror RESERVED_COLUMNS in atlas common/common/types.py.
// The spec publishes that list as the ReservedColumns component's
// x-reserved-columns, but `make generate-client` does not extract it, so no
// generated symbol tracks these names and a regen cannot notice a change here.
// Two consequences, both deliberate:
//
//   - The classification is an ALLOWLIST. A column qctl does not recognise is
//     user data and is always shown. A reserved column atlas adds later
//     therefore renders raw — today's behaviour for all of them — rather than
//     silently removing a column that might have held the reader's data.
//   - reserved_test.go pins the classification, so changing it is deliberate.

// reservedFate says what the Markdown view does with one reserved column.
type reservedFate int

const (
	// reservedShown renders the column as an ordinary data column.
	reservedShown reservedFate = iota
	// reservedFolded means the column reaches the reader in a compact form
	// built elsewhere (the Row State column, the ROW SIGNATURES dictionary,
	// the per-cell issue markers, the Source Files section).
	reservedFolded
	// reservedHidden means the column is dropped from the Markdown view.
	reservedHidden
)

// Field names this package reasons about by name. The two issue-token columns
// are in issues.go; metadata_ is appended by the backend rather than being a
// real table column.
const (
	idFieldName            = "id"
	signatureFieldName     = "initial_signature_"
	passesValidationField  = "passes_validation_"
	awaitingProcessingFld  = "awaiting_processing_"
	processingAlertIDField = "processing_alert_item_id_"
	keepInQuarantineField  = "keep_in_quarantine_"
	suspectedDuplicateFld  = "suspected_duplicate_"
	rowLogsFieldName       = "row_logs_"
)

// stateFieldNames are the quarantine-lifecycle columns folded into the Row
// State column, in the order that column renders them.
//
// All five are BAD_RECORDS_RESERVED_COLUMNS only — they are not on the clean
// records table — so the Row State column appears on a --is-bad-data page and
// nowhere else, which is exactly where the `qctl rows` verbs act.
var stateFieldNames = []string{
	passesValidationField,
	awaitingProcessingFld,
	processingAlertIDField,
	keepInQuarantineField,
	suspectedDuplicateFld,
}

// The compact forms a folded reserved column reaches the reader through, named
// as the payload names them, and the order the payload presents them in.
const (
	foldIssues     = "the [A<id>]/[W<id>] markers and the Issues column"
	foldState      = "the Row State column"
	foldSignatures = "the ROW SIGNATURES section"
	foldHistory    = "the CELL HISTORY section"
)

var foldOrder = []string{foldIssues, foldState, foldSignatures, foldHistory}

// stateTokens is what the Row State column prints for each boolean state
// column that is true. Naming the column rather than paraphrasing it keeps the
// token greppable against the flag that filters on it — `qctl get data
// --awaiting-processing` — and against the backend field it came from.
//
// Three of the five folded columns are not here, because none is a two-state
// boolean: passes_validation_ has three states, spelled by validationToken;
// processing_alert_item_id_ carries an id; and keep_in_quarantine_ reaches the
// reader as a QuarantineHold name or an approval state, spelled by
// decisionToken.
var stateTokens = map[string]string{
	awaitingProcessingFld: "awaiting-processing",
	suspectedDuplicateFld: "suspected-duplicate",
}

// quarantineHoldPrefix keys the Row State token carrying a row's hold
// decision, in the `<column>=<value>` form processing-alert=<id> already uses.
// The value after it is the QuarantineHold name `qctl get data
// --quarantine-hold` filters on and `qctl rows hold`/`release`/`reset-hold`
// write, so the token is greppable against both.
const quarantineHoldPrefix = "quarantine-hold="

// heldForApprovalPrefix keys the Row State token of an undecided row that has
// an approval hold reason. The value after it is the row's
// approval_hold_reasons entry.
const heldForApprovalPrefix = "held-for-approval="

// waitingForApprovalToken is the Row State token of an undecided row whose
// waiting_for_approval entry is true, spelled as the `qctl get data
// --waiting-for-approval` flag that selects those rows.
const waitingForApprovalToken = "waiting-for-approval"

// reservedColumns is the fate of every name in atlas's RESERVED_COLUMNS.
//
// Server-hidden columns (signature_, ingest_job_id_, is_deleted_, history_ids_,
// dataset_rule_ids_ on quarantined rows) never reach a get_data response at
// all; they are listed as hidden so the mirror is complete and a backend that
// starts returning one does not turn it into a rendered column.
var reservedColumns = map[string]reservedFate{
	// The row's primary key: the handle `qctl describe row`, `qctl rows
	// release --row-id` and every --filter-by-field-name id lookup take, and
	// the key of both dictionaries this view writes. get_limited_fields seeds
	// its result with it, so it is on every page whatever --fields says.
	idFieldName: reservedShown,

	// Folded into the ROW SIGNATURES dictionary: the hex the `qctl rows`
	// delete verbs take as --signature.
	signatureFieldName: reservedFolded,

	// Folded into the Row State column.
	passesValidationField:  reservedFolded,
	awaitingProcessingFld:  reservedFolded,
	processingAlertIDField: reservedFolded,
	keepInQuarantineField:  reservedFolded,
	suspectedDuplicateFld:  reservedFolded,

	// Folded into the Row State column as deleted=<kind> and row-delete=<id>;
	// deletes.go builds both tokens.
	deletedKindField: reservedFolded,
	rowDeleteIDField: reservedFolded,

	// Folded into the per-cell [A<id>]/[W<id>] markers and the Issues column.
	alertTokensFieldName:   reservedFolded,
	warningTokensFieldName: reservedFolded,

	// Bookkeeping. Each is reachable with --fields <name> or -o json.
	"signature_":                    reservedHidden, // server-hidden
	"ingest_job_id_":                reservedHidden, // server-hidden
	"is_deleted_":                   reservedHidden, // server-hidden
	"history_ids_":                  reservedHidden, // server-hidden
	"dataset_rule_ids_":             reservedHidden, // server-hidden on quarantined rows; --include-dataset-rule-ids is the qctl route
	"row_logs_":                     reservedHidden, // folded under --show-row-logs
	"row_number_":                   reservedHidden,
	"row_numbers_":                  reservedHidden,
	"occurrence_":                   reservedHidden,
	"cell_move_suggestions_":        reservedHidden,
	"cell_overrides_":               reservedHidden,
	"cluster_id_":                   reservedHidden,
	"confidence_score_":             reservedHidden,
	"data_source_model_id":          reservedHidden,
	"version_id_":                   reservedHidden,
	"rule_run_id_":                  reservedHidden,
	"rule_verdicts_":                reservedHidden, // temp-table only; never reaches a response
	"rule_suppression_history_ids_": reservedHidden, // temp-table only; never reaches a response
	"raw_key_id":                    reservedHidden,
	"raw_key_ids_":                  reservedHidden,
	"internal_id":                   reservedHidden,
	"composite_spine_row_id":        reservedHidden,
}

// fateOf returns the fate of one response column.
//
// unlocked names a column the request explicitly asked for; such a column is
// always shown. A name that is not reserved at all is user data
// and is shown.
func fateOf(name string, unlocked map[string]bool) reservedFate {
	if unlocked[name] {
		return reservedShown
	}
	fate, ok := reservedColumns[name]
	if !ok {
		return reservedShown
	}
	return fate
}
