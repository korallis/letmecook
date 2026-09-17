// Package opencode adapts the pinned OpenCode CLI. It owns neither launch
// authority, the isolation profile nor gateway credentials.
package opencode

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/harness"
)

const Version = "1.18.31"

var (
	ErrBinary          = errors.New("opencode_binary_refused")
	ErrConfigIsolation = errors.New("opencode_config_isolation_unproven")
	ErrRun             = errors.New("opencode_run_refused")
	ErrHandle          = errors.New("opencode_handle_unknown")
)

// Adapter pins the operator-selected binary by digest. Probe evidence is local
// to this object and is invalidated by a changed binary. It is not OS isolation
// evidence and never qualifies a launcher for unattended execution.
type Adapter struct {
	binary, digest  string
	mu              sync.Mutex
	gates           map[string]bool
	runs            map[string]*run
	grace, killWait time.Duration
}

var _ harness.Harness = (*Adapter)(nil)

// New binds an absolute --opencode-bin path without executing it. Probe verifies
// both the reported version and hostile-config isolation through its launcher.
func New(binary string) (*Adapter, error) {
	digest, err := binaryDigest(binary)
	if err != nil {
		return nil, err
	}
	return &Adapter{binary: binary, digest: digest, gates: map[string]bool{}, runs: map[string]*run{}, grace: 2 * time.Second, killWait: 5 * time.Second}, nil
}
func binaryDigest(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", ErrBinary
	}
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode().Perm()&0111 == 0 || before.Mode().Perm()&0022 != 0 {
		return "", ErrBinary
	}
	f, err := os.Open(path)
	if err != nil {
		return "", ErrBinary
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !os.SameFile(before, after) {
		return "", ErrBinary
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", ErrBinary
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func (a *Adapter) pinned() bool {
	got, err := binaryDigest(a.binary)
	return err == nil && got == a.digest
}
func (a *Adapter) Describe(ctx context.Context) (harness.Descriptor, error) {
	if ctx.Err() != nil {
		return harness.Descriptor{}, ctx.Err()
	}
	if !a.pinned() {
		return harness.Descriptor{}, ErrBinary
	}
	return harness.Descriptor{Name: "opencode", Version: Version, BinaryDigest: a.digest, Protocols: []string{"chat_completions", "responses", "messages"}, StructuredOutput: true, Approval: true, ModelSettings: true, Usage: true}, nil
}
func (a *Adapter) Start(ctx context.Context, req harness.RunRequest) (harness.RunHandle, error) {
	if ctx.Err() != nil {
		return harness.RunHandle{}, ctx.Err()
	}
	if !a.pinned() {
		return harness.RunHandle{}, ErrBinary
	}
	model, err := validateRun(req)
	if err != nil {
		return harness.RunHandle{}, err
	}
	a.mu.Lock()
	passed := a.gates[gateKey(req.Boundary.Protocol, model)]
	a.mu.Unlock()
	if !passed {
		return harness.RunHandle{}, ErrConfigIsolation
	}
	if err = prepareWorkspace(req.Workspace, true); err != nil {
		return harness.RunHandle{}, err
	}
	config, err := writeConfig(req.Workspace, req.Boundary, model)
	if err != nil {
		return harness.RunHandle{}, err
	}
	return a.launch(req, config, model)
}
func gateKey(protocol, model string) string { return protocol + "\x00" + model }
func (a *Adapter) Events(ctx context.Context, h harness.RunHandle, after int64) (harness.EventStream, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if after < 0 {
		return nil, ErrHandle
	}
	a.mu.Lock()
	r := a.runs[h.ID]
	a.mu.Unlock()
	if r == nil {
		return nil, ErrHandle
	}
	return &eventStream{run: r, after: after}, nil
}
func (a *Adapter) Cancel(ctx context.Context, h harness.RunHandle) (harness.CancelResult, error) {
	a.mu.Lock()
	r := a.runs[h.ID]
	a.mu.Unlock()
	if r == nil {
		return harness.CancelResult{}, ErrHandle
	}
	// A runner may replace PID/PGID/GuardianPID using its authenticated guardian
	// reply. Never signal the guardian's own group as if it were the worker.
	if h.PGID <= 1 || (h.GuardianPID > 0 && h.PGID == h.GuardianPID) {
		return harness.CancelResult{ConfirmedProcess: "unknown", RemoteWork: "unknown"}, nil
	}
	if h.GuardianPID == 0 && (h.PID != r.handle.PID || h.PGID != r.handle.PGID) {
		return harness.CancelResult{}, ErrHandle
	}
	select {
	case <-r.done:
		// A completed handle may now name a reused pgid. Observe only; do not kill
		// unrelated processes or claim that a surviving descendant is contained.
		if groupGone(h.PGID) {
			return harness.CancelResult{ConfirmedProcess: "terminated", RemoteWork: "unknown", ObservedUnixNS: time.Now().UnixNano()}, nil
		}
		return harness.CancelResult{ConfirmedProcess: "unknown", RemoteWork: "unknown"}, nil
	default:
	}
	return terminateGroup(ctx, h.PGID, a.grace, a.killWait), nil
}

// ConfigControls returns a copy of the pinned environment controls proven by
// Probe. They are compatibility facts, not execution or sandbox authority.
func ConfigControls() []string { return slices.Clone(configControls) }

var configControls = []string{
	"OPENCODE_DISABLE_PROJECT_CONFIG=1",
	"OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
}
