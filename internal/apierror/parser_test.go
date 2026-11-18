package apierror

import (
	"strings"
	"testing"
)

func TestParseErrorResponse_Format1_StringDetail(t *testing.T) {
	body := []byte(`{"detail":"Incorrect username or password"}`)
	opErr, err := ParseErrorResponse(body, 401)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "Incorrect username or password" {
		t.Errorf("expected message 'Incorrect username or password', got '%s'", opErr.Message)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}

	formatted := opErr.Format()
	if formatted != "Incorrect username or password\n"+LoginHint {
		t.Errorf("expected formatted message 'Incorrect username or password', got '%s'", formatted)
	}
}

func TestParseErrorResponse_Format2_StructuredError(t *testing.T) {
	body := []byte(`{
		"detail": {
			"msg": "Dataset not found",
			"title": "Error",
			"severity": "error",
			"error_code": "DATASET_NOT_FOUND",
			"error_id": "req_abc123xyz",
			"redirect_url": "/datasets"
		}
	}`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "Dataset not found" {
		t.Errorf("expected message 'Dataset not found', got '%s'", opErr.Message)
	}

	if opErr.Title != "Error" {
		t.Errorf("expected title 'Error', got '%s'", opErr.Title)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}

	if opErr.ErrorCode != "DATASET_NOT_FOUND" {
		t.Errorf("expected error_code 'DATASET_NOT_FOUND', got '%s'", opErr.ErrorCode)
	}

	if opErr.ErrorID != "req_abc123xyz" {
		t.Errorf("expected error_id 'req_abc123xyz', got '%s'", opErr.ErrorID)
	}

	if opErr.RedirectURL != "/datasets" {
		t.Errorf("expected redirect_url '/datasets', got '%s'", opErr.RedirectURL)
	}

	formatted := opErr.Format()
	// The backend's generic "Error" title is dropped -- see displayTitle.
	expected := "Dataset not found\n(Code: DATASET_NOT_FOUND, ID: req_abc123xyz)"
	if formatted != expected {
		t.Errorf("expected formatted message '%s', got '%s'", expected, formatted)
	}
}

// Atlas emits REVIEW_REQUIRED as an object-shaped detail. The message is
// written for cosmosview and carries both an HTML break and a Markdown link to
// a frontend route; parsing and formatting it for a terminal must retain the
// useful label while dropping both forms of markup and the unusable href. Its
// suggestions are printed under the message, before the code.
func TestParseErrorResponseReviewRequiredRendersPlainText(t *testing.T) {
	body := []byte(`{"detail":{"msg":"This rule cannot be enabled on the table. ` +
		`This table requires a review before rules can be used on it. ` +
		`Submit a review request scoped to this table and get it approved.` +
		`<br>[Request a review](/orgs/550e8400-e29b-41d4-a716-446655440000/` +
		`rule-revisions/6ba7b810-9dad-11d1-80b4-00c04fd430c8/request-review?dataset_id=42)",` +
		`"error_code":"REVIEW_REQUIRED","error_id":"5AC28A04","suggestions":[` +
		`"Submit a review request for this rule release 2.1.0 for this table.",` +
		`"An authorized reviewer must approve the review before the rule can be enabled on this table.",` +
		`"Alternatively, an admin can remove the review policy for this table."]}}`)

	opErr, err := ParseErrorResponse(body, 409)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "This rule cannot be enabled on the table. " +
		"This table requires a review before rules can be used on it. " +
		"Submit a review request scoped to this table and get it approved.\n" +
		"Request a review\n" +
		"Suggestions:\n" +
		"  - Submit a review request for this rule release 2.1.0 for this table.\n" +
		"  - An authorized reviewer must approve the review before the rule can be enabled on this table.\n" +
		"  - Alternatively, an admin can remove the review policy for this table.\n" +
		"(Code: REVIEW_REQUIRED, ID: 5AC28A04)"
	if got := opErr.Format(); got != want {
		t.Errorf("formatted REVIEW_REQUIRED error:\n got %q\nwant %q", got, want)
	}
	for _, leaked := range []string{"<br>", "[Request a review]", "/orgs/"} {
		if strings.Contains(opErr.Format(), leaked) {
			t.Errorf("terminal rendering leaked %q:\n%s", leaked, opErr.Format())
		}
	}
}

// A detail whose next step lives only in `suggestions` must print them. The
// body is the 409 DATA_REVIEW_REQUIRED that `qctl rows release` used to get;
// atlas no longer raises it, and the code survives only as an ErrorCode member
// (common/common/api_exceptions.py).
func TestParseErrorResponseDataReviewRequiredPrintsSuggestions(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })
	SetVerbosity(0)

	body := []byte(`{"detail":{"msg":"These rows require review before they can be released ` +
		`into the clean table.","error_code":"DATA_REVIEW_REQUIRED","error_id":"1F2E3D4C",` +
		`"suggestions":["Submit the row(s) for review and have an authorized reviewer approve them.",` +
		`"Alternatively, an admin can remove the data-review policy for this table."]}}`)

	opErr, err := ParseErrorResponse(body, 409)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "These rows require review before they can be released into the clean table.\n" +
		"Suggestions:\n" +
		"  - Submit the row(s) for review and have an authorized reviewer approve them.\n" +
		"  - Alternatively, an admin can remove the data-review policy for this table.\n" +
		"(Code: DATA_REVIEW_REQUIRED, ID: 1F2E3D4C)"
	if got := opErr.Format(); got != want {
		t.Errorf("formatted DATA_REVIEW_REQUIRED error:\n got %q\nwant %q", got, want)
	}
}

