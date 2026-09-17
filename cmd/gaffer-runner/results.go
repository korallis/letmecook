package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/korallis/letmecook/internal/artifacts"
	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/closedjson"
	"github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execclient"
	w "github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/repositories"
	"github.com/korallis/letmecook/internal/runner"
	"github.com/korallis/letmecook/internal/runstream"
	"github.com/korallis/letmecook/internal/store"
	p "github.com/korallis/letmecook/schemas/execution"
)

func checkEnvelope(ctx context.Context, root, base string, env g.Envelope, profile repositories.Profile) error {
	changed := map[string]bool{}
	for _, args := range [][]string{{"diff", "--no-ext-diff", "--name-only", "-z", base, "--"}, {"ls-files", "--others", "-z"}} {
		cmd := exec.CommandContext(ctx, "/usr/bin/git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "diff.external="}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_REPLACE_OBJECTS=1", "GIT_OPTIONAL_LOCKS=0"}
		b, e := cmd.Output()
		if e != nil {
			return e
		}
		for _, path := range strings.Split(strings.TrimSuffix(string(b), "\x00"), "\x00") {
			if path != "" {
				changed[path] = true
			}
		}
	}
	paths := []string{}
	for path := range changed {
		allowed := false
		for _, grant := range env.Paths {
			if path == grant || strings.HasPrefix(path, grant+"/") {
				allowed = true
			}
		}
		if !allowed {
			return errors.New("envelope_violation")
		}
		paths = append(paths, path)
	}
	if len(paths) > 0 {
		write := false
		for _, op := range env.Operations {
			if op == "write" {
				write = true
			}
		}
		if !write {
			return errors.New("envelope_violation")
		}
	}
	return profile.CheckChanges(paths)
}
func streamDigest(spool *runstream.Spool) string {
	hash := sha256.New()
	for _, r := range spool.Records() {
		hash.Write([]byte(r.Digest))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

// custodyPlan is immutable continuation intent. Every request ID and the exact
// result edge/manifest/usage/exit bytes are durable before the first custody call.
// Blob bytes live in a synced, sandbox-denied directory, not a mutable checkout.
type custodyPlan struct {
	Edge       w.MessageEnvelope
	Begin      w.UploadBegin
	Commit     execclient.Intent
	Usage      w.Usage
	Completion w.Completion
	BlobDir    string
	Outcome    string
}

func (s *supervisor) upload(ctx context.Context, r *runner.Runner, d store.Dispatch, dir string, checkout repositories.Checkout, spool *runstream.Spool, report runner.GuardianReport, boundary inference.State, failure string) error {
	outcome := "succeeded"
	if failure != "" {
		outcome = "failed"
	}
	// Once the trusted receipt says the job exited, cancellation of serve may
	// interrupt transport, not preparation of replayable finalization evidence.
	prepare, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pack, err := artifacts.Pack(prepare, artifacts.Request{Root: checkout.Path, Base: checkout.BaseCommit, RecoveryDir: filepath.Join(dir, "artifacts"), Identity: d.Assignment.Identity, Outcome: outcome})
	if err != nil {
		return err
	}
	blobDir := filepath.Join(dir, "custody-blobs")
	if err := os.MkdirAll(blobDir, 0700); err != nil {
		return err
	}
	// Persist the new directory's name before retaining any continuation intent.
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
		return err
	}
	for _, source := range pack.Sources {
		if err := retainBlob(blobDir, source.File); err != nil {
			return err
		}
	}
	sum := sha256.Sum256(pack.Manifest)
	manifest := p.Manifest{ManifestID: uuid(), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(pack.Manifest))}
	result := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "result", Identity: d.Assignment.Identity, Manifest: &manifest}
	revision := int64(3)
	edge := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "transition", Identity: d.Assignment.Identity, ExpectedRevision: &revision, From: p.Running, To: p.ResultPending}
	plan := custodyPlan{
		Edge:       w.MessageEnvelope{Version: w.Version, MessageID: edge.MessageID, DispatchID: d.ID, Message: edge, Evidence: &w.Evidence{Kind: "exit", Code: report.ExitCode, PID: report.PID, PGID: report.PGID, GuardianPID: r.Status().Runtime.GuardianPID, StartUnixNS: report.StartUnixNS, PGIDEmpty: report.PGIDEmpty && !report.Escaped, ObservedUnixNS: report.ObservedUnixNS, StreamThrough: spool.Acknowledged()}},
		Begin:      w.UploadBegin{Version: w.Version, MessageID: result.MessageID, Result: result, ManifestBase64: base64.StdEncoding.EncodeToString(pack.Manifest)},
		Commit:     execclient.Intent{Version: w.Version, MessageID: uuid()},
		Usage:      w.Usage{Version: w.Version, MessageID: uuid(), Identity: d.Assignment.Identity, Receipts: r.Usage()},
		Completion: w.Completion{Version: w.Version, MessageID: uuid(), Stream: w.StreamCompletion{Through: spool.Acknowledged(), Digest: streamDigest(spool)}, Exit: w.Exit{Code: report.ExitCode, PGID: report.PGID, ObservedUnixNS: report.ObservedUnixNS}, Boundary: boundary},
		BlobDir:    blobDir, Outcome: outcome,
	}
	if plan.Usage.Receipts == nil {
		plan.Usage.Receipts = []inference.Receipt{}
	}
	if err := r.Enqueue("custody", "custody", plan); err != nil {
		return err
	}
	return s.resumeCustody(ctx, r, plan)
}

