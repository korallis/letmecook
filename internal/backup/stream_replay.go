package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/runnerjournal"
	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

// These private wire projections mirror runnerjournal's canonical envelope and
// runstream's sink entries. Do not use OpenSink here: it is a live writable log
// owner, whereas Verify/Restore must work on immutable read-only backup media.
// Real journals produced by runstream and independently corrupted histories are
// exercised in stream_replay_test.go so format drift fails closed.
type sinkEnvelope struct {
	Sequence int             `json:"sequence"`
	Previous string          `json:"previous"`
	Data     json.RawMessage `json:"data"`
	Digest   string          `json:"digest"`
}
type sinkHeader struct {
	Version  string     `json:"version"`
	Role     string     `json:"role"`
	Identity p.Identity `json:"identity"`
	Limit    int64      `json:"limit"`
}
type sinkEntry struct {
	Open  *sinkHeader       `json:"open,omitempty"`
	Chunk *runstream.Record `json:"chunk,omitempty"`
	Ack   *int64            `json:"ack,omitempty"`
}

var errInvalidStream = errors.New("invalid_stream")

func sinkEnvelopeDigest(row sinkEnvelope) string {
	// The capitalized field names are part of runnerjournal's hash input, not the
	// lowercase on-disk envelope. Preserve the exact canonical encoding.
	raw, err := json.Marshal(struct {
		Sequence int
		Previous string
		Data     json.RawMessage
	}{row.Sequence, row.Previous, row.Data})
	if err != nil {
		return ""
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func replaySink(raw []byte) (p.Identity, int64, error) {
	var identity p.Identity
	if len(raw) == 0 || int64(len(raw)) > runstream.MaxLimit || raw[len(raw)-1] != '\n' {
		return identity, 0, errInvalidStream
	}
	lines := bytes.Split(raw[:len(raw)-1], []byte{'\n'})
	previous := ""
	var offset int64
	for i, line := range lines {
		var row sinkEnvelope
		if closedjson.Decode(line, &row, runnerjournal.MaxRecord, nil) != nil || row.Sequence != i || row.Previous != previous || row.Digest != sinkEnvelopeDigest(row) {
			return identity, 0, errInvalidStream
		}
		canonical, err := json.Marshal(row)
		if err != nil || !bytes.Equal(canonical, line) {
			return identity, 0, errInvalidStream
		}
		previous = row.Digest
		var e sinkEntry
		if closedjson.Decode(row.Data, &e, runnerjournal.MaxRecord, nil) != nil {
			return identity, 0, errInvalidStream
		}
		if i == 0 {
			if e.Open == nil || e.Chunk != nil || e.Ack != nil {
				return identity, 0, errInvalidStream
			}
			head := e.Open
			identity = head.Identity
			if head.Version != runstream.Version || head.Role != "sink" || head.Limit < runstream.MinLimit || head.Limit > runstream.MaxLimit || int64(len(raw)) > head.Limit || !p.ValidID(identity.Generation) || !p.ValidID(identity.TaskID) || !p.ValidID(identity.AttemptID) || identity.Epoch < 0 || identity.Epoch > p.MaxInteger {
				return identity, 0, errInvalidStream
			}
			continue
		}
		if e.Open != nil || e.Chunk == nil || e.Ack != nil {
			return identity, 0, errInvalidStream
		}
		r := *e.Chunk
		if runstream.Validate(r) != nil || r.Identity != identity || r.Sequence != int64(i) || r.Offset != offset {
			return identity, 0, errInvalidStream
		}
		offset += int64(len(r.Native.Data))
	}
	return identity, int64(len(lines) - 1), nil
}
