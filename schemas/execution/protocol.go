// Package execution defines provisional, side-effect-free protocol checks.
// Success means contract consistency, never authority, custody or safe execution.
package execution

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"regexp"
	"unicode/utf8"
)

const Version = "execution-provisional-v1"
const MaxBytes = 8192
const MaxInteger int64 = 9007199254740991

type Refusal string

func (r Refusal) Error() string { return string(r) }

const (
	OK                Refusal = "ok"
	Duplicate         Refusal = "duplicate"
	Malformed         Refusal = "malformed"
	Oversized         Refusal = "oversized"
	UnknownVersion    Refusal = "unknown_version"
	StaleGeneration   Refusal = "stale_generation"
	StaleAttempt      Refusal = "stale_attempt"
	IdentityConflict  Refusal = "identity_conflict"
	RevisionConflict  Refusal = "revision_conflict"
	InvalidTransition Refusal = "invalid_transition"
	NonceMismatch     Refusal = "nonce_mismatch"
	BootMismatch      Refusal = "boot_mismatch"
	DelayedReply      Refusal = "delayed_reply"
	AckNotDurable     Refusal = "ack_not_durable"
)

type Identity struct {
	Generation string `json:"generation"`
	TaskID     string `json:"task_id"`
	AttemptID  string `json:"attempt_id"`
	Epoch      int64  `json:"epoch"`
}
type Route struct {
	RouteRef       string `json:"route_ref"`
	DecisionDigest string `json:"decision_digest"`
	PolicyDigest   string `json:"policy_digest"`
	LimitsProfile  string `json:"limits_profile"`
}
type Manifest struct {
	ManifestID string `json:"manifest_id"`
	SHA256     string `json:"sha256"`
	Bytes      int64  `json:"bytes"`
}
type AttemptState string
type TaskState string

const (
	Assigned           AttemptState = "assigned"
	Starting           AttemptState = "starting"
	Running            AttemptState = "running"
	Stopping           AttemptState = "stopping"
	ResultPending      AttemptState = "result_pending"
	Succeeded          AttemptState = "succeeded"
	Failed             AttemptState = "failed"
	Cancelled          AttemptState = "cancelled"
	Expired            AttemptState = "expired"
	Unknown            AttemptState = "unknown"
	TaskDraft          TaskState    = "draft"
	TaskReady          TaskState    = "ready"
	TaskActive         TaskState    = "active"
	TaskVerifying      TaskState    = "verifying"
	TaskAwaitingReview TaskState    = "awaiting_review"
	TaskAccepted       TaskState    = "accepted"
	TaskReconciling    TaskState    = "reconciling"
	TaskBlocked        TaskState    = "blocked"
	TaskFailed         TaskState    = "failed"
	TaskCancelled      TaskState    = "cancelled"
)

// Message is a closed wire union. Decode and every check enforce kind-specific
// fields; pointers distinguish absent fields from required numeric zero.
// ponytail: six small variants; split payload structs when additional kinds land.
type Message struct {
	Version          string       `json:"version"`
	MessageID        string       `json:"message_id"`
	Identity         Identity     `json:"identity"`
	Kind             string       `json:"kind"`
	AssignmentID     string       `json:"assignment_id,omitempty"`
	InputDigest      string       `json:"input_digest,omitempty"`
	Route            *Route       `json:"route,omitempty"`
	Nonce            string       `json:"nonce,omitempty"`
	RunnerBoot       string       `json:"runner_boot,omitempty"`
	DaemonBoot       string       `json:"daemon_boot,omitempty"`
	SentMS           *int64       `json:"sent_ms,omitempty"`
	ValidityMS       *int64       `json:"validity_ms,omitempty"`
	ExpectedRevision *int64       `json:"expected_revision,omitempty"`
	From             AttemptState `json:"from,omitempty"`
	To               AttemptState `json:"to,omitempty"`
	Manifest         *Manifest    `json:"manifest,omitempty"`
	ReceiptID        string       `json:"receipt_id,omitempty"`
}
type Timing struct {
	RunnerBoot    string `json:"runner_boot"`
	DaemonBoot    string `json:"daemon_boot"`
	ReceivedMS    int64  `json:"received_ms"`
	DriftMS       int64  `json:"drift_ms"`
	TerminationMS int64  `json:"termination_ms"`
	PriorStopByMS *int64 `json:"prior_stop_by_ms"`
	NonceActive   bool   `json:"nonce_active"`
}
type LeaseCheck struct {
	Reason   Refusal `json:"reason"`
	StopByMS *int64  `json:"stop_by_ms,omitempty"`
}
type Receipt struct {
	Identity  Identity `json:"identity"`
	Manifest  Manifest `json:"manifest"`
	ReceiptID string   `json:"receipt_id"`
	Artifacts string   `json:"artifacts"`
	Metadata  string   `json:"metadata"`
}
type Observation struct {
	Desired          string `json:"desired"`
	ConfirmedProcess string `json:"confirmed_process"`
	RemoteWork       string `json:"remote_work"`
	Quarantined      bool   `json:"quarantined"`
}
type RecoveryEvent string

