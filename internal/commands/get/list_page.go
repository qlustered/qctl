package get

import (
	"fmt"
	"math"
	"reflect"

	"github.com/qlustered/qctl/internal/output"
	"github.com/spf13/cobra"
)

// listPage is where one page of a get list sits.
type listPage struct {
	// noun is the plural resource name, as countLabel takes it.
	noun string
	// page and limit are the values sent; nil for a list with no such flag.
	page, limit *int
	// total is the server's total_rows; nil when it sent none.
	total *int
	// more is whether the server sent next.
	more bool
	// noFilters drops "or add filters" from the hint.
	noFilters bool
}

// nextPage returns the page after lp when one holds rows, or nil. A known
// total decides; without one, more does.
func (lp listPage) nextPage() *int {
	if lp.page == nil {
		return nil
	}
	if lp.total != nil && lp.limit != nil && *lp.limit > 0 {
		if *lp.total <= 0 || *lp.page > (*lp.total-1) / *lp.limit {
			return nil
		}
	} else if !lp.more {
		return nil
	}
	if *lp.page == math.MaxInt {
		return nil
	}
	next := *lp.page + 1
	return &next
}

// hint returns the stderr line for a page with a page after it, or for an
// unpaged list that --limit cut short; otherwise "".
func (lp listPage) hint(shown int) string {
	if lp.page == nil {
		if lp.limit == nil || lp.total == nil || shown >= *lp.total {
			return ""
		}
		return fmt.Sprintf("Showing %d of %s. More available: raise --limit (0 = all) or add filters.",
			shown, countLabel(*lp.total, lp.noun))
	}
	next := lp.nextPage()
	if next == nil {
		return ""
	}
	advice := fmt.Sprintf("use --page %d or add filters.", *next)
	if lp.noFilters {
		advice = fmt.Sprintf("use --page %d.", *next)
	}
	if lp.total == nil || lp.limit == nil || *lp.limit <= 0 {
		return fmt.Sprintf("Showing %d results. More available: %s", shown, advice)
	}
	first := (*lp.page-1)**lp.limit + 1
	last := min(*lp.page**lp.limit, *lp.total)
	pages := (*lp.total + *lp.limit - 1) / *lp.limit
	return fmt.Sprintf("Showing %d-%d of %s (page %d of %d). More available: %s",
		first, last, countLabel(*lp.total, lp.noun), *lp.page, pages, advice)
}

// list returns items as the List that -o json|yaml prints for lp.
func (lp listPage) list(items any) output.List {
	return output.NewList(items, lp.page, lp.limit, lp.total, lp.nextPage())
}

// printListPage prints one page of a get list. Under -o json|yaml render
// receives an output.List of items; under table and name it receives items,
// and the hint for a following page goes to stderr.
func printListPage(cmd *cobra.Command, items any, lp listPage, render func(any) error) error {
	if format, _ := cmd.Flags().GetString("output"); format == string(output.FormatJSON) || format == string(output.FormatYAML) {
		return render(lp.list(items))
	}
	if err := render(items); err != nil {
		return err
	}
	if hint := lp.hint(sliceLen(items)); hint != "" {
		fmt.Fprintln(cmd.ErrOrStderr(), hint)
	}
	return nil
}

// sliceLen returns the length of a slice or array, else 0.
func sliceLen(items any) int {
	v := reflect.ValueOf(items)
	if v.Kind() == reflect.Slice || v.Kind() == reflect.Array {
		return v.Len()
	}
	return 0
}
