package opencode

import (
	"context"

	"github.com/korallis/letmecook/internal/harness"
)

// Release forgets a run only after process exit and both pipe readers finish.
// through is the exact final harness Event.Sequence (not a spool-record index)
// that the trusted caller has durably spooled and finalized/acknowledged. This
// method verifies the watermark, not custody: it grants no execution, release or
// finalization authority. Failure preserves every event and the probe gate.
func (a *Adapter) Release(ctx context.Context, h harness.RunHandle, through int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if through <= 0 {
		return ErrRelease
	}
	a.mu.Lock()
	r := a.runs[h.ID]
	a.mu.Unlock()
	if r == nil {
		return ErrHandle
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-r.done:
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.runs[h.ID] != r {
		return ErrHandle
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !r.ended || through != int64(len(r.events)) {
		return ErrRelease
	}
	last := r.events[len(r.events)-1]
	if last.Kind != harness.Completed && last.Kind != harness.Failed {
		return ErrRelease
	}
	delete(a.runs, h.ID)
	// Old streams may still reference r. Invalidate them and clear their retained
	// storage too; deleting the map entry alone would not release this memory.
	r.released = true
	clear(r.events)
	r.events = nil
	r.completion = nil
	r.token = ""
	r.cmd = nil
	return nil
}
