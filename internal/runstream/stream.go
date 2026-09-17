// Package runstream provides provisional durable ordering, acknowledgement and
// bounded spooling for runner output records. It contains no transport, no
// process supervision and no harness integration: nothing here produces output,
// and persisting a record grants no execution authority.
package runstream

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	j "github.com/korallis/letmecook/internal/runnerjournal"
	p "github.com/korallis/letmecook/schemas/execution"
)

// Version names this provisional framing. It is deliberately separate from the
// closed execution message union: O2 still owns the real stream encoding.
const Version = "stream-provisional-v1"

const (
	MaxNative       = 48 * 1024
	MaxText         = 8 * 1024
	MaxLabel        = 64
	MinLimit  int64 = 64 * 1024
	MaxLimit  int64 = 4 * 1024 * 1024
)

var (
	ErrUnavailable = errors.New("runstream_unavailable")
	ErrInvalid     = errors.New("runstream_record_invalid")
	ErrIdentity    = errors.New("runstream_identity_refused")
	ErrSpoolFull   = errors.New("runstream_spool_full")
	ErrGap         = errors.New("runstream_sequence_gap")
	ErrConflict    = errors.New("runstream_record_conflict")
)

// Native is the harness event exactly as produced, kept beside the normalized
// view so a later consumer never depends on this slice's normalization choices.
type Native struct {
	Version string `json:"version"`
	Kind    string `json:"kind"`
	Data    []byte `json:"data"`
}

// Normalized is the bounded, UTF-8 projection used for ordinary display.
type Normalized struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// Record is one ordered output chunk. Offset is the native byte offset of this
// chunk within the attempt's stream, so gaps are arithmetic, not inference.
type Record struct {
	Version    string     `json:"version"`
	Identity   p.Identity `json:"identity"`
	Sequence   int64      `json:"sequence"`
	Offset     int64      `json:"offset"`
	Native     Native     `json:"native"`
	Normalized Normalized `json:"normalized"`
	Digest     string     `json:"digest"`
}

// Ack reports the receiver's durable watermark. Through is acknowledged as
// synced by the receiver; Expected is the only sequence it will accept next.
type Ack struct {
	Through   int64 `json:"through"`
	Expected  int64 `json:"expected"`
	Bytes     int64 `json:"bytes"`
	Duplicate bool  `json:"duplicate"`
}

// Stats separates what a sender persisted locally from what a receiver has
// acknowledged. Appended bytes are never evidence of remote durability.
// UsedBytes is the complete journal file length on a healthy handle; LimitBytes
// includes the sender's acknowledgement reserve, not just chunk payloads.
type Stats struct {
	Appended     int64 `json:"appended"`
	Acknowledged int64 `json:"acknowledged"`
	NativeBytes  int64 `json:"native_bytes"`
	UsedBytes    int64 `json:"used_bytes"`
	LimitBytes   int64 `json:"limit_bytes"`
}

type header struct {
	Version  string     `json:"version"`
	Role     string     `json:"role"`
	Identity p.Identity `json:"identity"`
	Limit    int64      `json:"limit"`
}
type entry struct {
	Open  *header `json:"open,omitempty"`
	Chunk *Record `json:"chunk,omitempty"`
	Ack   *int64  `json:"ack,omitempty"`
}

