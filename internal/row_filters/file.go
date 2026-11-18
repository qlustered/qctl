package row_filters //nolint:revive // Matches the resource domain.

import (
	"fmt"
	"os"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/pkg/manifest"
)

// ReadFile reads one filter document, YAML or JSON, from path and validates it
// with Validate(saved, field). Error messages name the filter as field.
func ReadFile(path, field string, saved bool) (*Filter, error) {
	if path == "" {
		return nil, qerrors.NewBadUsagef("%s names no file", field)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", field, err)
	}
	docs, err := manifest.SplitDocuments(data)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, field+" is not valid YAML or JSON")
	}
	switch {
	case len(docs) == 0:
		return nil, qerrors.NewBadUsagef("%s is empty: give a row filter with conditions or a predicate", field)
	case len(docs) > 1:
		return nil, qerrors.NewBadUsagef("%s holds %d documents; give one row filter", field, len(docs))
	}
	var f Filter
	type plain Filter
	if err := manifest.StrictUnmarshal(docs[0], (*plain)(&f)); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, field)
	}
	if err := f.Validate(saved, field); err != nil {
		return nil, err
	}
	return &f, nil
}
