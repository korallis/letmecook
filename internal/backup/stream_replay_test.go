package backup

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/korallis/letmecook/internal/runstream"
)

func TestVerifyAndRestoreReadOnlySource(t *testing.T) {
	f := testFixture(t, true)
	f.custody(t)
	out := f.backup(t)
	before := map[string]string{}
	// Register before chmod so a failed assertion still leaves cleanup possible.
	t.Cleanup(func() {
		_ = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			mode := os.FileMode(0600)
			if d.IsDir() {
				mode = 0700
			}
			return os.Chmod(path, mode)
		})
	})
	must(t, filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0400)
		if d.IsDir() {
			mode = 0500
		} else {
			before[path] = sum(read(t, path))
		}
		return os.Chmod(path, mode)
	}))
	journal := filepath.Join(out, "streams", f.identity.AttemptID+".sink", "journal.jsonl")
	// O_RDWR is the exact old failure mode. Do not call a root-run chmod test
	// evidence of an enforced read-only filesystem if privilege bypasses it.
	writable, err := os.OpenFile(journal, os.O_RDWR|os.O_APPEND, 0)
	if err == nil {
		writable.Close()
		t.Skip("test requires unprivileged enforcement of source write permissions")
	}
	_, err = Verify(out)
	must(t, err)
	entry, err := Restore(ctx, out, filepath.Join(f.root, "readonly-state"), filepath.Join(f.root, "readonly-artifacts"))
	must(t, err)
	if entry.NewGeneration == f.identity.Generation {
		t.Fatal("generation not fenced")
	}
	after := map[string]string{}
	must(t, filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			after[path] = sum(read(t, path))
		}
		return nil
	}))
	if len(before) != len(after) {
		t.Fatal("source files changed")
	}
	for path, hash := range before {
		if after[path] != hash {
			t.Fatal("source mutated", path)
		}
	}
}

func TestReadOnlySinkReplayRejectsCorruptHistories(t *testing.T) {
	f := testFixture(t, true)
	raw := read(t, filepath.Join(f.streams, f.identity.AttemptID+".sink", "journal.jsonl"))
	identity, through, err := replaySink(raw)
	must(t, err)
	if identity != f.identity || through != 1 {
		t.Fatal(identity, through)
	}
	// Every semantic mutation below has a freshly recomputed outer journal hash;
	// rejection must come from replay validation, not just a file checksum.
	for _, mode := range []string{"chain", "sequence", "record-digest", "record-sequence", "record-offset", "record-identity", "spool-role", "limit", "ack", "unknown", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			lines := bytes.Split(bytes.TrimSuffix(raw, []byte{'\n'}), []byte{'\n'})
			rows := make([]sinkEnvelope, len(lines))
			for i, line := range lines {
				must(t, json.Unmarshal(line, &rows[i]))
			}
			var head, chunk sinkEntry
			must(t, json.Unmarshal(rows[0].Data, &head))
			must(t, json.Unmarshal(rows[1].Data, &chunk))
			switch mode {
			case "chain":
				rows[1].Previous = "bad"
			case "sequence":
				rows[1].Sequence++
			case "record-digest":
				chunk.Chunk.Digest = "bad"
			case "record-sequence":
				chunk.Chunk.Sequence++
			case "record-offset":
				chunk.Chunk.Offset++
			case "record-identity":
				chunk.Chunk.Identity.AttemptID = uuid()
			case "spool-role":
				head.Open.Role = "spool"
			case "limit":
				head.Open.Limit = runstream.MinLimit - 1
			case "ack":
				v := int64(1)
				chunk = sinkEntry{Ack: &v}
			}
			rows[0].Data = rawJSON(t, head)
			rows[0].Digest = sinkEnvelopeDigest(rows[0])
			if mode != "chain" {
				rows[1].Previous = rows[0].Digest
			}
			rows[1].Data = rawJSON(t, chunk)
			if mode == "unknown" {
				rows[1].Data = append([]byte(`{"extra":1,`), rows[1].Data[1:]...)
			}
			rows[1].Digest = sinkEnvelopeDigest(rows[1])
			var changed []byte
			for _, row := range rows {
				changed = append(changed, rawJSON(t, row)...)
				changed = append(changed, '\n')
			}
			if mode == "truncated" {
				changed = changed[:len(changed)-1]
			}
			if _, _, err := replaySink(changed); err == nil {
				t.Fatal("invalid journal accepted")
			}
		})
	}
}
