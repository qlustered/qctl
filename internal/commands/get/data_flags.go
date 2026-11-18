package get

import (
	"fmt"
	"reflect"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/paramflags"
	"github.com/spf13/cobra"
)

// dataFlagDenylist holds GetDataParams wire names that must never become user-facing flags.
var dataFlagDenylist = map[string]bool{
	"full_values":   true, // narrow-request-only; backend-gated (see spec Backend Contract)
	"oauth_session": true, // internal
	// Not "unsupported" but UNSENDABLE. The spec's `int | str` union becomes an
	// inline anonymous struct with an unexported field, so the value is
	// unconstructible outside package api AND panics the generated request
	// builder even from inside it. --alert-item-id is hand-registered by
	// RegisterDataFlags and reaches the wire through datasets.WithAlertItemID;
	// see internal/datasets/alert_item_filter.go.
	"alert_item_id": true,
}

// dataFlagHelp provides curated help text per wire name. This is help text ONLY — not a name
// mapping — so a newly added backend parameter still yields a flag (with generic help) without
// any change here.
var dataFlagHelp = map[string]string{
	paramflags.DeletedRowsWire: paramflags.DeletedRowsHelp,
	"page":                     "Page number to fetch (default 1)",
	"limit":                    "Maximum rows to return (default 20)",
	"anchor_row_id":            "Open the page containing this row under the current filters and ordering (positive ID; overrides --page)",
	"is_bad_data":              "Return quarantine (bad) rows instead of clean rows",
	"show_metadata":            "Include source-file provenance (adds a Source Files column)",
	"include_history_flags":    "Mark cells that have edit history with [H]",
	"max_cell_chars":           "Truncate cell values to this many characters (backend-validated)",
	"filter_by_field_name":     "Field name to filter on (pairs with --filter-by-field-value)",
	"filter_by_field_value":    "Value to filter the --filter-by-field-name field by",
	"search_query":             "Full-text search across fields",
	"order_by":                 "Field name(s) to order by (repeatable)",
	"fields":                   "Limit output to these field names, and show a reserved system column by name (comma-separated list; repeatable)",
	"show_row_logs":            "Include the row_logs_ column, which the backend otherwise withholds",
	"comment_thread_id":        "Filter to the rows targeted by this comment thread (full UUID)",
	"request_view":             "View for --request-state: approver (default) or sender; has no effect without --request-state",
}

var getDataParamsType = reflect.TypeOf(api.GetDataParams{})

// dataFlags reflects over api.GetDataParams and returns the derivable flags (denylist removed,
// unsupported types skipped).
func dataFlags() []paramflags.Flag {
	return paramflags.Derive(getDataParamsType, dataFlagDenylist)
}

func helpFor(wireName string) string {
	if h, ok := dataFlagHelp[wireName]; ok {
		return h
	}
	if h, ok := paramflags.BadDataOnlyHelp(wireName); ok {
		return h
	}
	return fmt.Sprintf("Set the %s query parameter (passed through to the backend)", wireName)
}

// RegisterDataFlags registers one cobra flag per derivable GetDataParams field,
// plus the one denylisted parameter that still gets a flag: --alert-item-id,
// which is hand-registered because its generated field cannot carry a value.
func RegisterDataFlags(cmd *cobra.Command) {
	paramflags.Register(cmd, dataFlags(), helpFor)
	paramflags.RegisterAlertItemIDFlag(cmd)
}

// BuildDataParams reads the registered flags and builds *api.GetDataParams. Only the flags the
// user changed are set (preserving backend defaults), except page/limit which default to 1/20 —
// see pinPageAndLimit.
func BuildDataParams(cmd *cobra.Command) (*api.GetDataParams, error) {
	params := &api.GetDataParams{}
	if err := paramflags.Build(cmd, params, dataFlags(), pinPageAndLimit); err != nil {
		return nil, err
	}
	return params, nil
}

// validateDataParams refuses parameter combinations the data endpoint
// rejects, before any request is sent.
func validateDataParams(params *api.GetDataParams) error {
	if err := validateDataRequestState(params); err != nil {
		return err
	}
	return validateDataProblemGroup(params)
}

// validateDataRequestState checks the request state's group and view.
func validateDataRequestState(params *api.GetDataParams) error {
	if params.RequestState == nil {
		return nil
	}
	if params.ReviewGroupID == nil {
		return qerrors.NewBadUsage("--request-state requires --review-group-id: give the submission-group UUID")
	}
	view := api.Approver
	if params.RequestView != nil {
		view = *params.RequestView
	}
	state := *params.RequestState
	if state == api.RequestStateFilterApproved || state == api.RequestStateFilterOnHold {
		return nil
	}
	if view == api.Approver && (state == api.RequestStateFilterToReview || state == api.RequestStateFilterFlaggedByMe) {
		return nil
	}
	if view == api.Sender && (state == api.RequestStateFilterWaiting || state == api.RequestStateFilterFlagged) {
		return nil
	}
	return qerrors.NewBadUsagef("--request-state %s is not valid for --request-view %s: approver accepts to_review, flagged_by_me, approved, on_hold; sender accepts waiting, flagged, approved, on_hold", state, view)
}

// validateDataProblemGroup checks that the problem-group flags name one whole
// group: --problem-kind and --problem-issue-type, plus exactly one of
// --problem-dataset-rule-id and --problem-field-names.
func validateDataProblemGroup(params *api.GetDataParams) error {
	hasRule := params.ProblemDatasetRuleID != nil
	hasFields := params.ProblemFieldNames != nil && len(*params.ProblemFieldNames) > 0
	if params.ProblemKind == nil && params.ProblemIssueType == nil && !hasRule && !hasFields {
		return nil
	}
	if hasRule && hasFields {
		return qerrors.NewBadUsage("--problem-dataset-rule-id and --problem-field-names cannot be used together: a rule's problem group is named by its rule alone")
	}
	if params.ProblemKind == nil || params.ProblemIssueType == nil || (!hasRule && !hasFields) {
		return qerrors.NewBadUsage("a problem group needs --problem-kind, --problem-issue-type and either --problem-dataset-rule-id or --problem-field-names")
	}
	return nil
}

// pinPageAndLimit forces page=1 and limit=20 onto the request even when the
// user changed neither, by setting the pflag defaults that paramflags.Build
// then reads back. get_data declares `page: int = 1, limit: int = 20`
// server-side, but qctl sends them explicitly so `get data`'s rendered
// "page N" header is never a guess about what the server did.
//
// Set() marks both flags Changed. That is invisible here because the only
// reader of Changed("page")/Changed("limit") is validateDataPagination, and
// data.go's RunE runs it before BuildDataParams.
func pinPageAndLimit(cmd *cobra.Command, f paramflags.Flag) bool {
	switch f.WireName {
	case "page":
		_ = cmd.Flags().Set("page", "1")
		return true
	case "limit":
		_ = cmd.Flags().Set("limit", "20")
		return true
	}
	return false
}

// validateDataPagination rejects non-positive page, limit and anchor values.
func validateDataPagination(cmd *cobra.Command) error {
	for _, name := range []string{"page", "limit", "anchor-row-id"} {
		if !cmd.Flags().Changed(name) {
			continue
		}
		if _, err := cmdutil.PositiveCountFlag(cmd, name); err != nil {
			return err
		}
	}
	return nil
}

// deletedRowsRequested is the --deleted-rows value params carries, or "".
func deletedRowsRequested(params *api.GetDataParams) api.DeletedRows {
	if params == nil || params.DeletedRows == nil {
		return ""
	}
	return *params.DeletedRows
}
