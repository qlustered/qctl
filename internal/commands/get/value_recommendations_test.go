package get

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/config"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

const recommendRoute = "/api/orgs/" + testOrgID + "/value-mapping/recommendation"

// The three answers this route can give, as the wire spells them.
//
// strongAndWeak carries both flags explicitly, which is what the recommender
// does today. absentStrong is the shape the wire schema allows and the
// recommender does not currently produce — `strong: bool = True` under
// response_model_exclude_unset — and it is here because the command's *bool
// handling is only observable through it.
const (
	recommendTwoValues    = `[{"value":"California","strong":true},{"value":"Canada","strong":false}]`
	recommendAbsentStrong = `[{"value":"California"}]`
	// NULL!~~ is the backend's SQL-NULL sentinel (common/common/types.py), and
	// the recommender appends it with strong=true for every nullable field. It
	// must survive to stdout byte for byte: a caller pipes it straight back
	// into `qctl rows set-cell --value`.
	recommendNullSentinel = `[{"value":"Anaheim","strong":true},{"value":"NULL!~~","strong":true}]`
)

// recommendEnv wires the route, the table name lookup and the cloud-source
// lookup, and keeps every body the route received in arrival order.
//
// The bodies are a list rather than a counter because the request IS the
// contract here: which id field carries a value decides which settings model
// the recommender resolves, and a second call would otherwise overwrite the
// first silently.
type recommendEnv struct {
	mock        *testutil.MockAPIServer
	bodies      []string
	sourceQuery url.Values
	sourceHits  int
	tableHits   int
}

// newRecommendEnv answers the route with status/resp. The table name lookup is
// registered here too, resolving `orders` to dataset 42 — the id every body
// assertion below expects.
func newRecommendEnv(t *testing.T, status int, resp string) *recommendEnv {
	t.Helper()
	env := testutil.NewTestEnv(t)
	t.Cleanup(env.Cleanup)
	mock := testutil.NewMockAPIServer()
	t.Cleanup(mock.Close)
	endpointKey, _ := config.NormalizeEndpointKey(mock.URL())
	env.SetupConfigWithOrg(mock.URL(), "test@example.com", testOrgID)
	env.SetupCredential(endpointKey, testOrgID, "test-token")

	e := &recommendEnv{mock: mock}
	mock.RegisterHandler("POST", recommendRoute, func(w http.ResponseWriter, r *http.Request) {
		body := ""
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = strings.TrimSpace(string(b))
		}
		e.bodies = append(e.bodies, body)
		testutil.RespondJSON(w, status, json.RawMessage(resp))
	})
	mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/datasets", func(w http.ResponseWriter, _ *http.Request) {
		e.tableHits++
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":42,"name":"orders","state":"active"}]}`))
	})
	return e
}

// serveCloudSourceByName answers the exact-name search
// cmdutil.ResolveCloudSource makes for `--cloud-source <name>`, and keeps the
// query: dataset_id is what scopes the name to THIS table, and is the whole
// difference between the name path and the id path.
func (e *recommendEnv) serveCloudSourceByName() {
	e.mock.RegisterHandler("GET", "/api/orgs/"+testOrgID+"/data-sources", func(w http.ResponseWriter, r *http.Request) {
		e.sourceHits++
		e.sourceQuery = r.URL.Query()
		testutil.RespondJSON(w, http.StatusOK, json.RawMessage(
			`{"total_rows":1,"page":1,"results":[{"id":7,"name":"s3-nightly"}]}`))
	})
}

func (e *recommendEnv) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := setupTestCommandForTable(NewValueRecommendationsCommand())
	root.SilenceUsage = true
	root.SilenceErrors = true
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"get", "value-recommendations"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// sent decodes the one body the route received, failing if the count is not 1.
func (e *recommendEnv) sent(t *testing.T) map[string]any {
	t.Helper()
	if len(e.bodies) != 1 {
		t.Fatalf("requests to the recommender = %d, want 1: %v", len(e.bodies), e.bodies)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(e.bodies[0]), &got); err != nil {
		t.Fatalf("decode request body %q: %v", e.bodies[0], err)
	}
	return got
}

// assertNulls fails unless every named key reached the wire as null. The keys
// are not omitempty in the generated schema, so an unset id is an explicit
// null, and asserting that is what catches a value leaking into the wrong one.
func assertNulls(t *testing.T, sent map[string]any, keys ...string) {
	t.Helper()
	for _, k := range keys {
		if v, ok := sent[k]; !ok || v != nil {
			t.Errorf("%s = %v (present: %t), want null", k, v, ok)
		}
	}
}

// --- the body, per flag combination ----------------------------------------

