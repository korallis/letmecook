package httpapi

// Execution route set (docs/decisions/0002 §4): the runner-mTLS /x/v1 routes
// mounted only on the execution listener. Every body is closed JSON decoded by
// execwire.Decode, every route re-authenticates the runner through the route
// seam and, except POST /x/v1/session, binds X-Gaffer-Session to a
// runner_sessions row of this daemon boot inside the store call. Route bodies
// convert wire types at the boundary and never carry authority: the store
// transaction decides, commits, and only then does a reply leave this file.

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	g "github.com/korallis/letmecook/internal/authority"
	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/reconcile"
	"github.com/korallis/letmecook/internal/runstream"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

// InboxKey is the notify.Hub key GET /x/v1/inbox waits on. A store write that
// can add an assignment or a cancel target (Dispatch, RequestStop, the
// lease-expiry latch) should Notify it after its commit; the inbox always
// re-reads durable state, so a missed or spurious wakeup only costs latency.
const InboxKey = "execution.inbox"

const (
	maxInboxWaitMS   = 25000
	inboxPollAfterMS = 500
	inboxPage        = 128
)

func init() { Register("execution", executionRoutes) }

type executionAPI struct{ Deps }

func executionRoutes(d Deps) []Route {
	x := executionAPI{d}
	return []Route{
		{Method: "POST", Pattern: "/x/v1/session", Role: "runner", Class: Control, Handle: x.session},
		{Method: "GET", Pattern: "/x/v1/state", Role: "runner", Class: Control, Handle: x.state},
		{Method: "GET", Pattern: "/x/v1/input", Role: "runner", Class: Control, Handle: x.input},
		{Method: "GET", Pattern: "/x/v1/inbox", Role: "runner", Class: Long, Handle: x.inbox},
		{Method: "POST", Pattern: "/x/v1/messages", Role: "runner", Class: Control, Handle: x.messages},
		{Method: "POST", Pattern: "/x/v1/lease", Role: "runner", Class: Control, Handle: x.lease},
		{Method: "POST", Pattern: "/x/v1/streams/{attempt_id}", Role: "runner", Class: Bulk, Handle: x.streams},
		{Method: "POST", Pattern: "/x/v1/attempts/{id}/uploads", Role: "runner", Class: Control, Handle: x.uploads},
		{Method: "PUT", Pattern: "/x/v1/uploads/{upload_id}/blobs/{sha256}", Role: "runner", Class: Bulk, MaxBody: store.MaxBlobBytes, Raw: true, Handle: x.blob},
		{Method: "POST", Pattern: "/x/v1/uploads/{upload_id}/commit", Role: "runner", Class: Bulk, Handle: x.commit},
		{Method: "POST", Pattern: "/x/v1/attempts/{id}/finalize", Role: "runner", Class: Control, Handle: x.finalize},
		{Method: "POST", Pattern: "/x/v1/usage", Role: "runner", Class: Control, Handle: x.usage},
	}
}

// conflicts are refusal codes a runner resolves by re-reading state or
// reconciling, not by changing its request.
var conflicts = map[string]bool{
	"session_stale": true, "stop_latched": true, "stopped": true, "paused": true, "upload_incomplete": true,
	"reconciliation_required": true, "identity_conflict": true, "revision_conflict": true, "current_assignment": true,
	"superseded": true, "stale_generation": true, "stale_attempt": true,
}

