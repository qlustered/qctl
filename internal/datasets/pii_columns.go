package datasets

import (
	"fmt"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/apierror"
)

// droppedPiiColumns returns the entries of stored that sent does not list, in
// stored order and each once. It is nil when sent is nil.
func droppedPiiColumns(stored []string, sent *[]string) []string {
	if sent == nil {
		return nil
	}
	kept := make(map[string]bool, len(*sent)+len(stored))
	for _, column := range *sent {
		kept[column] = true
	}
	var dropped []string
	for _, column := range stored {
		if !kept[column] {
			dropped = append(dropped, column)
			kept[column] = true
		}
	}
	return dropped
}

// explainPiiColumnsRemoval returns err with a note naming the pii_columns
// entries body drops from existing when err is a 403 at loc
// ["body", "pii_columns"] and body drops at least one; otherwise it returns
// err unchanged. The result unwraps to err.
func explainPiiColumnsRemoval(err error, existing *DatasetFull, body *api.DataSetPatchRequestSchema) error {
	if existing == nil || body == nil || !apierror.IsPiiColumnsRefusal(err) {
		return err
	}
	dropped := droppedPiiColumns(existing.PiiColumns, body.PiiColumns)
	if len(dropped) == 0 {
		return err
	}
	return fmt.Errorf("%w\n\nOnly an admin can remove a column from the PII list. "+
		"This apply drops %s from spec.pii_columns, so the table was not updated. "+
		"Keep %s in spec.pii_columns to apply the other changes, or ask an admin to apply this manifest",
		err, strings.Join(dropped, ", "), pluralThem(len(dropped)))
}

// pluralThem returns "it" for one and "them" otherwise.
func pluralThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}