func retainBlob(dir, source string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	hash := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, hash), in)
	if err == nil {
		err = out.Sync()
	}
	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(out.Name(), filepath.Join(dir, hex.EncodeToString(hash.Sum(nil)))); err != nil {
		return err
	}
	fd, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer fd.Close()
	return fd.Sync()
}

var errCustodyRefused = errors.New("custody request retained with terminal refusal")

func outboxEntry(r *runner.Runner, key string) runner.OutboxEntry {
	for _, e := range r.Outbox() {
		if e.Key == key {
			return e
		}
	}
	return runner.OutboxEntry{}
}

func custodyAccepted(r *runner.Runner) bool {
	for _, e := range r.Outbox() {
		if e.Kind == "custody" {
			var plan custodyPlan
			return json.Unmarshal(e.Body, &plan) == nil && outboxEntry(r, plan.Edge.MessageID).Acknowledged
		}
	}
	return false
}

func (s *supervisor) custodyStep(r *runner.Runner, kind, key string, body any, send func() error) error {
	e := outboxEntry(r, key)
	if e.Refused != "" {
		return errCustodyRefused
	}
	if e.Acknowledged {
		return nil
	}
	if err := r.Enqueue(kind, key, body); err != nil {
		return err
	}
	if err := send(); err != nil {
		return s.recordRefusal(r, key, err)
	}
	return r.Acknowledge(key)
}

func (s *supervisor) resumeCustody(ctx context.Context, r *runner.Runner, plan custodyPlan) error {
	if outboxEntry(r, "custody").Refused != "" {
		return errCustodyRefused
	}
	attempt := plan.Begin.Result.Identity.AttemptID
	if err := s.custodyStep(r, "message", plan.Edge.MessageID, plan.Edge, func() error {
		reply, err := s.client.Message(ctx, plan.Edge)
		if err == nil && (reply.Message == nil || !messagesEqual(plan.Edge.Message, *reply.Message)) {
			return p.IdentityConflict
		}
		return err
	}); err != nil {
		return err
	}
	var upload w.UploadSession
	responseKey := plan.Begin.MessageID + "/response"
	if body := outboxEntry(r, responseKey).Body; body != nil {
		if err := w.Decode(body, &upload); err != nil {
			return err
		}
	}
	if err := s.custodyStep(r, "begin:"+attempt, plan.Begin.MessageID, plan.Begin, func() error {
		var err error
		upload, err = s.client.BeginUpload(ctx, attempt, plan.Begin)
		if err != nil {
			return err
		}
		return r.Enqueue("upload_session", responseKey, upload)
	}); err != nil {
		return err
	}
	if upload.UploadID == "" {
		return errors.New("durable upload response missing")
	}
	// Commit intent is queued only after every blob PUT succeeded. Once intent
	// exists, retry commit directly: its lost reply may hide a closed upload,
	// for which the daemon correctly refuses further PUTs. Before that point,
	// idempotent PUTs may repeat; verify the exact retained bytes first.
	if outboxEntry(r, plan.Commit.MessageID).Body == nil {
		for _, missing := range upload.Missing {
			if len(missing.SHA256) != 64 || strings.ContainsAny(missing.SHA256, "/\\") {
				return p.Malformed
			}
			b, err := os.ReadFile(filepath.Join(plan.BlobDir, missing.SHA256))
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			if hex.EncodeToString(sum[:]) != missing.SHA256 || int64(len(b)) != missing.Bytes {
				return errors.New("retained blob mismatch")
			}
			if _, err := s.client.PutBlob(ctx, upload.UploadID, missing.SHA256, b); err != nil {
				return s.recordRefusal(r, "custody", err)
			}
		}
	}
	var receipt w.CommitReply
	receiptKey := plan.Commit.MessageID + "/receipt"
	if body := outboxEntry(r, receiptKey).Body; body != nil {
		if err := w.Decode(body, &receipt); err != nil {
			return err
		}
	}
	if err := s.custodyStep(r, "commit:"+upload.UploadID, plan.Commit.MessageID, plan.Commit, func() error {
		var err error
		receipt, err = s.client.Commit(ctx, upload.UploadID, plan.Commit.MessageID)
		if err != nil {
			return err
		}
		if receipt.Quarantined || receipt.Receipt.Identity != plan.Begin.Result.Identity || receipt.Receipt.Manifest != *plan.Begin.Result.Manifest || receipt.Ack.ReceiptID != receipt.Receipt.ReceiptID {
			return errors.New("custody acknowledgement mismatch")
		}
		if err = runner.DurableFile(filepath.Join(filepath.Dir(plan.BlobDir), "receipt.json"), receipt); err != nil {
			return err
		}
		return r.Enqueue("custody_receipt", receiptKey, receipt)
	}); err != nil {
		return err
	}
	if receipt.Receipt.ReceiptID == "" {
		return errors.New("durable custody receipt missing")
	}
	if err := s.custodyStep(r, "usage", plan.Usage.MessageID, plan.Usage, func() error { return s.client.Usage(ctx, plan.Usage) }); err != nil {
		return err
	}
	completion := plan.Completion
	completion.ReceiptID = receipt.Receipt.ReceiptID
	return s.custodyStep(r, "finalize:"+attempt, completion.MessageID, completion, func() error {
		reply, err := s.client.Finalize(ctx, attempt, completion)
		if err != nil {
			return err
		}
		if !reply.Released || reply.Outcome != plan.Outcome {
			return errors.New("finalization not confirmed")
		}
		return nil
	})
}