func digest(r Record) string {
	r.Digest = ""
	b, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func label(v string) bool {
	return v != "" && len(v) <= MaxLabel && utf8.ValidString(v)
}
func validIdentity(v p.Identity) bool {
	return p.ValidID(v.Generation) && p.ValidID(v.TaskID) && p.ValidID(v.AttemptID) && v.Epoch >= 0 && v.Epoch <= p.MaxInteger
}

// Validate revalidates every received record. Static types convey no trust and
// a matching digest only proves self-consistency, never authenticity.
func Validate(r Record) error {
	switch {
	case r.Version != Version, !validIdentity(r.Identity):
		return ErrInvalid
	case r.Sequence < 1 || r.Sequence > p.MaxInteger, r.Offset < 0 || r.Offset > p.MaxInteger:
		return ErrInvalid
	case len(r.Native.Data) == 0 || len(r.Native.Data) > MaxNative:
		return ErrInvalid
	case !label(r.Native.Version) || !label(r.Native.Kind):
		return ErrInvalid
	case r.Normalized.Stream != "stdout" && r.Normalized.Stream != "stderr" && r.Normalized.Stream != "status":
		return ErrInvalid
	case len(r.Normalized.Text) > MaxText || !utf8.ValidString(r.Normalized.Text):
		return ErrInvalid
	case r.Digest != digest(r):
		return ErrConflict
	}
	return nil
}

type log struct {
	mu       sync.Mutex
	store    *j.Journal
	path     string
	role     string
	entries  int
	identity p.Identity
	limit    int64
	used     int64
	rows     []Record
	acked    int64
	offset   int64
	failed   bool
}

// journalBytes mirrors runnerjournal's canonical envelope solely for preflight
// sizing. Hash contents do not affect JSON length. Check the prediction against
// the actual file on create, replay and every append so format drift fails closed.
func journalBytes(sequence int, data []byte) (int64, error) {
	hash := strings.Repeat("0", sha256.Size*2)
	previous := hash
	if sequence == 0 {
		previous = ""
	}
	line, err := json.Marshal(struct {
		Sequence int             `json:"sequence"`
		Previous string          `json:"previous"`
		Data     json.RawMessage `json:"data"`
		Digest   string          `json:"digest"`
	}{sequence, previous, data, hash})
	return int64(len(line) + 1), err // runnerjournal appends a newline.
}

func (l *log) measure(expected int64) error {
	info, err := os.Stat(filepath.Join(l.path, "journal.jsonl"))
	if err == nil {
		err = l.store.Check()
	}
	if err != nil {
		l.failed = true
		return errors.Join(ErrUnavailable, err)
	}
	l.used = info.Size()
	if l.used != expected {
		l.failed = true
		return ErrUnavailable
	}
	return nil
}

func create(path, role string, identity p.Identity, limit int64) (*log, error) {
	if !validIdentity(identity) {
		return nil, ErrIdentity
	}
	if limit < MinLimit || limit > MaxLimit {
		return nil, ErrInvalid
	}
	line, err := json.Marshal(entry{Open: &header{Version: Version, Role: role, Identity: identity, Limit: limit}})
	if err != nil {
		return nil, ErrInvalid
	}
	size, err := journalBytes(0, line)
	if err != nil || size > limit {
		return nil, ErrInvalid
	}
	store, err := j.Create(path, line)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	l := &log{store: store, path: path, role: role, entries: 1, identity: identity, limit: limit}
	if err := l.measure(size); err != nil {
		store.Close()
		return nil, err
	}
	return l, nil
}

// open refuses any history that does not replay to exactly this role, version
// and identity. A changed generation is reconciliation input, not a resume.
func open(path, role string, identity p.Identity) (*log, error) {
	if !validIdentity(identity) {
		return nil, ErrIdentity
	}
	store, err := j.Open(path)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	l := &log{store: store, path: path, role: role, identity: identity}
	rows := store.Records()
	if len(rows) == 0 {
		store.Close()
		return nil, ErrUnavailable
	}
	var head entry
	if json.Unmarshal(rows[0], &head) != nil || head.Open == nil || head.Chunk != nil || head.Ack != nil {
		store.Close()
		return nil, ErrUnavailable
	}
	if head.Open.Version != Version || head.Open.Role != role {
		store.Close()
		return nil, ErrUnavailable
	}
	if head.Open.Identity != identity {
		store.Close()
		return nil, ErrIdentity
	}
	if head.Open.Limit < MinLimit || head.Open.Limit > MaxLimit {
		store.Close()
		return nil, ErrInvalid
	}
	l.limit = head.Open.Limit
	var size int64
	for sequence, row := range rows {
		n, err := journalBytes(sequence, row)
		if err != nil {
			store.Close()
			return nil, ErrUnavailable
		}
		size += n
	}
	if err := l.measure(size); err != nil {
		store.Close()
		return nil, err
	}
	if l.used > l.limit {
		store.Close()
		return nil, ErrSpoolFull
	}
	l.entries = len(rows)
	for _, row := range rows[1:] {
		var e entry
		if json.Unmarshal(row, &e) != nil || e.Open != nil {
			store.Close()
			return nil, ErrUnavailable
		}
		switch {
		case e.Chunk != nil && e.Ack == nil:
			r := *e.Chunk
			if Validate(r) != nil || r.Identity != identity || r.Sequence != int64(len(l.rows))+1 || r.Offset != l.offset {
				store.Close()
				return nil, ErrUnavailable
			}
			l.rows = append(l.rows, r)
			l.offset += int64(len(r.Native.Data))
			if role == "sink" {
				l.acked = r.Sequence
			}
		case e.Ack != nil && e.Chunk == nil:
			if role != "spool" || *e.Ack <= l.acked || *e.Ack > int64(len(l.rows)) {
				store.Close()
				return nil, ErrUnavailable
			}
			l.acked = *e.Ack
		default:
			store.Close()
			return nil, ErrUnavailable
		}
	}
	return l, nil
}

// append persists before returning. Any uncertain write poisons this handle;
// callers must stop and reopen rather than retrying against unknown state.
func (l *log) append(e entry, chunk bool) error {
	line, err := json.Marshal(e)
	if err != nil {
		return ErrInvalid
	}
	size, err := journalBytes(l.entries, line)
	if err != nil {
		return ErrInvalid
	}
	budget := l.limit
	if chunk && l.role == "spool" {
		// Reserve acknowledgement capacity inside the total durable byte limit.
		budget -= l.limit / 4
	}
	if l.used+size > budget {
		return ErrSpoolFull
	}
	if err = l.store.Append(line); err != nil {
		l.failed = true
		return errors.Join(ErrUnavailable, err)
	}
	if err := l.measure(l.used + size); err != nil {
		return err
	}
	l.entries++
	return nil
}

func (l *log) stats() Stats {
	return Stats{
		Appended:     int64(len(l.rows)),
		Acknowledged: l.acked,
		NativeBytes:  l.offset,
		UsedBytes:    l.used,
		LimitBytes:   l.limit,
	}
}

func (l *log) close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failed = true
	if l.store == nil {
		return nil
	}
	err := l.store.Close()
	l.store = nil
	return err
}

