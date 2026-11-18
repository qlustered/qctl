package row_filters //nolint:revive // Matches the resource domain.

import "github.com/qlustered/qctl/internal/api"

// Edit is a requested change to a stored row filter. The zero value keeps it.
type Edit struct {
	named  bool
	filter *Filter
}

// Keep leaves the stored filter as it is.
func Keep() Edit { return Edit{} }

// Remove deletes the stored filter.
func Remove() Edit { return Edit{named: true} }

// Replace stores f in place of the current filter. Replace(nil) is Remove().
func Replace(f *Filter) Edit { return Edit{named: true, filter: f} }

// Named reports whether the request body names the field; only Keep does not.
func (e Edit) Named() bool { return e.named }

// Filter returns the replacement, or nil for Keep and Remove.
func (e Edit) Filter() *Filter { return e.filter }

// Saved returns the field's value for a Named edit: nil for Remove, Saved(f)
// for Replace(f).
func (e Edit) Saved() *api.DescribedRowFilter { return Saved(e.filter) }
