package cmdutil

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/qlustered/qctl/internal/apierror"
	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/spf13/cobra"
)

// assertBadUsage pins the exit code, which is the half of the contract a
// message assertion cannot see: these errors reach scripts as an exit status,
// and a downgrade from 2 to 1 makes "you typed the flags wrong" indistinguishable
// from "the server failed" while every substring test keeps passing.
func assertBadUsage(t *testing.T, err error) {
	t.Helper()
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (ExitBadUsage) for %v", got, qerrors.ExitBadUsage, err)
	}
}

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
				assertBadUsage(t, err)
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
				assertBadUsage(t, err)
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
				assertBadUsage(t, err)
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

// newIntFlagCommand registers one int flag and, when value is non-nil, sets it
// — which is what flips pflag's Changed. A nil value is the "user did not pass
// the flag" case, and it must stay distinguishable from an explicit "0".
func newIntFlagCommand(t *testing.T, name string, value *string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().Int(name, 0, "test flag")
	if value != nil {
		if err := cmd.Flags().Set(name, *value); err != nil {
			t.Fatalf("failed to set --%s=%s: %v", name, *value, err)
		}
	}
	return cmd
}

func strPtr(s string) *string { return &s }

// The whole point of the helper: these flags default to 0, so a bare GetInt
// reads "--table-id 0" and "no --table-id" identically. Passing 0 used to drop
// the filter and return the entire org with exit code 0.
func TestPositiveIDFlag(t *testing.T) {
	tests := []struct {
		name        string
		flag        string
		value       *string
		wantValue   int
		wantSet     bool
		wantErr     bool
		errContains string
	}{
		{
			name:      "flag not passed",
			flag:      "table-id",
			value:     nil,
			wantValue: 0,
			wantSet:   false,
		},
		{
			name:      "positive value",
			flag:      "table-id",
			value:     strPtr("42"),
			wantValue: 42,
			wantSet:   true,
		},
		{
			name:        "explicit zero is rejected, not treated as unset",
			flag:        "table-id",
			value:       strPtr("0"),
			wantErr:     true,
			errContains: "--table-id must be a positive integer, got 0",
		},
		{
			name:        "negative value is rejected",
			flag:        "cloud-source-id",
			value:       strPtr("-5"),
			wantErr:     true,
			errContains: "--cloud-source-id must be a positive integer, got -5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := newIntFlagCommand(t, tt.flag, tt.value)

			value, set, err := PositiveIDFlag(cmd, tt.flag)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errContains)
				}
				assertBadUsage(t, err)
				if set {
					t.Error("set must be false when the value is unusable")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if value != tt.wantValue {
				t.Errorf("value = %d, want %d", value, tt.wantValue)
			}
			if set != tt.wantSet {
				t.Errorf("set = %v, want %v", set, tt.wantSet)
			}
		})
	}
}

// A typo'd flag name would otherwise look exactly like "the user did not pass
// it" — silently restoring the bug at the one call site that got it wrong.
func TestPositiveIDFlagRejectsUnregisteredFlag(t *testing.T) {
	cmd := newIntFlagCommand(t, "table-id", nil)

	_, set, err := PositiveIDFlag(cmd, "tabel-id")
	if err == nil {
		t.Fatal("expected an error for a flag that is not registered")
	}
	if !strings.Contains(err.Error(), "tabel-id") {
		t.Errorf("error should name the missing flag, got: %v", err)
	}
	if set {
		t.Error("set must be false for an unregistered flag")
	}
	// Deliberately NOT ExitBadUsage: the user typed nothing wrong, a qctl
	// author did. Telling a script "bad usage" for a bug in the tool sends it
	// off to fix input that was already correct.
	if got := qerrors.GetExitCode(err); got != qerrors.ExitGenericError {
		t.Errorf("exit code = %d, want %d (ExitGenericError) for an internal error", got, qerrors.ExitGenericError)
	}
}