// routeError maps store outcomes onto the wire status/code table: protocol
// refusals and authority denies verbatim, identity denials 403, missing rows
// 404, uncertain storage 503. Nothing here invents success.
func routeError(err error) *Error {
	var refusal *g.Refusal
	var reason p.Refusal
	var limit *http.MaxBytesError
	switch {
	case errors.As(err, &refusal):
		status := 422
		switch {
		case conflicts[refusal.Code]:
			status = 409
		case refusal.Code == "digest_mismatch":
			status = 422
		case refusal.Code == "runner_disabled":
			status = 403
		case refusal.Code == "malformed":
			status = 400
		case refusal.Code == "oversized":
			status = 413
		case refusal.Code == "corrupt_record", refusal.Code == "store_closed", refusal.Code == "fixture_only":
			status = 503
		}
		return &Error{status, refusal.Code, refusal.Field}
	case errors.As(err, &reason):
		switch reason {
		case p.Malformed, p.UnknownVersion:
			return &Error{400, string(reason), ""}
		case p.Oversized:
			return &Error{413, string(reason), ""}
		}
		return &Error{409, string(reason), ""}
	case errors.Is(err, c.ErrFenced):
		return &Error{409, "revoked_or_expired", ""}
	case errors.Is(err, i.Denied):
		return &Error{403, "identity_denied", ""}
	case errors.Is(err, i.Invalid):
		return &Error{400, "malformed", ""}
	case errors.Is(err, sql.ErrNoRows):
		return &Error{404, "not_found", ""}
	case errors.As(err, &limit):
		return &Error{413, "oversized", ""}
	case errors.Is(err, runstream.ErrGap):
		return &Error{409, "stream_sequence_gap", ""}
	case errors.Is(err, runstream.ErrConflict):
		return &Error{409, "stream_record_conflict", ""}
	case errors.Is(err, runstream.ErrIdentity):
		return &Error{409, "identity_conflict", ""}
	case errors.Is(err, runstream.ErrInvalid), errors.Is(err, runstream.ErrBatch):
		return &Error{400, "malformed", ""}
	case errors.Is(err, runstream.ErrSpoolFull):
		return &Error{413, "spool_full", ""}
	}
	return &Error{503, "store_unavailable", ""}
}

func decode(body []byte, dst any) *Error {
	err := execwire.Decode(body, dst)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, p.Oversized):
		return &Error{413, "oversized", ""}
	case errors.Is(err, p.UnknownVersion):
		return &Error{400, "unknown_version", ""}
	}
	return &Error{400, "malformed", ""}
}

// oneQuery requires exactly the named keys, each once.
func oneQuery(r Request, keys ...string) *Error {
	if len(r.Query) != len(keys) {
		return &Error{400, "invalid_query", ""}
	}
	for _, key := range keys {
		if len(r.Query[key]) != 1 {
			return &Error{400, "invalid_query", ""}
		}
	}
	return nil
}

func (x executionAPI) session(ctx context.Context, a Actor, r Request) (any, *Error) {
	var hello execwire.Hello
	if e := decode(r.Body, &hello); e != nil {
		return nil, e
	}
	journals := make([]json.RawMessage, 0, len(hello.Journals))
	for _, journal := range hello.Journals {
		raw, err := json.Marshal(journal)
		if err != nil {
			return nil, &Error{400, "malformed", ""}
		}
		journals = append(journals, raw)
	}
	record := store.HelloRecord{Version: hello.Version, MessageID: hello.MessageID, RunnerBoot: hello.RunnerBoot, EligibilityID: hello.EligibilityID, EligibilityRevision: hello.EligibilityRevision, PolicyDigest: hello.PolicyDigest, Journals: journals}
	sess, err := x.Store.RunnerSession(ctx, a.Fingerprint, record)
	if err != nil {
		return nil, routeError(err)
	}
	// The recovery seam runs after the durable session; its explicit S0 stub is
	// tolerated, a real recovery failure withholds the reply so the runner retries
	// the same hello against the same retained row.
	if _, err := reconcile.OnHello(ctx, reconcile.Deps{Store: x.Store, Now: time.Now}, sess.RunnerID, hello); err != nil && !errors.Is(err, reconcile.ErrNotImplemented) {
		return nil, &Error{503, "store_unavailable", ""}
	}
	return execwire.Session{SessionID: sess.SessionID, Generation: sess.Generation, DaemonBoot: sess.DaemonBoot, DaemonFingerprint: sess.DaemonFingerprint, RunnerID: sess.RunnerID, Mode: sess.Mode, DriftMS: sess.DriftMS, TerminationMS: sess.TerminationMS, LeaseValidityMS: sess.LeaseValidityMS, RenewEveryMS: sess.RenewEveryMS, Paused: sess.Paused}, nil
}