// Spool is the sender side: durable local ordering plus the acknowledged
// watermark that alone decides what a reconnect may stop resending.
type Spool struct{ *log }

func CreateSpool(path string, identity p.Identity, limit int64) (*Spool, error) {
	l, err := create(path, "spool", identity, limit)
	if err != nil {
		return nil, err
	}
	return &Spool{l}, nil
}
func OpenSpool(path string, identity p.Identity) (*Spool, error) {
	l, err := open(path, "spool", identity)
	if err != nil {
		return nil, err
	}
	return &Spool{l}, nil
}

// Append assigns the next sequence and native offset and syncs the record
// before returning it. A full spool refuses new output and keeps every
// unacknowledged record recoverable instead of dropping the oldest work.
// Native bytes are copied; the returned record is detached from retained state.
func (s *Spool) Append(native Native, normalized Normalized) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.store == nil {
		return Record{}, ErrUnavailable
	}
	r := Record{
		Version:    Version,
		Identity:   s.identity,
		Sequence:   int64(len(s.rows)) + 1,
		Offset:     s.offset,
		Native:     native,
		Normalized: normalized,
	}
	r.Digest = digest(r)
	if err := Validate(r); err != nil {
		return Record{}, err
	}
	r = clone(r)
	if err := s.append(entry{Chunk: &r}, true); err != nil {
		return Record{}, err
	}
	s.rows = append(s.rows, r)
	s.offset += int64(len(native.Data))
	return clone(r), nil
}

