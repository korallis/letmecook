// Package closedjson bounds and checks untrusted JSON before decoding typed
// protocol records. It grants no semantic authority to a successfully decoded body.
package closedjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"unicode/utf8"
)

var ErrMalformed = errors.New("malformed")
var ErrOversized = errors.New("oversized")

// Decode rejects invalid UTF-8, duplicate keys, nulls, unknown fields, excessive
// nesting and trailing values. Nullable lists exact legacy field names only.
func Decode(data []byte, dst any, max int, nullable map[string]bool) error {
	if len(data) > max {
		return ErrOversized
	}
	if !utf8.Valid(data) {
		return ErrMalformed
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := value(d, 0, false, nullable); err != nil {
		return ErrMalformed
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrMalformed
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrMalformed
	}
	return nil
}

func value(d *json.Decoder, depth int, allowNull bool, nullable map[string]bool) error {
	if depth > 32 {
		return ErrMalformed
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil && !allowNull {
		return ErrMalformed
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
				return ErrMalformed
			}
			seen[name] = true
			if err := value(d, depth+1, nullable[name], nullable); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return ErrMalformed
		}
	case '[':
		for d.More() {
			if err := value(d, depth+1, false, nullable); err != nil {
				return err
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return ErrMalformed
		}
	default:
		return ErrMalformed
	}
	return nil
}