// The bare lookup: a table, a field and a value, and nothing else. The table
// rides in dataset_id and settings_model_id stays null — sending a settings
// model would bypass the server-side resolution that makes a table id enough.
func TestGetValueRecommendations_BareLookupSendsTheTableAsDatasetID(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

	stdout, stderr, err := e.run(t, "orders", "--field", "state", "--value", "CA")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := e.sent(t)
	if sent["dataset_id"] != float64(42) {
		t.Errorf("dataset_id = %v, want 42 (the resolved table)", sent["dataset_id"])
	}
	if sent["field_name"] != "state" {
		t.Errorf("field_name = %v, want state", sent["field_name"])
	}
	if sent["value"] != "CA" {
		t.Errorf("value = %v, want CA", sent["value"])
	}
	assertNulls(t, sent, "settings_model_id", "data_source_model_id", "alert_item_id", "warning_item_id_")

	// The trailing underscore is the backend's own spelling. A rename here
	// would send the warning id into a field the recommender never reads, and
	// the route would answer 200 with a silently unscoped list.
	if !strings.Contains(e.bodies[0], `"warning_item_id_"`) {
		t.Errorf("body does not carry warning_item_id_ under its wire name: %s", e.bodies[0])
	}

	for _, want := range []string{"VALUE", "STRONG", "California", "Canada"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout is missing %q\ngot: %q", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// A numeric positional is an id and costs no lookup; a name costs exactly one.
// Both end in the same dataset_id, which is the point of the id-then-name
// resolver the positional goes through.
func TestGetValueRecommendations_PositionalResolvesIDThenName(t *testing.T) {
	for _, tc := range []struct {
		name       string
		arg        string
		wantLookup int
	}{
		{"an id is used as-is", "42", 0},
		{"a name is looked up", "orders", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

			if _, _, err := e.run(t, tc.arg, "--field", "state", "--value", "CA"); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if e.tableHits != tc.wantLookup {
				t.Errorf("table name lookups = %d, want %d", e.tableHits, tc.wantLookup)
			}
			if sent := e.sent(t); sent["dataset_id"] != float64(42) {
				t.Errorf("dataset_id = %v, want 42", sent["dataset_id"])
			}
		})
	}
}

// --cloud-source-id is passed STRAIGHT THROUGH: no lookup, deliberately. A GET
// here would buy a check the server does not make anyway — the view verifies
// only that the source is in the organization — at the price of a request on
// every call, and its only other product would be a name nothing prints.
func TestGetValueRecommendations_CloudSourceIDIsPassedThroughUnresolved(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)
	e.serveCloudSourceByName() // registered so a stray lookup is COUNTED, not 404'd

	if _, _, err := e.run(t, "orders", "--field", "state", "--value", "CA",
		"--cloud-source-id", "9"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.sourceHits != 0 {
		t.Errorf("cloud-source lookups = %d, want 0 — an id is not resolved", e.sourceHits)
	}
	sent := e.sent(t)
	if sent["data_source_model_id"] != float64(9) {
		t.Errorf("data_source_model_id = %v, want 9", sent["data_source_model_id"])
	}
	assertNulls(t, sent, "alert_item_id", "warning_item_id_", "settings_model_id")
}

// --cloud-source is a NAME, and the resolver scopes the search by dataset_id
// before matching it exactly. That scoping is the reason the flag exists
// alongside --cloud-source-id: it is the only one of the two that can tell a
// source of this table from a source of another table in the same org.
func TestGetValueRecommendations_CloudSourceNameIsResolvedWithinTheTable(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)
	e.serveCloudSourceByName()

	if _, _, err := e.run(t, "orders", "--field", "state", "--value", "CA",
		"--cloud-source", "s3-nightly"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e.sourceHits != 1 {
		t.Fatalf("cloud-source lookups = %d, want 1", e.sourceHits)
	}
	if got := e.sourceQuery.Get("dataset_id"); got != "42" {
		t.Errorf("the name search was not scoped to the table: dataset_id = %q, want 42", got)
	}
	if got := e.sourceQuery.Get("search_query"); got != "s3-nightly" {
		t.Errorf("search_query = %q, want s3-nightly", got)
	}
	if sent := e.sent(t); sent["data_source_model_id"] != float64(7) {
		t.Errorf("data_source_model_id = %v, want 7 (the resolved source)", sent["data_source_model_id"])
	}
}

// The two issue flags, each on its own, so a value leaking into the other
// field could not hide behind a sibling that serialized correctly.
func TestGetValueRecommendations_IssueIDsRideUnderTheirOwnWireNames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flag   string
		set    string
		unset  string
		wantID float64
	}{
		{"alert", "--alert-item-id", "alert_item_id", "warning_item_id_", 8817},
		{"warning", "--warning-item-id", "warning_item_id_", "alert_item_id", 991},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

			id := "8817"
			if tc.name == "warning" {
				id = "991"
			}
			if _, _, err := e.run(t, "orders", "--field", "state", "--value", "Calif.", tc.flag, id); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			sent := e.sent(t)
			if sent[tc.set] != tc.wantID {
				t.Errorf("%s = %v, want %v", tc.set, sent[tc.set], tc.wantID)
			}
			assertNulls(t, sent, tc.unset, "data_source_model_id")
		})
	}
}