// Acknowledge records a receiver watermark durably. It is monotonic and
// idempotent; a watermark beyond what was appended is refused outright so a
// lost or forged acknowledgement can never imply undurable work is safe.
func (s *Spool) Acknowledge(through int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed || s.store == nil {
		return ErrUnavailable
	}
	if through < 0 || through > int64(len(s.rows)) {
		return ErrConflict
	}
	if through <= s.acked {
		return nil
	}
	if err := s.append(entry{Ack: &through}, false); err != nil {
		return err
	}
	s.acked = through
	return nil
}

// Pending returns the ordered records a reconnect must resend.
func (s *Spool) Pending() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, int64(len(s.rows))-s.acked)
	for _, r := range s.rows[s.acked:] {
		out = append(out, clone(r))
	}
	return out
}

// Records returns detached copies of every retained record, acknowledged or not.
func (s *Spool) Records() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.rows))
	for _, r := range s.rows {
		out = append(out, clone(r))
	}
	return out
}

func (s *Spool) Acknowledged() int64 { s.mu.Lock(); defer s.mu.Unlock(); return s.acked }
func (s *Spool) Stats() Stats        { s.mu.Lock(); defer s.mu.Unlock(); return s.stats() }
func (s *Spool) Close() error        { return s.close() }

// Sink is the receiver side: contiguous, deduplicated, synced acceptance.
type Sink struct{ *log }

func CreateSink(path string, identity p.Identity, limit int64) (*Sink, error) {
	l, err := create(path, "sink", identity, limit)
	if err != nil {
		return nil, err
	}
	return &Sink{l}, nil
}
func OpenSink(path string, identity p.Identity) (*Sink, error) {
	l, err := open(path, "sink", identity)
	if err != nil {
		return nil, err
	}
	return &Sink{l}, nil
}

// Receive accepts only the expected next sequence. An earlier sequence replays
// its retained acknowledgement without duplicating a logical record, a later
// one reports the gap explicitly, and a changed payload for a retained
// sequence conflicts. A positive Ack follows the durable write, never precedes it.
// Accepted native bytes are detached from the caller's buffer.
func (k *Sink) Receive(r Record) (Ack, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.failed || k.store == nil {
		return Ack{}, ErrUnavailable
	}
	position := k.position()
	if err := Validate(r); err != nil {
		return position, err
	}
	if r.Identity != k.identity {
		return position, ErrIdentity
	}
	switch {
	case r.Sequence < position.Expected:
		retained := k.rows[r.Sequence-1]
		if retained.Digest != r.Digest {
			return position, ErrConflict
		}
		position.Duplicate = true
		return position, nil
	case r.Sequence > position.Expected:
		return position, ErrGap
	case r.Offset != k.offset:
		return position, ErrConflict
	}
	r = clone(r)
	if err := k.append(entry{Chunk: &r}, true); err != nil {
		return k.position(), err
	}
	k.rows = append(k.rows, r)
	k.offset += int64(len(r.Native.Data))
	k.acked = r.Sequence
	return k.position(), nil
}

func (k *Sink) position() Ack {
	return Ack{Through: int64(len(k.rows)), Expected: int64(len(k.rows)) + 1, Bytes: k.offset}
}

// Records returns detached copies of the accepted stream in order.
func (k *Sink) Records() []Record {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]Record, 0, len(k.rows))
	for _, r := range k.rows {
		out = append(out, clone(r))
	}
	return out
}

func (k *Sink) Acknowledged() int64 { k.mu.Lock(); defer k.mu.Unlock(); return int64(len(k.rows)) }
func (k *Sink) Expected() int64     { k.mu.Lock(); defer k.mu.Unlock(); return int64(len(k.rows)) + 1 }
func (k *Sink) Stats() Stats        { k.mu.Lock(); defer k.mu.Unlock(); return k.stats() }
func (k *Sink) Close() error        { return k.close() }

func clone(r Record) Record {
	r.Native.Data = append([]byte(nil), r.Native.Data...)
	return r
}
