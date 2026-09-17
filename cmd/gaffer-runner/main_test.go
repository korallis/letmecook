package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/korallis/letmecook/internal/harness/opencode"
)

func TestHarnessRegistryIncludesOpenCode(t *testing.T) {
	if _, err := harnessFor(config{Harness: "opencode"}); !errors.Is(err, opencode.ErrBinary) {
		t.Fatal("OpenCode factory must validate the configured binary", err)
	}
	binary := filepath.Join(t.TempDir(), "opencode")
	// Construction pins without running the binary; launch still needs a probe.
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexit 99\n"), 0700); err != nil {
		t.Fatal(err)
	}
	adapter, err := harnessFor(config{Harness: "opencode", OpenCodeBin: binary})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := adapter.Describe(context.Background())
	if err != nil || descriptor.Name != "opencode" || descriptor.Version != opencode.Version || len(descriptor.BinaryDigest) != 64 {
		t.Fatal(descriptor, err)
	}
	if _, err := harnessFor(config{Harness: "fake"}); err != nil {
		t.Fatal("fake factory regressed", err)
	}
	if _, err := harnessFor(config{Harness: "unregistered"}); err == nil {
		t.Fatal("unknown harness admitted")
	}
}
