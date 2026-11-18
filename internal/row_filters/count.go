package row_filters //nolint:revive // Matches the resource domain.

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/terminal"
)

// FormatCount renders n, followed by "+" when n is a lower bound.
func FormatCount(n int, lowerBound bool) string {
	if lowerBound {
		return strconv.Itoa(n) + "+"
	}
	return strconv.Itoa(n)
}

// FormatRows renders n as FormatCount does, followed by "row" or "rows".
func FormatRows(n int, lowerBound bool) string {
	if n == 1 && !lowerBound {
		return "1 row"
	}
	return FormatCount(n, lowerBound) + " rows"
}

// WriteCount writes count as "<verb> M of T rows", followed by a line when
// counting stopped at a server limit and the lines of WritePredicateErrors.
// An empty table gets one line instead.
func WriteCount(w io.Writer, verb string, count *api.RowFilterCountSchema) error {
	if count.Total == 0 {
		return writeLines(w, []string{"No live rows to count: the table is empty."})
	}
	lines := []string{fmt.Sprintf("%s %s of %s", verb, FormatCount(count.Matched, count.Capped), FormatRows(count.Total, false))}
	if count.Capped {
		lines = append(lines, "Counting stopped at a server limit, so the counts marked + are lower bounds.")
	}
	lines = append(lines, predicateErrorLines(count.PredicateErrors, count.Capped, count.FirstPredicateError)...)
	return writeLines(w, lines)
}

// WritePredicateErrors writes how many rows the predicate failed on, then the
// first failure when first is not blank. It writes nothing unless failed is
// positive.
func WritePredicateErrors(w io.Writer, failed int, lowerBound bool, first *string) error {
	return writeLines(w, predicateErrorLines(failed, lowerBound, first))
}

func predicateErrorLines(failed int, lowerBound bool, first *string) []string {
	if failed <= 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("Not counted: %s where the predicate failed.", FormatRows(failed, lowerBound))}
	if first != nil && strings.TrimSpace(*first) != "" {
		lines = append(lines, "First failure: "+strings.TrimRight(terminal.Sanitize(*first), "\n"))
	}
	return lines
}

func writeLines(w io.Writer, lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	_, err := io.WriteString(w, strings.Join(lines, "\n")+"\n")
	return err
}
