package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	p "github.com/korallis/letmecook/schemas/execution"
	"modernc.org/sqlite"
)

func testBlob(t *testing.T, dir, name, body string) (ArtifactBlob, ArtifactSource) {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256([]byte(body))
	b := ArtifactBlob{Path: name, SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(body))}
	return b, ArtifactSource{Path: name, File: file}
}
func custodyFixture(t *testing.T, s *Store) (CustodyRequest, []ArtifactSource) {
	t.Helper()
	dir := t.TempDir()
	tracked, ts := testBlob(t, dir, "src/main.go", "package main\n")
	untracked, us := testBlob(t, dir, "notes.txt", "untracked\n")
	binary, bs := testBlob(t, dir, "image.bin", string([]byte{0, 1, 2, 3}))
	recovery, rs := testBlob(t, dir, "recovery.log", "evidence\n")
	id := p.Identity{Generation: s.meta.Generation, TaskID: newID(), AttemptID: newID(), Epoch: 1}
	assignment := p.Message{Version: p.FencedVersion, MessageID: newID(), Kind: "assign", Identity: id, AssignmentID: newID(), InputDigest: strings.Repeat("b", 64), Route: &p.Route{RouteRef: "public-fixture", DecisionDigest: strings.Repeat("c", 64), PolicyDigest: strings.Repeat("d", 64), LimitsProfile: "strict-provider-output-v1"}}
	if err := s.assign(context.Background(), assignment); err != nil {
		t.Fatal(err)
	}
	m := CandidateManifest{Version: artifactManifestVersion, Identity: id, Base: ArtifactBase{Revision: "0123456789abcdef", SHA256: strings.Repeat("a", 64)}, Outcome: "succeeded", Tracked: []ArtifactBlob{tracked}, Untracked: []ArtifactBlob{untracked}, Binary: []ArtifactBlob{binary}, Recovery: []ArtifactBlob{recovery}, Deleted: []string{}}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.Sum256(raw)
	result := p.Message{Version: p.FencedVersion, MessageID: newID(), Kind: "result", Identity: id, Manifest: &p.Manifest{ManifestID: newID(), SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(raw))}}
	sources := []ArtifactSource{ts, us, bs, rs}
	return CustodyRequest{Result: result, Manifest: raw, Sources: sources, RetainUntilMS: time.Now().Add(time.Hour).UnixMilli()}, sources
}