func (x executionAPI) state(ctx context.Context, a Actor, r Request) (any, *Error) {
	if e := oneQuery(r, "dispatch_id"); e != nil {
		return nil, e
	}
	id := r.Query.Get("dispatch_id")
	if !p.ValidID(id) {
		return nil, &Error{400, "invalid_id", ""}
	}
	st, err := x.Store.ExecutionState(ctx, a.Fingerprint, r.Session, id)
	if err != nil {
		return nil, routeError(err)
	}
	watermark, err := x.Store.Streams().Watermark(st.Identity.AttemptID)
	if err != nil {
		return nil, routeError(err)
	}
	v := execwire.State{AttemptState: st.AttemptState, Revision: st.Revision, Acknowledged: st.Acknowledged, Released: st.Released, Stream: execwire.StreamAck{Through: watermark.Through, Expected: watermark.Expected, Bytes: watermark.Bytes}, ReceiptID: st.ReceiptID, StopTargets: []c.Target{}, Paused: st.Paused}
	if st.LastLease.Reply.Kind != "" {
		reply := st.LastLease.Reply
		v.LastLease = &reply
	}
	if st.Head.ReceiptID != "" {
		v.Head = &execwire.Head{TaskID: st.Head.TaskID, Generation: st.Head.Generation, AttemptID: st.Head.AttemptID, Epoch: st.Head.Epoch, ManifestID: st.Head.ManifestID, ReceiptID: st.Head.ReceiptID, FinalizedMS: st.Head.FinalizedMS}
	}
	v.StopTargets = append(v.StopTargets, st.StopTargets...)
	return v, nil
}

func (x executionAPI) input(ctx context.Context, a Actor, r Request) (any, *Error) {
	if e := oneQuery(r, "dispatch_id"); e != nil {
		return nil, e
	}
	id := r.Query.Get("dispatch_id")
	if !p.ValidID(id) {
		return nil, &Error{400, "invalid_id", ""}
	}
	in, err := x.Store.TaskInput(ctx, a.Fingerprint, r.Session, id)
	if err != nil {
		return nil, routeError(err)
	}
	criteria := make([]execwire.Criterion, 0, len(in.Criteria))
	for _, criterion := range in.Criteria {
		criteria = append(criteria, execwire.Criterion{ID: criterion.ID, Text: criterion.Text})
	}
	return execwire.TaskInput{Version: execwire.Version, DispatchID: in.DispatchID, TaskID: in.TaskID, Repository: in.Repository, BaseCommit: in.BaseCommit, BriefSHA256: in.BriefSHA256, Brief: in.Brief, Criteria: criteria, Paths: in.Paths, Operations: in.Operations, Harness: in.Harness, Settings: in.Settings}, nil
}

func wireDispatch(d store.Dispatch) execwire.Dispatch {
	return execwire.Dispatch{DispatchRequest: execwire.DispatchRequest{ID: d.ID, Request: d.Request, Decision: d.Decision, Allowance: d.Allowance}, Facts: d.Facts, Assignment: d.Assignment, Acknowledged: d.Acknowledged, Released: d.Released}
}

