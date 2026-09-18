//go:build system

package system

import (
	"encoding/json"
	"fmt"

	p "github.com/korallis/letmecook/schemas/execution"
)

type streamRecord struct {
	Version  string     `json:"version"`
	Identity p.Identity `json:"identity"`
	Sequence int64      `json:"sequence"`
	Offset   int64      `json:"offset"`
	Native   struct {
		Version string `json:"version"`
		Kind    string `json:"kind"`
		Data    []byte `json:"data"`
	} `json:"native"`
	Normalized struct {
		Stream string `json:"stream"`
		Text   string `json:"text"`
	} `json:"normalized"`
	Digest string `json:"digest"`
}

func (r *installation) streamEvidence(id string, identity object, terminal bool) object {
	records := []any{}
	var after, offset int64
	var ack object
	valid := true
	limit := 128
	for page := 0; page < 1024; page++ {
		window := obj(r.get(fmt.Sprintf("/api/v1/attempts/%s/stream?after=%d&limit=%d", id, after, limit)))
		if window["error"] != nil && limit > 8 {
			// The failed documented page remains an assertion failure. A bounded
			// retry preserves the remaining custody evidence for diagnosis.
			limit = 8
			continue
		}
		ack = obj(window["ack"])
		chunk := arr(window["records"])
		if len(chunk) == 0 {
			break
		}
		for _, raw := range chunk {
			b, _ := json.Marshal(raw)
			var rec streamRecord
			if json.Unmarshal(b, &rec) != nil {
				valid = false
				continue
			}
			want := rec.Digest
			rec.Digest = ""
			canonical, _ := json.Marshal(rec)
			valid = valid && rec.Sequence == after+1 && rec.Offset == offset && digest(canonical) == want && jsonEqual(obj(raw)["identity"], identity)
			offset += int64(len(rec.Native.Data))
			after = rec.Sequence
			records = append(records, raw)
		}
		if after >= num(ack["through"]) {
			break
		}
	}
	r.s.check(r.t, "stream contiguous bytes identity and canonical digests", valid, object{"records": len(records), "native_bytes": offset, "ack": ack})
	if terminal {
		r.s.check(r.t, "terminal durable watermark matches complete read window", after == num(ack["through"]) && offset == num(ack["bytes"]) && num(ack["expected"]) == after+1, ack)
	}
	return object{"records": records, "ack": ack}
}
