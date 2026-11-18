package copycmd

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/apierror"
	"github.com/qlustered/qctl/internal/datasets"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// entryFieldKeys maps a field the server names in a data source entry's error
// to the --data-sources-file key that sets it, where the two differ.
var entryFieldKeys = map[string]string{"custom_api_file_url": "simple_url"}

// locateDataSourceError returns err, from the POST of request, reworded to
// name the --data-sources-file entry, and field, that a 400 or 5xx at loc
// [body, data_sources, "N", FIELD?] points to. Its Unwrap is err. Any other
// error is returned as given.
func locateDataSourceError(err error, request datasets.CopyRequest) error {
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || request.DataSources == nil ||
		(opErr.StatusCode != http.StatusBadRequest && opErr.StatusCode < http.StatusInternalServerError) {
		return err
	}
	entries := *request.DataSources
	index, field, ok := dataSourceEntryLoc(opErr.Loc, len(entries))
	if !ok {
		return err
	}
	if key, renamed := entryFieldKeys[field]; renamed {
		field = key
	}
	located := *opErr
	located.Field = field
	context, found := strings.CutSuffix(err.Error(), opErr.Error())
	if !found {
		context = ""
	}
	return qerrors.Caused(context+dataSourceEntryLabel(index+1, entries[index].SourceID)+": "+located.Format(), err)
}

// dataSourceEntryLoc returns the index and field of loc
// [body, data_sources, "N", FIELD?], N a string naming one of count entries
// and FIELD "" when absent; ok is false for any other loc.
func dataSourceEntryLoc(loc []interface{}, count int) (index int, field string, ok bool) {
	if (len(loc) != 3 && len(loc) != 4) || loc[0] != "body" || loc[1] != "data_sources" {
		return 0, "", false
	}
	n, isString := loc[2].(string)
	index, err := strconv.Atoi(n)
	if !isString || err != nil || strconv.Itoa(index) != n || index < 0 || index >= count {
		return 0, "", false
	}
	if len(loc) == 4 {
		field, isString = loc[3].(string)
		if !isString || field == "" {
			return 0, "", false
		}
	}
	return index, field, true
}
