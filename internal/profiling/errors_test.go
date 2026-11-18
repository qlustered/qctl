package profiling

import (
	"errors"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestExplainNoTrainingFilesLeavesOtherErrorsAlone(t *testing.T) {
	for _, err := range []error{
		errors.New("plain"),
		apierror.HandleHTTPErrorFromBytes(409,
			[]byte(`{"detail":{"msg":"busy","error_code":"ERR_MIGRATION_CONFLICT"}}`), "request failed"),
	} {
		if got := ExplainNoTrainingFiles(err); got != err {
			t.Errorf("ExplainNoTrainingFiles(%v) = %v, want the same error", err, got)
		}
	}
}

func TestExplainNoTrainingFilesKeepsTheExitCode(t *testing.T) {
	err := apierror.HandleHTTPErrorFromBytes(409,
		[]byte(`{"detail":{"msg":"nothing to profile","error_code":"NO_TRAINING_FILES"}}`), "request failed")

	got := ExplainNoTrainingFiles(err)
	if got == err {
		t.Fatal("expected a NO_TRAINING_FILES refusal to be explained")
	}
	if code := qerrors.GetExitCode(got); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitBadUsage)
	}
}
