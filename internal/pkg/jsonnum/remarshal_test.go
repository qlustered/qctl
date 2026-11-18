package jsonnum

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type failingJSON struct{ Name string }

func (failingJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("json method failed") }

// TestRemarshalDecodesTheJSONShape: a struct comes back keyed by its JSON tag
// names, with its integers as Unmarshal decodes them.
func TestRemarshalDecodesTheJSONShape(t *testing.T) {
	got, ok := Remarshal(struct {
		Rows int64  `json:"rows"`
		Skip string `json:"skip,omitempty"`
	}{Rows: 1234567})
	if want := map[string]interface{}{"rows": int64(1234567)}; !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("Remarshal = %#v, %v; want %#v, true", got, ok, want)
	}
}

// TestRemarshalFailureFailsBothEncoders: when encoding/json cannot encode v,
// the value Remarshal returns makes json.Marshal and yaml.Marshal both fail
// with that error.
func TestRemarshalFailureFailsBothEncoders(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    interface{}
		want string
	}{
		{"MarshalJSON", failingJSON{Name: "go-field"}, "json method failed"},
		{"NaN", map[string]float64{"score": math.NaN()}, "unsupported value: NaN"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Remarshal(tc.v)
			if ok {
				t.Fatalf("Remarshal = %#v, true; want false", got)
			}
			if _, err := json.Marshal(got); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("json.Marshal error = %v, want one containing %q", err, tc.want)
			}
			if out, err := yaml.Marshal(got); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("yaml.Marshal = %q, %v; want an error containing %q", out, err, tc.want)
			}
		})
	}
}