var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
var hash = regexp.MustCompile(`^[0-9a-f]{64}$`)
var routeRef = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var integerToken = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
var edges = map[AttemptState][]AttemptState{
	Assigned: {Starting, Stopping, Unknown}, Starting: {Running, Stopping, Unknown},
	Running: {ResultPending, Stopping, Unknown}, ResultPending: {Succeeded, Failed, Stopping, Unknown},
	Stopping: {Cancelled, Expired, Unknown}, Unknown: {Stopping, Cancelled, Expired, ResultPending},
	Succeeded: {}, Failed: {}, Cancelled: {}, Expired: {},
}

func in[T comparable](v T, values ...T) bool {
	for _, x := range values {
		if v == x {
			return true
		}
	}
	return false
}
func integer(v int64, min, max int64) bool { return v >= min && v <= max }
func validIdentity(v Identity) bool {
	return uuid.MatchString(v.Generation) && uuid.MatchString(v.TaskID) && uuid.MatchString(v.AttemptID) && integer(v.Epoch, 1, MaxInteger)
}
func validManifest(v Manifest) bool {
	return uuid.MatchString(v.ManifestID) && hash.MatchString(v.SHA256) && integer(v.Bytes, 1, 1048576)
}

// Decode bounds input before parsing and rejects duplicate decoded keys, invalid
// UTF-8, excessive nesting, unknown fields and trailing values.
func Decode(data []byte) (Message, error) {
	if len(data) > MaxBytes {
		return Message{}, Oversized
	}
	if !utf8.Valid(data) {
		return Message{}, Malformed
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	v, err := value(d, 0)
	if err != nil {
		return Message{}, Malformed
	}
	if _, err = d.Token(); err != io.EOF {
		return Message{}, Malformed
	}
	m, ok := v.(map[string]any)
	if !ok {
		return Message{}, Malformed
	}
	if version, ok := m["version"].(string); ok && version != Version {
		return Message{}, UnknownVersion
	}
	extras := map[string][]string{
		"assign":        {"assignment_id", "input_digest", "route"},
		"lease_request": {"nonce", "runner_boot", "daemon_boot", "sent_ms"},
		"lease_reply":   {"nonce", "runner_boot", "daemon_boot", "validity_ms"},
		"transition":    {"expected_revision", "from", "to"},
		"result":        {"manifest"}, "result_ack": {"manifest", "receipt_id"},
	}
	kind, ok := m["kind"].(string)
	extra, known := extras[kind]
	if !ok || !known || m["version"] != Version || !fields(m, append([]string{"version", "message_id", "identity", "kind"}, extra...)...) {
		return Message{}, Malformed
	}
	if !matches(m["message_id"], uuid) || !identityShape(m["identity"]) {
		return Message{}, Malformed
	}
	valid := true
	switch kind {
	case "assign":
		r, ok := m["route"].(map[string]any)
		valid = matches(m["assignment_id"], uuid) && matches(m["input_digest"], hash) && ok &&
			fields(r, "route_ref", "decision_digest", "policy_digest", "limits_profile") && matches(r["route_ref"], routeRef) &&
			matches(r["decision_digest"], hash) && matches(r["policy_digest"], hash) && in(r["limits_profile"], any("strict-provider-output-v1"), any("native-subscription-local-v1"))
	case "lease_request", "lease_reply":
		valid = matches(m["nonce"], uuid) && matches(m["runner_boot"], uuid) && matches(m["daemon_boot"], uuid)
		if kind == "lease_request" {
			valid = valid && number(m["sent_ms"], 0, MaxInteger)
		} else {
			valid = valid && number(m["validity_ms"], 1, 30000)
		}
	case "transition":
		from, fok := m["from"].(string)
		to, tok := m["to"].(string)
		_, f := edges[AttemptState(from)]
		_, t := edges[AttemptState(to)]
		valid = fok && tok && f && t && number(m["expected_revision"], 1, MaxInteger)
	case "result", "result_ack":
		valid = manifestShape(m["manifest"])
		if kind == "result_ack" {
			valid = valid && matches(m["receipt_id"], uuid)
		}
	}
	if !valid {
		return Message{}, Malformed
	}
	// Decode only after validating exact keys and canonical integer tokens.
	normalized, err := json.Marshal(v)
	if err != nil {
		return Message{}, Malformed
	}
	var result Message
	if json.Unmarshal(normalized, &result) != nil {
		return Message{}, Malformed
	}
	return result, nil
}
func fields(m map[string]any, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
func matches(v any, pattern *regexp.Regexp) bool {
	s, ok := v.(string)
	return ok && pattern.MatchString(s)
}
func number(v any, min, max int64) bool {
	n, ok := v.(json.Number)
	if !ok {
		return false
	}
	i, err := n.Int64()
	return err == nil && integer(i, min, max)
}
func identityShape(v any) bool {
	m, ok := v.(map[string]any)
	return ok && fields(m, "generation", "task_id", "attempt_id", "epoch") && matches(m["generation"], uuid) && matches(m["task_id"], uuid) && matches(m["attempt_id"], uuid) && number(m["epoch"], 1, MaxInteger)
}
func manifestShape(v any) bool {
	m, ok := v.(map[string]any)
	return ok && fields(m, "manifest_id", "sha256", "bytes") && matches(m["manifest_id"], uuid) && matches(m["sha256"], hash) && number(m["bytes"], 1, 1048576)
}
func value(d *json.Decoder, depth int) (any, error) {
	if depth > 32 {
		return nil, Malformed
	}
	token, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		m := map[string]any{}
		for d.More() {
			t, err := d.Token()
			if err != nil {
				return nil, err
			}
			key, ok := t.(string)
			if !ok || in(key, "__proto__", "constructor", "prototype") {
				return nil, Malformed
			}
			if _, exists := m[key]; exists {
				return nil, Malformed
			}
			child, err := value(d, depth+1)
			if err != nil {
				return nil, err
			}
			m[key] = child
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return nil, Malformed
		}
		return m, nil
	case json.Delim('['):
		a := []any{}
		for d.More() {
			child, err := value(d, depth+1)
			if err != nil {
				return nil, err
			}
			a = append(a, child)
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return nil, Malformed
		}
		return a, nil
	default:
		if n, ok := token.(json.Number); ok {
			if !integerToken.MatchString(string(n)) {
				return nil, Malformed
			}
		}
		if _, delim := token.(json.Delim); delim {
			return nil, Malformed
		}
		return token, nil
	}
}
func validate(m Message) Refusal {
	if m.Version == "" {
		return Malformed
	}
	data, err := json.Marshal(m)
	if err != nil {
		return Malformed
	}
	_, err = Decode(data)
	if err != nil {
		return err.(Refusal)
	}
	return OK
}
func current(m Message, c Identity) Refusal {
	if m.Identity.Generation != c.Generation {
		return StaleGeneration
	}
	if m.Identity != c {
		return StaleAttempt
	}
	return OK
}
func CheckCurrent(m Message, c Identity) Refusal {
	if r := validate(m); r != OK {
		return r
	}
	if !validIdentity(c) {
		return Malformed
	}
	return current(m, c)
}
func CheckReplay(m, previous Message, c Identity) Refusal {
	if r := CheckCurrent(m, c); r != OK {
		return r
	}
	if r := validate(previous); r != OK {
		return r
	}
	if m.MessageID == previous.MessageID {
		if reflect.DeepEqual(m, previous) {
			return Duplicate
		}
		return IdentityConflict
	}
	if m.Kind == "assign" && previous.Kind == "assign" && m.AssignmentID == previous.AssignmentID {
		m.MessageID = previous.MessageID
		if reflect.DeepEqual(m, previous) {
			return Duplicate
		}
		return IdentityConflict
	}
	return OK
}
func CheckTransition(m Message, c Identity, state AttemptState, revision int64) Refusal {
	if r := validate(m); r != OK {
		return r
	}
	_, known := edges[state]
	if !validIdentity(c) || m.Kind != "transition" || !known || !integer(revision, 1, MaxInteger) {
		return Malformed
	}
	if r := current(m, c); r != OK {
		return r
	}
	if *m.ExpectedRevision != revision || revision == MaxInteger {
		return RevisionConflict
	}
	if m.From != state || !in(m.To, edges[state]...) {
		return InvalidTransition
	}
	return OK
}

// CheckLease computes a conservative runner-local cutoff; it issues no lease.
func CheckLease(request, reply Message, c Identity, t Timing) LeaseCheck {
	refuse := func(r Refusal) LeaseCheck { return LeaseCheck{Reason: r} }
	for _, m := range []Message{request, reply} {
		if r := validate(m); r != OK {
			return refuse(r)
		}
	}
	if !validIdentity(c) || !uuid.MatchString(t.RunnerBoot) || !uuid.MatchString(t.DaemonBoot) || request.Kind != "lease_request" || reply.Kind != "lease_reply" ||
		!integer(t.ReceivedMS, 0, MaxInteger) || !integer(t.DriftMS, 1, MaxInteger) || !integer(t.TerminationMS, 1, MaxInteger) ||
		t.PriorStopByMS != nil && !integer(*t.PriorStopByMS, 0, MaxInteger) {
		return refuse(Malformed)
	}
	for _, m := range []Message{request, reply} {
		if r := current(m, c); r != OK {
			return refuse(r)
		}
	}
	if request.RunnerBoot != t.RunnerBoot || reply.RunnerBoot != t.RunnerBoot || request.DaemonBoot != t.DaemonBoot || reply.DaemonBoot != t.DaemonBoot {
		return refuse(BootMismatch)
	}
	if !t.NonceActive || request.Nonce != reply.Nonce {
		return refuse(NonceMismatch)
	}
	if t.DriftMS+t.TerminationMS >= *reply.ValidityMS || *request.SentMS > MaxInteger-*reply.ValidityMS {
		return refuse(Malformed)
	}
	stop := *request.SentMS + *reply.ValidityMS - t.DriftMS - t.TerminationMS
	if t.ReceivedMS < *request.SentMS || t.ReceivedMS >= stop || t.PriorStopByMS != nil && (*request.SentMS >= *t.PriorStopByMS || t.ReceivedMS >= *t.PriorStopByMS) {
		return refuse(DelayedReply)
	}
	return LeaseCheck{Reason: OK, StopByMS: &stop}
}

// CheckAck checks trusted-owner receipt claims, not actual artifact durability.
func CheckAck(result, ack Message, c Identity, receipt Receipt) Refusal {
	for _, m := range []Message{result, ack} {
		if r := validate(m); r != OK {
			return r
		}
	}
	if !validIdentity(c) || result.Kind != "result" || ack.Kind != "result_ack" || !validIdentity(receipt.Identity) || !validManifest(receipt.Manifest) ||
		!uuid.MatchString(receipt.ReceiptID) || !in(receipt.Artifacts, "unknown", "verified_durable") || !in(receipt.Metadata, "event_only", "manifest_and_result_committed") {
		return Malformed
	}
	for _, m := range []Message{result, ack} {
		if r := current(m, c); r != OK {
			return r
		}
	}
	if *result.Manifest != *ack.Manifest {
		return IdentityConflict
	}
	if receipt.Identity != c || receipt.Manifest != *result.Manifest || receipt.ReceiptID != ack.ReceiptID || receipt.Artifacts != "verified_durable" || receipt.Metadata != "manifest_and_result_committed" {
		return AckNotDurable
	}
	return OK
}

// Observe projects uncertainty without returning execution or retry permission.
func Observe(state Observation, event RecoveryEvent) (Observation, error) {
	if !in(state.Desired, "run", "stop") || !in(state.ConfirmedProcess, "not_started", "running", "terminated", "unknown") || !in(state.RemoteWork, "quiescent", "unknown") ||
		!in(event, "partition", "daemon_restart", "runner_restart", "lease_expired", "stop_requested", "process_terminated", "remote_quiescent") {
		return Observation{}, Malformed
	}
	switch event {
	case "process_terminated":
		state.ConfirmedProcess = "terminated"
	case "remote_quiescent":
		state.RemoteWork = "quiescent"
	default:
		state.Desired = "stop"
		if event != "stop_requested" {
			state.ConfirmedProcess = "unknown"
			state.RemoteWork = "unknown"
			state.Quarantined = true
		}
	}
	return state, nil
}
