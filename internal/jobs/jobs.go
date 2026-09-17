// Package jobs defines durable long-running owner operations. A queued job is not
// execution authority; the S4 worker rechecks authority at the domain commit.
package jobs

import (
	"context"
	"encoding/json"
)

type State string

const (
	Queued    State = "queued"
	Running   State = "running"
	Succeeded State = "succeeded"
	Failed    State = "failed"
)

// Job carries a stable intent key; result data is bounded by the implementing worker.
type Job struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	SubjectID  string          `json:"subject_id"`
	State      State           `json:"state"`
	DaemonBoot string          `json:"daemon_boot"`
	CreatedMS  int64           `json:"created_ms"`
	StartedMS  int64           `json:"started_ms"`
	FinishedMS int64           `json:"finished_ms"`
	Result     json.RawMessage `json:"result,omitempty"`
	Error      string          `json:"error"`
}

// Runner admits a job and exposes its durable status; implementations own scheduling.
type Runner interface {
	Submit(ctx context.Context, job Job) (Job, error)
	Get(ctx context.Context, id string) (Job, error)
}
