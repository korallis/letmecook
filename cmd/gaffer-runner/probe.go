package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	h "github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/runner"
	sc "github.com/korallis/letmecook/internal/scheduler"
)

type probeRecord struct {
	RunnerBoot   string        `json:"runner_boot"`
	Key          string        `json:"key"`
	BinaryDigest string        `json:"binary_digest"`
	Passed       bool          `json:"passed"`
	Result       h.ProbeResult `json:"result"`
}
type probeLauncher struct {
	ctx       context.Context
	profile   isolation.Profile
	workspace isolation.Workspace
	mu        sync.Mutex
	launchers []isolation.Launcher
}

func (l *probeLauncher) WithBoundary(addr string) (isolation.Launcher, error) {
	p, ok := l.profile.(interface {
		WithBoundary(string) isolation.Profile
	})
	if !ok {
		return nil, errors.New("probe profile cannot pin boundary")
	}
	launcher, err := p.WithBoundary(addr).Prepare(l.ctx, l.workspace)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.launchers = append(l.launchers, launcher)
	l.mu.Unlock()
	return launcher, nil
}
func (l *probeLauncher) Wrap(*exec.Cmd) error {
	return errors.New("probe requires its explicit loopback boundary")
}
func (l *probeLauncher) Observation() isolation.Observation {
	return isolation.Observation{Limitations: []string{"probe has not supplied boundary endpoint"}}
}
func (l *probeLauncher) Cleanup() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for _, v := range l.launchers {
		errs = append(errs, v.Cleanup())
	}
	return errors.Join(errs...)
}
func (s *supervisor) probe(ctx context.Context, local sc.Eligibility) error {
	if s.cfg.Harness == "fake" {
		return nil
	}
	descriptor, err := s.harness.Describe(ctx)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(struct {
		Binary, Profile, OS, Arch, Environment string
		Route                                  any
	}{descriptor.BinaryDigest, local.Isolation.RuntimeDigest, runtime.GOOS, runtime.GOARCH, "clean-home-xdg-v1", local.Route})
	hash := sha256.Sum256(raw)
	key := hex.EncodeToString(hash[:])
	if s.probeKey == key {
		return nil
	}
	parent, err := os.MkdirTemp(s.cfg.RepositoryRoot, "gaffer-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(parent)
	w := isolation.Workspace{Root: filepath.Join(parent, "root"), PrivateHome: filepath.Join(parent, "home"), TempDir: filepath.Join(parent, "tmp"), RuntimeDir: filepath.Join(parent, "runtime")}
	for _, p := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err = os.Mkdir(p, 0700); err != nil {
			return err
		}
	}
	launcher := &probeLauncher{ctx: ctx, profile: s.profile, workspace: w}
	defer launcher.Cleanup()
	result, probeErr := s.harness.Probe(ctx, h.ProbeRequest{Workspace: w, Route: local.Route, Launcher: launcher, Boundary: h.BoundaryHandle{Models: []string{local.Route.Targets[0].Model}, Protocol: local.Route.Protocol}})
	record := probeRecord{RunnerBoot: s.boot, Key: key, BinaryDigest: descriptor.BinaryDigest, Passed: probeErr == nil && result.Compatible && result.ConfigIsolated, Result: result}
	if err = runner.DurableFile(filepath.Join(s.cfg.StateDir, "probe.json"), record); err != nil {
		return err
	}
	if !record.Passed {
		return errors.New("harness config isolation probe failed")
	}
	s.probeKey = key
	return nil
}
