package output

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

type TestUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func TestPrinter_PrintJSON(t *testing.T) {
	tests := []struct {
		name                  string
		data                  interface{}
		allowPlaintextSecrets bool
		wantContains          []string
		wantNotContains       []string
	}{
		{
			name: "masks password by default",
			data: TestUser{
				ID:       "123",
				Email:    "test@example.com",
				Password: "secret123",
				Name:     "Test User",
			},
			allowPlaintextSecrets: false,
			wantContains:          []string{`"email": "test@example.com"`, `"name": "Test User"`},
			wantNotContains:       []string{"secret123"},
		},
		{
			name: "shows password with flag",
			data: TestUser{
				ID:       "123",
				Email:    "test@example.com",
				Password: "secret123",
				Name:     "Test User",
			},
			allowPlaintextSecrets: true,
			wantContains:          []string{`"email": "test@example.com"`, `"password": "secret123"`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printer := NewPrinter(Options{
				Format:                FormatJSON,
				AllowPlaintextSecrets: tt.allowPlaintextSecrets,
				Writer:                &buf,
			})

			err := printer.Print(tt.data)
			if err != nil {
				t.Errorf("Print() error = %v", err)
				return
			}

			output := buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("Print() output should contain %q, got: %s", want, output)
				}
			}

			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("Print() output should not contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

func TestPrinter_PrintTable(t *testing.T) {
	tests := []struct {
		name         string
		data         interface{}
		opts         Options
		wantContains []string
	}{
		{
			name: "prints table with headers",
			data: []TestUser{
				{ID: "1", Email: "user1@example.com", Password: "pass1", Name: "User 1"},
				{ID: "2", Email: "user2@example.com", Password: "pass2", Name: "User 2"},
			},
			opts: Options{
				Format:    FormatTable,
				NoHeaders: false,
			},
			wantContains: []string{"ID", "EMAIL", "NAME", "user1@example.com", "user2@example.com"},
		},
		{
			name: "prints table without headers",
			data: []TestUser{
				{ID: "1", Email: "user1@example.com", Password: "pass1", Name: "User 1"},
			},
			opts: Options{
				Format:    FormatTable,
				NoHeaders: true,
			},
			wantContains: []string{"user1@example.com"},
		},
		{
			name: "prints specific columns",
			data: []TestUser{
				{ID: "1", Email: "user1@example.com", Password: "pass1", Name: "User 1"},
			},
			opts: Options{
				Format:  FormatTable,
				Columns: []string{"id", "name"},
			},
			wantContains: []string{"ID", "NAME", "1", "User 1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			tt.opts.Writer = &buf
			printer := NewPrinter(tt.opts)

			err := printer.Print(tt.data)
			if err != nil {
				t.Errorf("Print() error = %v", err)
				return
			}

			output := buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("Print() output should contain %q, got: %s", want, output)
				}
			}
		})
	}
}

func TestPrinter_PrintNames(t *testing.T) {
	data := []TestUser{
		{ID: "1", Name: "Alice", Email: "alice@example.com"},
		{ID: "2", Name: "Bob", Email: "bob@example.com"},
	}

	var buf bytes.Buffer
	printer := NewPrinter(Options{
		Format: FormatName,
		Writer: &buf,
	})

	err := printer.Print(data)
	if err != nil {
		t.Errorf("Print() error = %v", err)
		return
	}

	output := buf.String()
	// Should print names (first name field found)
	if !strings.Contains(output, "Alice") || !strings.Contains(output, "Bob") {
		t.Errorf("Print() output should contain names, got: %s", output)
	}
}

func TestSecretMask_IsSecret(t *testing.T) {
	sm := NewSecretMask()

	tests := []struct {
		fieldName string
		want      bool
	}{
		{"password", true},
		{"Password", true},
		{"PASSWORD", true},
		{"access_token", true},
		{"secret", true},
		{"api_key", true},
		{"name", false},
		{"email", false},
		{"id", false},
		// Additional secret patterns
		{"aws_secret_access_key", true},
		{"AWS_SECRET_ACCESS_KEY", true},
		{"db_password", true},
		{"client_secret", true},
		{"encryption_key", true},
		{"connection_string", true},
		{"private_key", true},
		{"service_account_key", true},
		// Non-secrets
		{"user_name", false},
		{"description", false},
		{"created_at", false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			if got := sm.IsSecret(tt.fieldName); got != tt.want {
				t.Errorf("IsSecret(%q) = %v, want %v", tt.fieldName, got, tt.want)
			}
		})
	}
}

