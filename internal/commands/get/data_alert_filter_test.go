package get

import (
	"net/http"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// --alert-item-id is not derived from GetDataParams (its field is an unsendable
// union), so the assertion that matters is that the value reaches the WIRE.
func TestGetDataSendsAlertItemIDQueryParam(t *testing.T) {
	mock := dataTestMock(t)
	var gotAlert, gotBad string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, r *http.Request) {
			gotAlert = r.URL.Query().Get("alert_item_id")
			gotBad = r.URL.Query().Get("is_bad_data")
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--alert-item-id", "8817"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if gotAlert != "8817" {
		t.Errorf("alert_item_id = %q, want %q", gotAlert, "8817")
	}
	// The request editor rewrites the whole query string; the derived flags
	// must survive it.
	if gotBad != "true" {
		t.Errorf("is_bad_data = %q, want true", gotBad)
	}
	// Control for the "no request was made" assertion in the refusal test
	// below: prove a get_data request DOES show up in mock.Requests() as a
	// string containing "/data", so that assertion can actually go red.
	var sawData bool
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/data") {
			sawData = true
		}
	}
	if !sawData {
		t.Fatalf("mock.Requests() never records \"/data\" (%v); the no-request assertion "+
			"in TestGetDataRefusesAlertItemIDWithoutIsBadData cannot fail", mock.Requests())
	}
}

// Without --is-bad-data the backend accepts the parameter, drops the filter and
// answers 200 with the whole unfiltered page — so qctl refuses first, and the
// refusal must cost no request at all.
func TestGetDataRefusesAlertItemIDWithoutIsBadData(t *testing.T) {
	mock := dataTestMock(t)
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, _ *http.Request) {
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})

	out, err := execData(t, []string{"1", "--alert-item-id", "8817"})
	if err == nil {
		t.Fatalf("expected refusal, got success:\n%s", out)
	}
	if code := qerrors.GetExitCode(err); code != 2 {
		t.Errorf("exit code = %d, want 2 (err: %v)", code, err)
	}
	for _, req := range mock.Requests() {
		if strings.Contains(req, "/data") {
			t.Errorf("refusal must precede every request; saw %q", req)
		}
	}
}

// A non-positive id is a client-side usage error too: 0 is what an unset pflag
// reads back as, and a negative can never name an alert.
func TestGetDataRefusesNonPositiveAlertItemID(t *testing.T) {
	for _, value := range []string{"0", "-3"} {
		t.Run(value, func(t *testing.T) {
			dataTestMock(t)
			_, err := execData(t, []string{"1", "--is-bad-data", "--alert-item-id", value})
			if err == nil {
				t.Fatal("expected refusal")
			}
			if code := qerrors.GetExitCode(err); code != 2 {
				t.Errorf("exit code = %d, want 2 (err: %v)", code, err)
			}
		})
	}
}

// The pairing with --warning-item-id is a legal AND server-side, not a
// conflict: both filters land in one AND-joined list, so the intersection is a
// real query. qctl must send both rather than invent a restriction.
func TestGetDataAllowsAlertAndWarningFiltersTogether(t *testing.T) {
	mock := dataTestMock(t)
	var got map[string]string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, r *http.Request) {
			got = map[string]string{
				"alert_item_id":    r.URL.Query().Get("alert_item_id"),
				"warning_item_id_": r.URL.Query().Get("warning_item_id_"),
			}
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})

	out, err := execData(t, []string{"1", "--is-bad-data", "--alert-item-id", "8817", "--warning-item-id", "55"})
	if err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if got["alert_item_id"] != "8817" || got["warning_item_id_"] != "55" {
		t.Errorf("both filters must reach the wire, got %v", got)
	}
}

// No flag, no parameter. alert_item_id=0 would be read as a real alert id the
// backend then 404s on.
func TestGetDataOmitsAlertItemIDWhenUnset(t *testing.T) {
	mock := dataTestMock(t)
	var raw string
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets/1/data",
		func(w http.ResponseWriter, r *http.Request) {
			raw = r.URL.RawQuery
			testutil.RespondJSON(w, http.StatusOK, sampleIssueData())
		})

	if out, err := execData(t, []string{"1", "--is-bad-data"}); err != nil {
		t.Fatalf("execute: %v\noutput:\n%s", err, out)
	}
	if strings.Contains(raw, "alert_item_id") {
		t.Errorf("alert_item_id sent without the flag, query = %q", raw)
	}
}
