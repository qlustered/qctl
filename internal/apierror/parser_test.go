package apierror

import (
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
	if formatted != "Incorrect username or password" {
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
	expected := "Error\nDataset not found\n(Code: DATASET_NOT_FOUND, ID: req_abc123xyz)"
	if formatted != expected {
		t.Errorf("expected formatted message '%s', got '%s'", expected, formatted)
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
	expected := "Something went wrong on the server.\n(Code: ERR_INTERNAL)"
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
		Title:    "Error",
		Severity: "error",
	}

	formatted := opErr.Format()
	expected := "Error\nSomething went wrong"
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
	expected := "Invalid bearer token.\n(Code: AUTH_ERR_INVALID_TOKEN)\nRun `qctl auth login` to authenticate."
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