// readInbox composes one durable read: deliverable assignments for this runner
// (refused deliveries are logged, never sent) and pending cancel targets.
func (x executionAPI) readInbox(ctx context.Context, a Actor, session string) (execwire.Inbox, error) {
	sess, err := x.Store.ExecutionSession(ctx, a.Fingerprint, session)
	if err != nil {
		return execwire.Inbox{}, err
	}
	inbox := execwire.Inbox{Assignments: []execwire.Dispatch{}, Cancels: []p.Message{}, Paused: sess.Paused, PollAfterMS: inboxPollAfterMS}
	if !sess.Paused && sess.Mode == "normal" {
		ids, err := x.Store.PendingAssignments(ctx, "", inboxPage)
		if err != nil {
			return execwire.Inbox{}, err
		}
		for _, id := range ids {
			d, err := x.Store.Assignment(ctx, id)
			if err != nil || d.Facts.Repository.RunnerRoot.RunnerID != a.ID {
				continue
			}
			if _, err := x.Store.Delivery(ctx, a.Fingerprint, id); err != nil {
				log.Printf("execution inbox: dispatch %s not delivered: %v", id, routeError(err).Code)
				continue
			}
			inbox.Assignments = append(inbox.Assignments, wireDispatch(d))
		}
	}
	cancels, err := x.Store.PendingCancels(ctx, a.Fingerprint, session)
	if err != nil {
		return execwire.Inbox{}, err
	}
	inbox.Cancels = append(inbox.Cancels, cancels...)
	return inbox, nil
}

func (x executionAPI) inbox(ctx context.Context, a Actor, r Request) (any, *Error) {
	wait := int64(0)
	if len(r.Query) > 0 {
		if e := oneQuery(r, "wait_ms"); e != nil {
			return nil, e
		}
		n, err := strconv.ParseInt(r.Query.Get("wait_ms"), 10, 64)
		if err != nil || strconv.FormatInt(n, 10) != r.Query.Get("wait_ms") || n < 0 || n > maxInboxWaitMS {
			return nil, &Error{400, "invalid_bound", ""}
		}
		wait = n
	}
	var wake <-chan struct{}
	if x.Hub != nil && wait > 0 {
		ch, cancel := x.Hub.Subscribe(InboxKey)
		defer cancel()
		wake = ch
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Millisecond)
	for {
		inbox, err := x.readInbox(ctx, a, r.Session)
		if err != nil {
			return nil, routeError(err)
		}
		if len(inbox.Assignments)+len(inbox.Cancels) > 0 || wake == nil || !time.Now().Before(deadline) {
			return inbox, nil
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-wake:
			timer.Stop()
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return inbox, nil
		}
	}
}

type messageReply struct {
	Outcome string     `json:"outcome"`
	Message *p.Message `json:"message,omitempty"`
}

func (x executionAPI) messages(ctx context.Context, a Actor, r Request) (any, *Error) {
	var env execwire.MessageEnvelope
	if e := decode(r.Body, &env); e != nil {
		return nil, e
	}
	if !p.ValidID(env.MessageID) || !p.ValidID(env.DispatchID) {
		return nil, &Error{400, "malformed", ""}
	}
	switch env.Message.Kind {
	case "accept":
		if _, err := x.Store.ExecutionSession(ctx, a.Fingerprint, r.Session); err != nil {
			return nil, routeError(err)
		}
		if err := x.Store.AcknowledgeAssignment(ctx, a.Fingerprint, env.DispatchID, env.Message); err != nil {
			return nil, routeError(err)
		}
		return messageReply{Outcome: "acknowledged"}, nil
	case "refuse":
		if err := x.Store.RecordRefusal(ctx, a.Fingerprint, r.Session, r.Selected, env.DispatchID, env.Message); err != nil {
			return nil, routeError(err)
		}
		return messageReply{Outcome: "recorded"}, nil
	case "transition":
		d, err := x.Store.Assignment(ctx, env.DispatchID)
		if err != nil {
			return nil, routeError(err)
		}
		if d.Assignment.Identity.AttemptID != env.Message.Identity.AttemptID {
			return nil, &Error{409, "identity_conflict", ""}
		}
		var evidence store.RuntimeEvidence
		if env.Evidence != nil {
			e := env.Evidence
			evidence = store.RuntimeEvidence{Kind: e.Kind, Workspace: e.Workspace, BoundaryPort: e.BoundaryPort, GuardianPID: e.GuardianPID, Nonce: e.Nonce, PID: e.PID, PGID: e.PGID, StartUnixNS: e.StartUnixNS, Code: e.Code, PGIDEmpty: e.PGIDEmpty, ObservedUnixNS: e.ObservedUnixNS, StreamThrough: e.StreamThrough}
		}
		m, err := x.Store.ProposeTransition(ctx, a.Fingerprint, r.Session, r.Selected, env.Message, evidence)
		if err != nil {
			return nil, routeError(err)
		}
		return messageReply{Outcome: "applied", Message: &m}, nil
	case "terminated":
		if env.Measurement == nil || env.Boundary == nil {
			return nil, &Error{400, "malformed", ""}
		}
		evidence := c.Evidence{Terminated: env.Message, Measurement: *env.Measurement}
		boundary := store.BoundaryState{Reservations: env.Boundary.Reservations, TerminalReceipts: env.Boundary.TerminalReceipts, InFlight: env.Boundary.InFlight, Quiescent: env.Boundary.Quiescent}
		reply, err := x.Store.ReportTermination(ctx, a.Fingerprint, r.Session, r.Selected, evidence, boundary)
		if err != nil {
			return nil, routeError(err)
		}
		if x.Hub != nil {
			x.Hub.Notify(InboxKey)
		}
		return reply, nil
	}
	return nil, &Error{400, "malformed", ""}
}

