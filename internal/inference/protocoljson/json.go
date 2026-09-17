// Package protocoljson validates provider payloads and native harness events.
// Unlike closedjson's control records, these carry arbitrary JSON data (tool
// schemas, arguments and output), where null is legal at any depth. Callers must
// separately close their envelope and validate every authority-bearing field.
package protocoljson

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/korallis/letmecook/internal/closedjson"
)

// Decode preserves closedjson's byte, UTF-8, duplicate-key, depth, trailing-value
// and typed unknown-field checks, but permits null data. It is not a replacement
// for control-wire decoding or semantic validation of model, stream and routing.
func Decode(data []byte, dst any, max int) error {
	if len(data) > max {
		return closedjson.ErrOversized
	}
	if !utf8.Valid(data) {
		return closedjson.ErrMalformed
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if value(d, 0) != nil {
		return closedjson.ErrMalformed
	}
	if _, err := d.Token(); err != io.EOF {
		return closedjson.ErrMalformed
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return closedjson.ErrMalformed
	}
	return nil
}

func value(d *json.Decoder, depth int) error {
	if depth > 32 {
		return closedjson.ErrMalformed
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return closedjson.ErrMalformed
			}
			seen[name] = true
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return closedjson.ErrMalformed
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return closedjson.ErrMalformed
		}
	default:
		return closedjson.ErrMalformed
	}
	return nil
}
