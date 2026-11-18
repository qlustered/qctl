package output

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingJSON struct{ Name string }

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("json method failed") }

type failingText struct{ Name string }

func (failingText) MarshalText() ([]byte, error) { return nil, errors.New("text method failed") }

type failingPointerJSON struct{ Name string }

func (*failingPointerJSON) MarshalJSON() ([]byte, error) {
	return nil, errors.New("pointer method failed")
}

// A value whose MarshalJSON or MarshalText fails makes Print return that
// method's error.
func TestPrinterReportsAFailingMarshalMethod(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format Format
		data   interface{}
		want   string
	}{
		{"MarshalJSON -o json", FormatJSON, struct {
			F failingJSON `json:"f"`
		}{}, "json method failed"},
		{"pointer MarshalJSON -o json", FormatJSON, &struct {
			F failingPointerJSON `json:"f"`
		}{}, "pointer method failed"},
		{"MarshalText -o json", FormatJSON, struct {
			F failingText `json:"f"`
		}{}, "text method failed"},
		{"MarshalText -o yaml", FormatYAML, struct {
			F failingText `json:"f"`
		}{}, "text method failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := NewPrinter(Options{Format: tc.format, Writer: &buf}).Print(tc.data)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Print error = %v, want one containing %q; wrote:\n%s", err, tc.want, buf.String())
			}
		})
	}
}

type failingSecretJSON struct{ Password string }

func (failingSecretJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("json method failed") }

type failingSecretText struct{ Password string }

func (failingSecretText) MarshalText() ([]byte, error) { return nil, errors.New("text method failed") }

// A value whose MarshalJSON or MarshalText fails makes Print return that
// method's error in either format, and none of its fields is written.
func TestPrinterWritesNothingOfAFailingMarshaler(t *testing.T) {
	for _, format := range []Format{FormatJSON, FormatYAML} {
		for _, tc := range []struct {
			name string
			data interface{}
			want string
		}{
			{"MarshalJSON", struct {
				F failingSecretJSON `json:"f"`
			}{failingSecretJSON{Password: "hunter2"}}, "json method failed"},
			{"MarshalText", struct {
				F failingSecretText `json:"f"`
			}{failingSecretText{Password: "hunter2"}}, "text method failed"},
		} {
			t.Run(tc.name+" -o "+string(format), func(t *testing.T) {
				var buf bytes.Buffer
				err := NewPrinter(Options{Format: format, Writer: &buf}).Print(tc.data)
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Errorf("Print error = %v, want one containing %q", err, tc.want)
				}
				if strings.Contains(buf.String(), "hunter2") {
					t.Errorf("wrote the field:\n%s", buf.String())
				}
			})
		}
	}
}