// Commands return these errors through fmt.Errorf("...: %w", err), so the exit
// code has to survive unwrapping. It rides an errors.Unwrap chain: %w keeps it,
// %v or errors.New silently flattens it to 1 while the message — and any test
// that only reads the message — looks exactly the same.
func TestBadUsageExitCodeSurvivesWrapping(t *testing.T) {
	cmd := newIntFlagCommand(t, "table-id", strPtr("0"))

	_, _, err := PositiveIDFlag(cmd, "table-id")
	if err == nil {
		t.Fatal("expected an error for --table-id 0")
	}

	wrapped := fmt.Errorf("failed to build request params: %w", err)
	assertBadUsage(t, wrapped)

	// Two levels deep, as a bootstrap → parse → validate chain produces.
	assertBadUsage(t, fmt.Errorf("get ingestion-jobs: %w", wrapped))

	// The counter-example this guards against, stated as a test so the rule is
	// not just a comment: %v is where the code goes to die.
	if got := qerrors.GetExitCode(fmt.Errorf("failed to build request params: %v", err)); got != qerrors.ExitGenericError {
		t.Errorf("sanity check failed: %%v-formatted error should have flattened to %d, got %d", qerrors.ExitGenericError, got)
	}
}

func TestPositiveSecondsFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr string
	}{
		{name: "default when unset", args: nil, want: 5},
		{name: "explicit value", args: []string{"--watch-interval", "30"}, want: 30},
		{
			name:    "zero is a usage error",
			args:    []string{"--watch-interval", "0"},
			wantErr: "--watch-interval must be at least 1 second, got 0",
		},
		{
			name:    "negative is a usage error",
			args:    []string{"--watch-interval", "-5"},
			wantErr: "--watch-interval must be at least 1 second, got -5",
		},
		{name: "the largest time.Duration in seconds", args: []string{"--watch-interval", "9223372036"}, want: 9223372036},
		{
			name:    "a value that overflows time.Duration is a usage error",
			args:    []string{"--watch-interval", "9999999999"},
			wantErr: "--watch-interval must be at most 9223372036 seconds, got 9999999999",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Int("watch-interval", 5, "Interval in seconds for watch mode")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			got, err := PositiveSecondsFlag(cmd, "watch-interval")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("value = %d, want %d", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error for %v", tt.args)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
			assertBadUsage(t, err)
		})
	}
}

// A typo'd flag name must be loud. Reading a flag that does not exist returns
// the pflag zero value, which would look like "the user asked for 0 seconds"
// and reinstate the very bug this validator removes.
func TestPositiveSecondsFlagUndefinedFlagIsInternalError(t *testing.T) {
	cmd := &cobra.Command{Use: "test"}
	_, err := PositiveSecondsFlag(cmd, "watch-intervl")
	if err == nil {
		t.Fatal("want an error for an undefined flag")
	}
	if !strings.Contains(err.Error(), "internal error") {
		t.Errorf("error should say it is an internal error, got: %v", err)
	}
}

func TestPositiveCountFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr string
	}{
		{name: "default when unset", args: nil, want: 100},
		{name: "explicit value", args: []string{"--limit", "5"}, want: 5},
		{name: "zero", args: []string{"--limit", "0"}, wantErr: "--limit must be a positive integer, got 0"},
		{name: "negative", args: []string{"--limit", "-1"}, wantErr: "--limit must be a positive integer, got -1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Int("limit", 100, "Maximum number of things to return")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			got, err := PositiveCountFlag(cmd, "limit")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("value = %d, want %d", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error for %v", tt.args)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
			assertBadUsage(t, err)
		})
	}
}

func TestNonNegativeCountFlag(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		want    int
		wantErr string
	}{
		{name: "zero means unlimited and is allowed", args: []string{"--limit", "0"}, want: 0},
		{name: "default when unset", args: nil, want: 0},
		{name: "positive", args: []string{"--limit", "25"}, want: 25},
		{name: "negative", args: []string{"--limit", "-5"}, wantErr: "--limit cannot be negative, got -5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "test"}
			cmd.Flags().Int("limit", 0, "Maximum number of things to return (0 = unlimited)")
			if err := cmd.ParseFlags(tt.args); err != nil {
				t.Fatalf("ParseFlags: %v", err)
			}

			got, err := NonNegativeCountFlag(cmd, "limit")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("value = %d, want %d", got, tt.want)
				}
				return
			}
			if err == nil {
				t.Fatalf("want an error for %v", tt.args)
			}
			if err.Error() != tt.wantErr {
				t.Errorf("error = %q, want %q", err.Error(), tt.wantErr)
			}
			assertBadUsage(t, err)
		})
	}
}

