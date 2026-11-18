package api

import (
	"reflect"
	"slices"
)

// EnumValues returns every value of the generated string enum T, in spec
// order. It returns nil when StringEnumValues has no entry for T.
func EnumValues[T ~string]() []string {
	return slices.Clone(StringEnumValues[reflect.TypeFor[T]().Name()])
}
