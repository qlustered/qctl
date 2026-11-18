package output

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

type colTestRow struct {
	ID      string `json:"id"`
	Rule    string `json:"rule"`
	Created string `json:"created"`
	hidden  string //nolint:unused // untagged fields are not columns
}

func TestAvailableColumnsUsesStructTagsOnEmptySlice(t *testing.T) {
	// The empty case is the whole point: an unknown column must fail the same
	// way on an empty list as on a full one, so the names cannot come from
	// rows[0].
	got := AvailableColumns([]colTestRow{}, nil)
	want := []string{"id", "rule", "created"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("AvailableColumns = %v, want %v", got, want)
	}
}

func TestAvailableColumnsFallsBackToRowKeys(t *testing.T) {
	rows := []map[string]string{{"b": "2", "a": "1"}}
	got := AvailableColumns([]map[string]string{}, rows)
	if strings.Join(got, ",") != "a,b" {
		t.Errorf("AvailableColumns = %v, want sorted row keys [a b]", got)
	}
}

func TestAvailableColumnsSkipsWhenNothingIsKnown(t *testing.T) {
	if got := AvailableColumns(nil, nil); got != nil {
		t.Errorf("AvailableColumns = %v, want nil so validation is skipped", got)
	}
}

func TestValidateColumnsNamesTheBadColumnAndTheValidOnes(t *testing.T) {
	err := ValidateColumns([]string{"id", "dataset_colums"}, []string{"id", "rule", "dataset_columns"})
	if err == nil {
		t.Fatal("expected an error for an unknown column")
	}
	for _, want := range []string{`unknown column "dataset_colums"`, "valid: id, rule, dataset_columns"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q\n%s", want, err)
		}
	}
}

func TestValidateColumnsExitsBadUsage(t *testing.T) {
	// The exit code is part of the answer a script gets: a typo'd --columns
	// must be distinguishable from "the API fell over" (exit 1).
	err := ValidateColumns([]string{"dataset_colums"}, []string{"id", "dataset_columns"})
	if got := qerrors.GetExitCode(err); got != qerrors.ExitBadUsage {
		t.Errorf("exit code = %d, want %d (bad usage)", got, qerrors.ExitBadUsage)
	}
	// Wrapping is how commands actually return this error; the code must
	// survive an errors.Unwrap chain.
	wrapped := fmt.Errorf("failed to print output: %w", err)
	if got := qerrors.GetExitCode(wrapped); got != qerrors.ExitBadUsage {
		t.Errorf("wrapped exit code = %d, want %d (bad usage)", got, qerrors.ExitBadUsage)
	}
}

func TestValidateColumnsAcceptsKnownNames(t *testing.T) {
	if err := ValidateColumns([]string{"rule", "id"}, []string{"id", "rule"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateColumnsSkipsWhenAvailableIsUnknown(t *testing.T) {
	if err := ValidateColumns([]string{"whatever"}, nil); err != nil {
		t.Errorf("unknown-schema payloads must not be validated: %v", err)
	}
}

func TestPrintTableRejectsUnknownColumnOnEmptyResult(t *testing.T) {
	var buf bytes.Buffer
	p := NewPrinter(Options{
		Writer:  &buf,
		Format:  FormatTable,
		Columns: []string{"dataset_colums"},
	})
	err := p.Print([]colTestRow{})
	if err == nil || !strings.Contains(err.Error(), `unknown column "dataset_colums"`) {
		t.Fatalf("expected an unknown-column error on an empty result, got %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing should have been printed, got %q", buf.String())
	}
}