func (x executionAPI) lease(ctx context.Context, a Actor, r Request) (any, *Error) {
	var env execwire.LeaseEnvelope
	if e := decode(r.Body, &env); e != nil {
		return nil, e
	}
	if !p.ValidID(env.MessageID) || !p.ValidID(env.DispatchID) {
		return nil, &Error{400, "malformed", ""}
	}
	lease, err := x.Store.IssueLease(ctx, a.Fingerprint, r.Session, env.DispatchID, r.Selected, env.Request)
	if err != nil {
		return nil, routeError(err)
	}
	return lease.Reply, nil
}

func (x executionAPI) streams(ctx context.Context, a Actor, r Request) (any, *Error) {
	var batch execwire.StreamBatch
	if e := decode(r.Body, &batch); e != nil {
		return nil, e
	}
	attempt := r.Path["attempt_id"]
	if !p.ValidID(attempt) {
		return nil, &Error{400, "invalid_id", ""}
	}
	if len(batch.Records) == 0 {
		return nil, &Error{400, "malformed", ""}
	}
	identity, err := x.Store.StreamIdentity(ctx, a.Fingerprint, r.Session, attempt)
	if err != nil {
		return nil, routeError(err)
	}
	ack, err := x.Store.Streams().Receive(attempt, identity, batch.Records)
	if err != nil {
		e := routeError(err)
		if errors.Is(err, runstream.ErrGap) || errors.Is(err, runstream.ErrConflict) {
			e.Detail = strconv.FormatInt(ack.Expected, 10)
		}
		return nil, e
	}
	return execwire.StreamAck{Through: ack.Through, Expected: ack.Expected, Bytes: ack.Bytes}, nil
}

func (x executionAPI) uploads(ctx context.Context, a Actor, r Request) (any, *Error) {
	var begin execwire.UploadBegin
	if e := decode(r.Body, &begin); e != nil {
		return nil, e
	}
	attempt := r.Path["id"]
	if !p.ValidID(attempt) {
		return nil, &Error{400, "invalid_id", ""}
	}
	manifest, err := base64.StdEncoding.DecodeString(begin.ManifestBase64)
	if err != nil {
		return nil, &Error{400, "malformed", ""}
	}
	session, err := x.Store.BeginUpload(ctx, a.Fingerprint, r.Session, attempt, store.UploadBegin{Version: begin.Version, MessageID: begin.MessageID, Result: begin.Result, Manifest: manifest})
	if err != nil {
		return nil, routeError(err)
	}
	missing := make([]execwire.MissingBlob, 0, len(session.Missing))
	for _, b := range session.Missing {
		missing = append(missing, execwire.MissingBlob{SHA256: b.SHA256, Bytes: b.Bytes})
	}
	return Response{Status: 201, Body: execwire.UploadSession{UploadID: session.UploadID, Missing: missing, BytesAllowed: session.BytesAllowed}}, nil
}