// TestPrinter_PrintYAML tests YAML output formatting
func TestPrinter_PrintYAML(t *testing.T) {
	tests := []struct {
		name                  string
		data                  interface{}
		allowPlaintextSecrets bool
		wantContains          []string
		wantNotContains       []string
	}{
		{
			name: "masks password in YAML by default",
			data: TestUser{
				ID:       "123",
				Email:    "test@example.com",
				Password: "secret123",
				Name:     "Test User",
			},
			allowPlaintextSecrets: false,
			wantContains:          []string{"email: test@example.com", "name: Test User", "password: '***'"},
			wantNotContains:       []string{"secret123"},
		},
		{
			name: "shows password in YAML with flag",
			data: TestUser{
				ID:       "123",
				Email:    "test@example.com",
				Password: "secret123",
				Name:     "Test User",
			},
			allowPlaintextSecrets: true,
			wantContains:          []string{"email: test@example.com", "password: secret123"},
		},
		{
			name: "handles slice in YAML",
			data: []TestUser{
				{ID: "1", Email: "user1@example.com", Password: "pass1", Name: "User 1"},
				{ID: "2", Email: "user2@example.com", Password: "pass2", Name: "User 2"},
			},
			allowPlaintextSecrets: false,
			wantContains:          []string{"id:", "email: user1@example.com", "password: '***'"},
			wantNotContains:       []string{"pass1", "pass2"},
		},
		{
			name: "handles nested structures in YAML with structs",
			data: struct {
				User struct {
					Name     string `json:"name"`
					Password string `json:"password"`
				} `json:"user"`
				Count int `json:"count"`
			}{
				User: struct {
					Name     string `json:"name"`
					Password string `json:"password"`
				}{
					Name:     "John",
					Password: "secret",
				},
				Count: 42,
			},
			allowPlaintextSecrets: false,
			wantContains:          []string{"user:", "name: John", "password: '***'", "count: 42"},
			wantNotContains:       []string{"secret"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			printer := NewPrinter(Options{
				Format:                FormatYAML,
				AllowPlaintextSecrets: tt.allowPlaintextSecrets,
				Writer:                &buf,
			})

			err := printer.Print(tt.data)
			if err != nil {
				t.Errorf("Print() error = %v", err)
				return
			}

			output := buf.String()
			for _, want := range tt.wantContains {
				if !strings.Contains(output, want) {
					t.Errorf("Print() output should contain %q, got: %s", want, output)
				}
			}

			for _, notWant := range tt.wantNotContains {
				if strings.Contains(output, notWant) {
					t.Errorf("Print() output should not contain %q, got: %s", notWant, output)
				}
			}
		})
	}
}

