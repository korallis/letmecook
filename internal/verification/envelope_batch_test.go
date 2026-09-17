package verification

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEnvelopeReadsBaseWithOneGitBatch(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init", "-q")
	for n := 0; n < 48; n++ {
		if err := os.WriteFile(filepath.Join(repo, fmt.Sprintf("file-%02d", n)), []byte(fmt.Sprintf("base\x00\n%d\n", n)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-qm", "many base blobs")
	base := gitTest(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "file-00"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", log, realGit)
	if err = os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	envelope := envelopeFor(base)
	envelope.Paths = []string{"file-00"}
	if err = CheckEnvelope(repo, base, envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(raw), "cat-file --batch") != 1 || strings.Contains(string(raw), "cat-file blob") {
		t.Fatalf("not one batch: %s", raw)
	}
}

func TestBaseGitBatchRejectsMalformedOutput(t *testing.T) {
	oid := strings.Repeat("a", 40)
	for _, tc := range []struct{ name, output string }{
		{"missing", oid + " missing\n"},
		{"wrong-object", strings.Repeat("b", 40) + " blob 0\n\n"},
		{"wrong-type", oid + " tree 0\n\n"},
		{"negative", oid + " blob -1\n"},
		{"oversized", oid + " blob 1048577\n"},
		{"overflow", oid + " blob 9999999999999999999999999\n"},
		{"truncated", oid + " blob 4\nxx"},
		{"delimiter", oid + " blob 1\nx!"},
		{"trailing", oid + " blob 1\nx\nextra"},
		{"header-bound", strings.Repeat("x", 8192) + "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bin := t.TempDir()
			fixture := filepath.Join(bin, "output")
			if err := os.WriteFile(fixture, []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			script := fmt.Sprintf("#!/bin/sh\n/bin/cat %q\n", fixture)
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := baseBlobs(ctx, bin, []baseObject{{"file", oid}}); err == nil {
				t.Fatal("malformed batch accepted")
			}
		})
	}
}

func TestBaseGitBatchCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := baseBlobs(ctx, t.TempDir(), []baseObject{{"file", strings.Repeat("a", 40)}}); err == nil {
		t.Fatal("cancelled batch succeeded")
	}
}
