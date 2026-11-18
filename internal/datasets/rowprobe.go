package datasets

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
)

// The get_data field names and page size these probes pin, named once so the
// requests, the identity check and the doc comments cannot drift apart.
const (
	rowIDField              = "id"
	awaitingProcessingField = "awaiting_processing_"
	rowProbeResultLimit     = 1
)

// QuarantinedRow is what one live quarantine row looks like to a probe.
//
// It carries AwaitingProcessing and nothing else on purpose — not even the id,
// which readRowByID reads to check the server answered about the row it asked
// for and then has no further use for. The submit guard
// behind `alert submit` / `warning submit` is a four-way conjunction —
// `is_deleted_ IS FALSE AND awaiting_processing_ IS NOT TRUE AND ingest_job_id_
// IS NULL` plus a per-field value guard (atlas
// cettings/cettings/controllers/data_controller/quarantine.py) — and
// get_data can only show one of the four. `ingest_job_id_` and `is_deleted_`
// are both in HIDDEN_FIELDS_BAD_DATA (cettings .../data_controller/core.py)
// so the payload never carries them, and the value guard compares against
// values the caller sent, not against anything a row read can answer. A caller
// may therefore say "this row IS awaiting processing", never "this row is
// flaggable".
type QuarantinedRow struct {
	AwaitingProcessing bool
}

// LookupQuarantinedRow answers what the quarantine table says about one row
// right now: nil for "no live quarantined row of this table carries that id",
// and otherwise the row.
//
// This is the read behind the honest `row_not_found` message. `alert submit`
// and `warning submit` are refused for a row that has just been moved by `rows
// send-to-quarantine`, because the move stamps `awaiting_processing_=True`
// (the parameter default, quarantine.py, which the route does not
// override) and publishes a reprocess — and the backend deliberately answers
// that with the same `row_not_found` it uses for a wrong id
// (capi/capi/views/user_submitted_issue_view.py). One read tells the
// retryable case from the mistyped one.
//
// The row is readable while it is refused: read_bad_row_values applies no
// awaiting_processing_ filter (data_retrieval.py), and neither does
// get_data unless the parameter is sent.
//
// `--fields id` is not a narrowing that loses awaiting_processing_:
// get_limited_fields unions the requested fields with
// BAD_RECORDS_RESERVED_COLUMNS_SET and then subtracts the hidden ones
// (capi/capi/views/data_view_core.py). awaiting_processing_ is in
// that set (common/common/types.py) and is NOT in HIDDEN_FIELDS_BAD_DATA, so
// it survives the narrowing — which is exactly why this probe cannot be
// pointed at `ingest_job_id_` or `is_deleted_` instead: those two are in the
// hidden set and no field selection brings them back. Asking for one field is
// what keeps a probe of a 300-column table cheap.
//
// A nil row here does NOT mean the table has no such row — the clean side is
// invisible to a bad-data read. LookupCleanRow is the other half.
func (c *Client) LookupQuarantinedRow(accessToken string, datasetID, rowID int) (*QuarantinedRow, error) {
	page, row, err := c.readRowByID(accessToken, datasetID, rowID, true)
	if err != nil || page == nil {
		return nil, err
	}

	value, err := columnValue(page, row, awaitingProcessingField, datasetID)
	if err != nil {
		return nil, err
	}

	// SQL NULL arrives as JSON null and the guard reads `IS NOT TRUE`, so a
	// null column is "not awaiting" — the same bucket as false, and NOT an
	// unreadable value. Anything that is neither a bool nor null IS
	// unreadable: reporting it as "not awaiting" would state the opposite of
	// what the caller is trying to find out.
	switch awaiting := value.(type) {
	case nil:
		return &QuarantinedRow{}, nil
	case bool:
		return &QuarantinedRow{AwaitingProcessing: awaiting}, nil
	default:
		return nil, fmt.Errorf("get_data returned a %T for %s on row %d of table %d, want a boolean",
			value, awaitingProcessingField, rowID, datasetID)
	}
}