// --- refusals, every one of them before the first request -------------------

// Every flag refusal this command makes, and the assertion that matters as
// much as the exit code: NO request, not even the table lookup. All of these
// run before cmdutil.Bootstrap, so a wrong command line costs nothing and is
// reported before any error in a config file the user did not just type.
func TestGetValueRecommendations_FlagRefusalsExitTwoWithoutARequest(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"both issue flags", []string{"orders", "--field", "state", "--value", "CA",
			"--alert-item-id", "1", "--warning-item-id", "2"}},
		{"both source flags", []string{"orders", "--field", "state", "--value", "CA",
			"--cloud-source", "s3-nightly", "--cloud-source-id", "7"}},
		{"no --field", []string{"orders", "--value", "CA"}},
		{"empty --field", []string{"orders", "--field", "", "--value", "CA"}},
		{"no --value", []string{"orders", "--field", "state"}},
		{"empty --cloud-source", []string{"orders", "--field", "state", "--value", "CA",
			"--cloud-source", ""}},
		{"zero --cloud-source-id", []string{"orders", "--field", "state", "--value", "CA",
			"--cloud-source-id", "0"}},
		{"negative --cloud-source-id", []string{"orders", "--field", "state", "--value", "CA",
			"--cloud-source-id", "-1"}},
		{"zero --alert-item-id", []string{"orders", "--field", "state", "--value", "CA",
			"--alert-item-id", "0"}},
		{"negative --warning-item-id", []string{"orders", "--field", "state", "--value", "CA",
			"--warning-item-id", "-5"}},
		{"two tables", []string{"orders", "--table", "trips", "--field", "state", "--value", "CA"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

			_, _, err := e.run(t, tc.args...)
			if err == nil {
				t.Fatal("expected the command line to be refused")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
			}
			if reqs := e.mock.Requests(); len(reqs) != 0 {
				t.Errorf("a refused command line spent %d request(s): %v", len(reqs), reqs)
			}
		})
	}
}

// `--table` and `--table-id` reach the same table as the positional, so the
// selector is the group's, not a local reinvention.
func TestGetValueRecommendations_TableFlagsAddressTheSameTable(t *testing.T) {
	for _, args := range [][]string{{"--table", "orders"}, {"--table-id", "42"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

			if _, _, err := e.run(t, append(args, "--field", "state", "--value", "CA")...); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sent := e.sent(t); sent["dataset_id"] != float64(42) {
				t.Errorf("dataset_id = %v, want 42", sent["dataset_id"])
			}
		})
	}
}

// --value "" is LEGAL and it is the useful ask: with nothing to match against,
// the recommender takes its empty-query branch and lists the values the field
// already holds. Testing the value rather than its presence would make that
// unreachable, so this is the guard on the guard.
func TestGetValueRecommendations_EmptyValueIsSentAndNotRefused(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

	if _, _, err := e.run(t, "orders", "--field", "state", "--value", ""); err != nil {
		t.Fatalf(`--value "" was refused: %v`, err)
	}
	sent := e.sent(t)
	if v, ok := sent["value"]; !ok || v != "" {
		t.Errorf("value = %v (present: %t), want the empty string on the wire", v, ok)
	}
}

// --- the empty list ---------------------------------------------------------

// An empty list is a normal answer — an array or JSON field always gets one,
// and a field name this table does not have gets one too — so it is exit 0,
// nothing on stdout, and the house message on stderr.
func TestGetValueRecommendations_EmptyListExitsZero(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, `[]`)

	stdout, stderr, err := e.run(t, "orders", "--field", "payload", "--value", "x")
	if err != nil {
		t.Fatalf("an empty list must not be an error: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty — the message belongs on stderr", stdout)
	}
	if !strings.Contains(stderr, "No value recommendations") {
		t.Errorf("stderr is missing the empty-result message\ngot: %q", stderr)
	}
}

