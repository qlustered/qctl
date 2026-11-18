package profiling

import (
	"errors"
	"fmt"

	"github.com/qlustered/qctl/internal/apierror"
)

// CodeNoTrainingFiles is the error_code of the 409 refusing to profile a
// table that has no file enabled for training.
const CodeNoTrainingFiles = "NO_TRAINING_FILES"

// ExplainNoTrainingFiles appends a pointer to the page that says what enables
// a file for training when err carries CodeNoTrainingFiles, and returns any
// other error unchanged.
func ExplainNoTrainingFiles(err error) error {
	var opErr *apierror.OperationalError
	if !errors.As(err, &opErr) || opErr.ErrorCode != CodeNoTrainingFiles {
		return err
	}
	return fmt.Errorf("%w\n\nWhat enables a file for training:\n  qctl explain profiling-job", err)
}
