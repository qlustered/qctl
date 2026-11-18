package user_submitted //nolint:revive // Matches the backend's own name for this feature.

import (
	"fmt"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/prompt"
)

// RowState is what a fresh read of the table said about one row that came
// back `row_not_found`.
//
// It is deliberately a state of the ROW as read, not a cause of the refusal.
// The submit guard is a four-way conjunction (`is_deleted_ IS FALSE AND
// awaiting_processing_ IS NOT TRUE AND ingest_job_id_ IS NULL` plus a per-field
// value guard, atlas cettings/.../data_controller/quarantine.py
// QuarantineMixin.append_issue_tokens_for_row) and `get_data` can show exactly
// one of the four: `ingest_job_id_` and `is_deleted_` are hidden from its
// payload, and the value guard compares against values the caller sent.
//
// So a probe can show that a reprocess is queued, and it can show that the
// QUARANTINE SIDE does not hold the id. That second answer is not the same as
// the row not existing — a bad-data read cannot see a clean row — so it is
// followed by a second read of the CLEAN side, which splits it into the two
// states that need opposite actions: park the row, or fix the id. What no
// read narrows is the remainder behind a row that IS quarantined, and even
// "not awaiting processing" is a statement about the read and not about the
// refusal: the reprocess may have settled in between. Each note below says
// which of those it is, rather than picking one and prescribing from it.
type RowState int

const (
	// RowStateUnknown is the state of every row when no prober was supplied,
	// when the probe itself failed, and when the row fell past MaxProbedRows.
	// It prints nothing extra: a diagnostic that turned one failure into a
	// different-looking failure would be worse than the message it replaced.
	RowStateUnknown RowState = iota
	// RowStateAbsentFromQuarantine: no live quarantined row of this table
	// carries that id, AND the clean-side read that would say whether it is
	// nonetheless a row of this table did not answer.
	//
	// This is the degraded arm, not the common one: the clean read runs
	// whenever the quarantine read comes back empty, and only its failure
	// lands here. It offers an id check before the move, since this read
	// cannot establish that the id exists on the clean side. A wholly
	// missing move now reports not found (exit 3).
	//
	// Degrading to RowStateUnknown instead would print nothing here, and the
	// quarantine read SUCCEEDED — its verdict is worth the sentence it costs.
	// Degrading to RowStateAbsentFromTable would state as fact the one thing
	// this arm failed to establish.
	RowStateAbsentFromQuarantine
	// RowStateClean: the id is not in quarantine, and a live row of the CLEAN
	// side carries it. The move is the fix and the probe has READ the row it
	// is about to name, so this note prescribes rather than offers — the
	// clean and quarantine tables share one id sequence and a moved row keeps
	// its primary key, so the id the caller typed is the id to park.
	RowStateClean
	// RowStateAbsentFromTable: no live row on EITHER side carries that id.
	//
	// "Live" is the whole qualifier: neither read sends is_deleted_, so both
	// take the route's default of false and a tombstoned row reads exactly
	// like an id that never existed. The note must therefore not say "no such
	// row", and must NOT suggest the move — there is nothing on the clean side
	// for it to move, which is the case the pre-split note had to warn about
	// instead of ruling out.
	RowStateAbsentFromTable
	// RowStateAwaitingProcessing: the row IS quarantined, with a reprocess
	// queued against it — which is what `rows send-to-quarantine` leaves
	// behind, among several other writers (atlas
	// cettings/cettings/controllers/quarantine_reprocess.py
	// mark_bad_rows_for_reprocess and drain_intents_for_job for the reprocess
	// UPDATE, row_overwrite.py for a row edit, and
	// cettings/cettings/controllers/data_controller/quarantine.py
	// quarantine_offending_rows for the ALTER evacuation), so the state must
	// not be reported as that verb's signature.
	//
	// It is A cause of `row_not_found` that clears itself, not THE one: an
	// ingest job holding the row fails `ingest_job_id_ IS NULL` and clears when
	// the job finishes. The difference is visibility — this conjunct is on the
	// wire and that one is not.
	RowStateAwaitingProcessing
	// RowStateQuarantined: the row is quarantined and, AS OF THIS READ, not
	// awaiting processing. That does not locate the refusal in the two
	// conjuncts a row read cannot see: the read happens after the submit, so a
	// reprocess that settled in the window between them lands here too — the
	// one case where a plain retry succeeds. Three causes, and this state
	// separates none of them.
	RowStateQuarantined
)

// RowProber re-reads one row of the table. It returns RowStateUnknown rather
// than an error: the caller is already reporting a failure, and a probe that
// fails wholesale must leave that report exactly as it would have been.
//
// It is up to two reads, not one — the clean side is consulted only when the
// quarantine side comes back empty — but that is the adapter's business, and
// the states above are what this package renders from.
type RowProber func(rowID int) RowState

