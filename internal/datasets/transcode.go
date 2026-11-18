package datasets

import (
	"bytes"
	"encoding/json"

	"github.com/qlustered/qctl/internal/pkg/jsonnum"
)

// transcodeJSON copies src onto dst through JSON.
//
// Every settings conversion in this package goes through it. api.SettingsSchema
// (read), api.NewSettingsSchema (create), api.SettingsSchemaOptional (update)
// and the manifest's own SettingsModel share one vocabulary of JSON keys --
// settings_model.gen.go derives the manifest from the first of them, and each of
// the other two has its OWN tripwire against it: TestSettingsSchemaAndOptionalShareKeySets
// compares the read and update types key set to key set, and
// TestEverySettingsManifestFieldReachesTheCreateBody walks a fully populated
// manifest into a create body and compares the values that arrive -- so a
// single marshal/unmarshal transfers the whole settings model, including a key
// a later regen adds that nobody has wired up yet. A field-by-field copy is what
// let three keys go missing from four converters at once (qctl-o22).
//
// Keys with no counterpart on dst are dropped by encoding/json, and that is the
// mechanism that keeps id, version_id, whodunit_id and schema_overrides off a
// manifest: they are absent from SettingsModel, so they simply do not survive
// the trip.
//
// A number bound for an empty interface in dst decodes as jsonnum.Unmarshal
// decodes it. transcodeJSONToWire is the variant for a destination bound for
// the server.
func transcodeJSON(src, dst any) error {
	return transcode(src, dst, false)
}

// transcodeJSONToWire is transcodeJSON for a destination bound for the server.
//
// It decodes numbers as json.Number, because
// default_value_for_field_when_casting_error is a map[string]any on all four
// types: without it every JSON number becomes a float64, and re-encoding a value
// past 2^53 hands the server back a DIFFERENT number (12345678901234567 ->
// 12345678901234568). yaml.v3 decodes a big integer in a manifest as a Go int,
// so the manifest side is exact and this decode is the only place the digits
// could be lost.
//
// It is deliberately NOT used for a manifest-bound destination: a json.Number is
// a string type, so yaml.v3 renders it quoted, and `get table -o yaml` would
// start emitting every number in that map as "2" instead of 2 -- which then
// applies back as a JSON string.
func transcodeJSONToWire(src, dst any) error {
	return transcode(src, dst, true)
}

func transcode(src, dst any, useNumber bool) error {
	raw, err := json.Marshal(src)
	if err != nil {
		return err
	}
	if !useNumber {
		return jsonnum.Unmarshal(raw, dst)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	return dec.Decode(dst)
}
