package root

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
	"github.com/qlustered/qctl/internal/testutil"
)

// storedDataFilter is sheet Data's stored row filter in sheetsDraftTree
// form, reading column.
func storedDataFilter(column string) string {
	return `"header_rows":[1],"instruction_row_count":0,"row_filter":{"description":"Positive premiums",` +
		`"conditions":[{"column_name":"` + column + `","operator":"gt","value":"0","value_high":null,"value_list":null,"case_sensitive":false}],"predicate":null}`
}

// filteredSheetsServer is sheetsServer with sheet Data's stored row filter
// reading column.
func filteredSheetsServer(t *testing.T, column string) *manifestServer {
	t.Helper()
	s := sheetsServer(t)
	answerRevisionTree(t, s.MockAPIServer, draftRevisionID, strings.Replace(sheetsDraftTree,
		`"header_rows":[1],"instruction_row_count":0,"row_filter":null`, storedDataFilter(column), 1))
	return s
}

const keptDataCount = "Row filter spec.sheets.Data.row_filter (the stored filter, which the save keeps) on table 42:\n  Keeps 12 of 40 rows\n"

// A PATCH that leaves row_filter out keeps the stored filter, which diff
// and apply count before the first write, once even when apply prompts.
func TestDeliverableCountsAKeptSheetFilterBeforeTheFirstWrite(t *testing.T) {
	manifest := removalManifest["re-anchor"].manifest
	t.Run("diff", func(t *testing.T) {
		s := filteredSheetsServer(t, "premium")
		out, _, err := runManifest(t, "diff", "-f", writeManifest(t, t.TempDir(), "deliverable.yaml", manifest))
		if code := qerrors.GetExitCode(err); code != 1 {
			t.Fatalf("exit = %d, want 1; error = %v", code, err)
		}
		if !strings.Contains(out, "}\n"+keptDataCount+"Note: ") && !strings.HasSuffix(out, "}\n"+keptDataCount) {
			t.Errorf("diff =\n%s\nwant the kept filter's count after the requests", out)
		}
		if got := s.counts(); !reflect.DeepEqual(got, []string{"POST " + sheetsCountPath}) {
			t.Errorf("counts = %v", got)
		}
	})
	for _, tc := range []struct {
		name   string
		args   []string
		answer string
	}{
		{name: "with yes", args: []string{"--yes"}},
		{name: "at a prompt", answer: "y\n"},
	} {
		t.Run("apply "+tc.name, func(t *testing.T) {
			s := filteredSheetsServer(t, "premium")
			if tc.answer != "" {
				answerPrompts(t, tc.answer)
			} else {
				requireNonTTY(t)
			}
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", manifest)
			out, _, err := runManifest(t, append([]string{"apply", "-f", path}, tc.args...)...)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(out, keptDataCount); got != 1 {
				t.Errorf("stdout carries the count %d times, want once:\n%s", got, out)
			}
			if strings.Index(out, keptDataCount) > strings.Index(out, "deliverable/monthly-bordereau updated") {
				t.Errorf("stdout =\n%s\nwant the count before the result", out)
			}
			if got := s.writes(); !reflect.DeepEqual(got, []string{patchData}) {
				t.Errorf("writes = %v, want %v", got, []string{patchData})
			}
		})
	}
}

// A filter the server cannot compile against the table is refused before
// any write.
func TestDeliverableRefusesARowFilterTheTableRejectsBeforeAnyWrite(t *testing.T) {
	requireNonTTY(t)
	s := sheetsServer(t)
	s.RegisterHandler(http.MethodPost, sheetsCountPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusUnprocessableEntity, map[string]any{"detail": map[string]any{
			"msg": "Row filter invalid: condition 1: column 'premium' is not in this table", "error_code": "ERR_VALIDATION",
			"loc": []string{"body", "row_filter"}}})
	})
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataFilter("0"), sheetsDataColumns, sheetsNotes))
	for _, args := range [][]string{{"diff", "-f", path}, {"apply", "-f", path, "--yes"}} {
		out, _, err := runManifest(t, args...)
		if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
			t.Fatalf("%s exit = %d, want 2; error = %v", args[0], code, err)
		}
		if !strings.HasPrefix(err.Error(), "spec.sheets.Data.row_filter does not fit table 42: ") ||
			!strings.Contains(err.Error(), "Set spec.sheets.Data.row_filter to a row filter that fits the table, or to null to clear it.") {
			t.Errorf("%s error = %v", args[0], err)
		}
		if got := s.writes(); len(got) != 0 || out != "" {
			t.Fatalf("%s: writes = %v and stdout = %q, want neither", args[0], got, out)
		}
	}
}

