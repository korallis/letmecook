// Package reconcile defines evidence-led startup and retry planning. Stubs never
// release reservations, declare process death, or authorize retry.
package reconcile

import (
	"context"
	"errors"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/store"
	"time"
)

var ErrNotImplemented = errors.New("reconcile_not_implemented")

type Deps struct {
	Store     *store.Store
	Now       func() time.Time
	AutoRetry bool
}
type Entry struct {
	AttemptID      string `json:"attempt_id"`
	Classification string `json:"classification"`
	ActionRequired bool   `json:"action_required"`
	Detail         string `json:"detail"`
}
type Report struct {
	ID         string  `json:"id"`
	DaemonBoot string  `json:"daemon_boot"`
	CreatedMS  int64   `json:"created_ms"`
	Entries    []Entry `json:"entries"`
}

// Reader exposes a persisted report without granting mutation authority.
type Reader interface {
	Read(ctx context.Context) (Report, error)
}

func Startup(ctx context.Context, d Deps) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	return Report{}, ErrNotImplemented
}
func OnHello(ctx context.Context, d Deps, runnerID string, h execwire.Hello) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	return Report{}, ErrNotImplemented
}
func Sweep(ctx context.Context, d Deps) (Report, error) {
	if d.Store == nil {
		return Report{}, nil
	}
	return Report{}, ErrNotImplemented
}
func PlanRetry(ctx context.Context, d Deps, taskID string) (store.DispatchRequest, error) {
	if d.Store == nil {
		return store.DispatchRequest{}, nil
	}
	return store.DispatchRequest{}, ErrNotImplemented
}