func TestValidateUUIDArgs(t *testing.T) {
	if err := ValidateUUIDArgs(nil, "comment thread id"); err != nil {
		t.Errorf("no args must be no error (the arity check is the caller's): %v", err)
	}
	valid := []string{"0190bbbb-1111-2222-3333-444444444444", "0190bbbb-5555-6666-7777-888888888888"}
	if err := ValidateUUIDArgs(valid, "comment thread id"); err != nil {
		t.Errorf("full UUIDs were rejected: %v", err)
	}

	// A short prefix is the failure that matters: these ids are never
	// prefix-resolved, so "0190bb" is a typo, not an abbreviation.
	for _, bad := range []string{"not-a-uuid", "0190bb", ""} {
		err := ValidateUUIDArgs([]string{bad}, "comment thread id")
		if err == nil {
			t.Errorf("ValidateUUIDArgs(%q) accepted a non-UUID", bad)
			continue
		}
		if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
			t.Errorf("ValidateUUIDArgs(%q) exit code = %d, want %d", bad, got, qerrors.ExitBadUsage)
		}
	}

	// The first bad argument is the one reported, not the last.
	err := ValidateUUIDArgs([]string{"nope", "also-nope"}, "comment thread id")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) {
		t.Errorf("error names the wrong argument: %v", err)
	}
	if !strings.Contains(err.Error(), "comment thread id") {
		t.Errorf("error does not name the item type: %v", err)
	}
}

func TestParseUUIDArg(t *testing.T) {
	const raw = "0190bbbb-1111-2222-3333-444444444444"
	id, err := ParseUUIDArg(raw, "created-by user ID")
	if err != nil {
		t.Fatalf("valid UUID was rejected: %v", err)
	}
	if id.String() != raw {
		t.Errorf("parsed UUID = %q, want %q", id, raw)
	}

	_, err = ParseUUIDArg("not-a-uuid", "created-by user ID")
	if err == nil {
		t.Fatal("expected an error for a malformed UUID")
	}
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d", got, qerrors.ExitBadUsage)
	}
	if !strings.Contains(err.Error(), "created-by user ID") {
		t.Errorf("error does not name the value: %v", err)
	}
}

// ReadBodyFlags is the one implementation of the --body/--body-file pair, so
// its four branches are pinned here rather than once per command that spells
// the pair. Exit codes, not message substrings: the messages are what a script
// cannot branch on.
func TestReadBodyFlagsRefusesBothFlagsAtOnce(t *testing.T) {
	_, err := ReadBodyFlags(&cobra.Command{}, "inline", "notes.md")
	if err == nil {
		t.Fatal("expected an error when both --body and --body-file are set")
	}
	assertBadUsage(t, err)
}

func TestReadBodyFlagsRequiresOneOfThem(t *testing.T) {
	_, err := ReadBodyFlags(&cobra.Command{}, "", "")
	if err == nil {
		t.Fatal("expected an error when neither --body nor --body-file is set")
	}
	assertBadUsage(t, err)
}

func TestReadBodyFlagsReturnsTheInlineBody(t *testing.T) {
	got, err := ReadBodyFlags(&cobra.Command{}, "inline body", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "inline body" {
		t.Errorf("body = %q, want %q", got, "inline body")
	}
}

// "-" is stdin, and it must be the command's stdin rather than the process's,
// or a test (and a piped invocation under a wrapper) reads the wrong stream.
func TestReadBodyFlagsReadsStdinForDash(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader("piped body\n"))
	got, err := ReadBodyFlags(cmd, "", "-")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "piped body\n" {
		t.Errorf("body = %q, want the piped text", got)
	}
}

func TestReadBodyFlagsReadsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	if err := os.WriteFile(path, []byte("from a file"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	got, err := ReadBodyFlags(&cobra.Command{}, "", path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "from a file" {
		t.Errorf("body = %q, want %q", got, "from a file")
	}
}

// An unreadable file is an environment failure, not bad usage: exit 1, and the
// %w chain must survive so the underlying os error is still inspectable.
func TestReadBodyFlagsWrapsAMissingFile(t *testing.T) {
	_, err := ReadBodyFlags(&cobra.Command{}, "", filepath.Join(t.TempDir(), "absent.md"))
	if err == nil {
		t.Fatal("expected an error for a missing body file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the os error was not wrapped with %%w: %v", err)
	}
}

// ReadBodyFlags never reaches this with an empty path, but a caller that
// validates its own pair with RequireOneOfFlags does: `--x-file ""` counts as
// set. That is a malformed command line, not a failed read.
func TestReadTextFileRefusesAnEmptyPath(t *testing.T) {
	_, err := ReadTextFile(&cobra.Command{}, "", "justification")
	if err == nil {
		t.Fatal("expected an error for an empty path")
	}
	assertBadUsage(t, err)
}

// RequireCommentBody is the one spelling of "comment body is empty" across
// both comment arcs, so its two branches are pinned here rather than once per
// command that calls it.
func TestRequireCommentBodyRejectsBlankText(t *testing.T) {
	for name, text := range map[string]string{
		"empty":      "",
		"spaces":     "   ",
		"whitespace": " \t\n ",
	} {
		t.Run(name, func(t *testing.T) {
			err := RequireCommentBody(text)
			if err == nil {
				t.Fatalf("expected an error for %q", text)
			}
			assertBadUsage(t, err)
		})
	}
}

func TestRequireCommentBodyAcceptsText(t *testing.T) {
	// Surrounding whitespace is trimmed only for the emptiness test: the body
	// itself is sent verbatim, so this must not be an error.
	if err := RequireCommentBody("  still wrong  "); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// ValidateCommentBody counts runes, as Pydantic's max_length does, so a body
// of exactly the cap in multi-byte characters is accepted.
func TestValidateCommentBody(t *testing.T) {
	for name, text := range map[string]string{
		"blank":        " \n ",
		"one over cap": strings.Repeat("a", MaxCommentBodyChars+1),
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateCommentBody(text)
			if err == nil {
				t.Fatal("expected an error")
			}
			assertBadUsage(t, err)
		})
	}
	if err := ValidateCommentBody(strings.Repeat("é", MaxCommentBodyChars)); err != nil {
		t.Errorf("a body of exactly the cap: unexpected error: %v", err)
	}
}

// HintOnNotFound appends only to a 404, and the wrap must be %w or the hint
// costs the caller exit 3.
func TestHintOnNotFoundAppendsToANotFound(t *testing.T) {
	base := qerrors.NewNotFound("comment thread 42 not found")
	got := HintOnNotFound(base, "try the other arc")
	if !strings.Contains(got.Error(), "try the other arc") {
		t.Errorf("hint missing: %v", got)
	}
	if !strings.Contains(got.Error(), "comment thread 42 not found") {
		t.Errorf("original message lost: %v", got)
	}
	if code := qerrors.GetExitCode(got); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d (ExitNotFound)", code, qerrors.ExitNotFound)
	}
	if !errors.Is(got, base) {
		t.Error("the wrap is not %w: the cause is no longer reachable")
	}
}

func TestHintOnNotFoundPrecedesTheFinalScopeNote(t *testing.T) {
	apierror.SetOrgScope("Qluster-dev", "from the current context")
	t.Cleanup(func() { apierror.SetOrgScope("", "") })

	base := apierror.HandleHTTPErrorFromBytes(http.StatusNotFound,
		[]byte(`{"detail":"comment thread 42 not found"}`), "failed to get comment thread")
	got := apierror.FinalizeScopeNote(HintOnNotFound(base, "try the other arc"))

	if !strings.HasSuffix(got.Error(), "(try the other arc)\n\n"+apierror.ScopeNote()) {
		t.Errorf("error = %q, want the hint before the final organization-scope note", got)
	}
	if code := qerrors.GetExitCode(got); code != qerrors.ExitNotFound {
		t.Errorf("exit code = %d, want %d", code, qerrors.ExitNotFound)
	}
}

// The pass-through branch: a hint about the wrong failure is worse than none,
// so anything that is not a 404 must come back byte-identical and unwrapped.
func TestHintOnNotFoundPassesOtherErrorsThrough(t *testing.T) {
	for name, base := range map[string]error{
		"bad usage":    qerrors.NewBadUsage("--table is required"),
		"unauthorized": qerrors.NewUnauthorized("Forbidden"),
		"generic":      errors.New("connection reset"),
	} {
		t.Run(name, func(t *testing.T) {
			got := HintOnNotFound(base, "try the other arc")
			if got != base {
				t.Errorf("error was rewrapped: %#v", got)
			}
			if strings.Contains(got.Error(), "try the other arc") {
				t.Errorf("a non-404 must not carry the hint: %v", got)
			}
		})
	}
}
