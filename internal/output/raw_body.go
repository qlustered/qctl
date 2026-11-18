package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// WriteRaw writes data for -o json or -o yaml under its JSON field names,
// indented by two spaces; the YAML is data's JSON encoding passed through
// WriteJSONAsYAML. Unlike Printer it masks no secrets and applies no column
// logic, so it is only for a backend response a command emits verbatim. Any
// other format is an error.
func WriteRaw(w io.Writer, format string, data interface{}) error {
	switch Format(format) {
	case FormatJSON:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(data)
	case FormatYAML:
		raw, err := json.Marshal(data)
		if err != nil {
			return err
		}
		return WriteJSONAsYAML(w, raw)
	}
	return fmt.Errorf("unsupported output format: %s", format)
}

// WriteIndentedJSON writes a JSON body indented by two spaces and otherwise
// as it is: key order, escaping and number spelling are the body's.
func WriteIndentedJSON(w io.Writer, body []byte) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(body), "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := buf.WriteTo(w)
	return err
}

// WriteJSONAsYAML writes a JSON document as YAML.
//
// It decodes with UseNumber so an integer past 2^53 -- a BigInteger data cell
// -- survives; jsonnum.YAMLNumbers then turns each json.Number back into a
// YAML number, which yaml.v3 would otherwise quote as a string.
func WriteJSONAsYAML(w io.Writer, raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var generic interface{}
	if err := dec.Decode(&generic); err != nil {
		return err
	}
	encoder := jsonnum.NewYAMLEncoder(w)
	encoder.SetIndent(2)
	defer encoder.Close()
	return encoder.Encode(jsonnum.YAMLNumbers(generic))
}
