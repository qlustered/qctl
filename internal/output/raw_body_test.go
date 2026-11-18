package output

import (
	"bytes"
	"testing"
)

// TestYAMLNumbersPastInt64 pins the spellings jsonnum.YAMLNumbers' comment
// promises for the numbers neither int64 nor float64 holds.
func TestYAMLNumbersPastInt64(t *testing.T) {
	var out bytes.Buffer
	raw := `[18446744073709551615, 123456789012345678901234567890, 1e400]`
	if err := WriteJSONAsYAML(&out, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	want := "- 18446744073709551615\n- !!int 123456789012345678901234567890\n- !!float 1e400\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// TestWriteJSONAsYAMLKeepsTheDigitsOfAnIntegerFloat: an integer past int64 that
// encoding/json also writes for a float64 keeps its digits.
func TestWriteJSONAsYAMLKeepsTheDigitsOfAnIntegerFloat(t *testing.T) {
	var out bytes.Buffer
	if err := WriteJSONAsYAML(&out, []byte(`[10000000000000000000, -10000000000000000000]`)); err != nil {
		t.Fatal(err)
	}
	want := "- 10000000000000000000\n- !!int -10000000000000000000\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// TestWriteIndentedJSONKeepsTheBodysSpelling: the body's key order and number
// spelling survive, which a decode-and-re-encode would lose.
func TestWriteIndentedJSONKeepsTheBodysSpelling(t *testing.T) {
	var out bytes.Buffer
	if err := WriteIndentedJSON(&out, []byte(` {"z":1,"a":12345678901234567890} `)); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 1,\n  \"a\": 12345678901234567890\n}\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

// rawRecord carries a json tag that yaml.v3 would not derive from the field
// name, and integers a float64 decode would spell in exponent form or round.
type rawRecord struct {
	DatasetName string  `json:"dataset_name"`
	RowID       int64   `json:"row_id"`
	Big         int64   `json:"big"`
	Rate        float64 `json:"rate"`
}

func TestWriteRawYAMLKeepsWireKeysAndIntegers(t *testing.T) {
	var out bytes.Buffer
	rec := rawRecord{DatasetName: "orders", RowID: 1234567, Big: 9007199254740993, Rate: 0.5}
	if err := WriteRaw(&out, "yaml", rec); err != nil {
		t.Fatal(err)
	}
	want := "big: 9007199254740993\ndataset_name: orders\nrate: 0.5\nrow_id: 1234567\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestWriteRawJSONIsIndentedByTwoSpaces(t *testing.T) {
	var out bytes.Buffer
	if err := WriteRaw(&out, "json", rawRecord{DatasetName: "orders", RowID: 1234567}); err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"dataset_name\": \"orders\",\n  \"row_id\": 1234567,\n  \"big\": 0,\n  \"rate\": 0\n}\n"
	if out.String() != want {
		t.Errorf("got\n%s\nwant\n%s", out.String(), want)
	}
}

func TestWriteRawRejectsOtherFormats(t *testing.T) {
	var out bytes.Buffer
	if err := WriteRaw(&out, "table", rawRecord{}); err == nil {
		t.Fatal("WriteRaw accepted -o table")
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q before rejecting the format", out.String())
	}
}
