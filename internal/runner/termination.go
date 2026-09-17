package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
)

var ErrTerminationUnconfirmed = errors.New("termination_unconfirmed")

type TerminationBounds struct {
	Grace   time.Duration
	Timeout time.Duration
}

type TerminationRecord struct {
	Stage        c.Status      `json:"stage"`
	Cancel       p.Message     `json:"cancel"`
	ProcessGroup int           `json:"process_group"`
	Measurement  c.Measurement `json:"measurement"`
	Terminated   *p.Message    `json:"terminated,omitempty"`
}

func terminationDigest(v TerminationRecord) string {
	v.Terminated = nil
	body, _ := json.Marshal(v)
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func reduceTermination(s state, e event) (state, error) {
	v := e.Termination
	if v == nil || s.Assignment == nil || v.ProcessGroup <= 1 || v.Cancel.Kind != "cancel" {
		return s, p.Malformed
	}
	if r := p.CheckSession(v.Cancel, s.Assignment.Identity, s.Session.Version); r != p.OK {
		return s, r
	}
	if v.Cancel.RunnerBoot != s.Boot || v.Cancel.DaemonBoot != s.Session.DaemonBoot {
		return s, p.BootMismatch
	}
	if e.At < s.Last {
		return s, p.DelayedReply
	}
	if previous, ok := s.Messages[v.Cancel.MessageID]; ok && !reflect.DeepEqual(previous, v.Cancel) {
		return s, p.IdentityConflict
	}
	old := s.Termination
	if v.Stage == c.StopRequested {
		if old != nil || v.Terminated != nil || v.Measurement.RequestedAt.IsZero() || !v.Measurement.AcknowledgedAt.IsZero() || v.Measurement.ObservedAt != nil || v.Measurement.AckToObservedNS != nil || v.Measurement.RequestToAckNS != 0 || v.Measurement.Escalated {
			return s, p.Malformed
		}
	} else {
		if old == nil || !reflect.DeepEqual(old.Cancel, v.Cancel) || old.ProcessGroup != v.ProcessGroup || !old.Measurement.RequestedAt.Equal(v.Measurement.RequestedAt) {
			return s, p.IdentityConflict
		}
		switch v.Stage {
		case c.StopAcknowledged:
			if old.Stage != c.StopRequested || v.Terminated != nil || v.Measurement.Validate(false) != nil || v.Measurement.Escalated {
				return s, p.Malformed
			}
		case c.TerminationObserved:
			if old.Stage != c.StopAcknowledged || v.Terminated == nil || v.Measurement.Validate(true) != nil || !old.Measurement.AcknowledgedAt.Equal(v.Measurement.AcknowledgedAt) || old.Measurement.RequestToAckNS != v.Measurement.RequestToAckNS {
				return s, p.Malformed
			}
			m := v.Terminated
			if p.CheckSession(*m, s.Assignment.Identity, s.Session.Version) != p.OK || m.Kind != "terminated" || m.StopID != v.Cancel.StopID || m.RunnerBoot != s.Boot || m.DaemonBoot != s.Session.DaemonBoot || m.ConfirmedProcess != "terminated" || m.RemoteWork != "unknown" || m.EvidenceDigest != terminationDigest(*v) {
				return s, p.IdentityConflict
			}
		default:
			return s, p.Malformed
		}
	}
	s.Stopped = true
	s.Reason = "cancel_requested"
	s.Pending = nil
	s.StopBy = nil
	s.Messages[v.Cancel.MessageID] = v.Cancel
	s.Termination = v
	s.Last = e.At
	return s, nil
}

// TerminateProcessGroup stops an externally supplied, trusted process group. It
// NEVER launches or adopts one as an execution runtime. The trusted supervisor
// must own this group's identity and ensure descendants cannot escape it. Group
// IDs alone are not a containment or PID-reuse defense; see the control README.
//
// Request, durable acknowledgement and observed group disappearance are separate
// journal records. A sent signal or timeout yields no terminated message. Replay
// returns retained evidence without signaling a possibly reused PID. Reopen must
// reconcile; it never resumes a partly completed termination operation.
func (r *Runner) TerminateProcessGroup(session Session, cancel p.Message, group int, bounds TerminationBounds) (TerminationRecord, error) {
	requested := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.session(session); err != nil {
		return TerminationRecord{}, err
	}
	if bounds.Grace <= 0 || bounds.Timeout <= bounds.Grace || bounds.Timeout > 5*time.Second || group <= 1 {
		return TerminationRecord{}, p.Malformed
	}
	if r.state.Assignment == nil || cancel.Kind != "cancel" {
		return TerminationRecord{}, p.Malformed
	}
	if reason := p.CheckSession(cancel, r.state.Assignment.Identity, session.Version); reason != p.OK {
		return TerminationRecord{}, reason
	}
	if cancel.RunnerBoot != r.state.Boot || cancel.DaemonBoot != session.DaemonBoot {
		return TerminationRecord{}, p.BootMismatch
	}
	if err := r.log.Check(); err != nil {
		r.failed = true
		return TerminationRecord{}, err
	}
	if old := r.state.Termination; old != nil {
		if !reflect.DeepEqual(old.Cancel, cancel) || old.ProcessGroup != group {
			return TerminationRecord{}, p.IdentityConflict
		}
		if old.Stage == c.TerminationObserved {
			return clone(*old), nil
		}
		return clone(*old), ErrTerminationUnconfirmed
	}
	v := TerminationRecord{Stage: c.StopRequested, Cancel: cancel, ProcessGroup: group, Measurement: c.Measurement{RequestedAt: requested.UTC()}}
	persist := func() error {
		return r.commit(event{Kind: "termination", At: r.options.MonotonicMS(), Termination: &v})
	}
	if err := persist(); err != nil {
		return TerminationRecord{}, err
	}
	ack := time.Now()
	v.Stage = c.StopAcknowledged
	v.Measurement.AcknowledgedAt = ack.UTC()
	v.Measurement.RequestToAckNS = ack.Sub(requested).Nanoseconds()
	if err := persist(); err != nil {
		return TerminationRecord{}, err
	}
	escalated, err := terminateGroup(group, bounds.Grace, requested.Add(bounds.Timeout))
	if err != nil {
		return clone(v), err
	}
	observed := time.Now()
	wall := observed.UTC()
	elapsed := observed.Sub(ack).Nanoseconds()
	v.Stage = c.TerminationObserved
	v.Measurement.ObservedAt = &wall
	v.Measurement.AckToObservedNS = &elapsed
	v.Measurement.Escalated = escalated
	v.Terminated = &p.Message{Version: p.FencedVersion, MessageID: id(), Kind: "terminated", Identity: cancel.Identity, StopID: cancel.StopID, RunnerBoot: cancel.RunnerBoot, DaemonBoot: cancel.DaemonBoot, ConfirmedProcess: "terminated", RemoteWork: "unknown", EvidenceDigest: terminationDigest(v)}
	if r.beforeTerminationCommit != nil {
		r.beforeTerminationCommit()
	}
	if err := persist(); err != nil {
		return TerminationRecord{}, err
	}
	return clone(v), nil
}
