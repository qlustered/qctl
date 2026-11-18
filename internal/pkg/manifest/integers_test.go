package manifest

import (
	"reflect"
	"strings"
	"testing"

	qerrors "github.com/qlustered/qctl/internal/errors"
)

func TestStrictUnmarshalRejectsFractionalIntegers(t *testing.T) {
	for _, target := range []any{
		int(0), int8(0), int16(0), int32(0), int64(0),
		uint(0), uint8(0), uint16(0), uint32(0), uint64(0), uintptr(0),
	} {
		t.Run(reflect.TypeOf(target).String(), func(t *testing.T) {
			for _, value := range []string{"2.7", "-0.7", "2.0000000000000001", "2.7e0", "27e-1", "2_7.1", "1e-400", "1e-99999999999999999999"} {
				v := reflect.New(reflect.TypeOf(target)).Interface()
				err := StrictUnmarshal([]byte(value), v)
				if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage {
					t.Errorf("%s: exit = %d, want 2; error = %v", value, code, err)
				}
			}
		})
	}
}

func TestStrictUnmarshalIntegerPaths(t *testing.T) {
	type embedded struct {
		DefaultName *int
	}
	type spec struct {
		Integer int               `yaml:"integer"`
		List    *[]int            `yaml:"list"`
		Array   [1]uint           `yaml:"array"`
		Map     map[string]*int64 `yaml:"map"`
		Keys    map[int]string    `yaml:"keys"`
	}
	type document struct {
		Embedded *embedded `yaml:",inline"`
		Spec     *spec     `yaml:"spec"`
		Loose    any       `yaml:"loose"`
	}
	for _, tc := range []struct {
		data string
		path string
	}{
		{"defaultname: 2.7", "defaultname"},
		{"spec: {integer: 2.7}", "spec.integer"},
		{"spec: {list: [2.7]}", "spec.list[0]"},
		{"spec: {array: [2.7]}", "spec.array[0]"},
		{"spec: {map: {count: 2.7}}", `spec.map["count"]`},
		{"spec: {keys: {2.7: value}}", `spec.keys["2.7"] (key)`},
		{"loose: &fraction 2.7\nspec: {integer: *fraction}", "spec.integer"},
		{"loose: &key integer\nspec: {*key: 2.7}", "spec.integer"},
		{"loose: &defaults {integer: 2.7}\nspec: {<<: *defaults}", "spec.integer"},
		{"loose: &defaults {integer: 2.7}\nspec: {<<: [*defaults, {integer: 2}]}", "spec.integer"},
	} {
		t.Run(tc.path+"/"+tc.data, func(t *testing.T) {
			var got document
			err := StrictUnmarshal([]byte(tc.data), &got)
			if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("exit = %d, error = %v; want exit 2 naming %s", code, err, tc.path)
			}
		})
	}
}

func TestStrictUnmarshalInlineMapKeepsNamedFloat(t *testing.T) {
	type document struct {
		Ratio float64        `yaml:"ratio"`
		Extra map[string]int `yaml:",inline"`
	}
	var got document
	if err := StrictUnmarshal([]byte("ratio: 2.7\ncount: 2"), &got); err != nil || got.Ratio != 2.7 || got.Extra["count"] != 2 {
		t.Fatalf("got %+v, %v", got, err)
	}
	err := StrictUnmarshal([]byte("ratio: 2.7\ncount: 2.7"), &got)
	if code := qerrors.GetExitCode(err); code != qerrors.ExitBadUsage || !strings.Contains(err.Error(), "count") {
		t.Fatalf("exit = %d, error = %v; want exit 2 naming count", code, err)
	}
}

func TestStrictUnmarshalKeepsValidYAMLNumbers(t *testing.T) {
	for _, value := range []string{"2", "2.0", "2e0", "20e-1", "0x2", "0o2", "0b10", "+2.0", "2_0.0e-1"} {
		var got int
		if err := StrictUnmarshal([]byte(value), &got); err != nil || got != 2 {
			t.Errorf("%s: got %d, %v; want 2", value, got, err)
		}
	}
	type document struct {
		Integer int     `yaml:"integer"`
		Big     uint64  `yaml:"big"`
		Ratio   float64 `yaml:"ratio"`
		Loose   any     `yaml:"loose"`
	}
	for _, data := range []string{
		"integer: 2\nbig: 18446744073709551615\nratio: 2.7\nloose: {value: 1.9}",
		"loose: &defaults {integer: 2.7}\n<<: *defaults\ninteger: 2\nbig: 18446744073709551615\nratio: 2.7",
		"loose: &defaults {integer: 2.7}\n<<: [{integer: 2}, *defaults]\nbig: 18446744073709551615\nratio: 2.7",
	} {
		var got document
		if err := StrictUnmarshal([]byte(data), &got); err != nil {
			t.Fatal(err)
		}
		if got.Integer != 2 || got.Big != ^uint64(0) || got.Ratio != 2.7 {
			t.Errorf("decoded numbers = %+v", got)
		}
	}
}
