package reconcile

// Regression probes adopted from the S5 review of 2a220a8
// (/Users/leebarry/.claude/jobs/13633b12/tmp/s5-review/reconcile_probe_test.go):
// each reproduced an unsafe outcome before its fix and passes once reconcile
// refuses it. Beyond gofmt and this header: TestS5ReviewHelloReplayGrowth
// asserts the row count it previously only logged, and
// TestS5ReviewStaleAutoRetryCause drives a fake-harness attempt (runFake)
// because the P1 fix no longer finalizes a real-boundary attempt without a
// quiescence proof, which its setup relied on.

import (
	"database/sql"
	"encoding/json"
	"github.com/korallis/letmecook/internal/execwire"
	p "github.com/korallis/letmecook/schemas/execution"
	"path/filepath"
	"testing"
)

func TestS5ReviewCorruptJournalSurvivesSweep(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.start(t)
	stop := k.cancel(t)
	if got := k.report(t, k.terminated(stop.ID, "terminated", "quiescent"), quiescent()); got.Released {
		t.Fatal("fixture prematurely released")
	}
	hello := execwire.Hello{Version: execwire.Version, MessageID: uuid(), RunnerBoot: f.facts.RunnerBoot, Journals: []execwire.Journal{{DispatchID: k.d.ID, Identity: k.id(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.boot, State: p.Running, Corrupt: true}}}
	record := f.helloRecord()
	raw, err := json.Marshal(hello.Journals[0])
	if err != nil {
		t.Fatal(err)
	}
	record.Journals = []json.RawMessage{raw}
	if _, err := f.s.RunnerSession(ctx, f.runner, record); err != nil {
		t.Fatal(err)
	}
	report, err := OnHello(ctx, f.deps(), f.runnerID, hello)
	if err != nil {
		t.Fatal(err)
	}
	if e := entryFor(t, report, k.id().AttemptID); e.Classification != JournalCorrupt || !e.ActionRequired {
		t.Fatal(e)
	}
	report = f.sweep(t, false)
	if e := entryFor(t, report, k.id().AttemptID); e.Released {
		t.Fatalf("UNSAFE: next sweep forgot corrupt journal and released via %s", e.Classification)
	}
}

func TestS5ReviewHelloReplayGrowth(t *testing.T) {
	f := newFixture(t)
	hello := execwire.Hello{Version: execwire.Version, MessageID: uuid(), RunnerBoot: f.facts.RunnerBoot}
	for range 100 {
		if _, err := OnHello(ctx, f.deps(), f.runnerID, hello); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(f.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var rows, bytes int
	if err := db.QueryRow("SELECT count(*),sum(length(body)) FROM reconcile_reports").Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	t.Logf("100 identical empty hellos: %d report rows, %d JSON bytes", rows, bytes)
	if rows > 1 {
		t.Fatalf("GROWTH: %d report rows for identical empty hellos", rows)
	}
}

func TestS5ReviewStaleAutoRetryCause(t *testing.T) {
	f := newFixture(t)
	k := f.newTask(t)
	k.runFake(t)
	custody := k.upload(t, "failed", map[string]string{"result.txt": "failed\n"})
	_ = custody
	f.reopen(t)
	report := f.startup(t, false)
	if e := entryFor(t, report, k.id().AttemptID); e.State != p.Failed || !e.Released {
		t.Fatal(e)
	}
	// The sweep previously released an infrastructure failure for this task;
	// an intervening owner retry has now completed with a non-allowlisted cause.
	entry, err := autoRetry(ctx, f.deps(), k.id().TaskID, CauseLeaseExpired)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Classification == RetryDispatched {
		t.Fatalf("ALLOWLIST BYPASS: retry dispatched despite current cause %q", entry.Cause)
	}
}