func isRefusal(err error) bool {
	var remote *execclient.Error
	return errors.As(err, &remote) && remote.Status >= 400 && remote.Status < 500
}

func (s *supervisor) recordRefusal(r *runner.Runner, key string, err error) error {
	if !isRefusal(err) {
		return err
	}
	if e := r.Refuse(key, err.Error()); e != nil {
		return e
	}
	if execclient.IsFence(err) {
		if e := r.Fence(err.Error()); e != nil {
			return e
		}
	}
	return err
}

func (s *supervisor) replay(ctx context.Context, r *runner.Runner) error {
	var plan custodyPlan
	hasPlan := false
	for _, entry := range r.Outbox() {
		if entry.Kind == "custody" {
			if err := closedjson.Decode(entry.Body, &plan, 128<<10, nil); err != nil {
				return err
			}
			hasPlan = true
		}
	}
	for _, entry := range r.Outbox() {
		if entry.Acknowledged || entry.Refused != "" {
			continue
		}
		var err error
		kind, id, _ := strings.Cut(entry.Kind, ":")
		if hasPlan && (entry.Key == plan.Edge.MessageID || kind == "begin" || kind == "commit" || kind == "usage" || kind == "finalize") {
			continue
		}
		switch kind {
		case "message":
			var m w.MessageEnvelope
			err = w.Decode(entry.Body, &m)
			if err == nil {
				_, err = s.client.Message(ctx, m)
			}
		case "lease": // A prior boot's lease is evidence, never renewed or installed.
			continue
		case "begin":
			var m w.UploadBegin
			err = w.Decode(entry.Body, &m)
			if err == nil {
				_, err = s.client.BeginUpload(ctx, id, m)
			}
		case "commit":
			var m execclient.Intent
			err = w.Decode(entry.Body, &m)
			if err == nil {
				_, err = s.client.Commit(ctx, id, m.MessageID)
			}
		case "finalize":
			var m w.Completion
			err = w.Decode(entry.Body, &m)
			if err == nil {
				_, err = s.client.Finalize(ctx, id, m)
			}
		case "usage":
			var m w.Usage
			err = w.Decode(entry.Body, &m)
			if err == nil {
				err = s.client.Usage(ctx, m)
			}
		default:
			continue
		}
		if err != nil {
			if isRefusal(err) {
				if e := s.recordRefusal(r, entry.Key, err); !errors.Is(e, err) {
					return e
				}
				continue // terminal refusal retains bytes; do not poison every restart
			}
			return err
		}
		if err = r.Acknowledge(entry.Key); err != nil {
			return err
		}
	}
	if hasPlan {
		err := s.resumeCustody(ctx, r, plan)
		if err != nil && !isRefusal(err) && !errors.Is(err, errCustodyRefused) {
			return err
		}
	}
	return nil
}