// The name-collision 409s (atlas settings_view_core.py, rule_view_controller.py)
// carry bare alternative NAMES as suggestions, and the message already lists
// every one of them. Printing them again as bullets would repeat the sentence,
// so a suggestion the rendered message contains is skipped -- the same check
// cosmosview's ErrorHelper.ts makes before appending one to a snackbar. The
// long name exercises a ~*value*~ marker, which the message renders whole.
func TestFormatSkipsSuggestionsTheMessageAlreadyContains(t *testing.T) {
	longName := "customer_orders_reconciled_daily_2"
	body := []byte(`{"detail":{"msg":"The name **\"orders_check\"** is already used in this table. ` +
		`Try one of these alternatives: **\"orders_check_2\"**, **\"~*` + longName + `*~\"**.",` +
		`"loc":["body","instance_name"],"suggestions":["orders_check_2","` + longName + `"]}}`)

	opErr, err := ParseErrorResponse(body, 409)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(opErr.Suggestions) != 2 {
		t.Fatalf("Suggestions = %q, want both names parsed", opErr.Suggestions)
	}

	want := `The name "orders_check" is already used in this table. ` +
		`Try one of these alternatives: "orders_check_2", "` + longName + `".` + "\nField: instance_name"
	if got := opErr.Format(); got != want {
		t.Errorf("suggestions the message already names were repeated:\n got %q\nwant %q", got, want)
	}
}

