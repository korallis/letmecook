package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

// A missing journal is not "no output" when durable runtime/finalization
// evidence promises a watermark. The file's checksum alone also cannot prove
// its identity, hash-chain or acknowledged sequence; replay its bounded bytes
// without opening a live writer or modifying the source backup.
func checkStreams(ctx context.Context, root *os.Root, m Manifest) error {
	db, err := openDatabase(filepath.Join(root.Name(), "state.db"), true)
	if err != nil {
		return err
	}
	defer db.Close()
	type expectation struct {
		task           string
		epoch, through int64
	}
	expected := map[string]expectation{}
	rows, err := db.QueryContext(ctx, "SELECT id,task_id,epoch FROM attempts")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var v expectation
		if err = rows.Scan(&id, &v.task, &v.epoch); err != nil {
			rows.Close()
			return err
		}
		expected[id] = v
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	generations := map[string]bool{m.Generation: true}
	rows, err = db.QueryContext(ctx, "SELECT old_generation,new_generation FROM restore_history")
	if err != nil {
		return err
	}
	for rows.Next() {
		var old, next string
		if err = rows.Scan(&old, &next); err != nil {
			rows.Close()
			return err
		}
		generations[old] = true
		generations[next] = true
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	rows, err = db.QueryContext(ctx, "SELECT attempt_id,body FROM runtime_observations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var id string
		var body []byte
		if err = rows.Scan(&id, &body); err != nil {
			rows.Close()
			return err
		}
		// S1's exit observation uses stream_through; a retained completion may use
		// stream.through. Unknown unrelated evidence fields do not grant authority.
		var evidence struct {
			Through int64 `json:"stream_through"`
			Stream  struct {
				Through int64 `json:"through"`
			} `json:"stream"`
		}
		if json.Unmarshal(body, &evidence) != nil || evidence.Through < 0 || evidence.Through > p.MaxInteger || evidence.Stream.Through < 0 || evidence.Stream.Through > p.MaxInteger {
			rows.Close()
			return errors.New("invalid_stream_evidence")
		}
		v, ok := expected[id]
		if !ok {
			rows.Close()
			return errors.New("invalid_stream_evidence")
		}
		v.through = max(v.through, evidence.Through, evidence.Stream.Through)
		expected[id] = v
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	present := map[string]bool{}
	for _, entry := range m.Inventory {
		if entry.Kind != "stream" {
			continue
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		id := strings.TrimSuffix(strings.TrimPrefix(entry.RelativePath, "streams/"), ".sink/journal.jsonl")
		want, ok := expected[id]
		if !ok {
			return errors.New("unreferenced_stream")
		}
		raw, err := readBounded(root, entry.RelativePath, int(runstream.MaxLimit))
		if err != nil {
			return err
		}
		identity, through, err := replaySink(raw)
		if err != nil {
			return err
		}
		if identity.TaskID != want.task || identity.AttemptID != id || identity.Epoch != want.epoch || !generations[identity.Generation] {
			return errors.New("stream_identity_mismatch")
		}
		if through < want.through {
			return errors.New("stream_watermark_missing")
		}
		present[id] = true
	}
	for id, want := range expected {
		if want.through > 0 && !present[id] {
			return fmt.Errorf("required_stream_missing: %s", id)
		}
	}
	return nil
}
