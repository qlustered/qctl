package output

import "reflect"

// List is the -o json|yaml shape of a get list: one page of items and where
// that page sits. Every key is written, with null for an unknown value.
type List struct {
	Kind     string `json:"kind" yaml:"kind"`
	Items    any    `json:"items" yaml:"items"`
	Page     *int   `json:"page" yaml:"page"`
	Limit    *int   `json:"limit" yaml:"limit"`
	Total    *int   `json:"total" yaml:"total"`
	NextPage *int   `json:"next_page" yaml:"next_page"`
}

// NewList returns a List of kind "List" holding items, with a nil slice
// written as [].
func NewList(items any, page, limit, total, nextPage *int) List {
	if v := reflect.ValueOf(items); !v.IsValid() || (v.Kind() == reflect.Slice && v.IsNil()) {
		if v.IsValid() {
			items = reflect.MakeSlice(v.Type(), 0, 0).Interface()
		} else {
			items = []any{}
		}
	}
	return List{Kind: "List", Items: items, Page: page, Limit: limit, Total: total, NextPage: nextPage}
}
