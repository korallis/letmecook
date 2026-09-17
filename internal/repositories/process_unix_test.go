//go:build linux || darwin

package repositories

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCancelledGitKillsOwnedTransportChild(t *testing.T) {
	root := t.TempDir()
	ready, escaped := filepath.Join(root, "ready"), filepath.Join(root, "escaped")
	fakeGit := filepath.Join(root, "git")
	// Deterministic stand-in for a blocking transport child, not a source check.
	put(t, fakeGit, "#!/bin/sh\n(sleep 1; printf escaped > '"+escaped+"') &\nprintf ready > '"+ready+"'\nwait\n", 0700)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() { _, err := (gitClient{binary: fakeGit, home: root}).run(ctx, root, "fetch"); result <- err }()
	// Parallel race builds can delay transport startup on a loaded host. Only
	// readiness gets extra time; cancellation and the escaped-child assertion
	// below retain their original semantics.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-result:
			t.Fatal("transport exited before readiness", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("transport never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-result; !errors.Is(err, Unavailable) {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := os.Stat(escaped); !os.IsNotExist(err) {
		t.Fatal("transport escaped cancellation", err)
	}
}