// LookupCleanRow answers whether a LIVE row of the CLEAN side of this table
// carries that id.
//
// It exists to split the one thing LookupQuarantinedRow's nil cannot: a row
// still sitting on the clean side and an id that exists nowhere are the same
// answer to a bad-data read, and they need opposite actions — park the row, or
// fix the id. Nothing about the row itself is returned, because nothing on the
// clean side bears on a submit that was refused: the submit guard runs against
// the QUARANTINE table, and the clean table carries neither the
// awaiting_processing_ column nor the ingest_job_id_ one. This read exists to
// say which side the id is on, and stops there.
//
// The id it is asked for is the id the caller typed, and that is sound across
// the two tables: the clean and quarantine tables share ONE sequence and a
// moved row keeps its primary key (atlas
// cettings/.../data_controller/quarantine.py, and `id` is in the copied
// field list). The backend classifies an unmovable id the same way —
// `_classify_unmovable_quarantine_ids` calls an id with nothing live under it
// anywhere "gone" (quarantine.py).
//
// false means "no LIVE clean row", not "no such row ever": this read does not
// send is_deleted_, so a tombstoned clean row is invisible to it. Only paired
// with an empty quarantine read — the sole context it is called in — does it
// support the caller's "no live row on either side".
func (c *Client) LookupCleanRow(accessToken string, datasetID, rowID int) (bool, error) {
	page, _, err := c.readRowByID(accessToken, datasetID, rowID, false)
	if err != nil {
		return false, err
	}
	return page != nil, nil
}

// readRowByID asks get_data for one live row of ONE side of the table, by id.
//
// It returns a nil page for "no live row of that side carries that id", and an
// error whenever the answer cannot be trusted to be about the row that was
// asked for. Both probes go through it so the request they send — and the
// proof they demand that the server honoured the filter — cannot drift apart.
//
// is_bad_data is not optional for either caller, and is sent explicitly rather
// than left to the route's default: `filter_by_field_name` is validated
// against the field set that flag selects, and a miss is a 404
// (data_view_core.py), so the pair has to describe the same side of
// the table. `id` survives on both sides — it seeds
// get_field_names_set_for_table (cettings .../data_controller/core.py)
// and get_limited_fields (data_view_core.py), and neither HIDDEN_FIELDS nor
// HIDDEN_FIELDS_BAD_DATA hides it (core.py) — which is what makes
// one filter legal against both. `filter_by_field_name` itself is not one of
// the bad-data-only parameters the clean side refuses with a 400
// (data_view_core.py).
//
// Neither read sends is_deleted_, so both take the route's own default of
// false (capi/capi/views/data_view.py, the get_data signature itself) and
// get_filter_queries applies it unconditionally as an equality, not a
// "not true" (core.py). A tombstoned row is invisible to both, which
// is what the "no LIVE row" wording downstream is being careful about.
func (c *Client) readRowByID(accessToken string, datasetID, rowID int, isBadData bool) (*DataPage, []interface{}, error) {
	limit := rowProbeResultLimit
	fieldName := rowIDField
	fieldValue := strconv.Itoa(rowID)

	page, err := c.GetData(accessToken, datasetID, &api.GetDataParams{
		IsBadData:          &isBadData,
		Limit:              &limit,
		Fields:             &[]string{rowIDField},
		FilterByFieldName:  &fieldName,
		FilterByFieldValue: &fieldValue,
	})
	if err != nil {
		return nil, nil, err
	}
	if len(page.Results) == 0 {
		return nil, nil, nil
	}

	row := page.Results[0]
	idColumn, err := columnValue(page, row, rowIDField, datasetID)
	if err != nil {
		return nil, nil, err
	}
	// The caller is about to be told something about a NAMED row, so prove the
	// server answered about that row. filter_by_field_name is applied
	// unconditionally today, but a filter silently dropped server-side would
	// otherwise hand back an arbitrary row of the page and its state would be
	// reported as this row's — the precise kind of confident wrong answer this
	// probe exists to remove. It matters most on the clean read, where an
	// arbitrary row would report an absent id as "it is on the clean side".
	// GetData decodes numbers as json.Number; anything else is not an id.
	id, _ := idColumn.(json.Number)
	if n, err := id.Int64(); err != nil || n != int64(rowID) {
		return nil, nil, fmt.Errorf("get_data answered with row %v for a request filtered to row %d of table %d",
			idColumn, rowID, datasetID)
	}
	return page, row, nil
}

// columnValue reads one named column out of the parallel-array payload.
//
// A column that is absent, or a row shorter than the header, is an error
// rather than a zero value: every caller here turns "could not read it" into
// silence, and a zero value would turn it into a claim.
//
// The message names no side of the table, because both reads come through
// here and a clean-side failure reported as a quarantine one would send the
// reader looking in the wrong place.
func columnValue(page *DataPage, row []interface{}, name string, datasetID int) (interface{}, error) {
	for i, field := range page.Fields {
		if field != name {
			continue
		}
		if i >= len(row) {
			return nil, fmt.Errorf("get_data returned %d values for a %d-column row of table %d",
				len(row), len(page.Fields), datasetID)
		}
		return row[i], nil
	}
	return nil, fmt.Errorf("get_data returned no %s column for table %d", name, datasetID)
}
