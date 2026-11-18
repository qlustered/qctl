package ingestion

import (
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// ParseFilter parses the --filter value of run and kill ingestion-job
// (key=value pairs joined by commas) into list params. A filter without a
// kind or kinds key matches RunnableKinds; one naming a kind outside
// RunnableKinds is refused. Every refusal is a bad-usage error.
func ParseFilter(filter string) (GetIngestionJobsParams, error) {
	var params GetIngestionJobsParams
	for _, part := range strings.Split(filter, ",") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return params, qerrors.NewBadUsagef("invalid filter format: %s (expected key=value)", part)
		}
		if err := applyFilterParam(&params, strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])); err != nil {
			return params, err
		}
	}
	if len(params.Kinds) == 0 {
		params.Kinds = RunnableKinds()
	}
	for _, k := range params.Kinds {
		if !slices.Contains(RunnableKinds(), k) {
			return params, qerrors.NewBadUsagef("a %s job can't be run again or stopped; leave it out of the filter's kind", k)
		}
	}
	return params, nil
}

// applyFilterParam applies one filter key and value to params.
func applyFilterParam(params *GetIngestionJobsParams, key, value string) error {
	switch key {
	case "table-id", "cloud-source-id", "file-id", "stored-item-id":
		return applyIDFilter(params, key, value)
	case "state", "states", "kind", "kinds":
		return applyListFilter(params, key, value)
	case "is-dry-run":
		isDryRun, err := strconv.ParseBool(value)
		if err != nil {
			return qerrors.NewBadUsagef("invalid is-dry-run: %q (expected true or false)", value)
		}
		params.IsDryRun = &isDryRun
	case "created-by":
		id, err := uuid.Parse(value)
		if err != nil {
			return qerrors.NewBadUsagef("invalid created-by user ID %q: expected a full UUID", value)
		}
		params.WhodunitID = &id
	default:
		return qerrors.NewBadUsagef("unknown filter key: %s", key)
	}
	return nil
}

// applyIDFilter applies a numeric filter key to params.
func applyIDFilter(params *GetIngestionJobsParams, key, value string) error {
	id, err := strconv.Atoi(value)
	if err != nil {
		return qerrors.NewBadUsagef("invalid %s: %s", key, value)
	}
	switch key {
	case "table-id":
		params.DatasetID = &id
	case "cloud-source-id":
		params.DataSourceID = &id
	default:
		params.StoredItemID = &id
	}
	return nil
}

// applyListFilter applies state, states, kind or kinds to params. The plural
// keys take pipe-separated values.
func applyListFilter(params *GetIngestionJobsParams, key, value string) error {
	values := []string{value}
	if strings.HasSuffix(key, "s") {
		values = strings.Split(value, "|")
		for i := range values {
			values[i] = strings.TrimSpace(values[i])
		}
	}
	if strings.HasPrefix(key, "kind") {
		kinds, err := ParseJobKinds(key, values)
		params.Kinds = kinds
		return err
	}
	for _, state := range values {
		if err := ValidateJobState(key, state); err != nil {
			return err
		}
	}
	params.States = values
	return nil
}
