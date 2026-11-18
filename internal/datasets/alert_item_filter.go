package datasets

import (
	"context"
	"net/http"
	"strconv"

	"github.com/qlustered/qctl/internal/api"
)

// alertItemIDParam is the get_data query parameter this file exists to send.
const alertItemIDParam = "alert_item_id"

// DataQueryOption is a get_data query parameter that the generated params
// struct cannot carry. Today that is exactly one parameter — see
// WithAlertItemID for why it needs its own channel.
type DataQueryOption func(*dataQuery)

type dataQuery struct {
	alertItemID *int
}

// WithAlertItemID filters the rows to those held by one alert.
//
// It does NOT go through api.GetDataParams.AlertItemID, and it cannot: the
// spec declares alert_item_id as an `int | str` union, so oapi-codegen emits
// it as an INLINE anonymous struct with an unexported field —
//
//	AlertItemID *struct{ union json.RawMessage } `form:"alert_item_id,omitempty"`
//
// (internal/api/client.gen.go). oapi-codegen generates As*/From*/Merge*
// accessors only for NAMED union types, so there is no helper for this one and
// the unexported field makes it unconstructible outside package api. Worse, a
// value constructed INSIDE that package still cannot be sent: the generated
// request builder hands the struct to runtime.StyleParamWithLocation, which
// reflects over it and panics with "reflect.Value.Interface: cannot return
// value obtained from unexported field or method". So that field is dead wire
// at every value, on all six get_data mirrors.
//
// A request editor is the generated client's own documented extension point,
// which is why the filter rides one rather than a patch to client.gen.go (see
// CLAUDE.md: that file is never edited by hand). The int is deliberate: the
// backend coerces the parameter with int() and answers 400 for anything else,
// treating only the literal "null" as "no filter" — a browser-URL artifact,
// not a second identifier space (atlas capi/capi/views/data_view_core.py,
// get_data_core).
//
// The caller is responsible for also sending is_bad_data: the filter is
// applied under `if alert_item_id is not None and is_bad_data`, and
// alert_item_id is absent from get_data_core's bad-data-only 400 list, so a
// clean-rows request returns the whole unfiltered page at HTTP 200.
func WithAlertItemID(id int) DataQueryOption {
	return func(q *dataQuery) { q.alertItemID = &id }
}

// editors turns the collected options into request editors for the generated
// client. Returns nil when nothing was set, so the ordinary request is built
// and sent exactly as before.
func (q dataQuery) editors() []api.RequestEditorFn {
	var out []api.RequestEditorFn
	if q.alertItemID != nil {
		id := *q.alertItemID
		out = append(out, func(_ context.Context, req *http.Request) error {
			values := req.URL.Query()
			values.Set(alertItemIDParam, strconv.Itoa(id))
			req.URL.RawQuery = values.Encode()
			return nil
		})
	}
	return out
}

// applyDataQueryOptions collapses the variadic options into request editors.
func applyDataQueryOptions(opts []DataQueryOption) []api.RequestEditorFn {
	var q dataQuery
	for _, opt := range opts {
		opt(&q)
	}
	return q.editors()
}
