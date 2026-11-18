// Package codegen renders Go source for fields copied off internal/api types.
package codegen

import (
	"fmt"
	"go/token"
	"path"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Imports gives each package a rendered type names one qualifier, and renders
// the import declaration for them. The zero value is ready to use.
type Imports struct {
	qualifiers map[string]string // import path -> qualifier
	paths      map[string]string // qualifier -> import path
}

// GoType renders t as Go source, naming a type from another package as
// <qualifier>.<Name> and recording that package's import. It returns an error
// for a type the generated file could not name and for a kind a manifest field
// cannot hold.
func (im *Imports) GoType(t reflect.Type) (string, error) {
	if name := t.Name(); name != "" && t.PkgPath() != "" {
		q, err := im.qualifier(t)
		if err != nil {
			return "", err
		}
		return q + "." + name, nil
	}

	switch t.Kind() {
	case reflect.Pointer:
		elem, err := im.GoType(t.Elem())
		if err != nil {
			return "", err
		}
		return "*" + elem, nil
	case reflect.Slice:
		elem, err := im.GoType(t.Elem())
		if err != nil {
			return "", err
		}
		return "[]" + elem, nil
	case reflect.Map:
		key, err := im.GoType(t.Key())
		if err != nil {
			return "", err
		}
		elem, err := im.GoType(t.Elem())
		if err != nil {
			return "", err
		}
		return "map[" + key + "]" + elem, nil
	case reflect.Interface:
		if t.NumMethod() != 0 {
			return "", fmt.Errorf("interface %s has methods; only the empty interface is representable in a manifest", t.String())
		}
		return "any", nil
	case reflect.Bool, reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return t.Kind().String(), nil
	default:
		return "", fmt.Errorf("unsupported kind %s (%s)", t.Kind(), t.String())
	}
}

// qualifier returns the name the named type t's package goes by, recording its
// import. A package whose name another import path already holds goes by that
// name with the lowest free number from 2 appended.
func (im *Imports) qualifier(t reflect.Type) (string, error) {
	pkgPath, name := t.PkgPath(), t.Name()
	if !token.IsExported(name) || strings.Contains(name, "[") {
		return "", fmt.Errorf("type %s is unexported or generic; the generated file cannot name it", t.String())
	}
	if q, ok := im.qualifiers[pkgPath]; ok {
		return q, nil
	}
	pkgName, ok := strings.CutSuffix(t.String(), "."+name)
	if !ok || !token.IsIdentifier(pkgName) {
		return "", fmt.Errorf("cannot tell the package name of %s from %s", t.String(), pkgPath)
	}

	if im.qualifiers == nil {
		im.qualifiers, im.paths = map[string]string{}, map[string]string{}
	}
	q := pkgName
	for n := 2; im.paths[q] != ""; n++ {
		q = pkgName + strconv.Itoa(n)
	}
	im.qualifiers[pkgPath], im.paths[q] = q, pkgPath
	return q, nil
}

// Decl renders an import declaration for every package recorded so far,
// followed by a blank line, or "" when none is.
func (im *Imports) Decl() string {
	pkgPaths := make([]string, 0, len(im.qualifiers))
	for pkgPath := range im.qualifiers {
		pkgPaths = append(pkgPaths, pkgPath)
	}
	sort.Strings(pkgPaths)

	specs := make([]string, len(pkgPaths))
	for i, pkgPath := range pkgPaths {
		specs[i] = strconv.Quote(pkgPath)
		if q := im.qualifiers[pkgPath]; q != path.Base(pkgPath) {
			specs[i] = q + " " + specs[i]
		}
	}

	switch len(specs) {
	case 0:
		return ""
	case 1:
		return "import " + specs[0] + "\n\n"
	default:
		return "import (\n\t" + strings.Join(specs, "\n\t") + "\n)\n\n"
	}
}

// PointerTo returns the rendered type t with one pointer added, unless it
// already is one.
func PointerTo(t string) string {
	if strings.HasPrefix(t, "*") {
		return t
	}
	return "*" + t
}

// JSONKey returns the name in f's json tag.
func JSONKey(f reflect.StructField) string {
	key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return key
}
