package manifest

import (
	"bytes"
	"testing"
)

func TestSplitDocuments(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "zero documents",
			input: "# comments do not make a document\n",
		},
		{
			name:  "one document preserves its bytes",
			input: "name: one   # keep this layout\n",
			want:  []string{"name: one   # keep this layout\n"},
		},
		{
			name:  "many documents",
			input: "name: one\n---\nname: two\n",
			want:  []string{"name: one\n", "name: two\n"},
		},
		{
			name:  "leading separator",
			input: "---\nname: one\n",
			want:  []string{"---\nname: one\n"},
		},
		{
			name:  "trailing separator",
			input: "name: one\n---\n",
			want:  []string{"name: one\n"},
		},
		{
			name:    "malformed later document",
			input:   "name: one\n---\nname: [unterminated\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SplitDocuments([]byte(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatal("SplitDocuments() error = nil, want malformed YAML error")
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitDocuments() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("SplitDocuments() returned %d documents, want %d: %q", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if !bytes.Equal(got[i], []byte(tt.want[i])) {
					t.Errorf("document %d = %q, want %q", i+1, got[i], tt.want[i])
				}
			}
		})
	}
}
