package deliverables

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/testutil"
)

func TestRunTransitionsPreserveExactNumbersAndResponseBody(t *testing.T) {
	for _, action := range []string{"mark-delivered", "cancel"} {
		t.Run(action, func(t *testing.T) {
			mock := testutil.NewMockAPIServer()
			defer mock.Close()
			body := `{"id":42,"state":"delivered","run_params":{"count":9007199254740993},"delivery_note":null,"future_field":null}`
			mock.RegisterHandler("POST", runPath+"/"+action, func(w http.ResponseWriter, _ *http.Request) {
				testutil.RespondJSON(w, http.StatusOK, json.RawMessage(body))
			})
			c := NewClient(mock.URL(), testOrgID, 0)
			var result *RunTransitionResult
			var err error
			if action == "mark-delivered" {
				result, err = c.MarkDelivered("token", 42, "Emailed to the carrier")
			} else {
				result, err = c.CancelRun("token", 42)
			}
			if err != nil {
				t.Fatal(err)
			}
			if result.Run.RunParams["count"] != int64(9007199254740993) {
				t.Errorf("parameters = %#v, want exact int64", result.Run.RunParams)
			}
			if strings.TrimSpace(string(result.Body)) != body || result.Run.DeliveryNote != nil {
				t.Errorf("body = %s; note = %v", result.Body, result.Run.DeliveryNote)
			}
		})
	}
}
