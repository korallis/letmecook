// Package runnerjournal provides a bounded, private, single-owner durable log.
// It is provisional runner metadata, not a process supervisor or backup format.
package runnerjournal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const MaxRecord = 256 * 1024
const MaxLog = 8 * 1024 * 1024

var ErrUnavailable = errors.New("runner_journal_unavailable")

type record struct {
	Sequence int             `json:"sequence"`
	Previous string          `json:"previous"`
	Data     json.RawMessage `json:"data"`
	Digest   string          `json:"digest"`
}

type Journal struct {
	mu        sync.Mutex
	path      string
	dir, file *os.File
	root      *os.Root
	rows      []record
	size      int64
	failed    bool
	// Test seams exercise actual write and sync failure ordering, not success mocks.
	syncFile func() error
	syncDir  func() error
}

func digest(sequence int, previous string, data []byte) string {
	b, _ := json.Marshal(struct {
		Sequence int
		Previous string
		Data     json.RawMessage
	}{sequence, previous, data})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Create requires a new directory. Existing or lost logs are never initialized.
// Initialization uses a private sibling, published without replacing existing
// state. Failed creation removes only its own file and now-empty directory.
func Create(path string, initial []byte) (*Journal, error) {
	return create(path, initial, func(j *Journal) error { return j.open(true) }, (*os.File).Sync)
}

// Per-call seams cover failures before opening and at each durability boundary.
func create(path string, initial []byte, prepare func(*Journal) error, syncParent func(*os.File) error) (_ *Journal, err error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnavailable
	}
	parentPath := filepath.Dir(path)
	canonical, err := filepath.EvalSymlinks(parentPath)
	if err != nil || canonical != parentPath {
		return nil, ErrUnavailable
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	temporary, err := os.MkdirTemp(parentPath, ".runner-journal-")
	if err != nil {
		return nil, err
	}
	j := &Journal{path: temporary}
	created, err := os.Lstat(temporary)
	defer func() {
		if err != nil {
			err = errors.Join(err, j.removeCreated(created), parent.Sync(), j.Close())
		}
	}()
	if err != nil {
		return nil, err
	}
	if err = prepare(j); err != nil {
		return nil, err
	}
	opened, err := j.dir.Stat()
	if err != nil || !os.SameFile(created, opened) {
		return nil, ErrUnavailable
	}
	if err = j.Append(initial); err != nil {
		return nil, err
	}
	// Keep the same locked inode and open handles throughout publication.
	if err = renameNew(temporary, path); err != nil {
		return nil, err
	}
	j.path = path
	// Persist the new directory entry before its first acknowledgement can escape.
	if err = syncParent(parent); err != nil {
		return nil, err
	}
	opened, err = parent.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(parentPath)
	if err != nil || !os.SameFile(opened, current) {
		return nil, ErrUnavailable
	}
	if err = j.Check(); err != nil {
		return nil, err
	}
	return j, nil
}

// removeCreated runs while any acquired ownership lock is still held. It never
// recursively removes content, and checks retained inode identities before unlink.
// Cleanup errors are returned with the creation error, rather than hidden.
func (j *Journal) removeCreated(created os.FileInfo) error {
	current, err := os.Lstat(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if created == nil || !os.SameFile(created, current) || !current.IsDir() {
		return ErrUnavailable
	}
	if j.file != nil {
		root, err := j.root.Stat(".")
		if err != nil || !os.SameFile(created, root) {
			return ErrUnavailable
		}
		original, err := j.file.Stat()
		if err != nil {
			return err
		}
		current, err := j.root.Lstat("journal.jsonl")
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err == nil {
			if !os.SameFile(original, current) {
				return ErrUnavailable
			}
			if err := j.root.Remove("journal.jsonl"); err != nil {
				return err
			}
		}
	}
	// Remove refuses a non-empty directory, preserving any foreign additions.
	return os.Remove(j.path)
}

func Open(path string) (*Journal, error) {
	j := &Journal{path: path}
	if err := j.open(false); err != nil {
		j.Close()
		return nil, err
	}
	return j, nil
}

// The caller retains partial handles on failure for rollback before Close.
func (j *Journal) open(create bool) (err error) {
	path := j.path
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrUnavailable
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || canonical != path {
		return ErrUnavailable
	}
	j.dir, err = openOwnedDirectory(path)
	if err != nil {
		return err
	}
	j.root, err = os.OpenRoot(path)
	if err != nil {
		return err
	}
	a, err := j.dir.Stat()
	if err != nil {
		return err
	}
	b, err := j.root.Stat(".")
	if err != nil || !os.SameFile(a, b) {
		return ErrUnavailable
	}
	entries, err := j.dir.ReadDir(-1)
	if err != nil {
		return err
	}
	if create && len(entries) != 0 || !create && (len(entries) != 1 || entries[0].Name() != "journal.jsonl") {
		return ErrUnavailable
	}
	flags := os.O_RDWR | os.O_APPEND
	if create {
		flags |= os.O_CREATE | os.O_EXCL
	} else {
		info, e := j.root.Lstat("journal.jsonl")
		if e != nil || !ownedFile(info) {
			return ErrUnavailable
		}
	}
	j.file, err = j.root.OpenFile("journal.jsonl", flags, 0600)
	if err != nil {
		return err
	}
	info, err := j.file.Stat()
	if err != nil || !ownedFile(info) || info.Size() > MaxLog {
		return ErrUnavailable
	}
	j.syncFile, j.syncDir = j.file.Sync, j.dir.Sync
	j.size = info.Size()
	if !create {
		data, e := io.ReadAll(io.LimitReader(j.file, MaxLog+1))
		if e != nil || len(data) == 0 || len(data) > MaxLog || data[len(data)-1] != '\n' {
			return ErrUnavailable
		}
		previous := ""
		for _, line := range bytes.Split(data[:len(data)-1], []byte{'\n'}) {
			if len(line) > MaxRecord {
				return ErrUnavailable
			}
			var row record
			d := json.NewDecoder(bytes.NewReader(line))
			d.DisallowUnknownFields()
			if d.Decode(&row) != nil || row.Sequence != len(j.rows) || row.Previous != previous || row.Digest != digest(row.Sequence, row.Previous, row.Data) {
				return ErrUnavailable
			}
			canonical, e := json.Marshal(row)
			if e != nil || !bytes.Equal(canonical, line) {
				return ErrUnavailable
			}
			j.rows = append(j.rows, row)
			previous = row.Digest
		}
	}
	return nil
}

// Check verifies the still-owned directory/file identity before replaying a
// positive acknowledgement. Closing/replacing/chmodding retained state refuses.
func (j *Journal) Check() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(); err != nil {
		j.failed = true
		return err
	}
	return nil
}

// Records returns detached copies; callers cannot mutate retained history.
func (j *Journal) Records() [][]byte {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([][]byte, len(j.rows))
	for i, r := range j.rows {
		out[i] = bytes.Clone(r.Data)
	}
	return out
}

func (j *Journal) check() error {
	if j.failed || j.file == nil || j.dir == nil {
		return ErrUnavailable
	}
	a, e := j.dir.Stat()
	if e != nil {
		return ErrUnavailable
	}
	b, e := os.Lstat(j.path)
	if e != nil || !os.SameFile(a, b) || !ownedDirectory(b) {
		return ErrUnavailable
	}
	a, e = j.file.Stat()
	if e != nil || !ownedFile(a) || a.Size() != j.size {
		return ErrUnavailable
	}
	b, e = j.root.Lstat("journal.jsonl")
	if e != nil || !ownedFile(b) || !os.SameFile(a, b) {
		return ErrUnavailable
	}
	return nil
}

// Append returns success only after file and directory sync. Every uncertain
// outcome poisons this handle; callers must stop and reopen for reconciliation.
func (j *Journal) Append(data []byte) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(); err != nil {
		j.failed = true
		return err
	}
	if !json.Valid(data) || len(data) > MaxRecord/2 {
		return ErrUnavailable
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return err
	}
	previous := ""
	if len(j.rows) > 0 {
		previous = j.rows[len(j.rows)-1].Digest
	}
	r := record{Sequence: len(j.rows), Previous: previous, Data: bytes.Clone(compact.Bytes())}
	r.Digest = digest(r.Sequence, r.Previous, r.Data)
	line, err := json.Marshal(r)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	if len(line) > MaxRecord || j.size+int64(len(line)) > MaxLog {
		j.failed = true
		return ErrUnavailable
	}
	j.failed = true
	n, err := j.file.Write(line)
	if err != nil {
		return err
	}
	if n != len(line) {
		return io.ErrShortWrite
	}
	if err = j.syncFile(); err != nil {
		return fmt.Errorf("runner journal file sync: %w", err)
	}
	if err = j.syncDir(); err != nil {
		return fmt.Errorf("runner journal directory sync: %w", err)
	}
	j.size += int64(n)
	j.failed = false
	if err = j.check(); err != nil {
		j.failed = true
		return err
	}
	j.rows = append(j.rows, r)
	return nil
}

func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.failed = true
	var err error
	if j.file != nil {
		err = j.file.Close()
		j.file = nil
	}
	if j.root != nil {
		err = errors.Join(err, j.root.Close())
		j.root = nil
	}
	if j.dir != nil {
		err = errors.Join(err, j.dir.Close())
		j.dir = nil
	}
	return err
}