// TestPrinter_PrintTable_EdgeCases tests table output edge cases
func TestPrinter_PrintTable_EdgeCases(t *testing.T) {
	type ComplexStruct struct {
		ID        string            `json:"id"`
		Tags      []string          `json:"tags"`
		Metadata  map[string]string `json:"metadata"`
		NilPtr    *string           `json:"nil_ptr"`
		EmptyTags []string          `json:"empty_tags"`
	}

	t.Run("empty slice", func(t *testing.T) {
		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatTable,
			Writer: &buf,
		})

		err := printer.Print([]TestUser{})
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		// Should produce no output for empty slice
		output := buf.String()
		if len(output) > 0 {
			t.Errorf("Expected empty output for empty slice, got: %s", output)
		}
	})

	t.Run("nil pointer in struct", func(t *testing.T) {
		data := ComplexStruct{
			ID:        "123",
			Tags:      []string{"tag1", "tag2"},
			NilPtr:    nil,
			EmptyTags: []string{},
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:  FormatTable,
			Columns: []string{"id", "nil_ptr"},
			Writer:  &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "123") {
			t.Errorf("Expected to find ID in output, got: %s", output)
		}
	})

	t.Run("complex nested types as JSON", func(t *testing.T) {
		data := ComplexStruct{
			ID:       "123",
			Tags:     []string{"tag1", "tag2"},
			Metadata: map[string]string{"key": "value"},
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:  FormatTable,
			Columns: []string{"id", "tags", "metadata"},
			Writer:  &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Complex types should be JSON-encoded
		if !strings.Contains(output, "123") {
			t.Errorf("Expected to find ID in output, got: %s", output)
		}
	})

	t.Run("unicode and emoji in values", func(t *testing.T) {
		data := []TestUser{
			{ID: "1", Name: "用户 👤", Email: "user@example.com"},
			{ID: "2", Name: "Müller ñ", Email: "test@example.com"},
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:  FormatTable,
			Columns: []string{"id", "name"},
			Writer:  &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "用户 👤") {
			t.Errorf("Expected to find Unicode/emoji in output, got: %s", output)
		}
		if !strings.Contains(output, "Müller ñ") {
			t.Errorf("Expected to find Unicode characters in output, got: %s", output)
		}
	})

	t.Run("very wide table with many columns", func(t *testing.T) {
		type WideStruct struct {
			Col1  string `json:"col1"`
			Col2  string `json:"col2"`
			Col3  string `json:"col3"`
			Col4  string `json:"col4"`
			Col5  string `json:"col5"`
			Col6  string `json:"col6"`
			Col7  string `json:"col7"`
			Col8  string `json:"col8"`
			Col9  string `json:"col9"`
			Col10 string `json:"col10"`
		}

		data := WideStruct{
			Col1: "value1", Col2: "value2", Col3: "value3", Col4: "value4", Col5: "value5",
			Col6: "value6", Col7: "value7", Col8: "value8", Col9: "value9", Col10: "value10",
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatTable,
			Writer: &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Should contain all values
		for i := 1; i <= 10; i++ {
			expected := fmt.Sprintf("value%d", i)
			if !strings.Contains(output, expected) {
				t.Errorf("Expected to find %q in output", expected)
			}
		}
	})
}

// TestPrinter_PrintNames_EdgeCases tests name extraction edge cases
func TestPrinter_PrintNames_EdgeCases(t *testing.T) {
	type NoNameStruct struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}

	type OnlyIDStruct struct {
		ID string `json:"id"`
	}

	type OnlyEmailStruct struct {
		Email string `json:"email"`
	}

	type PriorityStruct struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}

	t.Run("object with no name/id/email falls back to id", func(t *testing.T) {
		data := OnlyIDStruct{ID: "12345"}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatName,
			Writer: &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "12345") {
			t.Errorf("Expected to find ID in output, got: %s", output)
		}
	})

	t.Run("object with email but no name", func(t *testing.T) {
		data := OnlyEmailStruct{Email: "test@example.com"}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatName,
			Writer: &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		if !strings.Contains(output, "test@example.com") {
			t.Errorf("Expected to find email in output, got: %s", output)
		}
	})

	t.Run("priority: name > email > id", func(t *testing.T) {
		data := PriorityStruct{
			ID:    "123",
			Email: "test@example.com",
			Name:  "John Doe",
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatName,
			Writer: &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Should print name, not email or id
		if !strings.Contains(output, "John Doe") {
			t.Errorf("Expected to find name in output, got: %s", output)
		}
		if strings.Contains(output, "test@example.com") {
			t.Errorf("Should not contain email when name is present, got: %s", output)
		}
	})

	t.Run("object with only description field - no output", func(t *testing.T) {
		data := NoNameStruct{Description: "Some description"}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format: FormatName,
			Writer: &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := strings.TrimSpace(buf.String())
		if output != "" {
			t.Errorf("Expected no output for struct without name/id/email, got: %s", output)
		}
	})
}

// TestTruncate_VerySmallWidth tests truncation with very small widths
func TestTruncate_VerySmallWidth(t *testing.T) {
	tests := []struct {
		name     string
		maxWidth int
		value    string
		expected string
	}{
		{
			name:     "width 1 - no ellipsis",
			maxWidth: 1,
			value:    "longvalue",
			expected: "l",
		},
		{
			name:     "width 2 - no ellipsis",
			maxWidth: 2,
			value:    "longvalue",
			expected: "lo",
		},
		{
			name:     "width 3 - with ellipsis",
			maxWidth: 3,
			value:    "longvalue",
			expected: "...",
		},
		{
			name:     "width 4 - with ellipsis",
			maxWidth: 4,
			value:    "longvalue",
			expected: "l...",
		},
		{
			name:     "unicode - truncation at byte boundary",
			maxWidth: 10,
			value:    "hello world",
			expected: "hello w...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPrinter(Options{
				MaxColumnWidth: tt.maxWidth,
			})

			result := p.truncateValue(tt.value)
			if result != tt.expected {
				t.Errorf("truncateValue() = %q, want %q", result, tt.expected)
			}
		})
	}
}