// A count the server will not answer is a warning, and apply still writes.
func TestApplyDeliverableWarnsWhenItCannotCountAndStillWrites(t *testing.T) {
	requireNonTTY(t)
	s := sheetsServer(t)
	s.RegisterHandler(http.MethodPost, sheetsCountPath, func(w http.ResponseWriter, _ *http.Request) {
		testutil.RespondJSON(w, http.StatusForbidden, map[string]any{"detail": "User is not authorized to access table 42"})
	})
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataFilter("0"), sheetsDataColumns, sheetsNotes))
	out, _, err := runManifest(t, "apply", "-f", path)
	if err != nil {
		t.Fatal(err)
	}
	warning := "Warning: could not count rows for spec.sheets.Data.row_filter: "
	if !strings.HasPrefix(out, warning) || !strings.Contains(out, "\ndeliverable/monthly-bordereau updated\n") {
		t.Errorf("stdout =\n%s\nwant it to start %q, then the result", out, warning)
	}
	if got := s.writes(); !reflect.DeepEqual(got, []string{patchData}) {
		t.Errorf("writes = %v, want %v", got, []string{patchData})
	}
}

// A total's row filter is counted on its aggregate table.
func TestDiffDeliverableCountsACellFilter(t *testing.T) {
	s := cellsServer(t)
	path := writeManifest(t, t.TempDir(), "cells.yaml", rootCellsManifest("{'Summary!B1': {total: {table: 42, func: count, "+
		strings.TrimSpace(sheetsDataFilter("0"))+"}}}"))
	out, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d, want 1; error = %v", code, err)
	}
	if want := "Row filter spec.cells.Summary!B1.total.row_filter on table 42:\n  Keeps 12 of 40 rows\n"; !strings.Contains(out, want) {
		t.Errorf("diff =\n%s\nwant it to contain\n%s", out, want)
	}
	if got := s.counts(); !reflect.DeepEqual(got, []string{"POST " + sheetsCountPath}) {
		t.Errorf("counts = %v", got)
	}
}

// A series sheet's count names the series' reference table.
func TestDiffDeliverableCountsASeriesSheetOnItsReferenceTable(t *testing.T) {
	sheetsServer(t)
	sheet := sheetsData + "      series: \"" + exportSeriesID + "\"\n"
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheet, sheetsDataFilter("0"), sheetsDataColumns, sheetsNotes))
	out, _, err := runManifest(t, "diff", "-f", path)
	if code := qerrors.GetExitCode(err); code != 1 {
		t.Fatalf("exit = %d, want 1; error = %v", code, err)
	}
	if want := "Row filter spec.sheets.Data.row_filter on table 42, the series' reference table:\n  Keeps 12 of 40 rows\n"; !strings.Contains(out, want) {
		t.Errorf("diff =\n%s\nwant it to contain\n%s", out, want)
	}
}

// A stored filter no save touches is checked against its table's columns.
func TestDeliverableWarnsOfAStoredFilterReadingAMissingColumn(t *testing.T) {
	s := filteredSheetsServer(t, "region")
	path := writeManifest(t, t.TempDir(), "deliverable.yaml", sheetsManifest(sheetsData, sheetsDataColumns, sheetsNotes))
	warning := `Warning: spec.sheets.Data.row_filter: the stored row filter reads column "region", which is not in table 42's non-deleted ` +
		"applied schema; revision validation reports it as an error, and enable and preview refuse the release, until " +
		"spec.sheets.Data.row_filter is set to a row filter that fits the table, or to null to clear it\n"
	out, errOut, err := runManifest(t, "diff", "-f", path)
	if err != nil || out != "" || errOut != warning {
		t.Errorf("diff: stdout = %q, stderr =\n%s\nwant\n%s; error = %v", out, errOut, warning, err)
	}
	out, _, err = runManifest(t, "apply", "-f", path)
	if err != nil || out != "deliverable/monthly-bordereau unchanged\n"+warning {
		t.Errorf("apply: stdout = %q, error = %v", out, err)
	}
	if got := s.counts(); len(got) != 0 {
		t.Errorf("counts = %v, want none", got)
	}
}

// A plan that sends and keeps no row filter makes no count request.
func TestDeliverableWithoutARowFilterMakesNoCountRequest(t *testing.T) {
	for name, tc := range removalManifest {
		t.Run(name, func(t *testing.T) {
			s := sheetsServer(t)
			path := writeManifest(t, t.TempDir(), "deliverable.yaml", tc.manifest)
			if _, _, err := runManifest(t, "diff", "-f", path); qerrors.GetExitCode(err) != 1 {
				t.Fatalf("diff error = %v, want differences", err)
			}
			if _, _, err := runManifest(t, "apply", "-f", path, "--yes"); err != nil {
				t.Fatal(err)
			}
			if got := s.counts(); len(got) != 0 {
				t.Errorf("counts = %v, want none", got)
			}
		})
	}
}