// The structured formats do NOT take the stderr path: a caller piping JSON gets
// a List with items [] on stdout and a clean stderr.
func TestGetValueRecommendations_EmptyListIsAnEmptyList(t *testing.T) {
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, `[]`)

			stdout, stderr, err := e.run(t, "orders", "--field", "payload", "--value", "x", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if items := unpagedListItems(t, format, stdout); len(items) != 0 {
				t.Errorf("items = %v, want []", items)
			}
			if stderr != "" {
				t.Errorf("stderr = %q, want empty for a structured format", stderr)
			}
		})
	}
}

// --columns is validated on the EMPTY path too. Before printEmptyResult was a
// choke point, an empty list returned before the printer that checks the names
// was ever built, so a typo'd column silently exited 0.
func TestGetValueRecommendations_UnknownColumnIsRefusedOnAnEmptyList(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, `[]`)

	_, _, err := e.run(t, "orders", "--field", "state", "--value", "CA", "--columns", "confidence")
	if err == nil {
		t.Fatal("expected an unknown --columns name to be refused")
	}
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitBadUsage, err)
	}
}

// --- the three output formats ----------------------------------------------

// -o json and -o yaml wrap the RAW wire list in a List with null paging, so a
// caller reading .strong sees exactly what the server sent — including its
// absence — and a weak row is kept.
func TestGetValueRecommendations_StructuredOutputIsAListOfTheRawRows(t *testing.T) {
	var want []map[string]any
	if err := json.Unmarshal([]byte(recommendTwoValues), &want); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

			stdout, _, err := e.run(t, "orders", "--field", "state", "--value", "CA", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := unpagedListItems(t, format, stdout); !reflect.DeepEqual(got, want) {
				t.Errorf("items = %v\nwant %v", got, want)
			}
		})
	}
}

// -o name prints one value per line and nothing else. It is hand-written
// because output.extractName looks for a json field called name, email or id
// and this schema has none of the three: through the shared printer a
// non-empty list would print NOTHING at exit 0.
func TestGetValueRecommendations_NameOutputIsOneValuePerLine(t *testing.T) {
	e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)

	stdout, _, err := e.run(t, "orders", "--field", "state", "--value", "CA", "-o", "name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "California\nCanada\n" {
		t.Errorf("stdout = %q, want one value per line and no header", stdout)
	}
}

// The absent-strong shape. The wire schema declares `strong: bool = True`, so
// an absent flag is the schema default and means TRUE; a nil pointer handed to
// the reflection printer would render an empty cell and read as false — the
// opposite answer. json keeps the absence, because that is what the server sent.
func TestGetValueRecommendations_AbsentStrongRendersTrue(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		e := newRecommendEnv(t, http.StatusOK, recommendAbsentStrong)

		stdout, _, err := e.run(t, "orders", "--field", "state", "--value", "CA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "true") {
			t.Errorf("an absent strong must render as true (the schema default)\ngot: %q", stdout)
		}
	})

	t.Run("json keeps the absence", func(t *testing.T) {
		e := newRecommendEnv(t, http.StatusOK, recommendAbsentStrong)

		stdout, _, err := e.run(t, "orders", "--field", "state", "--value", "CA", "-o", "json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := unpagedListItems(t, "json", stdout)
		if len(got) != 1 {
			t.Fatalf("got %d rows, want 1: %s", len(got), stdout)
		}
		if _, ok := got[0]["strong"]; ok {
			t.Errorf("json invented a strong the server did not send: %s", stdout)
		}
	})
}

// NULL!~~ is a recommended VALUE, not an error and not an empty cell. It is
// what the caller pipes back into `qctl rows set-cell --value` to empty the
// cell, so both renderings must keep it byte for byte.
func TestGetValueRecommendations_NullSentinelSurvivesToStdout(t *testing.T) {
	for _, format := range []string{"table", "name"} {
		t.Run(format, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusOK, recommendNullSentinel)

			stdout, _, err := e.run(t, "orders", "--field", "city", "--value", "", "-o", format)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(stdout, "NULL!~~") {
				t.Errorf("the SQL-NULL sentinel was mangled\ngot: %q", stdout)
			}
		})
	}
}

// --- the exit codes ---------------------------------------------------------