// TestSecretMask_DeeplyNested tests masking of deeply nested secrets
func TestSecretMask_DeeplyNested(t *testing.T) {
	type Credentials struct {
		Username string `json:"username"`
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
	}

	type Config struct {
		Name        string      `json:"name"`
		Credentials Credentials `json:"credentials"`
		Token       string      `json:"token"`
	}

	t.Run("deeply nested struct secrets", func(t *testing.T) {
		data := Config{
			Name: "MyConfig",
			Credentials: Credentials{
				Username: "admin",
				Password: "secret123",
				APIKey:   "key123",
			},
			Token: "token123",
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatJSON,
			AllowPlaintextSecrets: false,
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Should contain non-secret fields
		if !strings.Contains(output, "MyConfig") {
			t.Errorf("Expected to find 'MyConfig' in output")
		}
		if !strings.Contains(output, "admin") {
			t.Errorf("Expected to find username in output")
		}

		// Should NOT contain secret values
		if strings.Contains(output, "secret123") {
			t.Errorf("Should not contain password in output")
		}
		if strings.Contains(output, "key123") {
			t.Errorf("Should not contain api_key in output")
		}
		if strings.Contains(output, "token123") {
			t.Errorf("Should not contain token in output")
		}

		// Should contain masked values
		if !strings.Contains(output, "***") {
			t.Errorf("Expected to find '***' mask in output")
		}
	})

	t.Run("secrets in maps with structs", func(t *testing.T) {
		type NestedCreds struct {
			APIKey      string `json:"api_key"`
			Description string `json:"description"`
		}

		type MapData struct {
			Name   string      `json:"name"`
			Secret string      `json:"password"`
			Nested NestedCreds `json:"nested"`
		}

		data := MapData{
			Name:   "test",
			Secret: "secret",
			Nested: NestedCreds{
				APIKey:      "key123",
				Description: "desc",
			},
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatJSON,
			AllowPlaintextSecrets: false,
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Should NOT contain secret values
		if strings.Contains(output, "secret") {
			t.Errorf("Should not contain password in output")
		}
		if strings.Contains(output, "key123") {
			t.Errorf("Should not contain api_key in output")
		}

		// Should contain non-secret values
		if !strings.Contains(output, "test") {
			t.Errorf("Expected to find 'test' in output")
		}
		if !strings.Contains(output, "desc") {
			t.Errorf("Expected to find 'desc' in output")
		}
	})

	t.Run("secrets in slice of structs", func(t *testing.T) {
		data := []Credentials{
			{Username: "user1", Password: "pass1", APIKey: "key1"},
			{Username: "user2", Password: "pass2", APIKey: "key2"},
		}

		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatJSON,
			AllowPlaintextSecrets: false,
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
		}

		output := buf.String()
		// Should NOT contain any passwords or keys
		if strings.Contains(output, "pass1") || strings.Contains(output, "pass2") {
			t.Errorf("Should not contain passwords in output")
		}
		if strings.Contains(output, "key1") || strings.Contains(output, "key2") {
			t.Errorf("Should not contain api_keys in output")
		}

		// Should contain usernames
		if !strings.Contains(output, "user1") || !strings.Contains(output, "user2") {
			t.Errorf("Expected to find usernames in output")
		}
	})
}

// TestPrinter_PreservesUnderscoresInFieldNames tests that JSON tags with underscores
// are preserved in YAML and JSON output (not converted to Go field names)
func TestPrinter_PreservesUnderscoresInFieldNames(t *testing.T) {
	// Define a struct with underscored JSON tags (like our DatasetFull struct)
	type DatasetExample struct {
		ID                          int      `json:"id"`
		VersionID                   int      `json:"version_id"`
		Name                        string   `json:"name"`
		BadRowsCount                *int     `json:"bad_rows_count,omitempty"`
		CleanRowsCount              *int     `json:"clean_rows_count,omitempty"`
		QuarantineRowsUntilApproved bool     `json:"quarantine_rows_until_approved"`
		DisabledFields              []string `json:"disabled_fields,omitempty"`
		MaxTriesToFixJSON           int      `json:"max_tries_to_fix_json"`
		EncryptRawDataDuringBackup  bool     `json:"encrypt_raw_data_during_backup"`
		AccessToken                 string   `json:"access_token"` // Secret field
	}

	badCount := 5
	cleanCount := 100
	data := DatasetExample{
		ID:                          1,
		VersionID:                   42,
		Name:                        "Test Table",
		BadRowsCount:                &badCount,
		CleanRowsCount:              &cleanCount,
		QuarantineRowsUntilApproved: true,
		DisabledFields:              []string{"field1", "field2"},
		MaxTriesToFixJSON:           3,
		EncryptRawDataDuringBackup:  true,
		AccessToken:                 "secret-token-123",
	}

	t.Run("YAML output preserves underscores in field names", func(t *testing.T) {
		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatYAML,
			AllowPlaintextSecrets: false,
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
			return
		}

		output := buf.String()

		// Should contain field names with underscores (from JSON tags)
		wantContains := []string{
			"version_id:",
			"bad_rows_count:",
			"clean_rows_count:",
			"quarantine_rows_until_approved:",
			"disabled_fields:",
			"max_tries_to_fix_json:",
			"encrypt_raw_data_during_backup:",
			"access_token:", // Secret field should exist
		}

		for _, want := range wantContains {
			if !strings.Contains(output, want) {
				t.Errorf("YAML output missing field %q\nGot output:\n%s", want, output)
			}
		}

		// Should NOT contain Go field names without underscores
		notWantContains := []string{
			"VersionID:",
			"BadRowsCount:",
			"CleanRowsCount:",
			"QuarantineRowsUntilApproved:",
			"DisabledFields:",
			"MaxTriesToFixJSON:",
			"EncryptRawDataDuringBackup:",
		}

		for _, notWant := range notWantContains {
			if strings.Contains(output, notWant) {
				t.Errorf("YAML output should not contain Go field name %q (should use JSON tag)\nGot output:\n%s", notWant, output)
			}
		}

		// Secret should be masked
		if strings.Contains(output, "secret-token-123") {
			t.Errorf("Secret value should be masked in output")
		}
		if !strings.Contains(output, "***") {
			t.Errorf("Expected masked secret ('***') in output")
		}
	})

	t.Run("JSON output preserves underscores in field names", func(t *testing.T) {
		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatJSON,
			AllowPlaintextSecrets: false,
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
			return
		}

		output := buf.String()

		// Should contain field names with underscores (from JSON tags)
		wantContains := []string{
			`"version_id"`,
			`"bad_rows_count"`,
			`"clean_rows_count"`,
			`"quarantine_rows_until_approved"`,
			`"disabled_fields"`,
			`"max_tries_to_fix_json"`,
			`"encrypt_raw_data_during_backup"`,
			`"access_token"`,
		}

		for _, want := range wantContains {
			if !strings.Contains(output, want) {
				t.Errorf("JSON output missing field %q\nGot output:\n%s", want, output)
			}
		}

		// Should NOT contain Go field names
		notWantContains := []string{
			`"VersionID"`,
			`"BadRowsCount"`,
			`"CleanRowsCount"`,
			`"QuarantineRowsUntilApproved"`,
			`"DisabledFields"`,
			`"MaxTriesToFixJSON"`,
			`"EncryptRawDataDuringBackup"`,
		}

		for _, notWant := range notWantContains {
			if strings.Contains(output, notWant) {
				t.Errorf("JSON output should not contain Go field name %q (should use JSON tag)\nGot output:\n%s", notWant, output)
			}
		}

		// Secret should be masked
		if strings.Contains(output, "secret-token-123") {
			t.Errorf("Secret value should be masked in output")
		}
	})

	t.Run("YAML with plaintext secrets still preserves underscores", func(t *testing.T) {
		var buf bytes.Buffer
		printer := NewPrinter(Options{
			Format:                FormatYAML,
			AllowPlaintextSecrets: true, // Allow plaintext secrets
			Writer:                &buf,
		})

		err := printer.Print(data)
		if err != nil {
			t.Errorf("Print() error = %v", err)
			return
		}

		output := buf.String()

		// Should still contain field names with underscores
		wantContains := []string{
			"version_id:",
			"bad_rows_count:",
			"access_token:",
		}

		for _, want := range wantContains {
			if !strings.Contains(output, want) {
				t.Errorf("YAML output missing field %q\nGot output:\n%s", want, output)
			}
		}

		// Should show the actual secret value
		if !strings.Contains(output, "secret-token-123") {
			t.Errorf("Expected plaintext secret in output when AllowPlaintextSecrets=true")
		}
	})
}