func TestObjectShapedDetailRendersCopyDataSourceIndex(t *testing.T) {
	body := []byte(`{"detail":{"msg":"Data source name is already used.","loc":["body","data_sources","1","name"]}}`)
	err := HandleHTTPErrorFromBytes(400, body, "failed to copy table")
	want := "failed to copy table: Data source name is already used.\nField: data_sources.1.name"
	if got := err.Error(); got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

func TestObjectShapedDetailPreservesNumericFieldIndex(t *testing.T) {
	body := []byte(`{"detail":{"msg":"Invalid name.","loc":["body","data_sources",1234567,"name"]}}`)
	opErr, err := ParseErrorResponse(body, 400)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := opErr.Format(), "Invalid name.\nField: data_sources.1234567.name"; got != want {
		t.Errorf("error = %q, want %q", got, want)
	}
}

// Only the suggestions the message does not already carry are listed; the
// heading goes with the first one printed.
func TestFormatListsOnlyTheSuggestionsTheMessageLacks(t *testing.T) {
	opErr := &OperationalError{
		Message:     "Cannot enable Table Rule. Wait for dry run #9 to finish, then enable the rule.",
		ErrorCode:   "RULE_VALIDATION_REQUIRED",
		Suggestions: []string{"Wait for dry run #9 to finish, then enable the rule.", "", "  ", "Open dry run #9 to see why it failed."},
	}
	want := "Cannot enable Table Rule. Wait for dry run #9 to finish, then enable the rule.\n" +
		"Suggestions:\n" +
		"  - Open dry run #9 to see why it failed.\n" +
		"(Code: RULE_VALIDATION_REQUIRED)"
	if got := opErr.Format(); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// A suggestion is backend copy like the message, so it gets the same
// treatment: markdown stripped, terminal control sequences neutralized, and a
// line break kept inside its own bullet.
func TestFormatRendersSuggestionsAsPlainText(t *testing.T) {
	opErr := &OperationalError{
		Message:     "Refused.",
		Suggestions: []string{"Ask **the reviewer** to approve it.<br>Then retry.", "Clear\x1b[2Jthe screen."},
	}
	got := opErr.Format()
	for _, leaked := range []string{"**", "<br>", "\x1b"} {
		if strings.Contains(got, leaked) {
			t.Errorf("suggestion leaked %q into the render:\n%q", leaked, got)
		}
	}
	if !strings.Contains(got, "  - Ask the reviewer to approve it.\n    Then retry.") {
		t.Errorf("a multi-line suggestion must stay inside its bullet:\n%s", got)
	}
}

// Replace exists for copy written for the React frontend, and the suggestions
// of a Replace code are that copy too ("Open the quarantine tab", atlas
// composite_status_controller.py): hidden by default, shown with the raw
// message under -v.
func TestFormatReplaceHidesSuggestionsUntilVerbose(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })

	opErr := &OperationalError{
		Message:   "**orders** was built from your primary table, but every row was sent to quarantine.",
		ErrorCode: "COMPOSITE_ALL_ROWS_QUARANTINED",
		Suggestions: []string{
			"Open the quarantine tab to review the rejected rows.",
			"Fix the flagged values or adjust the rules, then rebuild.",
		},
	}

	SetVerbosity(0)
	if got := opErr.Format(); strings.Contains(got, "quarantine tab") || strings.Contains(got, "Suggestions:") {
		t.Errorf("a Replace code's suggestions must be hidden without -v:\n%s", got)
	}

	SetVerbosity(1)
	got := opErr.Format()
	want := "Server said:\n" +
		"orders was built from your primary table, but every row was sent to quarantine.\n" +
		"Suggestions:\n" +
		"  - Open the quarantine tab to review the rejected rows.\n" +
		"  - Fix the flagged values or adjust the rules, then rebuild."
	if !strings.Contains(got, want) {
		t.Errorf("-v must show the suggestions inside the Server said: block:\n got %q\nwant it to contain %q", got, want)
	}
}

// suggestions is one peripheral field. A shape qctl does not expect -- null,
// objects, a bare string -- must cost only the suggestions, never knock the
// whole structured error back to the raw-JSON fallback.
func TestParseErrorResponseToleratesAnUnexpectedSuggestionsShape(t *testing.T) {
	for _, suggestions := range []string{`null`, `[{"text":"x"}]`, `"do this"`, `[1,2]`} {
		body := []byte(`{"detail":{"msg":"Refused.","error_code":"SOME_CODE","suggestions":` + suggestions + `}}`)
		opErr, err := ParseErrorResponse(body, 409)
		if err != nil {
			t.Fatalf("suggestions=%s: unexpected error: %v", suggestions, err)
		}
		if opErr.Suggestions != nil {
			t.Errorf("suggestions=%s: Suggestions = %q, want nil", suggestions, opErr.Suggestions)
		}
		if want := "Refused.\n(Code: SOME_CODE)"; opErr.Format() != want {
			t.Errorf("suggestions=%s: Format() = %q, want %q", suggestions, opErr.Format(), want)
		}
	}
}