// MaxProbedRows caps how many row_not_found rows one report re-reads.
//
// The probe is one GET per row — two for a row the quarantine side does not
// hold — and MaxRows is 500, so an uncapped report would turn a single failed
// call into 500 requests, or 1000, at the moment the caller is least able to
// absorb them. Ten covers the shape this exists for — a handful
// of rows moved with `rows send-to-quarantine` and flagged straight after —
// and rows submitted together almost always share a cause, so the first ten
// are representative of the rest. Past the cap the rows keep their unadorned
// line and the block says how many were not re-read, because a silent partial
// diagnosis reads as "the others are fine".
const MaxProbedRows = 10

// probeFindings holds one RowState per entry of the failed slice it was built
// from, positionally — never keyed by row id, so a server that answered for
// one row twice cannot collapse two verdicts into one.
type probeFindings struct {
	states   []RowState
	eligible int // row_not_found rows: the ones a probe could have applied to
	probed   int
}

// probeFailures re-reads the row_not_found rows, up to the cap.
//
// field_invalid is never probed: it says the FIELD is wrong, so the row may be
// perfectly flaggable and a note about its state would point at the wrong
// mistake.
func probeFailures(failed []Result, probe RowProber) probeFindings {
	findings := probeFindings{states: make([]RowState, len(failed))}
	if probe == nil {
		return findings
	}
	for i, r := range failed {
		if r.Status != api.RowNotFound {
			continue
		}
		findings.eligible++
		if findings.probed >= MaxProbedRows {
			continue
		}
		findings.probed++
		findings.states[i] = probe(r.RowID)
	}
	return findings
}

// stateOf is positional lookup with a safe answer for an out-of-range index,
// so a future caller that renders a different slice degrades to today's
// message rather than panicking inside an error path.
func (p probeFindings) stateOf(i int) RowState {
	if i < 0 || i >= len(p.states) {
		return RowStateUnknown
	}
	return p.states[i]
}

// capNote names the rows the cap left un-read, and is empty when there are
// none.
//
// It counts row_not_found rows and SAYS so: eligible excludes the field_invalid
// rows a report can also carry, so "unflagged rows" would name a larger set
// than the one the fraction is over.
func (p probeFindings) capNote() string {
	if p.eligible <= p.probed {
		return ""
	}
	return fmt.Sprintf("(only the first %d of %d row_not_found rows were re-read)", p.probed, p.eligible)
}

// probeNote is the continuation line under a failure line: what the re-read
// found, and what to do about it. Empty for RowStateUnknown.
//
// `<table>` stays a placeholder here, as it is in the rest of this package's
// messages: user_submitted is cobra-free and never learns the name the caller
// typed.
func probeNote(rowID int, state RowState) string {
	switch state {
	case RowStateAwaitingProcessing:
		return fmt.Sprintf("re-read just now: row %d IS in quarantine, but is awaiting processing — a reprocess is"+
			" queued against it, which `qctl rows send-to-quarantine` leaves behind and a row edit or"+
			" `qctl rows reprocess-quarantined` does too. Retry once it settles.", rowID)
	case RowStateAbsentFromQuarantine:
		return fmt.Sprintf("re-read just now: no live quarantined row of this table carries id %d, and the"+
			" clean-side read that would tell you more did not answer — so either it is still on the clean"+
			" side, or this table has no such row. Check which with: qctl get data <table>"+
			" --filter-by-field-name id --filter-by-field-value %d — then park it with: qctl rows"+
			" send-to-quarantine <table> --row-id %d%s. If that id is not found, the move reports"+
			" not found (exit 3) and changes nothing.", rowID, rowID, rowID, prompt.YesFlag(false))
	case RowStateClean:
		return fmt.Sprintf("re-read just now: row %d is not in quarantine — it is a live row on the CLEAN side"+
			" of this table, and flagging is quarantine-only. Park it with: qctl rows send-to-quarantine"+
			" <table> --row-id %d%s, then retry once it settles: the move lands the row awaiting processing.",
			rowID, rowID, prompt.YesFlag(false))
	case RowStateAbsentFromTable:
		return fmt.Sprintf("re-read just now: no live row of this table carries id %d on either side — the id is"+
			" wrong, or the row has been deleted. `qctl rows send-to-quarantine` has nothing to move here."+
			" Take the id from: qctl get data <table> --is-bad-data (drop the flag for the clean side).",
			rowID)
	case RowStateQuarantined:
		return fmt.Sprintf("re-read just now: row %d is in quarantine and NOT awaiting processing as of this read,"+
			" so the reprocess may have settled since the refusal — a retry is worth one attempt. Failing that,"+
			" its flagged values changed under you or an ingest job holds it; a row read can rule out none of"+
			" the three.", rowID)
	case RowStateUnknown:
		return ""
	}
	return ""
}