// The status codes this route actually produces, asserted through the whole
// command rather than the client alone: the exit code rides an errors.Unwrap
// chain, and a %v anywhere between the client and here degrades it to 1 while
// leaving the message looking perfect.
func TestGetValueRecommendations_ServerErrorsCarryTheirExitCodes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		body   string
		status int
		want   int
	}{
		// The table has no settings model and no source resolved: the caller
		// can fix it, so it is bad usage, not a missing table.
		{"400 is bad usage", `{"detail":{"msg":"Either the data source or settings model id need to be passed."}}`,
			http.StatusBadRequest, qerrors.ExitBadUsage},
		{"404 is not found", `{"detail":{"msg":"cloud source 9 can not be found."}}`,
			http.StatusNotFound, qerrors.ExitNotFound},
		// The route needs settings:read, which an external user does not have
		// — the one role this command refuses outright.
		{"403 is unauthorized", `{"detail":{"msg":"You do not have permission"}}`,
			http.StatusForbidden, qerrors.ExitUnauthorized},
		{"401 is unauthorized", `{"detail":{"msg":"Not authenticated"}}`,
			http.StatusUnauthorized, qerrors.ExitUnauthorized},
		{"500 is generic", `{"detail":{"msg":"Internal Server Error"}}`,
			http.StatusInternalServerError, qerrors.ExitGenericError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecommendEnv(t, tc.status, tc.body)

			_, _, err := e.run(t, "orders", "--field", "state", "--value", "CA")
			if err == nil {
				t.Fatalf("expected an error for %d", tc.status)
			}
			if code := qerrors.GetExitCode(err); code != tc.want {
				t.Errorf("exit code = %d, want %d (err: %v)", code, tc.want, err)
			}
		})
	}
}

// An explicit issue id selects a different recommendation path. A miss must
// therefore stay distinguishable from the ordinary list the route would have
// produced without that issue, for both wire spellings.
func TestGetValueRecommendations_MissingIssueExitsNotFound(t *testing.T) {
	for _, tc := range []struct {
		name    string
		flag    string
		wireKey string
		message string
	}{
		{"alert", "--alert-item-id", "alert_item_id", "Alert not found."},
		{"warning", "--warning-item-id", "warning_item_id_", "Warning not found."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newRecommendEnv(t, http.StatusNotFound,
				`{"detail":{"msg":"`+tc.message+`"}}`)

			_, _, err := e.run(t, "orders", "--field", "state", "--value", "CA",
				tc.flag, "999999999")
			if err == nil {
				t.Fatal("expected the missing issue to fail")
			}
			if code := qerrors.GetExitCode(err); code != qerrors.ExitNotFound {
				t.Errorf("exit code = %d, want %d (err: %v)", code, qerrors.ExitNotFound, err)
			}
			if !strings.Contains(err.Error(), tc.message) {
				t.Errorf("server message was lost: %v", err)
			}
			if sent := e.sent(t); sent[tc.wireKey] != float64(999999999) {
				t.Errorf("%s = %v, want 999999999", tc.wireKey, sent[tc.wireKey])
			}
		})
	}
}

// recommendMasked is the answer for a caller who sees the field masked (atlas
// capi/capi/controllers/pii_masking.py mask_value_recommendations): every
// value but the NULL!~~ sentinel is masked and names the field.
const recommendMasked = `[{"value":"•••••rnia","strong":true,"masked_fields":["state"]},` +
	`{"value":"•••••","strong":false,"masked_fields":["state"]},{"value":"NULL!~~","strong":true}]`

// The table prints each mask as sent and names the masked field on stderr.
// -o name leaves the masks out, since one written back to its PII column is a
// 400, and counts them on stderr; the NULL!~~ sentinel is still printed.
func TestGetValueRecommendations_MaskedValues(t *testing.T) {
	t.Run("table", func(t *testing.T) {
		e := newRecommendEnv(t, http.StatusOK, recommendMasked)
		stdout, stderr, err := e.run(t, "orders", "--field", "state", "--value", "CA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(stdout, "•••••rnia") || !strings.Contains(stdout, "NULL!~~") {
			t.Errorf("table does not print the values as sent\ngot: %q", stdout)
		}
		if want := "Masked fields: state. A masked value is •••••, or ••••• and its last 4 characters.\n"; !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})
	t.Run("name", func(t *testing.T) {
		e := newRecommendEnv(t, http.StatusOK, recommendMasked)
		stdout, stderr, err := e.run(t, "orders", "--field", "state", "--value", "CA", "-o", "name")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if stdout != "NULL!~~\n" {
			t.Errorf("stdout = %q, want only the unmasked sentinel", stdout)
		}
		if want := "2 masked value(s) left out: a masked value cannot be written back to its PII column.\n"; !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want %q", stderr, want)
		}
	})
	t.Run("unmasked names nothing", func(t *testing.T) {
		e := newRecommendEnv(t, http.StatusOK, recommendTwoValues)
		_, stderr, err := e.run(t, "orders", "--field", "state", "--value", "CA")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if strings.Contains(stderr, "Masked") {
			t.Errorf("stderr names masked fields for an unmasked answer: %q", stderr)
		}
	})
}