var _ = json.Valid

func (s *supervisor) recoverTermination(ctx context.Context, r *runner.Runner, meta attemptMeta) error {
	for _, entry := range r.Outbox() {
		if entry.Kind == "custody" {
			var plan custodyPlan
			if err := closedjson.Decode(entry.Body, &plan, 128<<10, nil); err != nil {
				return err
			}
			if outboxEntry(r, plan.Edge.MessageID).Refused == "" {
				return nil
			}
		} // accepted/pending custody is never converted to runner_shutdown
		if strings.HasPrefix(entry.Kind, "finalize:") {
			return nil
		}
		if entry.Kind == "message" {
			var m w.MessageEnvelope
			if json.Unmarshal(entry.Body, &m) == nil && m.Message.Kind == "terminated" {
				return nil
			}
		}
	}
	status := r.Status()
	if status.Runtime == nil {
		return nil
	}
	nonce := lastNonce(r)
	if nonce == "" {
		return runner.ErrTerminationUnconfirmed
	}
	stopID := w.LocalStopID(meta.Dispatch.Assignment.Identity.AttemptID, "runner_shutdown")
	if status.Guardian != nil && status.Guardian.Cause == "lease_expired" {
		stopID = w.ExpiryStopID(nonce)
	}
	cancel := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: meta.Dispatch.Assignment.Identity, StopID: stopID, RunnerBoot: meta.RunnerBoot, DaemonBoot: meta.DaemonBoot}
	for _, entry := range r.Outbox() {
		if entry.Kind == "cancel" {
			var retained p.Message
			if json.Unmarshal(entry.Body, &retained) == nil {
				cancel = retained
			}
		}
	}
	if status.Guardian == nil || status.Guardian.Escaped || !status.Guardian.PGIDEmpty {
		measurement := control.Measurement{RequestedAt: time.Now().UTC(), AcknowledgedAt: time.Now().UTC()}
		report := runner.GuardianReport{}
		if status.Guardian != nil {
			report = *status.Guardian
			if report.StopUnixNS > 0 {
				measurement.RequestedAt = time.Unix(0, report.StopUnixNS).UTC()
				measurement.AcknowledgedAt = measurement.RequestedAt
			}
			measurement.Escalated = report.Escalated
		}
		_, err := s.containment(ctx, r, meta.Dispatch, cancel, "unknown", r.RecoveredBoundaryState(), measurement, report, "recovered_containment_unconfirmed")
		return errors.Join(runner.ErrTerminationUnconfirmed, err)
	}
	evidence, err := r.GuardianEvidence(cancel)
	if err != nil {
		return err
	}
	boundary := r.RecoveredBoundaryState()
	if boundary.Quiescent {
		evidence.Terminated.RemoteWork = "quiescent"
	}
	_, err = s.message(ctx, r, meta.Dispatch.ID, evidence.Terminated, nil, &boundary, &evidence.Measurement)
	return err
}

// journalSummary reports retained facts, not inferred release. A missing guardian
// report is unknown containment, not corruption of an otherwise verified journal.
func journalSummary(r *runner.Runner, meta attemptMeta, dir string) w.Journal {
	v := w.Journal{DispatchID: meta.Dispatch.ID, Identity: meta.Dispatch.Assignment.Identity, RunnerBoot: meta.RunnerBoot, DaemonBoot: meta.DaemonBoot, State: p.Assigned}
	for _, e := range r.Outbox() {
		if e.Kind == "message" && e.Acknowledged {
			var m w.MessageEnvelope
			if w.Decode(e.Body, &m) == nil && m.Message.Kind == "transition" {
				v.State = m.Message.To
			}
		}
		if e.Kind == "custody_receipt" {
			var receipt w.CommitReply
			if w.Decode(e.Body, &receipt) == nil {
				v.ReceiptID = receipt.Receipt.ReceiptID
			}
		}
	}
	if r.Status().Runtime != nil && r.Status().Guardian == nil {
		v.State = p.Unknown
	}
	if v.ReceiptID == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "receipt.json")); err == nil {
			var receipt w.CommitReply
			if w.Decode(b, &receipt) == nil {
				v.ReceiptID = receipt.Receipt.ReceiptID
			} else {
				v.Corrupt = true
			}
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "spool")); err == nil {
		spool, err := runstream.OpenSpool(filepath.Join(dir, "spool"), v.Identity)
		if err != nil {
			v.Corrupt = true
		} else {
			v.StreamThrough = spool.Acknowledged()
			spool.Close()
		}
	}
	return v
}
