package cmdutil

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestValidateConflictingFlags(t *testing.T) {
	tests := []struct {
		name         string
		flagSets     [][]string
		changedFlags []string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "no flags changed",
			flagSets:     [][]string{{"table-id", "table"}},
			changedFlags: []string{},
			wantErr:      false,
		},
		{
			name:         "one flag changed",
			flagSets:     [][]string{{"table-id", "table"}},
			changedFlags: []string{"table-id"},
			wantErr:      false,
		},
		{
			name:         "two conflicting flags changed",
			flagSets:     [][]string{{"table-id", "table"}},
			changedFlags: []string{"table-id", "table"},
			wantErr:      true,
			errContains:  "--table-id and --table",
		},
		{
			name:         "multiple sets no conflict",
			flagSets:     [][]string{{"table-id", "table"}, {"source-id", "source"}},
			changedFlags: []string{"table-id", "source-id"},
			wantErr:      false,
		},
		{
			name:         "multiple sets one conflict",
			flagSets:     [][]string{{"table-id", "table"}, {"source-id", "source"}},
			changedFlags: []string{"table-id", "source-id", "source"},
			wantErr:      true,
			errContains:  "--source-id and --source",
		},
		{
			name:         "three flags in conflict set two changed",
			flagSets:     [][]string{{"a", "b", "c"}},
			changedFlags: []string{"a", "c"},
			wantErr:      true,
			errContains:  "--a and --c",
		},
		{
			name:         "empty flag sets",
			flagSets:     [][]string{},
			changedFlags: []string{"anything"},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := createTestCommandWithFlags(tt.changedFlags)

			err := ValidateConflictingFlags(cmd, tt.flagSets...)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRequireOneOfFlags(t *testing.T) {
	tests := []struct {
		name         string
		flags        []string
		changedFlags []string
		wantErr      bool
		errContains  string
	}{
		{
			name:         "no flags changed",
			flags:        []string{"table-id", "table"},
			changedFlags: []string{},
			wantErr:      true,
			errContains:  "--table-id or --table",
		},
		{
			name:         "first flag changed",
			flags:        []string{"table-id", "table"},
			changedFlags: []string{"table-id"},
			wantErr:      false,
		},
		{
			name:         "second flag changed",
			flags:        []string{"table-id", "table"},
			changedFlags: []string{"table"},
			wantErr:      false,
		},
		{
			name:         "both flags changed",
			flags:        []string{"table-id", "table"},
			changedFlags: []string{"table-id", "table"},
			wantErr:      false,
		},
		{
			name:         "single required flag not changed",
			flags:        []string{"id"},
			changedFlags: []string{},
			wantErr:      true,
			errContains:  "--id",
		},
		{
			name:         "single required flag changed",
			flags:        []string{"id"},
			changedFlags: []string{"id"},
			wantErr:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := createTestCommandWithFlags(tt.changedFlags)

			err := RequireOneOfFlags(cmd, tt.flags)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseIntArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		itemName    string
		want        []int
		wantErr     bool
		errContains string
	}{
		{
			name:     "empty args",
			args:     []string{},
			itemName: "job ID",
			want:     []int{},
			wantErr:  false,
		},
		{
			name:     "single valid integer",
			args:     []string{"123"},
			itemName: "job ID",
			want:     []int{123},
			wantErr:  false,
		},
		{
			name:     "multiple valid integers",
			args:     []string{"1", "2", "3", "456"},
			itemName: "job ID",
			want:     []int{1, 2, 3, 456},
			wantErr:  false,
		},
		{
			name:        "invalid integer",
			args:        []string{"abc"},
			itemName:    "job ID",
			wantErr:     true,
			errContains: "invalid job ID: abc",
		},
		{
			name:        "mixed valid and invalid",
			args:        []string{"1", "2", "bad", "4"},
			itemName:    "file ID",
			wantErr:     true,
			errContains: "invalid file ID: bad",
		},
		{
			name:     "zero is valid",
			args:     []string{"0"},
			itemName: "ID",
			want:     []int{0},
			wantErr:  false,
		},
		{
			name:     "negative numbers",
			args:     []string{"-1", "-100"},
			itemName: "offset",
			want:     []int{-1, -100},
			wantErr:  false,
		},
		{
			name:        "float is invalid",
			args:        []string{"1.5"},
			itemName:    "count",
			wantErr:     true,
			errContains: "invalid count: 1.5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIntArgs(tt.args, tt.itemName)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if !intSlicesEqual(got, tt.want) {
					t.Errorf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}

// createTestCommandWithFlags creates a cobra command with string flags and marks specified ones as changed.
func createTestCommandWithFlags(changedFlags []string) *cobra.Command {
	cmd := &cobra.Command{}

	// Create a set of all possible flags we might need
	allFlags := []string{
		"table-id", "table", "source-id", "source",
		"cloud-source-id", "cloud-source", "id",
		"a", "b", "c", "anything",
	}

	for _, flag := range allFlags {
		cmd.Flags().String(flag, "", "test flag")
	}

	// Mark specified flags as changed by setting a value
	for _, flag := range changedFlags {
		cmd.Flags().Set(flag, "test-value")
	}

	return cmd
}

// intSlicesEqual compares two int slices for equality.
func intSlicesEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i, v := range a {
		if v != b[i] {
			return false
		}
	}
	return true
}
