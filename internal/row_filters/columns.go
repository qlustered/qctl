package row_filters //nolint:revive // Matches the resource domain.

// Columns names the columns the filter reads: each condition's column, then
// the predicate's columns, each once, in first-seen order. It is nil for a nil
// filter and for a filter that reads none.
func (f *Filter) Columns() []string {
	if f == nil {
		return nil
	}
	var columns []string
	seen := map[string]bool{}
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			columns = append(columns, name)
		}
	}
	for _, c := range f.Conditions {
		add(c.ColumnName)
	}
	if f.Predicate != nil {
		for _, name := range f.Predicate.Columns {
			add(name)
		}
	}
	return columns
}
