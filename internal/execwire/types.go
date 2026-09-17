// Package execwire defines the bounded provisional execution-channel envelopes.
// These are data, not authority; authenticated store methods validate every action.
// Legacy nullable budget/timing fields (provider_cost_micros, prior_stop_by_ms,
// request_to_ack_ns) retain their original JSON; all other explicit nulls reject.
// It must not import store: store consumes these types at its transaction boundary.
package execwire

import (
	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/runstream"
	sc "github.com/korallis/letmecook/internal/scheduler"
	p "github.com/korallis/letmecook/schemas/execution"
)

const Version = "execution-channel-provisional-v1"
const MaxBytes = 64 * 1024

// Journal summarizes retained recovery evidence, never a request to resume work.
type Journal struct {
	DispatchID    string         `json:"dispatch_id"`
	Identity      p.Identity     `json:"identity"`
	RunnerBoot    string         `json:"runner_boot"`
	DaemonBoot    string         `json:"daemon_boot"`
	State         p.AttemptState `json:"state"`
	ReceiptID     string         `json:"receipt_id,omitempty"`
	StreamThrough int64          `json:"stream_through"`
	Corrupt       bool           `json:"corrupt"`
}
type Hello struct {
	Version             string    `json:"version"`
	MessageID           string    `json:"message_id"`
	RunnerBoot          string    `json:"runner_boot"`
	EligibilityID       string    `json:"eligibility_id"`
	EligibilityRevision int64     `json:"eligibility_revision"`
	PolicyDigest        string    `json:"policy_digest"`
	Journals            []Journal `json:"journals"`
}
type Session struct {
	SessionID         string `json:"session_id"`
	Generation        string `json:"generation"`
	DaemonBoot        string `json:"daemon_boot"`
	DaemonFingerprint string `json:"daemon_fingerprint"`
	RunnerID          string `json:"runner_id"`
	Mode              string `json:"mode"`
	DriftMS           int64  `json:"drift_ms"`
	TerminationMS     int64  `json:"termination_ms"`
	LeaseValidityMS   int64  `json:"lease_validity_ms"`
	RenewEveryMS      int64  `json:"renew_every_ms"`
	Paused            bool   `json:"paused"`
}

// Head identifies finalized custody, not merely uploaded candidate content.
type Head struct {
	TaskID      string `json:"task_id"`
	Generation  string `json:"generation"`
	AttemptID   string `json:"attempt_id"`
	Epoch       int64  `json:"epoch"`
	ManifestID  string `json:"manifest_id"`
	ReceiptID   string `json:"receipt_id"`
	FinalizedMS int64  `json:"finalized_ms"`
}
type State struct {
	AttemptState p.AttemptState `json:"attempt_state"`
	Revision     int64          `json:"revision"`
	Acknowledged bool           `json:"acknowledged"`
	Released     bool           `json:"released"`
	LastLease    *p.Message     `json:"last_lease,omitempty"`
	Stream       StreamAck      `json:"stream"`
	ReceiptID    string         `json:"receipt_id"`
	Head         *Head          `json:"head,omitempty"`
	StopTargets  []c.Target     `json:"stop_targets"`
	Paused       bool           `json:"paused"`
}

// DispatchRequest and Dispatch mirror store's immutable JSON without a package
// cycle. S1 converts at the route boundary; these do not replace store authority.
type DispatchRequest struct {
	ID        string      `json:"id"`
	Request   g.Request   `json:"request"`
	Decision  sc.Decision `json:"decision"`
	Allowance g.Budgets   `json:"allowance"`
}
type Dispatch struct {
	DispatchRequest
	Facts        sc.Eligibility `json:"facts"`
	Assignment   p.Message      `json:"assignment"`
	Acknowledged bool           `json:"acknowledged"`
	Released     bool           `json:"released"`
}
type Inbox struct {
	Assignments []Dispatch  `json:"assignments"`
	Cancels     []p.Message `json:"cancels"`
	Paused      bool        `json:"paused"`
	PollAfterMS int64       `json:"poll_after_ms"`
}

// Evidence binds a runtime observation to the proposal. Required fields depend
// on Kind and are checked by the store, never inferred from zero values here.
// Evidence mirrors store.RuntimeEvidence field for field. Every numeric and
// boolean field is always emitted, including zero values, so a receiver can
// distinguish observed zero evidence from absent evidence.
type Evidence struct {
	Kind           string `json:"kind"`
	Workspace      string `json:"workspace,omitempty"`
	BoundaryPort   int    `json:"boundary_port"`
	GuardianPID    int    `json:"guardian_pid"`
	Nonce          string `json:"nonce,omitempty"`
	PID            int    `json:"pid"`
	PGID           int    `json:"pgid"`
	StartUnixNS    int64  `json:"start_unix_ns"`
	Code           int    `json:"code"`
	PGIDEmpty      bool   `json:"pgid_empty"`
	ObservedUnixNS int64  `json:"observed_unix_ns"`
	StreamThrough  int64  `json:"stream_through"`
	// Cause names a runner-originated stop (one of LocalStopCauses) on a
	// stopping proposal so the daemon derives LocalStopID for the latch.
	Cause string `json:"cause,omitempty"`
}
type MessageEnvelope struct {
	Version     string           `json:"version"`
	MessageID   string           `json:"message_id"`
	DispatchID  string           `json:"dispatch_id"`
	Message     p.Message        `json:"message"`
	Evidence    *Evidence        `json:"evidence,omitempty"`
	Boundary    *inference.State `json:"boundary,omitempty"`
	Measurement *c.Measurement   `json:"measurement,omitempty"`
}
type LeaseEnvelope struct {
	Version    string    `json:"version"`
	MessageID  string    `json:"message_id"`
	DispatchID string    `json:"dispatch_id"`
	Request    p.Message `json:"request"`
}
type StreamBatch struct {
	Version string             `json:"version"`
	Records []runstream.Record `json:"records"`
}
type StreamAck struct {
	Through  int64 `json:"through"`
	Expected int64 `json:"expected"`
	Bytes    int64 `json:"bytes"`
}
type UploadBegin struct {
	Version        string    `json:"version"`
	MessageID      string    `json:"message_id"`
	Result         p.Message `json:"result"`
	ManifestBase64 string    `json:"manifest_base64"`
}
type MissingBlob struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type UploadSession struct {
	UploadID     string        `json:"upload_id"`
	Missing      []MissingBlob `json:"missing"`
	BytesAllowed int64         `json:"bytes_allowed"`
}
type CommitReply struct {
	Ack         p.Message `json:"ack"`
	Receipt     p.Receipt `json:"receipt"`
	Quarantined bool      `json:"quarantined"`
}
type StreamCompletion struct {
	Through int64  `json:"through"`
	Digest  string `json:"digest"`
}
type Exit struct {
	Code           int   `json:"code"`
	PGID           int   `json:"pgid"`
	ObservedUnixNS int64 `json:"observed_unix_ns"`
}
type Completion struct {
	Version   string           `json:"version"`
	MessageID string           `json:"message_id"`
	ReceiptID string           `json:"receipt_id"`
	Stream    StreamCompletion `json:"stream"`
	Exit      Exit             `json:"exit"`
	Boundary  inference.State  `json:"boundary"`
}
type Usage struct {
	Version   string              `json:"version"`
	MessageID string              `json:"message_id"`
	Identity  p.Identity          `json:"identity"`
	Receipts  []inference.Receipt `json:"receipts"`
}
type ErrorBody struct {
	Version string `json:"version"`
	Error   string `json:"error"`
	Detail  string `json:"detail"`
}
