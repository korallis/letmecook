package store

// Finalization seam (docs/decisions/0002 §4, POST /x/v1/attempts/{id}/finalize).
// S1 (#115) replaces the body in this file only; until then it returns
// ErrNotImplemented. Finalization is evidence-gated: a runner success flag is
// never enough, and nothing reruns after a crash between custody and finalize.

import "context"

// ExitRecord is the observed process exit for the attempt's job process group.
type ExitRecord struct {
	Code           int   `json:"code"`
	PGID           int   `json:"pgid"`
	ObservedUnixNS int64 `json:"observed_unix_ns"`
}

// Completion is the finalize body: the custody receipt, the stream watermark the
// runner claims, the exit observation and the boundary's terminal state.
type Completion struct {
	Version   string          `json:"version"`
	MessageID string          `json:"message_id"`
	ReceiptID string          `json:"receipt_id"`
	Stream    StreamWatermark `json:"stream"`
	Exit      ExitRecord      `json:"exit"`
	Boundary  BoundaryState   `json:"boundary"`
}

// FinalizeReply reports the terminal outcome (succeeded|failed) and that the
// reservation was released in the same transaction.
type FinalizeReply struct {
	Outcome  string `json:"outcome"`
	Released bool   `json:"released"`
}

// FinalizeAttempt verifies a non-quarantined current-generation receipt, a sink
// watermark at least the claimed one with an equal digest, an exit observation in
// runtime_observations, boundary.Quiescent, no stop latch, a live grant and
// current boots; incomplete evidence is reconciliation_required and a latched
// stop is stop_latched. Durable point: one committed transaction holding the CAS
// result_pending -> succeeded|failed from the manifest outcome, the event
// dispatchID(receipt_id, "terminal"), the dispatch_releases row (actor: runner
// principal), tasks.state awaiting_review|reconciling and, for succeeded, the
// artifact_result_heads upsert. Replay by message_id returns the same reply.
func (s *Store) FinalizeAttempt(ctx context.Context, fingerprint, session, attemptID string, completion Completion) (FinalizeReply, error) {
	return FinalizeReply{}, ErrNotImplemented
}