func (x executionAPI) blob(ctx context.Context, a Actor, r Request) (any, *Error) {
	upload, digest := r.Path["upload_id"], r.Path["sha256"]
	if !p.ValidID(upload) || len(digest) != 64 {
		return nil, &Error{400, "invalid_id", ""}
	}
	// The route seam does not expose Content-Length, so the inventory's promised
	// size is enforced instead: the body must end exactly there.
	blob, err := x.Store.RecordUploadedBlob(ctx, a.Fingerprint, r.Session, upload, digest, r.BodyReader, -1)
	if err != nil {
		return nil, routeError(err)
	}
	status := 201
	if blob.Duplicate {
		status = 200
		if _, err := io.Copy(io.Discard, r.BodyReader); err != nil {
			return nil, routeError(err)
		}
	}
	return Response{Status: status, Body: blob}, nil
}

type commitBody struct {
	Version   string `json:"version"`
	MessageID string `json:"message_id"`
}

func (x executionAPI) commit(ctx context.Context, a Actor, r Request) (any, *Error) {
	var body commitBody
	if e := decode(r.Body, &body); e != nil {
		return nil, e
	}
	upload := r.Path["upload_id"]
	if !p.ValidID(upload) || !p.ValidID(body.MessageID) {
		return nil, &Error{400, "invalid_id", ""}
	}
	reply, err := x.Store.CommitUpload(ctx, a.Fingerprint, r.Session, upload, body.MessageID)
	if err != nil {
		return nil, routeError(err)
	}
	return execwire.CommitReply{Ack: reply.Ack, Receipt: reply.Receipt, Quarantined: reply.Quarantined}, nil
}

func (x executionAPI) finalize(ctx context.Context, a Actor, r Request) (any, *Error) {
	var body execwire.Completion
	if e := decode(r.Body, &body); e != nil {
		return nil, e
	}
	attempt := r.Path["id"]
	if !p.ValidID(attempt) {
		return nil, &Error{400, "invalid_id", ""}
	}
	completion := store.Completion{Version: body.Version, MessageID: body.MessageID, ReceiptID: body.ReceiptID, Stream: store.StreamWatermark{Through: body.Stream.Through, Digest: body.Stream.Digest}, Exit: store.ExitRecord{Code: body.Exit.Code, PGID: body.Exit.PGID, ObservedUnixNS: body.Exit.ObservedUnixNS}, Boundary: store.BoundaryState{Reservations: body.Boundary.Reservations, TerminalReceipts: body.Boundary.TerminalReceipts, InFlight: body.Boundary.InFlight, Quiescent: body.Boundary.Quiescent}}
	reply, err := x.Store.FinalizeAttempt(ctx, a.Fingerprint, r.Session, attempt, completion)
	if err != nil {
		return nil, routeError(err)
	}
	return reply, nil
}

func (x executionAPI) usage(ctx context.Context, a Actor, r Request) (any, *Error) {
	var body execwire.Usage
	if e := decode(r.Body, &body); e != nil {
		return nil, e
	}
	receipts := make([]store.UsageReceipt, 0, len(body.Receipts))
	for _, v := range body.Receipts {
		receipts = append(receipts, store.UsageReceipt{RequestID: v.RequestID, Protocol: v.Protocol, Model: v.Model, Status: v.Status, PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens, BytesIn: v.BytesIn, BytesOut: v.BytesOut, StartedMS: v.Started.UnixMilli(), EndedMS: v.Ended.UnixMilli(), Terminal: v.Terminal, Source: v.Source})
	}
	if err := x.Store.RecordUsage(ctx, a.Fingerprint, r.Session, store.UsageReport{Version: body.Version, MessageID: body.MessageID, Identity: body.Identity, Receipts: receipts}); err != nil {
		return nil, routeError(err)
	}
	return messageReply{Outcome: "recorded"}, nil
}