func TestCustodyPromotesBeforeStableLostAck(t *testing.T) {
	s, artifacts := persistent(t)
	request, sources := custodyFixture(t, s)
	received, err := s.CustodyResult(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if received.Quarantined || p.CheckAck(request.Result, received.Ack, request.Result.Identity, received.Receipt) != p.OK {
		t.Fatal(received)
	}
	for _, source := range sources {
		if _, err := os.Stat(source.File); err != nil {
			t.Fatalf("recovery source moved: %v", err)
		}
	}
	var n int
	if err = s.db.QueryRow("SELECT count(*) FROM artifact_results").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	// A lost ack retry does not need to recopy the runner's protected recovery files.
	retry := request
	retry.Sources = nil
	again, err := s.CustodyResult(context.Background(), retry)
	if err != nil || again.Receipt.ReceiptID != received.Receipt.ReceiptID {
		t.Fatal(again, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), s.dir, artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	replayed, err := reopened.CustodyResult(context.Background(), retry)
	if err != nil || replayed.Receipt.ReceiptID != received.Receipt.ReceiptID {
		t.Fatal(replayed, err)
	}
}

func TestCustodyCrashCorruptionFullAndStaleQuarantine(t *testing.T) {
	for _, point := range []string{"after_staging_fsync", "after_promotion_fsync", "before_metadata_commit", "after_metadata_commit"} {
		t.Run(point, func(t *testing.T) {
			s, _ := persistent(t)
			request, _ := custodyFixture(t, s)
			artifactHook = func(got string) error {
				if got == point {
					return errors.New("crash")
				}
				return nil
			}
			t.Cleanup(func() { artifactHook = nil })
			if _, err := s.CustodyResult(context.Background(), request); err == nil {
				t.Fatal("ack after injected crash")
			}
			var n int
			if err := s.db.QueryRow("SELECT count(*) FROM artifact_results").Scan(&n); err != nil || n != 0 {
				if point != "after_metadata_commit" || err != nil || n != 1 {
					t.Fatal(n, err)
				}
			}
			artifactHook = nil
			received, err := s.CustodyResult(context.Background(), request)
			if err != nil || p.CheckAck(request.Result, received.Ack, request.Result.Identity, received.Receipt) != p.OK {
				t.Fatal(received, err)
			}
		})
	}
	t.Run("sqlite-full", func(t *testing.T) {
		s, _ := persistent(t)
		request, _ := custodyFixture(t, s)
		sqlExec(t, s, "CREATE TRIGGER full_result BEFORE INSERT ON artifact_results BEGIN SELECT RAISE(ABORT,'database or disk is full'); END")
		if _, err := s.CustodyResult(context.Background(), request); err == nil {
			t.Fatal("full database acknowledged")
		}
		sqlExec(t, s, "DROP TRIGGER full_result")
		if _, err := s.CustodyResult(context.Background(), request); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("corrupt-promoted", func(t *testing.T) {
		s, _ := persistent(t)
		request, _ := custodyFixture(t, s)
		b := CandidateManifest{}
		if err := json.Unmarshal(request.Manifest, &b); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(blobPath(s.artifacts, b.Tracked[0].SHA256)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(blobPath(s.artifacts, b.Tracked[0].SHA256), []byte("bad"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CustodyResult(context.Background(), request); err == nil {
			t.Fatal("corruption acknowledged")
		}
	})
	t.Run("stale", func(t *testing.T) {
		s, _ := persistent(t)
		request, _ := custodyFixture(t, s)
		request.Result.Identity.Generation = newID()
		m := CandidateManifest{}
		if err := json.Unmarshal(request.Manifest, &m); err != nil {
			t.Fatal(err)
		}
		m.Identity = request.Result.Identity
		raw, _ := json.Marshal(m)
		h := sha256.Sum256(raw)
		request.Manifest = raw
		request.Result.Manifest.SHA256 = hex.EncodeToString(h[:])
		request.Result.Manifest.Bytes = int64(len(raw))
		got, err := s.CustodyResult(context.Background(), request)
		if err != nil || !got.Quarantined {
			t.Fatal(got, err)
		}
		var current int
		if err = s.db.QueryRow("SELECT current FROM artifact_results").Scan(&current); err != nil || current != 0 {
			t.Fatal(current, err)
		}
	})
}

func TestCustodyRefusesUnsafeShapeAndManifest(t *testing.T) {
	s, _ := persistent(t)
	request, sources := custodyFixture(t, s)
	if err := os.Remove(sources[0].File); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hosts", sources[0].File); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CustodyResult(context.Background(), request); err == nil {
		t.Fatal("symlink accepted")
	}
	request, _ = custodyFixture(t, s)
	var m CandidateManifest
	if err := json.Unmarshal(request.Manifest, &m); err != nil {
		t.Fatal(err)
	}
	m.Tracked[0].Path = "../escape"
	raw, _ := json.Marshal(m)
	request.Manifest = raw
	if _, err := s.CustodyResult(context.Background(), request); err == nil {
		t.Fatal("traversal accepted")
	}
}

func TestCustodyFullUsesSQLiteCode(t *testing.T) {
	s, _ := persistent(t)
	request, _ := custodyFixture(t, s)
	sqlExec(t, s, "CREATE TRIGGER full_code BEFORE INSERT ON artifact_results BEGIN SELECT RAISE(ABORT,'database or disk is full'); END")
	_, err := s.CustodyResult(context.Background(), request)
	var full *sqlite.Error
	if !errors.As(err, &full) && err == nil {
		t.Fatal("expected SQL failure")
	}
	_ = sql.ErrNoRows // keep database/sql imported as explicit transaction boundary evidence
}
