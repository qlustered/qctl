package jsonnum

import "encoding/json"

// Remarshal returns v as encoding/json encodes it and Unmarshal decodes it,
// and true. When either step fails it returns false and a value whose
// MarshalJSON and MarshalYAML both return that failure.
func Remarshal(v interface{}) (interface{}, bool) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return marshalError{err}, false
	}
	var decoded interface{}
	if err := Unmarshal(encoded, &decoded); err != nil {
		return marshalError{err}, false
	}
	return decoded, true
}

// marshalError stands in for a value Remarshal could not convert.
type marshalError struct{ err error }

// MarshalJSON returns the failure.
func (m marshalError) MarshalJSON() ([]byte, error) { return nil, m.err }

// MarshalYAML returns the failure.
func (m marshalError) MarshalYAML() (interface{}, error) { return nil, m.err }
