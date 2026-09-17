// Package harness defines the supervisor-owned adapter seam. Adapters receive a
// launcher and a scoped boundary, never provider credentials or launch authority.
package harness

import (
	"context"
	"encoding/json"
	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/isolation"
	p "github.com/korallis/letmecook/schemas/execution"
	"time"
)

const (
	Started          = "started"
	Activity         = "activity"
	ToolRequested    = "tool_requested"
	ApprovalRequired = "approval_required"
	UsageKind        = "usage"
	Checkpoint       = "checkpoint"
	Completed        = "completed"
	Failed           = "failed"
	MaxRawBytes      = 48 * 1024
)

// Harness normalizes a pinned adapter. The supervisor spools events before acting.
type Harness interface {
	Describe(ctx context.Context) (Descriptor, error)
	Probe(ctx context.Context, req ProbeRequest) (ProbeResult, error)
	Start(ctx context.Context, req RunRequest) (RunHandle, error)
	Events(ctx context.Context, h RunHandle, after int64) (EventStream, error)
	Cancel(ctx context.Context, h RunHandle) (CancelResult, error)
}

type Descriptor struct {
	Name, Version, BinaryDigest                                       string
	Protocols                                                         []string
	StructuredOutput, Approval, Resume, Sandbox, ModelSettings, Usage bool
}
type RunRequest struct {
	Identity                                                   p.Identity
	AssignmentID, InputDigest, BaseCommit, ContextDigest, Role string
	Workspace                                                  Workspace
	Route                                                      g.Route
	Brief                                                      string
	Settings                                                   json.RawMessage
	Limits                                                     Limits
	Boundary                                                   BoundaryHandle
	Launcher                                                   isolation.Launcher
}

// Workspace is shared with isolation so the launcher and adapter agree on roots.
type Workspace = isolation.Workspace
type Limits struct{ AttemptMS, IdleMS, FirstOutputMS, MaxStdoutBytes int64 }
type BoundaryHandle struct {
	URL, Token string
	Models     []string
	Protocol   string
}

// Usage is an adapter observation, not a billing ledger or authoritative receipt.
type Usage struct {
	PromptTokens, CompletionTokens, BytesIn, BytesOut int64
	Source                                            string
}
type Event struct {
	Sequence int64
	Kind     string
	At       time.Time
	Summary  string
	Usage    *Usage
	Raw      json.RawMessage
	Native   bool
}

// EventStream ends with io.EOF after a completed or failed event.
type EventStream interface {
	Next(ctx context.Context) (Event, error)
}
type RunHandle struct {
	ID                     string
	PID, PGID, GuardianPID int
	StartUnixNS            int64
	SessionID              string
}

// ProbeRequest requests compatibility and configuration-isolation observations.
type ProbeRequest struct {
	Workspace Workspace
	Route     g.Route
	Boundary  BoundaryHandle
	Launcher  isolation.Launcher
	Settings  json.RawMessage
}
type ProbeResult struct {
	Descriptor                 Descriptor
	Compatible, ConfigIsolated bool
	Limitations                []string
}

// CancelResult is an observation only; unknown containment must remain unknown.
type CancelResult struct {
	ConfirmedProcess, RemoteWork string
	ObservedUnixNS               int64
	Escalated                    bool
}