func TestParseErrorResponse_Format2_WithResponseMsg(t *testing.T) {
	body := []byte(`{
		"detail": {
			"msg": "Error",
			"response_msg": "Your account setup isn't complete.\nPlease contact support.",
			"severity": "error",
			"error_code": "ERR_DB_ASYNC_TABLE_NOT_SETUP",
			"error_id": "CE7B4C7C"
		}
	}`)
	opErr, err := ParseErrorResponse(body, 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "Error" {
		t.Errorf("expected message 'Error', got '%s'", opErr.Message)
	}

	if opErr.ResponseMsg != "Your account setup isn't complete.\nPlease contact support." {
		t.Errorf("expected response_msg to be set, got '%s'", opErr.ResponseMsg)
	}

	// Format should prefer response_msg over msg
	formatted := opErr.Format()
	expected := "Your account setup isn't complete.\nPlease contact support.\n(Code: ERR_DB_ASYNC_TABLE_NOT_SETUP, ID: CE7B4C7C)"
	if formatted != expected {
		t.Errorf("expected formatted message:\n%s\ngot:\n%s", expected, formatted)
	}
}

func TestParseErrorResponse_Format2_ResponseMsgWithoutMsg(t *testing.T) {
	body := []byte(`{
		"detail": {
			"response_msg": "Something went wrong on the server.",
			"error_code": "ERR_INTERNAL"
		}
	}`)
	opErr, err := ParseErrorResponse(body, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.ResponseMsg != "Something went wrong on the server." {
		t.Errorf("expected response_msg 'Something went wrong on the server.', got '%s'", opErr.ResponseMsg)
	}

	formatted := opErr.Format()
	expected := "Something went wrong on the server.\n(Code: ERR_INTERNAL)\nThis is a transient server failure; retry this command."
	if formatted != expected {
		t.Errorf("expected formatted message:\n%s\ngot:\n%s", expected, formatted)
	}
}

func TestParseErrorResponse_Format2_WithWarning(t *testing.T) {
	body := []byte(`{
		"detail": {
			"msg": "No tables found in processing state",
			"severity": "warning"
		}
	}`)
	opErr, err := ParseErrorResponse(body, 200)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "No tables found in processing state" {
		t.Errorf("expected message 'No tables found in processing state', got '%s'", opErr.Message)
	}

	if opErr.Severity != "warning" {
		t.Errorf("expected severity 'warning', got '%s'", opErr.Severity)
	}
}

func TestParseErrorResponse_Format3_ValidationErrors(t *testing.T) {
	body := []byte(`{
		"detail": [
			{
				"loc": ["body", "email"],
				"msg": "invalid email format",
				"type": "value_error"
			},
			{
				"loc": ["body", "password"],
				"msg": "field required",
				"type": "value_error.missing"
			}
		]
	}`)
	opErr, err := ParseErrorResponse(body, 422)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "invalid email format\nField: email\nfield required\nField: password"
	if opErr.Message != expected {
		t.Errorf("expected message:\n%s\ngot:\n%s", expected, opErr.Message)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}
}

// A list index in loc prints as the integer it is, however many digits it has.
func TestParseErrorResponse_ValidationErrorIndexPrintsAsAnInteger(t *testing.T) {
	body := []byte(`{"detail": [{"loc": ["body", "rows", 1234567, "name"], "msg": "field required", "type": "missing"}]}`)
	opErr, err := ParseErrorResponse(body, 422)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "field required\nField: rows.1234567.name"; opErr.Message != want {
		t.Errorf("message = %q, want %q", opErr.Message, want)
	}
}

func TestParseErrorResponse_Format4_ArrayOfSimpleErrors(t *testing.T) {
	body := []byte(`{
		"detail": [
			{
				"msg": "First error message"
			}
		]
	}`)
	opErr, err := ParseErrorResponse(body, 400)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "First error message" {
		t.Errorf("expected message 'First error message', got '%s'", opErr.Message)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}
}

func TestParseErrorResponse_EmptyBody(t *testing.T) {
	body := []byte{}
	opErr, err := ParseErrorResponse(body, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "Request failed with status 500"
	if opErr.Message != expected {
		t.Errorf("expected message '%s', got '%s'", expected, opErr.Message)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}
}

func TestParseErrorResponse_NonJSON(t *testing.T) {
	body := []byte("Internal Server Error")
	opErr, err := ParseErrorResponse(body, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "Internal Server Error" {
		t.Errorf("expected message 'Internal Server Error', got '%s'", opErr.Message)
	}

	if opErr.Severity != "error" {
		t.Errorf("expected severity 'error', got '%s'", opErr.Severity)
	}
}

func TestOperationalError_Format_OnlyMessage(t *testing.T) {
	opErr := &OperationalError{
		Message:  "Simple error message",
		Severity: "error",
	}

	formatted := opErr.Format()
	expected := "Simple error message"
	if formatted != expected {
		t.Errorf("expected '%s', got '%s'", expected, formatted)
	}
}

func TestOperationalError_Format_WithTitle(t *testing.T) {
	opErr := &OperationalError{
		Message:  "Something went wrong",
		Title:    "Preparing table",
		Severity: "error",
	}

	formatted := opErr.Format()
	expected := "Preparing table\nSomething went wrong"
	if formatted != expected {
		t.Errorf("expected '%s', got '%s'", expected, formatted)
	}
}

func TestOperationalError_Format_WithErrorID(t *testing.T) {
	opErr := &OperationalError{
		Message:  "Processing failed",
		ErrorID:  "req_xyz789",
		Severity: "error",
	}

	formatted := opErr.Format()
	expected := "Processing failed\n(ID: req_xyz789)"
	if formatted != expected {
		t.Errorf("expected '%s', got '%s'", expected, formatted)
	}
}

func TestOperationalError_Format_WithErrorCode(t *testing.T) {
	opErr := &OperationalError{
		Message:   "Invalid format",
		ErrorCode: "INVALID_FORMAT",
		Severity:  "error",
	}

	formatted := opErr.Format()
	expected := "Invalid format\n(Code: INVALID_FORMAT)"
	if formatted != expected {
		t.Errorf("expected '%s', got '%s'", expected, formatted)
	}
}

func TestOperationalError_Format_AllFields(t *testing.T) {
	opErr := &OperationalError{
		Message:   "Something went wrong",
		Title:     "Ingestion Error",
		ErrorID:   "req_abc123",
		ErrorCode: "INVALID_FORMAT",
		Severity:  "error",
	}

	formatted := opErr.Format()
	expected := "Ingestion Error\nSomething went wrong\n(Code: INVALID_FORMAT, ID: req_abc123)"
	if formatted != expected {
		t.Errorf("expected '%s', got '%s'", expected, formatted)
	}
}

func TestParseErrorResponse_JSONWithoutDetailField(t *testing.T) {
	// Simulates a reverse proxy returning JSON without the "detail" key
	// (e.g. {"error": "bad gateway"} or just {})
	tests := []struct {
		name       string
		body       string
		statusCode int
		wantMsg    string
	}{
		{
			name:       "empty JSON object",
			body:       `{}`,
			statusCode: 502,
			wantMsg:    "server returned 502 Bad Gateway",
		},
		{
			name:       "JSON with non-detail fields",
			body:       `{"error": "bad gateway"}`,
			statusCode: 502,
			wantMsg:    "server returned 502 Bad Gateway",
		},
		{
			name:       "503 service unavailable",
			body:       `{}`,
			statusCode: 503,
			wantMsg:    "server returned 503 Service Unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opErr, err := ParseErrorResponse([]byte(tt.body), tt.statusCode)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if opErr.Message != tt.wantMsg {
				t.Errorf("expected message %q, got %q", tt.wantMsg, opErr.Message)
			}
		})
	}
}

func TestOperationalError_Format_AuthErrorHint(t *testing.T) {
	opErr := &OperationalError{
		Message:   "Invalid bearer token.",
		ErrorCode: "AUTH_ERR_INVALID_TOKEN",
		Severity:  "error",
	}

	formatted := opErr.Format()
	expected := "Invalid bearer token.\n(Code: AUTH_ERR_INVALID_TOKEN)\nAsk the user to run `qctl auth login` in their own terminal, then re-run this command."
	if formatted != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, formatted)
	}
}

