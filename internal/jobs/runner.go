package jobs

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/store"
)

// Handler receives durable input. It must repeat owner authentication at each
// domain commit. Work outlives the HTTP request, never the worker's lifetime.
type Handler func(context.Context, Job) (json.RawMessage, error)
type payload struct {
	Request  json.RawMessage `json:"request"`
	Response json.RawMessage `json:"response,omitempty"`
}
type Worker struct {
	store    *store.Store
	handlers map[string]Handler
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	pending  []string
	wake     chan struct{}
	done     chan struct{}
	boot     string
}

// NewWorker must be constructed once per daemon, before listeners. Interrupted
// running jobs fail closed; queued jobs retain their original typed request.
func NewWorker(ctx context.Context, s *store.Store, handlers map[string]Handler) (*Worker, error) {
	if s == nil {
		return nil, fmt.Errorf("job store required")
	}
	status, err := s.Status(ctx)
	if err != nil {
		return nil, err
	}
	retained, err := s.RecoverJobs(ctx)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(ctx)
	w := &Worker{store: s, handlers: map[string]Handler{}, ctx: lifetime, cancel: cancel, wake: make(chan struct{}, 1), done: make(chan struct{}), boot: status.DaemonBoot}
	for key, h := range handlers {
		if key == "" || h == nil {
			cancel()
			return nil, fmt.Errorf("invalid job handler")
		}
		w.handlers[key] = h
	}
	for _, j := range retained {
		w.pending = append(w.pending, j.ID)
	}
	go w.run()
	w.signal()
	return w, nil
}
func (w *Worker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}
func (w *Worker) Close() error { w.cancel(); <-w.done; return nil }
func decode(j store.Job) (Job, payload, error) {
	var p payload
	if err := json.Unmarshal([]byte(j.Result), &p); err != nil || !json.Valid(p.Request) {
		return Job{}, p, fmt.Errorf("invalid durable job input")
	}
	return Job{ID: j.ID, Kind: j.Kind, SubjectID: j.SubjectID, State: State(j.State), DaemonBoot: j.DaemonBoot, CreatedMS: j.CreatedMS, StartedMS: j.StartedMS, FinishedMS: j.FinishedMS, Result: p.Response, Error: j.Error}, p, nil
}
func (w *Worker) Get(ctx context.Context, id string) (Job, error) {
	j, err := w.store.Job(ctx, id)
	if err != nil {
		return Job{}, err
	}
	out, _, err := decode(j)
	return out, err
}
func (w *Worker) Submit(ctx context.Context, j Job) (Job, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ctx.Err() != nil {
		return Job{}, g.Deny("store_unavailable", "jobs")
	}
	if len(j.Result) == 0 {
		j.Result = json.RawMessage(`{}`)
	}
	if !json.Valid(j.Result) || len(j.Result) > 60000 {
		return Job{}, g.Deny("malformed", "job_input")
	}
	// Canonicalize whitespace for caller retries, without interpreting the request.
	var compact bytes.Buffer
	if err := json.Compact(&compact, j.Result); err != nil {
		return Job{}, err
	}
	input := compact.Bytes()
	old, err := w.store.Job(ctx, j.ID)
	if err == nil {
		out, p, err := decode(old)
		if err != nil {
			return Job{}, err
		}
		if old.Kind != j.Kind || old.SubjectID != j.SubjectID || !bytes.Equal(p.Request, input) {
			return Job{}, g.Deny("identity_conflict", "job")
		}
		return out, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Job{}, err
	}
	if len(w.pending) >= 128 {
		return Job{}, g.Deny("busy", "jobs")
	}
	raw, _ := json.Marshal(payload{Request: input})
	saved, err := w.store.PutJob(ctx, store.Job{ID: j.ID, Kind: j.Kind, SubjectID: j.SubjectID, State: string(Queued), DaemonBoot: w.boot, CreatedMS: time.Now().UnixMilli(), Result: string(raw)})
	if err != nil {
		return Job{}, err
	}
	w.pending = append(w.pending, j.ID)
	w.signal()
	out, _, err := decode(saved)
	return out, err
}
func (w *Worker) run() {
	defer close(w.done)
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.wake:
		}
		for {
			w.mu.Lock()
			if len(w.pending) == 0 {
				w.mu.Unlock()
				break
			}
			id := w.pending[0]
			w.pending = w.pending[1:]
			w.mu.Unlock()
			if w.ctx.Err() != nil {
				return
			}
			w.execute(id)
		}
	}
}
func (w *Worker) execute(id string) {
	saved, err := w.store.Job(w.ctx, id)
	if err != nil || saved.State != string(Queued) {
		return
	}
	job, p, err := decode(saved)
	if err != nil {
		return
	}
	saved.State = string(Running)
	saved.StartedMS = time.Now().UnixMilli()
	saved.DaemonBoot = w.boot
	if _, err = w.store.PutJob(w.ctx, saved); err != nil {
		return
	}
	h := w.handlers[job.Kind]
	var result json.RawMessage
	if h == nil {
		err = fmt.Errorf("unknown_job_kind")
	} else {
		ctx, cancel := context.WithTimeout(w.ctx, 30*time.Minute)
		// A panicking handler is a failed job, never an acknowledgement of success.
		func() {
			defer func() {
				if recover() != nil {
					err = fmt.Errorf("job_handler_panic")
				}
			}()
			job.Result = p.Request
			result, err = h(ctx, job)
		}()
		cancel()
	}
	if w.ctx.Err() != nil {
		return
	} // running survives shutdown and is failed on restart
	saved.FinishedMS = time.Now().UnixMilli()
	saved.State = string(Succeeded)
	if err != nil {
		saved.State = string(Failed)
		saved.Error = jobError(err)
	} else if !json.Valid(result) {
		saved.State = string(Failed)
		saved.Error = "invalid_job_result"
	} else {
		p.Response = result
	}
	raw, marshalErr := json.Marshal(p)
	if marshalErr != nil || len(raw) > 65536 {
		saved.State = string(Failed)
		saved.Error = "oversized_job_result"
		p.Response = nil
		raw, _ = json.Marshal(p)
	}
	saved.Result = string(raw)
	// If this commit fails, the durable running row is intentionally left for
	// startup recovery. Get must never report an uncommitted terminal outcome.
	_, _ = w.store.PutJob(w.ctx, saved)
}
func jobError(err error) string {
	var deny *g.Refusal
	if errors.As(err, &deny) {
		return deny.Code + ":" + deny.Field
	}
	s := err.Error()
	if len(s) > 4096 {
		return "job_failed"
	}
	return s
}
