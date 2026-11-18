package history

import "github.com/qlustered/qctl/internal/api"

// models is every model the ledger records history for.
//
// It exists for COMPLETION, not validation. `get history --model` forwards
// whatever it is given, on the precedent of `get alerts --issue-type`: a
// generated enum that lags a spec pull must never reject a value the backend
// has just started sending, and this enum grows as models gain history (it
// gained ReviewPolicy, DataReviewPolicy and CellOverride after the first set).
var models = []Model{
	api.ModelsWithHistoryBackupSettings,
	api.ModelsWithHistoryCellOverride,
	api.ModelsWithHistoryChangeProposal,
	api.ModelsWithHistoryChangeReviewPolicy,
	api.ModelsWithHistoryConditionalActionItem,
	api.ModelsWithHistoryDataReviewPolicy,
	api.ModelsWithHistoryDataSet,
	api.ModelsWithHistoryDataSourceModel,
	api.ModelsWithHistoryDatasetRule,
	api.ModelsWithHistoryDatasetRuleRowIgnore,
	api.ModelsWithHistoryDestination,
	api.ModelsWithHistoryExtendedContextAwareModel,
	api.ModelsWithHistoryReviewPolicy,
	api.ModelsWithHistoryRowDelete,
	api.ModelsWithHistoryRuleRevision,
	api.ModelsWithHistorySettingsModel,
	api.ModelsWithHistoryStoredItem,
	api.ModelsWithHistoryTableValueOverwrite,
	api.ModelsWithHistoryTrainingDataItem,
}

// operations is the fixed domain of LazyTypes.SIMPLE_TO_HISTORY_ACTIONS. Unlike
// models this IS validated client-side: it is a closed vocabulary, so a typo
// should exit 2 locally rather than travel to the server for a 422.
var operations = []Operation{
	api.SimpleHistoryActionAdded,
	api.SimpleHistoryActionChanged,
	api.SimpleHistoryActionEqual,
	api.SimpleHistoryActionInitiated,
	api.SimpleHistoryActionMoved,
	api.SimpleHistoryActionRemoved,
}

// undoStatuses are the three states of one nullable SQL column
// (HistoryItem.is_deleted): false, true, NULL. Also validated client-side.
var undoStatuses = []UndoStatus{
	api.HistoryUndoStatusActive,
	api.HistoryUndoStatusPartial,
	api.HistoryUndoStatusUndone,
}

// orderByFields are the sort keys the endpoint accepts.
var orderByFields = []OrderBy{
	api.HistoryOrderByCreatedAt,
	api.HistoryOrderByDatasetID,
	api.HistoryOrderByDeletedByUser,
	api.HistoryOrderByID,
	api.HistoryOrderByModel,
	api.HistoryOrderBySettingsModelID,
	api.HistoryOrderByUser,
}

// ModelNames returns the model vocabulary as strings, for --model completion.
func ModelNames() []string { return names(models) }

// OperationNames returns the accepted --operation values.
func OperationNames() []string { return names(operations) }

// UndoStatusNames returns the accepted --undo-status values.
func UndoStatusNames() []string { return names(undoStatuses) }

// OrderByNames returns the accepted --order-by values.
func OrderByNames() []string { return names(orderByFields) }

func names[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