func TestParseErrorResponse_StructuredError_WithoutSeverity(t *testing.T) {
	body := []byte(`{
		"detail": {
			"msg": "Dataset not found"
		}
	}`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if opErr.Message != "Dataset not found" {
		t.Errorf("expected message 'Dataset not found', got '%s'", opErr.Message)
	}

	// Should default to "error" when severity is not provided
	if opErr.Severity != "error" {
		t.Errorf("expected default severity 'error', got '%s'", opErr.Severity)
	}
}

func TestToPlainText(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			// The ticket's opening repro, verbatim from
			// atlas capi/capi/auth/permissions.py.
			name: "html break and markdown bold",
			in:   "You don't have any tables yet!<br>Please create a new table by clicking on **File > New Table**.",
			want: "You don't have any tables yet!\nPlease create a new table by clicking on File > New Table.",
		},
		{
			// A frontend route is unreachable from a terminal: keep the
			// label, drop the href.
			name: "frontend route link keeps only its label",
			in:   "[View Setup Details](/orgs/abc/datasets/1/ingestion-jobs/2)",
			want: "View Setup Details",
		},
		{
			// An absolute URL is still usable, so it survives.
			name: "absolute url survives",
			in:   "See [the docs](https://example.com/x).",
			want: "See the docs (https://example.com/x).",
		},
		{
			name: "setup message loses its markup and trailing blanks",
			in:   "We are preparing **orders** for review.\n\n**Current status:** Profiling\n\n",
			want: "We are preparing orders for review.\n\nCurrent status: Profiling",
		},
		{
			name: "paragraph tags",
			in:   "<p>first</p><p>second</p>",
			want: "first\n\nsecond",
		},
		{
			name: "plain text is untouched",
			in:   "Our database took too long to respond. Please try again in a few moments.",
			want: "Our database took too long to respond. Please try again in a few moments.",
		},
		{
			// A regex quoted back in a validation message has the exact
			// "]" immediately followed by "(" shape mdLink matches, but it
			// is not a markdown link -- it must round-trip unchanged.
			name: "regex in message is not mistaken for a link",
			in:   "pattern must match ^[A-Z]([0-9])$",
			want: "pattern must match ^[A-Z]([0-9])$",
		},
		{
			// mailto: is neither a frontend route, an anchor, nor an
			// absolute http(s) URL, so it is left alone rather than
			// rewritten into "text (mailto:...)".
			name: "mailto link target is not rewritten",
			in:   "[email us](mailto:support@example.com)",
			want: "[email us](mailto:support@example.com)",
		},
		{
			// The bold-red wrapper is frontend styling like **bold**, and a
			// terminal error has no colour for it.
			name: "bold red loses its delimiters",
			in:   `~|"abc"|~ is not a valid integer.`,
			want: `"abc" is not a valid integer.`,
		},
		// Value markers, from atlas common/tests/fixtures/
		// message_value_markers_cases.json. A marked value is printed whole,
		// between the quotes and inside the style the backend wrote around it.
		{
			name: "marked bad value keeps its quotes and loses every delimiter",
			in:   `~|"~*Hyundai Sonata Hybrid Limited*~"|~ is invalid`,
			want: `"Hyundai Sonata Hybrid Limited" is invalid`,
		},
		{
			name: "typographic quotes around a marker are the backend's",
			in:   "~|“~*Hyundai Sonata Hybrid Limited*~”|~ is invalid",
			want: "“Hyundai Sonata Hybrid Limited” is invalid",
		},
		{
			name: "short and long values in prose",
			in:   `Name changed from **"Sonata"** to **"~*Hyundai Sonata Hybrid Limited*~"**.`,
			want: `Name changed from "Sonata" to "Hyundai Sonata Hybrid Limited".`,
		},
		{
			name: "marker in link text keeps the label",
			in:   "**max_days_back** changed from **5** to **10** in [~*Very long dataset rule name for the claims table*~](/orgs/1/datasets/2).",
			want: "max_days_back changed from 5 to 10 in Very long dataset rule name for the claims table.",
		},
		{
			// The payload's "](b) *~" would close the link early if it were
			// parsed; it is not.
			name: "delimiter-shaped marker in link text",
			in:   `[~*a](b) \*~ c*~](/orgs/1/alerts/2)`,
			want: "a](b) *~ c",
		},
		{
			name: "long column name and a line break",
			in:   "Inconsistent date format in **~*Policy Effective Date (UTC)*~** column.<br>Modify this to the format: `%Y`",
			want: "Inconsistent date format in Policy Effective Date (UTC) column.\nModify this to the format: `%Y`",
		},
		{
			// Every character here would be syntax in prose.
			name: "payload is literal text",
			in:   `Value "~*\*\*x\*\* <br> [a](/b) ~|y|~ C:\\temp\\*~" kept`,
			want: `Value "**x** <br> [a](/b) ~|y|~ C:\temp\" kept`,
		},
		{
			name: "bold-red delimiters inside a payload do not end the wrapper",
			in:   `~|"~*a|~ ~|b*~"|~ ok`,
			want: `"a|~ ~|b" ok`,
		},
		{
			name: "several markers, an empty one and a multiline one",
			in:   "~*a*~~*b*~ \"~**~\" ~|\"~*line 1\nline 2*~\"|~",
			want: "ab \"\" \"line 1\nline 2\"",
		},
		{
			// A payload's own spacing is the value, so trimming the message
			// must not reach it.
			name: "payload spaces survive the trim",
			in:   "~*  padded  *~",
			want: "  padded  ",
		},
		{
			name: "removed combined marker is a plain value with pipes",
			in:   "~*|value|*~ is plain",
			want: "|value| is plain",
		},
		{
			name: "unterminated marker is prose",
			in:   "Broken ~*oops",
			want: "Broken ~*oops",
		},
		{
			name: "markdown escape before an opener holds no marker",
			in:   `\~*not a marker*~`,
			want: `\~*not a marker*~`,
		},
		{
			// A tooltip is a link to "##", not a truncation, and stays as the
			// renderer has always shown one.
			name: "tooltip link is prose",
			in:   `CI is [**3.8%**](## "3.5%–4.1%") for "~*claims over the reserve limit*~"`,
			want: `CI is 3.8% (## "3.5%–4.1%") for "claims over the reserve limit"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := toPlainText(tt.in); got != tt.want {
				t.Errorf("toPlainText(%q)\n got %q\nwant %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestFormatDropsUninformativeTitle: every middleware-handled error sets
// title "Error" (atlas capi/capi/middleware.py), which as a leading line
// tells the reader nothing. A title the raise site actually chose is kept.
func TestFormatDropsUninformativeTitle(t *testing.T) {
	generic := &OperationalError{Message: "Dataset not found", Title: "Error", Severity: SeverityError}
	if got := generic.Format(); got != "Dataset not found" {
		t.Errorf("generic title: got %q, want %q", got, "Dataset not found")
	}

	specific := &OperationalError{Message: "Something went wrong", Title: "Ingestion Error", Severity: SeverityError}
	want := "Ingestion Error\nSomething went wrong"
	if got := specific.Format(); got != want {
		t.Errorf("specific title: got %q, want %q", got, want)
	}
}

// The ticket's rule: a Replace code prints qctl's wording, never the server's
// -- but always the code and the request ID, so a support conversation still
// has something to go on.
func TestFormatReplacesFrontendCopy(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })
	SetVerbosity(0)

	opErr := &OperationalError{
		Message:   "We are preparing **orders** for review.\n\n**Current status:** Profiling\n\n[View Setup Details](/orgs/a/datasets/1/training-jobs/2)",
		Title:     "Preparing table",
		Severity:  "info",
		ErrorCode: "TABLE_SETUP_IN_PROGRESS",
		ErrorID:   "5AC28A04",
	}

	got := opErr.Format()

	for _, leaked := range []string{"View Setup Details", "/orgs/", "Current status", "**"} {
		if strings.Contains(got, leaked) {
			t.Errorf("backend copy %q leaked into a Replace render:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "qctl describe table") {
		t.Errorf("Replace text missing from render:\n%s", got)
	}
	if !strings.Contains(got, "(Code: TABLE_SETUP_IN_PROGRESS, ID: 5AC28A04)") {
		t.Errorf("code and id must survive a Replace:\n%s", got)
	}
}

func TestFormatReplaceShowsProgress(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })
	SetVerbosity(0)

	pct := 42
	opErr := &OperationalError{
		Message:         "We are preparing **orders** for review.",
		ErrorCode:       "TABLE_SETUP_IN_PROGRESS",
		ProgressPercent: &pct,
	}
	if got := opErr.Format(); !strings.Contains(got, "42% complete") {
		t.Errorf("progress_percent not rendered:\n%s", got)
	}
}

func TestFormatReplaceShowsRawTextUnderVerbose(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })

	opErr := &OperationalError{
		Message:   "Upload a file to start preparing **orders**.",
		ErrorCode: "EMPTY_TABLE_MESSAGE",
	}

	SetVerbosity(0)
	if got := opErr.Format(); strings.Contains(got, "Upload a file") {
		t.Errorf("raw server text must be hidden without -v:\n%s", got)
	}

	SetVerbosity(1)
	got := opErr.Format()
	if !strings.Contains(got, "Server said:") {
		t.Errorf("-v must label the raw server text:\n%s", got)
	}
	// Even under -v the text is rendered as plain text, not raw markdown.
	if !strings.Contains(got, "Upload a file to start preparing orders.") {
		t.Errorf("-v must show the server's own message:\n%s", got)
	}
}

// Passthrough is the default and must stay byte-identical to today's
// behavior apart from the markup stripping Task 3 added.
func TestFormatPassthroughIsUnchangedByVerbosity(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })

	opErr := &OperationalError{
		Message:   "We temporarily lost connection to our database. Please try again shortly.",
		ErrorCode: "ERR_DB_CONN_LOST",
		ErrorID:   "9F1C2D3E",
	}
	want := "We temporarily lost connection to our database. Please try again shortly.\n" +
		"(Code: ERR_DB_CONN_LOST, ID: 9F1C2D3E)"

	SetVerbosity(0)
	if got := opErr.Format(); got != want {
		t.Errorf("passthrough at -v0:\n got %q\nwant %q", got, want)
	}
	SetVerbosity(9)
	if got := opErr.Format(); got != want {
		t.Errorf("passthrough at -v9 must not gain a Server said: block:\n%s", got)
	}
}

func TestParseErrorResponseCapturesProgressPercent(t *testing.T) {
	body := []byte(`{"detail":{"msg":"preparing","error_code":"TABLE_SETUP_IN_PROGRESS","progress_percent":73}}`)
	opErr, err := ParseErrorResponse(body, 404)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if opErr.ProgressPercent == nil {
		t.Fatal("progress_percent was not parsed")
	}
	if *opErr.ProgressPercent != 73 {
		t.Errorf("progress_percent = %d, want 73", *opErr.ProgressPercent)
	}
}

// EmptyState with Text follows the same render path as Replace: on any route
// that still reaches Format() (ResolveTable, ResolveID, apply, ...) qctl's
// own wording is printed instead of the backend's frontend CTA.
func TestFormatEmptyStateWithTextReplacesFrontendCopy(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })
	SetVerbosity(0)

	opErr := &OperationalError{
		Message:   "You don't have any tables yet!<br>Please create a new table by clicking on **File > New Table**.",
		Severity:  "info",
		ErrorCode: CodeNoTableMessage,
		ErrorID:   "5AC28A04",
	}

	got := opErr.Format()

	for _, leaked := range []string{"File > New Table", "Welcome", "**"} {
		if strings.Contains(got, leaked) {
			t.Errorf("backend copy %q leaked into an EmptyState render:\n%s", leaked, got)
		}
	}
	if !strings.Contains(got, "Run `qctl auth me`") {
		t.Errorf("EmptyState replacement text missing from render:\n%s", got)
	}
	if !strings.Contains(got, "(Code: NO_TABLE_MESSAGE, ID: 5AC28A04)") {
		t.Errorf("code and id must survive an EmptyState-with-text render:\n%s", got)
	}
}

func TestFormatEmptyStateShowsRawTextUnderVerbose(t *testing.T) {
	t.Cleanup(func() { SetVerbosity(0) })

	opErr := &OperationalError{
		Message:   "You don't have any tables yet!<br>Please create a new table by clicking on **File > New Table**.",
		ErrorCode: CodeNoTableMessage,
	}

	SetVerbosity(0)
	if got := opErr.Format(); strings.Contains(got, "File > New Table") {
		t.Errorf("raw server text must be hidden without -v:\n%s", got)
	}

	SetVerbosity(1)
	got := opErr.Format()
	if !strings.Contains(got, "Server said:") {
		t.Errorf("-v must label the raw server text:\n%s", got)
	}
	// Even under -v the text is rendered as plain text, not raw markdown.
	if !strings.Contains(got, "Please create a new table by clicking on File > New Table.") {
		t.Errorf("-v must show the server's own message, plain-texted:\n%s", got)
	}
}

// A Passthrough render with no message must not leave a leading blank line
// before the (Code: ...) meta line -- the analogous guard the Replace branch
// already has on its "Server said:" append.
func TestFormatPassthroughOmitsEmptyMessage(t *testing.T) {
	opErr := &OperationalError{
		ErrorCode: "ERR_SOME_CODE",
	}
	want := "(Code: ERR_SOME_CODE)"
	if got := opErr.Format(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ToPlainText is the exported door onto the same renderer every error message
// goes through. internal/history needs it for the undo/redo STEP message,
// which is a 200-response body and never reaches Format().
func TestToPlainTextIsTheExportedToPlainText(t *testing.T) {
	const raw = "**a**<br>[b](/orgs/1)"
	if ToPlainText(raw) != toPlainText(raw) {
		t.Errorf("ToPlainText(%q) = %q, want %q", raw, ToPlainText(raw), toPlainText(raw))
	}
}
