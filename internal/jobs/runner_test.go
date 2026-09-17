package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/store"
	"github.com/korallis/letmecook/internal/workflow"
)

func jobStore(t *testing.T) *store.Store {
	t.Helper()
	root := t.TempDir()
	s, err := store.Open(context.Background(), filepath.Join(root, "state"), filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func awaitJob(t *testing.T, w *Worker, id string, state State) Job {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		j, err := w.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == state {
			return j
		}
		select {
		case <-deadline:
			t.Fatalf("job stays %s want %s", j.State, state)
		case <-time.After(time.Millisecond):
		}
	}
}
func TestDurableWorkerReplayRestartAndUnknownKind(t *testing.T) {
	s := jobStore(t)
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	w, err := NewWorker(ctx, s, map[string]Handler{"block": func(ctx context.Context, j Job) (json.RawMessage, error) {
		close(entered)
		select {
		case <-release:
			return json.RawMessage(`{"saved":true}`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	input := Job{ID: workflow.IntentID("jobs", "block"), Kind: "block", SubjectID: "task", Result: json.RawMessage(`{"input":true}`)}
	first, err := w.Submit(ctx, input)
	if err != nil || first.State != Queued {
		t.Fatal(first, err)
	}
	<-entered
	again, err := w.Submit(ctx, input)
	if err != nil || again.State != Running {
		t.Fatal(again, err)
	}
	changed := input
	changed.Result = json.RawMessage(`{"input":false}`)
	_, err = w.Submit(ctx, changed)
	var refusal *g.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "identity_conflict" {
		t.Fatal(err)
	}
	queued := Job{ID: workflow.IntentID("jobs", "unknown"), Kind: "absent"}
	if _, err = w.Submit(ctx, queued); err != nil {
		t.Fatal(err)
	}
	// Closing cancels the process, not its durable evidence. Next startup owns recovery.
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := s.Job(ctx, input.ID)
	if err != nil || retained.State != "running" {
		t.Fatal(retained, err)
	}
	restarted, err := NewWorker(ctx, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	failed := awaitJob(t, restarted, input.ID, Failed)
	if failed.Error != "daemon_restart" {
		t.Fatal(failed)
	}
	unknown := awaitJob(t, restarted, queued.ID, Failed)
	if unknown.Error != "unknown_job_kind" {
		t.Fatal(unknown)
	}
	replay, err := restarted.Submit(ctx, input)
	if err != nil || replay.State != Failed {
		t.Fatal(replay, err)
	}
}
func TestDurableWorkerNeverAcknowledgesFailedTerminalCommit(t *testing.T) {
	s := jobStore(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	w, err := NewWorker(context.Background(), s, map[string]Handler{"saved": func(ctx context.Context, j Job) (json.RawMessage, error) {
		close(entered)
		<-release
		return json.RawMessage(`{"saved":true}`), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	id := workflow.IntentID("jobs", "failed-commit")
	if _, err = w.Submit(context.Background(), Job{ID: id, Kind: "saved"}); err != nil {
		t.Fatal(err)
	}
	<-entered
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	// Store closure makes durable success impossible; the read must fail, not use a cache.
	if _, err = w.Get(context.Background(), id); err == nil {
		t.Fatal("undurable success exposed")
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
}
