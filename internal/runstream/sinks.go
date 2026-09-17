package runstream

// Sinks is the daemon's per-attempt receiver set (docs/decisions/0002 §4,
// "Streams"). One Sink lives at <dir>/<attempt_id>.sink, bound to the attempt's
// execution identity at creation and limited to MaxLimit bytes. Receive appends
// through the fsync'd runner journal and reports an acknowledgement only after
// the record is durable; a lost acknowledgement is replayed from the retained
// record rather than a second logical write. Owner reads use SinkReader.Window.
// Persisting a record grants no execution authority and proves nothing about
// the process that produced it.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	j "github.com/korallis/letmecook/internal/runnerjournal"
	p "github.com/korallis/letmecook/schemas/execution"
)

// MaxBatch bounds the records one Receive or Window call handles.
const MaxBatch = 1024

// ErrBatch reports a batch that is empty, oversized, non-contiguous or carries a
// foreign identity; nothing from such a batch is appended.
var ErrBatch = errors.New("runstream_batch_invalid")

// SinkReader is the read seam the owner API consumes: the durable window of one
// attempt's stream after a sequence, with the sink's current acknowledgement.
type SinkReader interface {
	Window(attempt string, after, limit int64) ([]Record, Ack, error)
}

// Watermark is the durable position of one sink: records acknowledged through,
// the next accepted sequence, native bytes accepted and the chain digest of
// every acknowledged record (StreamDigest).
type Watermark struct {
	Through  int64  `json:"through"`
	Expected int64  `json:"expected"`
	Bytes    int64  `json:"bytes"`
	Digest   string `json:"digest"`
}

