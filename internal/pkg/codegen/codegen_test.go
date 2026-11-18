package codegen_test

import (
	htmltemplate "html/template"
	"reflect"
	"testing"
	texttemplate "text/template"
	"time"

	"github.com/google/uuid"
	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/pkg/codegen"
	"gopkg.in/yaml.v3"
)

func TestGoType(t *testing.T) {
	tests := []struct {
		name     string
		in       reflect.Type
		want     string
		wantDecl string
	}{
		{"pointer to a builtin", reflect.TypeFor[*string](), "*string", ""},
		{"pointer to a named api type", reflect.TypeFor[*api.XLSDateMode](), "*api.XLSDateMode", "import \"github.com/qlustered/qctl/internal/api\"\n\n"},
		{"map of slices", reflect.TypeFor[map[string][]string](), "map[string][]string", ""},
		{"slice of an api struct", reflect.TypeFor[[]api.ValidationError](), "[]api.ValidationError", "import \"github.com/qlustered/qctl/internal/api\"\n\n"},
		{"empty interface", reflect.TypeFor[any](), "any", ""},
		{"unsigned and float", reflect.TypeFor[map[uint]*float64](), "map[uint]*float64", ""},
		{"named type from another module", reflect.TypeFor[*uuid.UUID](), "*uuid.UUID", "import \"github.com/google/uuid\"\n\n"},
		{"standard library named type", reflect.TypeFor[time.Duration](), "time.Duration", "import \"time\"\n\n"},
		{"package name differs from the path", reflect.TypeFor[*yaml.Node](), "*yaml.Node", "import yaml \"gopkg.in/yaml.v3\"\n\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var im codegen.Imports
			got, err := im.GoType(tt.in)
			if err != nil {
				t.Fatalf("GoType(%s): %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("GoType(%s) = %q, want %q", tt.in, got, tt.want)
			}
			if decl := im.Decl(); decl != tt.wantDecl {
				t.Errorf("Decl() after GoType(%s) = %q, want %q", tt.in, decl, tt.wantDecl)
			}
		})
	}
}

func TestImportsDeclListsEveryPackageOnceSorted(t *testing.T) {
	var im codegen.Imports
	for _, rt := range []reflect.Type{
		reflect.TypeFor[*uuid.UUID](),
		reflect.TypeFor[api.XLSDateMode](),
		reflect.TypeFor[[]uuid.UUID](),
		reflect.TypeFor[time.Duration](),
	} {
		if _, err := im.GoType(rt); err != nil {
			t.Fatalf("GoType(%s): %v", rt, err)
		}
	}
	want := "import (\n\t\"github.com/google/uuid\"\n\t\"github.com/qlustered/qctl/internal/api\"\n\t\"time\"\n)\n\n"
	if got := im.Decl(); got != want {
		t.Errorf("Decl() = %q, want %q", got, want)
	}
}

func TestImportsAliasesAPackageNameTwoPathsShare(t *testing.T) {
	var im codegen.Imports
	for _, tt := range []struct {
		in   reflect.Type
		want string
	}{
		{reflect.TypeFor[*texttemplate.Template](), "*template.Template"},
		{reflect.TypeFor[htmltemplate.HTML](), "template2.HTML"},
		{reflect.TypeFor[texttemplate.FuncMap](), "template.FuncMap"},
	} {
		got, err := im.GoType(tt.in)
		if err != nil {
			t.Fatalf("GoType(%s): %v", tt.in, err)
		}
		if got != tt.want {
			t.Errorf("GoType(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
	want := "import (\n\ttemplate2 \"html/template\"\n\t\"text/template\"\n)\n\n"
	if got := im.Decl(); got != want {
		t.Errorf("Decl() = %q, want %q", got, want)
	}
}

// unexported is a named type the generated file could not name.
type unexported string

func TestGoTypeRejectsWhatAManifestCannotHold(t *testing.T) {
	tests := []struct {
		name string
		in   reflect.Type
	}{
		{"interface with methods", reflect.TypeFor[[]error]()},
		{"func", reflect.TypeFor[func()]()},
		{"unexported named type", reflect.TypeFor[unexported]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var im codegen.Imports
			if got, err := im.GoType(tt.in); err == nil {
				t.Errorf("GoType(%s) = %q, want an error", tt.in, got)
			}
		})
	}
}

func TestPointerTo(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"string", "*string"},
		{"*api.XLSDateMode", "*api.XLSDateMode"},
		{"[]api.ValidationError", "*[]api.ValidationError"},
	}
	for _, tt := range tests {
		if got := codegen.PointerTo(tt.in); got != tt.want {
			t.Errorf("PointerTo(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJSONKey(t *testing.T) {
	tests := []struct {
		tag  reflect.StructTag
		want string
	}{
		{`json:"x,omitempty"`, "x"},
		{`yaml:"other" json:"x"`, "x"},
		{`json:"-"`, "-"},
		{`yaml:"x"`, ""},
	}
	for _, tt := range tests {
		if got := codegen.JSONKey(reflect.StructField{Name: "X", Tag: tt.tag}); got != tt.want {
			t.Errorf("JSONKey of a field tagged %s = %q, want %q", tt.tag, got, tt.want)
		}
	}
}
