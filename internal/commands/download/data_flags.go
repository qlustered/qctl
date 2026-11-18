package download

import (
	"fmt"
	"reflect"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/datasets"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/spf13/cobra"
)

// exportFlagDenylist holds PutDataForDownloadParams wire names that must never
// become user-facing flags.
//
// The first four are accepted by the route and then ignored, which is worse
// than unsupported: a flag that is silently dropped invites a wrong conclusion
// about what was exported. That reading still holds for three of them;
// include_dataset_rule_ids is now ignored only on a bad-data export, and its
// entry below says why it stays denylisted anyway. Verified in atlas @
// a0e86b1a3 and re-read at cea3bdb05 -- re-read the docstrings themselves
// rather than trusting either sha.
var exportFlagDenylist = map[string]bool{
	// The download worker overwrites both before paginating:
	// msg_data["page"] = 1; msg_data["limit"] = 10 ** 7
	// (DataViewDownloaderBase.__init__, cmapper/data_view_downloader.py).
	// Every export is the whole matching set, capped at ten million rows.
	"page":  true,
	"limit": true,

	// del'd at the top of put_data_for_download_core with the docstring
	// "Accepted for signature parity only; unused here." Unchanged by #767.
	"include_history_flags": true,

	// Same denylist decision, no longer the same reason, so it no longer
	// shares the entry above. atlas PR #767 gave the download route the
	// bad-data-only guard, and this parameter is named in it: the docstring
	// is now "Shapes no response here; only rejected when set with
	// is_bad_data=False". So it is a 400 alongside is_bad_data=False and
	// still shapes nothing alongside True, rather than being ignored either
	// way. Denylisted for that surviving half -- on a bad-data export it is
	// accepted and changes nothing about what is exported.
	"include_dataset_rule_ids": true,

	// Structural, same reasons as get data's denylist. alert_item_id is not
	// "unsupported" but UNSENDABLE: the `int | str` union becomes an inline
	// anonymous struct with an unexported field, so the value is
	// unconstructible outside package api AND panics the generated request
	// builder even from inside it. --alert-item-id is hand-registered below
	// and reaches the wire through datasets.WithAlertItemID; see
	// internal/datasets/alert_item_filter.go.
	"alert_item_id": true,
	"oauth_session": true, // internal
}

// exportFlagHelp is help text for the parameters whose meaning differs on the
// export route, plus the one enum parameter whose values have to be readable
// in this command's --help too. Anything not named here falls back to the
// generic passthrough line, which is what `get data` already does for the
// parameters missing from its own map.
var exportFlagHelp = map[string]string{
	paramflags.DeletedRowsWire: paramflags.DeletedRowsHelp,
	"fields":                   "Limit the exported CSV to these columns (comma-separated list; repeatable)",
	"is_bad_data":              "Export quarantine (bad) rows instead of clean rows",
	"show_metadata":            "Include source-file provenance columns in the CSV",
}

var putDataForDownloadParamsType = reflect.TypeOf(api.PutDataForDownloadParams{})

func exportFlags() []paramflags.Flag {
	return paramflags.Derive(putDataForDownloadParamsType, exportFlagDenylist)
}

func exportHelpFor(wireName string) string {
	if h, ok := exportFlagHelp[wireName]; ok {
		return h
	}
	if h, ok := paramflags.BadDataOnlyHelp(wireName); ok {
		return h
	}
	return fmt.Sprintf("Set the %s query parameter (passed through to the backend)", wireName)
}

// registerExportFlags registers one cobra flag per derivable
// PutDataForDownloadParams field, plus the one denylisted parameter that still
// gets a flag: --alert-item-id, hand-registered because its generated field
// cannot carry a value.
func registerExportFlags(cmd *cobra.Command) {
	paramflags.Register(cmd, exportFlags(), exportHelpFor)
	paramflags.RegisterAlertItemIDFlag(cmd)
}

// exportQueryOptions collects the query parameters
// api.PutDataForDownloadParams cannot carry. Today that is only
// --alert-item-id (internal/datasets/alert_item_filter.go).
//
// Note that put_data_for_download_core does NOT carry get_data_core's
// `int(alert_item_id)` coercion, so a string would reach the paginator raw on
// this route; sending an int is the only safe form here.
func exportQueryOptions(cmd *cobra.Command) ([]datasets.DataQueryOption, error) {
	alertItemID, ok, err := paramflags.AlertItemIDFilter(cmd)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	return []datasets.DataQueryOption{datasets.WithAlertItemID(alertItemID)}, nil
}

// buildExportParams reads the registered flags into *api.PutDataForDownloadParams.
// Only flags the user changed are set; there is no page/limit pinning here
// because both are denylisted.
func buildExportParams(cmd *cobra.Command) (*api.PutDataForDownloadParams, error) {
	params := &api.PutDataForDownloadParams{}
	if err := paramflags.Build(cmd, params, exportFlags(), nil); err != nil {
		return nil, err
	}
	return params, nil
}