// StreamDigest chains the record digests in order: SHA-256 over the
// concatenated lowercase hex digests of records 1..n. Both peers compute it from
// their own retained records, so a claimed watermark can be checked against the
// daemon's durable sink without trusting either side's byte count alone.
func StreamDigest(records []Record) string {
	h := sha256.New()
	for _, r := range records {
		h.Write([]byte(r.Digest))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Sinks keeps open sinks by attempt. It is safe for concurrent use; per-attempt
// appends serialize on the sink, reads of other attempts do not wait.
type Sinks struct {
	dir    string
	mu     sync.Mutex
	open   map[string]*Sink
	closed bool
	locks  sync.Map // attempt id -> *sync.Mutex, the per-attempt append/finalize lock
}

var _ SinkReader = (*Sinks)(nil)

// SinkView is the read-only face of Sinks: what the store hands to owner reads
// and routes, so nothing outside the store's serialized append path can write.
type SinkView interface {
	SinkReader
	Watermark(attempt string) (Watermark, error)
	Digest(attempt string, through int64) (string, error)
}

type sinkView struct{ k *Sinks }

func (v sinkView) Window(attempt string, after, limit int64) ([]Record, Ack, error) {
	return v.k.Window(attempt, after, limit)
}
func (v sinkView) Watermark(attempt string) (Watermark, error) { return v.k.Watermark(attempt) }
func (v sinkView) Digest(attempt string, through int64) (string, error) {
	return v.k.Digest(attempt, through)
}

// View returns the read-only face of the sink set.
func (k *Sinks) View() SinkView { return sinkView{k} }

// Serialize takes the per-attempt append/finalize lock and returns its release.
// Appends and finalizations of one attempt run one at a time, holding this lock
// across their file and directory syncs; other attempts and the caller's own
// locks are unaffected. Callers take it before any coarser lock.
func (k *Sinks) Serialize(attempt string) func() {
	lock, _ := k.locks.LoadOrStore(attempt, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// NewSinks roots sinks under dir (created 0700 on first use).
func NewSinks(dir string) *Sinks {
	return &Sinks{dir: dir, open: map[string]*Sink{}}
}

// Path is the sink journal directory for one attempt.
func (k *Sinks) Path(attempt string) string { return filepath.Join(k.dir, attempt+".sink") }

// sinkIdentity reads the identity a retained sink was created for, so owner
// reads and watermark checks need no caller-supplied identity.
func sinkIdentity(path string) (p.Identity, error) {
	store, err := j.Open(path)
	if err != nil {
		return p.Identity{}, errors.Join(ErrUnavailable, err)
	}
	rows := store.Records()
	closeErr := store.Close()
	if len(rows) == 0 || closeErr != nil {
		return p.Identity{}, ErrUnavailable
	}
	var head entry
	if json.Unmarshal(rows[0], &head) != nil || head.Open == nil || head.Open.Role != "sink" {
		return p.Identity{}, ErrUnavailable
	}
	return head.Open.Identity, nil
}

// resolve canonicalizes the sink root (journals refuse symlinked paths),
// creating and fsyncing it when create is set.
func (k *Sinks) resolve(create bool) (string, error) {
	if create {
		if err := os.MkdirAll(k.dir, 0700); err != nil {
			return "", errors.Join(ErrUnavailable, err)
		}
		parent, err := os.Open(filepath.Dir(k.dir))
		if err == nil {
			err = errors.Join(parent.Sync(), parent.Close())
		}
		if err != nil {
			return "", errors.Join(ErrUnavailable, err)
		}
	}
	dir, err := filepath.EvalSymlinks(k.dir)
	if errors.Is(err, os.ErrNotExist) {
		return "", os.ErrNotExist
	}
	if err != nil {
		return "", errors.Join(ErrUnavailable, err)
	}
	return dir, nil
}

// get returns the open sink for attempt, opening a retained one or, when create
// is set, creating it bound to identity. A zero identity accepts the retained one.
func (k *Sinks) get(attempt string, identity p.Identity, create bool) (*Sink, error) {
	if !p.ValidID(attempt) {
		return nil, ErrInvalid
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.closed {
		return nil, ErrUnavailable
	}
	if s := k.open[attempt]; s != nil {
		if identity != (p.Identity{}) && s.identity != identity {
			return nil, ErrIdentity
		}
		return s, nil
	}
	dir, err := k.resolve(create)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, attempt+".sink")
	_, err = os.Lstat(path)
	switch {
	case err == nil:
		retained, err := sinkIdentity(path)
		if err != nil {
			return nil, err
		}
		if identity == (p.Identity{}) {
			identity = retained
		}
		s, err := OpenSink(path, identity)
		if err != nil {
			return nil, err
		}
		k.open[attempt] = s
		return s, nil
	case !errors.Is(err, os.ErrNotExist):
		return nil, errors.Join(ErrUnavailable, err)
	case !create:
		return nil, os.ErrNotExist
	}
	if identity.AttemptID != attempt {
		return nil, ErrIdentity
	}
	s, err := CreateSink(path, identity, MaxLimit)
	if err != nil {
		return nil, err
	}
	k.open[attempt] = s
	return s, nil
}

// Receive appends one contiguous batch for attempt. Every record must carry the
// attempt's identity and consecutive sequences; the batch is validated before
// any append. Records are appended in order and each is durable before the next
// is considered. The returned Ack is the sink position after the last durable
// record: on ErrGap or ErrConflict it names what the sink still expects, and
// earlier records of the same batch stay accepted. Duplicate is set only when
// the whole batch replayed retained records.
func (k *Sinks) Receive(attempt string, identity p.Identity, records []Record) (Ack, error) {
	if len(records) == 0 || len(records) > MaxBatch || identity.AttemptID != attempt {
		return Ack{}, ErrBatch
	}
	for n, r := range records {
		if err := Validate(r); err != nil {
			return Ack{}, err
		}
		if r.Identity != identity || (n > 0 && r.Sequence != records[n-1].Sequence+1) {
			return Ack{}, ErrBatch
		}
	}
	s, err := k.get(attempt, identity, true)
	if err != nil {
		return Ack{}, err
	}
	duplicates := 0
	var ack Ack
	for _, r := range records {
		ack, err = s.Receive(r)
		if err != nil {
			ack.Duplicate = false
			return ack, err
		}
		if ack.Duplicate {
			duplicates++
		}
	}
	ack.Duplicate = duplicates == len(records)
	return ack, nil
}

// Watermark reports the durable position of attempt's sink. A sink that was
// never created is an empty stream: through 0, expected 1, the digest of no
// records. It never creates a sink.
func (k *Sinks) Watermark(attempt string) (Watermark, error) {
	s, err := k.get(attempt, p.Identity{}, false)
	if errors.Is(err, os.ErrNotExist) {
		return Watermark{Expected: 1, Digest: StreamDigest(nil)}, nil
	}
	if err != nil {
		return Watermark{}, err
	}
	records := s.Records()
	stats := s.Stats()
	return Watermark{Through: int64(len(records)), Expected: int64(len(records)) + 1, Bytes: stats.NativeBytes, Digest: StreamDigest(records)}, nil
}

// Digest is the chain digest of attempt's first through records. A claim beyond
// the durable watermark is ErrGap: nothing can be attested that was never received.
func (k *Sinks) Digest(attempt string, through int64) (string, error) {
	if through < 0 {
		return "", ErrInvalid
	}
	if through == 0 {
		return StreamDigest(nil), nil
	}
	s, err := k.get(attempt, p.Identity{}, false)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrGap
	}
	if err != nil {
		return "", err
	}
	records := s.Records()
	if through > int64(len(records)) {
		return "", ErrGap
	}
	return StreamDigest(records[:through]), nil
}

// Window returns up to limit retained records with sequence greater than after,
// plus the sink's current acknowledgement. An attempt without a sink is an empty
// window at position zero. Reading never acknowledges or creates anything.
func (k *Sinks) Window(attempt string, after, limit int64) ([]Record, Ack, error) {
	if after < 0 || after > p.MaxInteger || limit < 1 || limit > MaxBatch {
		return nil, Ack{}, ErrInvalid
	}
	s, err := k.get(attempt, p.Identity{}, false)
	if errors.Is(err, os.ErrNotExist) {
		return []Record{}, Ack{Expected: 1}, nil
	}
	if err != nil {
		return nil, Ack{}, err
	}
	records := s.Records()
	stats := s.Stats()
	ack := Ack{Through: int64(len(records)), Expected: int64(len(records)) + 1, Bytes: stats.NativeBytes}
	if after >= int64(len(records)) {
		return []Record{}, ack, nil
	}
	end := min(after+limit, int64(len(records)))
	return records[after:end], ack, nil
}

// Close releases every open sink. Later calls refuse with ErrUnavailable.
func (k *Sinks) Close() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
	var err error
	for attempt, s := range k.open {
		err = errors.Join(err, s.Close())
		delete(k.open, attempt)
	}
	return err
}
