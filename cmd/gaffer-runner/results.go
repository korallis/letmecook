package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/korallis/letmecook/internal/artifacts"
	g "github.com/korallis/letmecook/internal/authority"
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
func (s *supervisor) upload(ctx context.Context, r *runner.Runner, d store.Dispatch, dir string, checkout repositories.Checkout, spool *runstream.Spool, report runner.GuardianReport, boundary inference.State, failure string) error {
	attempt := d.Assignment.Identity.AttemptID
	outcome := "succeeded"
	if failure != "" {
		outcome = "failed"
	}
	if _, err := s.transition(ctx, r, d, p.Running, p.ResultPending, 3, w.Evidence{Kind: "exit", Code: report.ExitCode, PID: report.PID, PGID: report.PGID, GuardianPID: r.Status().Runtime.GuardianPID, StartUnixNS: report.StartUnixNS, PGIDEmpty: report.PGIDEmpty && !report.Escaped, ObservedUnixNS: report.ObservedUnixNS, StreamThrough: spool.Acknowledged()}, ""); err != nil {
		return err
	}
	pack, err := artifacts.Pack(ctx, artifacts.Request{Root: checkout.Path, Base: checkout.BaseCommit, RecoveryDir: filepath.Join(dir, "artifacts"), Identity: d.Assignment.Identity, Outcome: outcome})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(pack.Manifest)
	manifest := p.Manifest{ManifestID: uuid(), SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(pack.Manifest))}
	result := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "result", Identity: d.Assignment.Identity, Manifest: &manifest}
	begin := w.UploadBegin{Version: w.Version, MessageID: result.MessageID, Result: result, ManifestBase64: base64.StdEncoding.EncodeToString(pack.Manifest)}
	if err = r.Enqueue("begin:"+attempt, begin.MessageID, begin); err != nil {
		return err
	}
	upload, err := s.client.BeginUpload(ctx, attempt, begin)
	if err != nil {
		return err
	}
	if err = r.Acknowledge(begin.MessageID); err != nil {
		return err
	}
	blobs := map[string][]byte{}
	for _, source := range pack.Sources {
		b, e := os.ReadFile(source.File)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(b)
		blobs[hex.EncodeToString(sum[:])] = b
	}
	for _, missing := range upload.Missing {
		b, ok := blobs[missing.SHA256]
		if !ok || int64(len(b)) != missing.Bytes {
			return errors.New("daemon requested unknown blob")
		}
		if _, err = s.client.PutBlob(ctx, upload.UploadID, missing.SHA256, b); err != nil {
			return err
		}
	}
	commitID := uuid()
	intent := execclient.Intent{Version: w.Version, MessageID: commitID}
	if err = r.Enqueue("commit:"+upload.UploadID, commitID, intent); err != nil {
		return err
	}
	receipt, err := s.client.Commit(ctx, upload.UploadID, commitID)
	if err != nil {
		return err
	}
	if receipt.Quarantined || receipt.Receipt.Identity != d.Assignment.Identity || receipt.Receipt.Manifest != manifest || receipt.Ack.ReceiptID != receipt.Receipt.ReceiptID {
		return errors.New("custody acknowledgement mismatch")
	}
	if err = runner.DurableFile(filepath.Join(dir, "receipt.json"), receipt); err != nil {
		return err
	}
	if err = r.Acknowledge(commitID); err != nil {
		return err
	}
	usage := w.Usage{Version: w.Version, MessageID: uuid(), Identity: d.Assignment.Identity, Receipts: r.Usage()}
	if usage.Receipts == nil {
		usage.Receipts = []inference.Receipt{}
	}
	if err = r.Enqueue("usage", usage.MessageID, usage); err != nil {
		return err
	}
	if err = s.client.Usage(ctx, usage); err != nil {
		return err
	}
	if err = r.Acknowledge(usage.MessageID); err != nil {
		return err
	}
	completion := w.Completion{Version: w.Version, MessageID: uuid(), ReceiptID: receipt.Receipt.ReceiptID, Stream: w.StreamCompletion{Through: spool.Acknowledged(), Digest: streamDigest(spool)}, Exit: w.Exit{Code: report.ExitCode, PGID: report.PGID, ObservedUnixNS: report.ObservedUnixNS}, Boundary: boundary}
	if err = r.Enqueue("finalize:"+attempt, completion.MessageID, completion); err != nil {
		return err
	}
	reply, err := s.client.Finalize(ctx, attempt, completion)
	if err != nil {
		return err
	}
	if !reply.Released || reply.Outcome != outcome {
		return errors.New("finalization not confirmed")
	}
	return r.Acknowledge(completion.MessageID)
}
func (s *supervisor) replay(ctx context.Context, r *runner.Runner) error {
	for _, entry := range r.Outbox() {
		if entry.Acknowledged {
			continue
		}
		var err error
		kind, id, _ := strings.Cut(entry.Kind, ":")
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
			if execclient.IsFence(err) {
				_ = r.Fence(err.Error())
			}
			return err
		}
		if err = r.Acknowledge(entry.Key); err != nil {
			return err
		}
	}
	return nil
}

var _ = json.Valid

func (s *supervisor) recoverTermination(ctx context.Context, r *runner.Runner, meta attemptMeta) error {
	for _, entry := range r.Outbox() {
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
	if status.Guardian == nil || status.Guardian.Escaped || !status.Guardian.PGIDEmpty {
		return runner.ErrTerminationUnconfirmed
	}
	nonce := lastNonce(r)
	if nonce == "" {
		return runner.ErrTerminationUnconfirmed
	}
	cancel := p.Message{Version: p.FencedVersion, MessageID: uuid(), Kind: "cancel", Identity: meta.Dispatch.Assignment.Identity, StopID: w.ExpiryStopID(nonce), RunnerBoot: meta.RunnerBoot, DaemonBoot: meta.DaemonBoot}
	for _, entry := range r.Outbox() {
		if entry.Kind == "cancel" {
			var retained p.Message
			if json.Unmarshal(entry.Body, &retained) == nil {
				cancel = retained
			}
		}
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
