package stored_items

import (
	"encoding/json"
	"net/http"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const replacementPathItem3 = "/api/orgs/" + siOrgID + "/stored-items/3/replacement-question"

// The server answers null for a file with no question; that is no question,
// not an empty one.
func TestGetReplacementQuestionNullIsNil(t *testing.T) {
	c, mock := newSIClient(t)
	mock.RegisterHandler(http.MethodGet, replacementPathItem3, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`null`))
	})

	got, err := c.GetReplacementQuestion("tok", 3)
	if err != nil {
		t.Fatalf("GetReplacementQuestion: %v", err)
	}
	if got != nil {
		t.Errorf("question = %+v, want nil", got)
	}
}

func TestGetReplacementQuestionReadsAnOpenQuestion(t *testing.T) {
	c, mock := newSIClient(t)
	mock.RegisterHandler(http.MethodGet, replacementPathItem3, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(`{"new_stored_item_id":3,"older_stored_item_id":2,`+
			`"new_file_name":"b.csv","old_file_name":"a.csv","old_file_sent_at":"2026-10-01T09:00:00Z",`+
			`"old_period_label":null,"new_period_label":null,"answer":null,"answered_at":null}`))
	})

	got, err := c.GetReplacementQuestion("tok", 3)
	if err != nil {
		t.Fatalf("GetReplacementQuestion: %v", err)
	}
	if got == nil || got.OlderStoredItemID != 2 || got.Answer != nil {
		t.Errorf("question = %+v, want an open question about file 2", got)
	}
}

func TestGetReplacementQuestionForbiddenExits4(t *testing.T) {
	c, mock := newSIClient(t)
	mock.RegisterHandler(http.MethodGet, replacementPathItem3, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, json.RawMessage(`{"detail":{"msg":"no permission"}}`))
	})

	_, err := c.GetReplacementQuestion("tok", 3)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitUnauthorized {
		t.Errorf("exit code = %d, want %d (err %v)", code, qerrors.ExitUnauthorized, err)
	}
}
