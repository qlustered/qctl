// Package yamlkeys is the control for TestYAMLEncodesWriteYAMLTaggedFields:
// every function named Flagged* holds exactly one YAML encode the sweep must
// report, and every function named Clean* holds encodes it must not.
//
// The directory is testdata, so ./... never builds, vets or lints it.
package yamlkeys

import (
	"encoding/json"
	"io"
	"time"

	"github.com/qlustered/qctl/internal/markdown"
	"github.com/qlustered/qctl/internal/pkg/jsonnum"
	"gopkg.in/yaml.v3"
)

type jsonOnly struct {
	FamilyID string `json:"family_id"`
	Name     string `json:"name,omitempty"`
}

type untagged struct {
	Name string
}

type tagged struct {
	Name  string            `yaml:"name" json:"name"`
	Inner *tagged           `yaml:"inner,omitempty" json:"inner,omitempty"`
	Extra map[string]string `yaml:"extra" json:"extra"`
}

type nested struct {
	Kind  string                 `yaml:"kind" json:"kind"`
	Items map[string][]*jsonOnly `yaml:"items" json:"items"`
}

type embedding struct {
	jsonOnly
	Kind string `yaml:"kind" json:"kind"`
}

type inlined struct {
	tagged `yaml:",inline"`
	Kind   string `yaml:"kind" json:"kind"`
}

type union struct {
	union json.RawMessage
}

func (u union) MarshalJSON() ([]byte, error) { return u.union, nil }

type withUnion struct {
	Value union `yaml:"value" json:"value"`
}

type selfMarshaled struct {
	inner jsonOnly
}

func (s selfMarshaled) MarshalYAML() (interface{}, error) {
	return map[string]string{"family_id": s.inner.FamilyID}, nil
}

type textual [4]byte

func (t textual) MarshalText() ([]byte, error) { return []byte("t"), nil }

type hidden struct {
	Name   string   `yaml:"name" json:"name"`
	Secret jsonOnly `yaml:"-" json:"secret"`
	secret jsonOnly
}

type withTime struct {
	At   time.Time  `yaml:"at" json:"at"`
	Node yaml.Node  `yaml:"node" json:"node"`
	Ref  *yaml.Node `yaml:"ref" json:"ref"`
	ID   textual    `yaml:"id" json:"id"`
}

type withAny struct {
	Name  string      `yaml:"name" json:"name"`
	Value interface{} `yaml:"value" json:"value"`
}

// FlaggedJSONOnly encodes a struct with json tags and no yaml tags.
func FlaggedJSONOnly() ([]byte, error) { return jsonnum.MarshalYAML(jsonOnly{}) }

// FlaggedUntagged encodes a struct with no tags at all.
func FlaggedUntagged(w io.Writer) error { return jsonnum.NewYAMLEncoder(w).Encode(&untagged{}) }

// FlaggedNested reaches a json-only struct through a map, a slice and a
// pointer inside a yaml-tagged one.
func FlaggedNested() ([]byte, error) { return jsonnum.MarshalYAML(nested{}) }

// FlaggedEmbedded embeds a struct without an inline tag, which yaml.v3 nests
// under the lowercased type name where encoding/json promotes its fields.
func FlaggedEmbedded() ([]byte, error) { return jsonnum.MarshalYAML(embedding{}) }

// FlaggedMarshalJSON reaches a type whose only marshaler is MarshalJSON.
func FlaggedMarshalJSON() ([]byte, error) { return jsonnum.MarshalYAML(withUnion{}) }

// FlaggedBareYAML encodes with yaml.v3 directly.
func FlaggedBareYAML() ([]byte, error) { return yaml.Marshal(jsonOnly{}) }

// FlaggedBareYAMLEncoder encodes with a yaml.v3 Encoder directly.
func FlaggedBareYAMLEncoder(w io.Writer) error { return yaml.NewEncoder(w).Encode(untagged{}) }

func encodeAny(w io.Writer, v interface{}) error {
	enc := jsonnum.NewYAMLEncoder(w)
	defer enc.Close()
	return enc.Encode(v)
}

// FlaggedThroughParameter hands a json-only struct to a helper typed
// interface{}; the finding is at the helper's encode.
func FlaggedThroughParameter(w io.Writer) error { return encodeAny(w, jsonOnly{}) }

// CleanThroughParameter hands a yaml-tagged struct to the same helper.
func CleanThroughParameter(w io.Writer) error { return encodeAny(w, tagged{}) }

// FlaggedThroughLocal assigns a json-only struct to an interface{} local.
func FlaggedThroughLocal() ([]byte, error) {
	var v interface{}
	v = jsonOnly{}
	return jsonnum.MarshalYAML(v)
}

func build(kind string) interface{} {
	if kind == "tagged" {
		return tagged{}
	}
	return &untagged{}
}

// FlaggedThroughReturn encodes a function's interface{} result, one of whose
// returns is untagged.
func FlaggedThroughReturn() ([]byte, error) { return jsonnum.MarshalYAML(build("")) }

// FlaggedThroughMultiReturn encodes the first of two results.
func FlaggedThroughMultiReturn() ([]byte, error) {
	v, err := buildTwo()
	if err != nil {
		return nil, err
	}
	return jsonnum.MarshalYAML(v)
}

func buildTwo() (interface{}, error) { return jsonOnly{}, nil }

// FlaggedUnresolved encodes an interface{} read from a struct field, which the
// sweep cannot follow.
func FlaggedUnresolved(v withAny) ([]byte, error) { return jsonnum.MarshalYAML(v.Value) }

// FlaggedFuncValue uses an encode as a function value, which hides its
// argument.
func FlaggedFuncValue() func(interface{}) ([]byte, error) { return jsonnum.MarshalYAML }

// CleanTagged encodes yaml-tagged structs, a map and a slice.
func CleanTagged(w io.Writer) error {
	enc := jsonnum.NewYAMLEncoder(w)
	if err := enc.Encode(tagged{}); err != nil {
		return err
	}
	if err := enc.Encode(map[string]interface{}{"a": 1}); err != nil {
		return err
	}
	return enc.Encode([]tagged{})
}

// CleanInline inlines a yaml-tagged struct.
func CleanInline() ([]byte, error) { return jsonnum.MarshalYAML(inlined{}) }

// CleanMarshalers reaches types yaml.v3 hands to their own marshalers, and
// yaml.Node, which it writes itself.
func CleanMarshalers() ([]byte, error) {
	if _, err := jsonnum.MarshalYAML(selfMarshaled{}); err != nil {
		return nil, err
	}
	return jsonnum.MarshalYAML(withTime{})
}

// CleanHidden reaches a json-only struct only under yaml:"-" and an unexported
// field.
func CleanHidden() ([]byte, error) { return jsonnum.MarshalYAML(hidden{}) }

// CleanNestedInterface stops at an interface{} inside the encoded value.
func CleanNestedInterface() ([]byte, error) { return jsonnum.MarshalYAML(withAny{Value: jsonOnly{}}) }

// CleanProcessed encodes what markdown.ProcessFieldsPlain returns for a
// yaml-tagged struct.
func CleanProcessed() ([]byte, error) {
	processed := markdown.ProcessFieldsPlain(tagged{}, []string{"name"})
	return jsonnum.MarshalYAML(processed)
}
