package protocoljson

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/korallis/letmecook/internal/closedjson"
)

func TestDataNullsDoNotRelaxStructure(t *testing.T) {
	for _, data := range []string{`{"schema":{"default":null,"enum":[null,"ok"]},"output":[null,{"arbitrary":null}]}`, `null`, `[null]`} {
		var v any
		if err := Decode([]byte(data), &v, len(data)); err != nil {
			t.Fatal("legal null data refused", err)
		}
	}
	for _, data := range []string{``, `{"a":null,"a":1}`, `{"a":[{"x":null,"x":0}]}`, `{"a":null} {}`, `{"a":null} garbage`, "{\"a\":\"\xff\"}", strings.Repeat("[", 34) + "null" + strings.Repeat("]", 34)} {
		var v map[string]json.RawMessage
		if err := Decode([]byte(data), &v, len(data)); !errors.Is(err, closedjson.ErrMalformed) {
			t.Fatalf("malformed input accepted: %q: %v", data, err)
		}
	}
	var typed struct {
		Known any `json:"known"`
	}
	if err := Decode([]byte(`{"unknown":null}`), &typed, 100); !errors.Is(err, closedjson.ErrMalformed) {
		t.Fatal("typed unknown field accepted", err)
	}
	if err := Decode([]byte(`{}`), &typed, 1); !errors.Is(err, closedjson.ErrOversized) {
		t.Fatal("byte bound lost", err)
	}
}
