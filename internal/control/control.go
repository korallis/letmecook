// Package control defines provisional durable stop intent and evidence. These are
// trusted local library inputs, not a second execution protocol or launch authority.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
)

type Kind string

const (
	PauseTask             Kind = "pause_task"
	CancelAttempt         Kind = "cancel_attempt"
	GlobalStop            Kind = "global_stop"
	AuthoritySupersession Kind = "authority_supersession"
)

type Status string

const (
	StopRequested          Status = "stop_requested"
	StopAcknowledged       Status = "stop_acknowledged"
	TerminationObserved    Status = "termination_observed"
	TerminationUnconfirmed Status = "termination_unconfirmed"
	TerminationExpired     Status = "termination_expired"
)

var ErrFenced = errors.New("stop_fenced")

type Request struct {
	ID        string `json:"id"`
	Kind      Kind   `json:"kind"`
	TaskID    string `json:"task_id,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	GrantID   string `json:"grant_id,omitempty"`
	Cause     string `json:"cause"`
}

func (r Request) Validate() error {
	if !p.ValidID(r.ID) {
		return p.Malformed
	}
	switch r.Kind {
	case PauseTask:
		if !p.ValidID(r.TaskID) || r.AttemptID != "" || r.GrantID != "" || r.Cause != "operator" {
			return p.Malformed
		}
	case CancelAttempt:
		if !p.ValidID(r.TaskID) || !p.ValidID(r.AttemptID) || r.GrantID != "" || (r.Cause != "operator" && r.Cause != "lease_expired") {
			return p.Malformed
		}
	case GlobalStop:
		if r.TaskID != "" || r.AttemptID != "" || r.GrantID != "" || r.Cause != "operator" {
			return p.Malformed
		}
	case AuthoritySupersession:
		if !p.ValidID(r.TaskID) || !p.ValidID(r.GrantID) || r.AttemptID != "" || (r.Cause != "superseded" && r.Cause != "expired" && r.Cause != "revoked") {
			return p.Malformed
		}
	default:
		return p.Malformed
	}
	return nil
}

// Stamp is a wall timestamp and a boot-local monotonic-derived elapsed duration.
// ElapsedNS values from different boots must never be subtracted.
type Stamp struct {
	Boot      string    `json:"boot"`
	Wall      time.Time `json:"wall"`
	ElapsedNS int64     `json:"elapsed_ns"`
}

type Acknowledgement struct {
	At             Stamp  `json:"at"`
	RequestToAckNS *int64 `json:"request_to_ack_ns"` // nil if recovered in another boot
}

type Receipt struct {
	Request      Request          `json:"request"`
	Actor        string           `json:"actor"`
	Requested    Stamp            `json:"requested"`
	Acknowledged *Acknowledgement `json:"acknowledged,omitempty"`
}

// Target is also the immutable cancel outbox. Reading it does not send a signal.
type Target struct {
	DispatchID string    `json:"dispatch_id"`
	Cancel     p.Message `json:"cancel"`
}

// Lease is a conservative record of a potentially delivered v2 reply. Recording
// it does not qualify a runtime or authorize sending that reply to a real runner.
type Lease struct {
	DispatchID string    `json:"dispatch_id"`
	Request    p.Message `json:"request"`
	Reply      p.Message `json:"reply"`
	Issued     Stamp     `json:"issued"`
	DeadlineNS int64     `json:"deadline_ns"`
	MarginNS   int64     `json:"margin_ns"`
}

// Measurement belongs to one runner boot. It is persisted separately from intent;
// nil observation is explicitly unconfirmed, even when signals were sent.
type Measurement struct {
	RequestedAt     time.Time  `json:"requested_at"`
	AcknowledgedAt  time.Time  `json:"acknowledged_at"`
	ObservedAt      *time.Time `json:"observed_at,omitempty"`
	RequestToAckNS  int64      `json:"request_to_ack_ns"`
	AckToObservedNS *int64     `json:"ack_to_observed_ns,omitempty"`
	Escalated       bool       `json:"escalated"`
}

func (m Measurement) Validate(observed bool) error {
	if _, err := json.Marshal(m); err != nil {
		return p.Malformed
	}
	if m.RequestedAt.IsZero() || m.AcknowledgedAt.IsZero() || m.RequestToAckNS < 0 || (m.ObservedAt != nil) != observed || (m.AckToObservedNS != nil) != observed {
		return p.Malformed
	}
	if observed && (m.ObservedAt.IsZero() || *m.AckToObservedNS < 0) {
		return p.Malformed
	}
	return nil
}

// Evidence is imported only after the trusted owner verifies the supervisor's
// journal digest, full tree containment and exact identity/boots. No worker claim
// or elapsed timer is sufficient. Remote unknown never releases reservations.
type Evidence struct {
	Terminated  p.Message   `json:"terminated"`
	Measurement Measurement `json:"measurement"`
}

type View struct {
	Receipt          Receipt
	Target           Target
	Status           Status
	ConfirmedProcess string
	RemoteWork       string
	Quarantined      bool
	Evidence         *Evidence
}

// Backend keeps persistence and transaction ownership in internal/store.
type Backend interface {
	RequestStop(context.Context, string, Request) (Receipt, error)
	StopRequests(context.Context, string, int) ([]string, error)
	StopTargets(context.Context, string) ([]Target, error)
	StopStatus(context.Context, string, string) (View, error)
	ObserveTermination(context.Context, string, string, Evidence) error
}
