package store

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	p "github.com/korallis/letmecook/schemas/execution"
	"modernc.org/sqlite"
)

const dropControlSchema = `DROP TABLE control_observations; DROP TABLE control_messages; DROP TABLE control_fenced; DROP TABLE control_revocations; DROP TABLE control_leases; DROP TABLE control_targets; DROP TABLE control_acks; DROP TABLE control_stops;`

func leaseMessages(f dispatchFixture, d Dispatch) (p.Message, p.Message) {
	sent, validity := int64(100), int64(30000)
	req := p.Message{Version: p.FencedVersion, Kind: "lease_request", MessageID: newID(), Identity: d.Assignment.Identity, Nonce: newID(), RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.s.meta.DaemonBoot, SentMS: &sent}
	reply := p.Message{Version: p.FencedVersion, Kind: "lease_reply", MessageID: newID(), Identity: req.Identity, Nonce: req.Nonce, RunnerBoot: req.RunnerBoot, DaemonBoot: req.DaemonBoot, ValidityMS: &validity}
	return req, reply
}

func controlFixture(t *testing.T) (dispatchFixture, Dispatch, c.Lease) {
	t.Helper()
	f := dispatchFixtureFor(t, nil)
	d := admitted(t, f)
	ack := p.Message{Version: p.FencedVersion, Kind: "accept", MessageID: newID(), Identity: d.Assignment.Identity, AssignmentID: d.Assignment.AssignmentID, RunnerBoot: f.facts.RunnerBoot, DaemonBoot: f.s.meta.DaemonBoot}
	if err := f.s.AcknowledgeAssignment(ctx, f.runner, d.ID, ack); err != nil {
		t.Fatal(err)
	}
	req, reply := leaseMessages(f, d)
	lease, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, req, reply, 250*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	return f, d, lease
}

func rowCount(t *testing.T, s *Store, table string, want int) {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != want {
		t.Fatalf("%s: %d != %d (%v)", table, n, want, err)
	}
}

func stopRequest(kind c.Kind, d Dispatch) c.Request {
	r := c.Request{ID: newID(), Kind: kind, Cause: "operator"}
	if kind != c.GlobalStop {
		r.TaskID = d.Assignment.Identity.TaskID
	}
	if kind == c.CancelAttempt {
		r.AttemptID = d.Assignment.Identity.AttemptID
	}
	return r
}

func TestControlReplayRestartAndScopes(t *testing.T) {
	for _, kind := range []c.Kind{c.PauseTask, c.CancelAttempt, c.GlobalStop} {
		t.Run(string(kind), func(t *testing.T) {
			f, d, lease := controlFixture(t)
			r := stopRequest(kind, d)
			ack, err := f.s.RequestStop(ctx, f.owner, r)
			if err != nil || ack.Acknowledged == nil || ack.Acknowledged.RequestToAckNS == nil || *ack.Acknowledged.RequestToAckNS < 0 {
				t.Fatal(ack, err)
			}
			t.Logf("durable request-to-ack: %.3f ms", float64(*ack.Acknowledged.RequestToAckNS)/1e6)
			replay, err := f.s.RequestStop(ctx, f.owner, r)
			if err != nil || !reflect.DeepEqual(ack, replay) {
				t.Fatal("replay changed ack", err)
			}
			bad := r
			bad.Kind = c.GlobalStop
			bad.TaskID = ""
			bad.AttemptID = ""
			if kind == c.GlobalStop {
				bad.Kind = c.PauseTask
				bad.TaskID = d.Assignment.Identity.TaskID
			}
			if _, err := f.s.RequestStop(ctx, f.owner, bad); !errors.Is(err, p.IdentityConflict) {
				t.Fatal("identity collision", err)
			}
			rowCount(t, f.s, "control_stops", 1)
			rowCount(t, f.s, "control_acks", 1)
			rowCount(t, f.s, "control_revocations", 1)
			targets, err := f.s.StopTargets(ctx, r.ID)
			if err != nil || len(targets) != 1 || targets[0].Cancel.Kind != "cancel" || p.CheckSession(targets[0].Cancel, d.Assignment.Identity, p.FencedVersion) != p.OK {
				t.Fatal(targets, err)
			}
			view, err := f.s.StopStatus(ctx, r.ID, d.Assignment.Identity.AttemptID)
			if err != nil || view.Status != c.TerminationUnconfirmed || view.ConfirmedProcess != "unknown" {
				t.Fatal(view, err)
			}
			_, err = f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, lease.Request, lease.Reply, 250*time.Millisecond)
			if !errors.Is(err, c.ErrFenced) {
				t.Fatal("revoked lease replay authorized", err)
			}
			rowCount(t, f.s, "control_fenced", 1)
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(ctx, f.s.dir, f.artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			replay, err = s.RequestStop(ctx, f.owner, r)
			if err != nil || !reflect.DeepEqual(ack, replay) {
				t.Fatal("restart changed ack", err)
			}
			rowCount(t, s, "control_revocations", 1)
			ids, err := s.StopRequests(ctx, "", 1)
			if err != nil || !reflect.DeepEqual(ids, []string{r.ID}) {
				t.Fatal("restart lost cancel discovery", ids, err)
			}
			ids, err = s.StopRequests(ctx, r.ID, 128)
			if err != nil || len(ids) != 0 {
				t.Fatal("stop cursor", ids, err)
			}
			f.request.ID = newID()
			_, err = s.Dispatch(ctx, f.request)
			requireReason(t, err, "stop_latched")
			if _, err := s.Delivery(ctx, f.runner, d.ID); err == nil {
				t.Fatal("restart resumed delivery")
			}
			retained, err := s.Assignment(ctx, d.ID)
			if err != nil || retained.Released {
				t.Fatal("stop released reservation", err)
			}
			for _, table := range []string{"control_stops", "control_acks", "control_targets", "control_leases", "control_revocations", "control_fenced"} {
				if _, err := s.db.Exec("DELETE FROM " + table); err == nil {
					t.Fatal("mutable history", table)
				}
			}
		})
	}
}

func TestControlPartitionExpiryMarginAndLateRenewal(t *testing.T) {
	f, d, lease := controlFixture(t)
	clock := f.s.controlStart.Add(time.Duration(lease.DeadlineNS) + time.Nanosecond)
	f.s.controlNow = func() time.Time { return clock }
	req, reply := leaseMessages(f, d)
	_, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, req, reply, time.Second)
	if !errors.Is(err, c.ErrFenced) {
		t.Fatal("delayed renewal extended lease", err)
	}
	stopID := dispatchID(lease.Request.Nonce, "expired")
	for _, tc := range []struct {
		delta  time.Duration
		status c.Status
	}{{0, c.TerminationUnconfirmed}, {time.Duration(lease.MarginNS) - time.Nanosecond, c.TerminationUnconfirmed}, {time.Duration(lease.MarginNS), c.TerminationExpired}} {
		clock = f.s.controlStart.Add(time.Duration(lease.DeadlineNS) + time.Nanosecond + tc.delta)
		v, err := f.s.StopStatus(ctx, stopID, d.Assignment.Identity.AttemptID)
		if err != nil || v.Status != tc.status || v.ConfirmedProcess != "unknown" || v.RemoteWork != "unknown" || !v.Quarantined {
			t.Fatal(v, err)
		}
	}
	rowCount(t, f.s, "control_leases", 1)
	rowCount(t, f.s, "control_revocations", 1)
	rowCount(t, f.s, "control_fenced", 1)
	f.request.ID = newID()
	_, err = f.s.Dispatch(ctx, f.request)
	requireReason(t, err, "stop_latched")
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, elapsed := range []time.Duration{time.Second, 30*time.Second + time.Duration(lease.MarginNS), 30*time.Second + time.Duration(lease.MarginNS) + 1} {
		s.controlNow = func() time.Time { return s.controlStart.Add(elapsed) }
		v, err := s.StopStatus(ctx, stopID, d.Assignment.Identity.AttemptID)
		want := c.TerminationUnconfirmed
		if elapsed > 30*time.Second+time.Duration(lease.MarginNS) {
			want = c.TerminationExpired
		}
		if err != nil || v.Status != want || v.ConfirmedProcess != "unknown" {
			t.Fatal("restart reused monotonic domain", v, err)
		}
	}
}

func TestControlLateResultPreservesCustodyAndJournal(t *testing.T) {
	f, d, _ := controlFixture(t)
	blob, source := testBlob(t, t.TempDir(), "partial.patch", "partial uncommitted work\n")
	manifest := CandidateManifest{Version: artifactManifestVersion, Identity: d.Assignment.Identity, Base: ArtifactBase{Revision: "base", SHA256: strings.Repeat("a", 64)}, Outcome: "failed", Tracked: []ArtifactBlob{}, Untracked: []ArtifactBlob{}, Binary: []ArtifactBlob{}, Recovery: []ArtifactBlob{blob}, Deleted: []string{}}
	body, _ := json.Marshal(manifest)
	h := sha256.Sum256(body)
	m := p.Message{Version: p.FencedVersion, Kind: "result", MessageID: newID(), Identity: d.Assignment.Identity, Manifest: &p.Manifest{ManifestID: newID(), SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(body))}}
	before := snapshot(t, f.s)
	r := stopRequest(c.GlobalStop, d)
	if _, err := f.s.RequestStop(ctx, f.owner, r); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := f.s.FenceControlMessage(ctx, f.runner, p.FencedVersion, m); !errors.Is(err, c.ErrFenced) {
			t.Fatal(err)
		}
	}
	bad := m
	copied := *m.Manifest
	bad.Manifest = &copied
	bad.Manifest.Bytes++
	if err := f.s.FenceControlMessage(ctx, f.runner, p.FencedVersion, bad); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("changed result replay", err)
	}
	receipt, err := f.s.CustodyResult(ctx, CustodyRequest{Result: m, Manifest: body, Sources: []ArtifactSource{source}, RetainUntilMS: time.Now().Add(time.Hour).UnixMilli()})
	if err != nil || !receipt.Quarantined || p.CheckAck(m, receipt.Ack, m.Identity, receipt.Receipt) != p.OK {
		t.Fatal(receipt, err)
	}
	rowCount(t, f.s, "artifact_results WHERE current=1", 0)
	rowCount(t, f.s, "artifact_results WHERE quarantined=1", 1)
	rowCount(t, f.s, "control_fenced", 1)
	data, err := os.ReadFile(source.File)
	if err != nil || string(data) != "partial uncommitted work\n" {
		t.Fatal("partial work lost", err)
	}
	if err := verifyBlob(blobPath(f.artifacts, blob.SHA256), blob); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Events, snapshot(t, f.s).Events) {
		t.Fatal("stop rewrote attempt journal")
	}
	retained, err := f.s.Assignment(ctx, d.ID)
	if err != nil || retained.Released {
		t.Fatal("custody released execution", err)
	}
}

func TestControlAuthoritySupersessionUsesSameAtomicLatch(t *testing.T) {
	for _, cause := range []string{"superseded", "expired", "revoked"} {
		t.Run(cause, func(t *testing.T) {
			f, d, _ := controlFixture(t)
			apply := func() error {
				if cause == "superseded" {
					next := cloneGrant(t, f.grant)
					next.ID = newID()
					next.Revision++
					next.Envelope.Paths = []string{"a.txt"}
					_, err := f.s.RestrictExecution(ctx, f.grant.ID, next)
					return err
				}
				if cause == "expired" {
					tx, err := f.s.grantTransaction(ctx)
					if err != nil {
						return err
					}
					defer tx.Rollback()
					if err := expireGrants(ctx, tx, f.grant.Envelope.ExpiresMS); err != nil {
						return err
					}
					return tx.Commit()
				}
				return f.s.InvalidateExecution(ctx, f.grant.TaskID, f.grant.ID, "operator", "revoked")
			}
			sqlExec(t, f.s, "CREATE TRIGGER fail_control BEFORE INSERT ON control_revocations BEGIN SELECT RAISE(ABORT,'interrupted'); END")
			if err := apply(); err == nil {
				t.Fatal("partial authority supersession committed")
			}
			rowCount(t, f.s, "control_stops", 0)
			rowCount(t, f.s, "execution_invalidations", 0)
			sqlExec(t, f.s, "DROP TRIGGER fail_control")
			if err := apply(); err != nil {
				t.Fatal(err)
			}
			v, err := f.s.StopStatus(ctx, dispatchID(f.grant.ID, "authority-stop"), d.Assignment.Identity.AttemptID)
			if err != nil || v.Receipt.Request.Kind != c.AuthoritySupersession || v.Receipt.Request.Cause != cause || v.Status != c.TerminationUnconfirmed {
				t.Fatal(v, err)
			}
			rowCount(t, f.s, "control_revocations", 1)
		})
	}
}

func TestControlConcurrentDispatchAndLeaseRace(t *testing.T) {
	for range 8 {
		f := dispatchFixtureFor(t, nil)
		request := c.Request{ID: newID(), Kind: c.GlobalStop, Cause: "operator"}
		start := make(chan struct{})
		var wg sync.WaitGroup
		var admissionErr, stopErr error
		wg.Go(func() { <-start; _, admissionErr = f.s.Dispatch(ctx, f.request) })
		wg.Go(func() { <-start; _, stopErr = f.s.RequestStop(ctx, f.owner, request) })
		close(start)
		wg.Wait()
		if stopErr != nil {
			t.Fatal(stopErr)
		}
		if admissionErr == nil {
			rowCount(t, f.s, "control_targets", 1)
			if _, err := f.s.Delivery(ctx, f.runner, f.request.ID); err == nil {
				t.Fatal("live admission overlaps committed latch")
			}
		} else {
			requireReason(t, admissionErr, "stop_latched")
			dispatchRows(t, f.s, 0)
		}
		f.request.ID = newID()
		_, err := f.s.Dispatch(ctx, f.request)
		requireReason(t, err, "stop_latched")
	}
	f, d, _ := controlFixture(t)
	req, reply := leaseMessages(f, d)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		<-start
		_, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, req, reply, time.Second)
		if err != nil && !errors.Is(err, c.ErrFenced) {
			t.Error(err)
		}
	})
	wg.Go(func() {
		<-start
		_, err := f.s.RequestStop(ctx, f.owner, stopRequest(c.GlobalStop, d))
		if err != nil {
			t.Error(err)
		}
	})
	close(start)
	wg.Wait()
	rowCount(t, f.s, "control_leases l WHERE NOT EXISTS(SELECT 1 FROM control_revocations r WHERE r.nonce=l.nonce)", 0)
}

func TestControlWriteFailureAndCorruption(t *testing.T) {
	for _, table := range []string{"control_stops", "control_targets", "control_revocations", "control_acks"} {
		t.Run(table, func(t *testing.T) {
			f, d, _ := controlFixture(t)
			r := stopRequest(c.GlobalStop, d)
			sqlExec(t, f.s, "CREATE TRIGGER fail_control BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(ABORT,'interrupted'); END")
			if ack, err := f.s.RequestStop(ctx, f.owner, r); err == nil || ack.Acknowledged != nil {
				t.Fatal("write failure acknowledged", ack, err)
			}
			want := 0
			if table == "control_acks" {
				want = 1
			}
			rowCount(t, f.s, "control_stops", want)
			rowCount(t, f.s, "control_targets", want)
			rowCount(t, f.s, "control_revocations", want)
			rowCount(t, f.s, "control_acks", 0)
			sqlExec(t, f.s, "DROP TRIGGER fail_control")
			if _, err := f.s.RequestStop(ctx, f.owner, r); err != nil {
				t.Fatal(err)
			}
		})
	}
	f, d, _ := controlFixture(t)
	r := stopRequest(c.GlobalStop, d)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.s.RequestStop(cancelled, f.owner, r); err == nil {
		t.Fatal("cancelled context acknowledged")
	}
	sqlExec(t, f.s, "CREATE TABLE pressure(payload BLOB) STRICT; CREATE TRIGGER full_control BEFORE INSERT ON control_targets BEGIN INSERT INTO pressure VALUES(zeroblob(1048576)); END")
	var pages int
	if err := f.s.db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, f.s, fmt.Sprintf("PRAGMA max_page_count=%d", pages))
	_, err := f.s.RequestStop(ctx, f.owner, r)
	var full *sqlite.Error
	if !errors.As(err, &full) || full.Code() != 13 {
		t.Fatal("expected SQLITE_FULL", err)
	}
	rowCount(t, f.s, "control_stops", 0)
	rowCount(t, f.s, "control_revocations", 0)
	sqlExec(t, f.s, "DROP TRIGGER full_control; PRAGMA max_page_count=1073741823")
	if _, err := f.s.RequestStop(ctx, f.owner, r); err != nil {
		t.Fatal(err)
	}
	sqlExec(t, f.s, "DROP TRIGGER control_stops_no_update; UPDATE control_stops SET body='{}'")
	if _, err := f.s.RequestStop(ctx, f.owner, r); err == nil {
		t.Fatal("corrupt replay accepted")
	}
}

func TestControlObservationRequiresExactVerifiedEvidence(t *testing.T) {
	f, d, _ := controlFixture(t)
	r := stopRequest(c.GlobalStop, d)
	if _, err := f.s.RequestStop(ctx, f.owner, r); err != nil {
		t.Fatal(err)
	}
	targets, err := f.s.StopTargets(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	m := targets[0].Cancel
	m.Kind = "terminated"
	m.MessageID = newID()
	m.ConfirmedProcess = "terminated"
	m.RemoteWork = "unknown"
	m.EvidenceDigest = strings.Repeat("a", 64)
	wall := time.Now().UTC()
	duration := int64(time.Millisecond)
	e := c.Evidence{Terminated: m, Measurement: c.Measurement{RequestedAt: wall, AcknowledgedAt: wall, ObservedAt: &wall, RequestToAckNS: duration, AckToObservedNS: &duration}}
	for _, mutate := range []func(*c.Evidence){func(e *c.Evidence) { e.Terminated.StopID = newID() }, func(e *c.Evidence) { e.Terminated.RunnerBoot = newID() }, func(e *c.Evidence) { e.Terminated.DaemonBoot = newID() }, func(e *c.Evidence) { e.Terminated.Identity.Epoch++ }, func(e *c.Evidence) { e.Measurement.ObservedAt = nil }} {
		bad := e
		mutate(&bad)
		if err := f.s.ObserveTermination(ctx, f.owner, p.FencedVersion, bad); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
	if err := f.s.ObserveTermination(ctx, f.runner, p.FencedVersion, e); err == nil {
		t.Fatal("runner self-certified containment")
	}
	if err := f.s.ObserveTermination(ctx, f.owner, p.Version, e); !errors.Is(err, p.UnknownVersion) {
		t.Fatal(err)
	}
	sqlExec(t, f.s, "CREATE TRIGGER fail_observation BEFORE INSERT ON control_observations BEGIN SELECT RAISE(ABORT,'interrupted'); END")
	if err := f.s.ObserveTermination(ctx, f.owner, p.FencedVersion, e); err == nil {
		t.Fatal("observation write failure acknowledged")
	}
	v, err := f.s.StopStatus(ctx, r.ID, d.Assignment.Identity.AttemptID)
	if err != nil || v.Status != c.TerminationUnconfirmed {
		t.Fatal(v, err)
	}
	sqlExec(t, f.s, "DROP TRIGGER fail_observation")
	for range 2 {
		if err := f.s.ObserveTermination(ctx, f.owner, p.FencedVersion, e); err != nil {
			t.Fatal(err)
		}
	}
	v, err = f.s.StopStatus(ctx, r.ID, d.Assignment.Identity.AttemptID)
	if err != nil || v.Status != c.TerminationObserved || v.ConfirmedProcess != "terminated" || v.RemoteWork != "unknown" || !v.Quarantined {
		t.Fatal(v, err)
	}
	rowCount(t, f.s, "control_observations", 1)
	rowCount(t, f.s, "dispatch_releases", 0)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ObserveTermination(ctx, f.owner, p.FencedVersion, e); !errors.Is(err, p.BootMismatch) {
		t.Fatal("old-boot evidence promoted", err)
	}
	v, err = s.StopStatus(ctx, r.ID, d.Assignment.Identity.AttemptID)
	if err != nil || v.Status != c.TerminationUnconfirmed || v.ConfirmedProcess != "unknown" || v.Evidence == nil {
		t.Fatal("history lost or reused", v, err)
	}
}

func ownedControl(t *testing.T, s *Store, mode string) {
	t.Helper()
	var r c.Request
	if err := json.Unmarshal([]byte(os.Getenv("GAFFER_OWNED_TEST_STOP")), &r); err != nil {
		t.Fatal(err)
	}
	s.controlHook = func(step string) error {
		if mode == "control-"+step {
			fmt.Println("interrupted")
			_, _ = bufio.NewReader(os.Stdin).ReadByte()
		}
		return nil
	}
	if _, err := s.RequestStop(ctx, os.Getenv("GAFFER_OWNED_TEST_OWNER"), r); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash test escaped barrier")
}

func TestControlSIGKILLBeforeAcknowledgement(t *testing.T) {
	for _, step := range []string{"before_latch_commit", "after_latch_commit"} {
		t.Run(step, func(t *testing.T) {
			f, d, _ := controlFixture(t)
			r := stopRequest(c.GlobalStop, d)
			t.Setenv("GAFFER_OWNED_TEST_STOP", controlJSON(r))
			t.Setenv("GAFFER_OWNED_TEST_OWNER", f.owner)
			t.Setenv("GAFFER_OWNED_TEST_ARTIFACTS", f.artifacts)
			if err := f.s.Close(); err != nil {
				t.Fatal(err)
			}
			child(t, f.s.dir, "control-"+step, "interrupted", true)
			s, err := Open(ctx, f.s.dir, f.artifacts)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			want := 0
			if step == "after_latch_commit" {
				want = 1
			}
			rowCount(t, s, "control_stops", want)
			rowCount(t, s, "control_targets", want)
			rowCount(t, s, "control_revocations", want)
			rowCount(t, s, "control_acks", 0)
			ack, err := s.RequestStop(ctx, f.owner, r)
			if err != nil || ack.Acknowledged == nil {
				t.Fatal(ack, err)
			}
			if want == 1 && ack.Acknowledged.RequestToAckNS != nil {
				t.Fatal("invented cross-boot duration")
			}
			replay, err := s.RequestStop(ctx, f.owner, r)
			if err != nil || !reflect.DeepEqual(ack, replay) {
				t.Fatal("lost-ack replay changed", err)
			}
			f.request.ID = newID()
			_, err = s.Dispatch(ctx, f.request)
			requireReason(t, err, "stop_latched")
		})
	}
}

func TestControlSchemaSevenMigration(t *testing.T) {
	f := dispatchFixtureFor(t, nil)
	d := admitted(t, f)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(f.s.dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(dropVerificationSchema + dropControlSchema + "PRAGMA user_version=7"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.meta.SchemaVersion != 9 {
		t.Fatal(s.meta)
	}
	retained, err := s.Assignment(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(retained.Assignment, d.Assignment) {
		t.Fatal("migration lost assignment", err)
	}
	if _, err := s.RequestStop(ctx, f.owner, stopRequest(c.GlobalStop, d)); err != nil {
		t.Fatal(err)
	}
}

func TestControlLeaseReplayAndMaximumIssuanceBarrier(t *testing.T) {
	f, d, first := controlFixture(t)
	for range 2 {
		v, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, first.Request, first.Reply, time.Duration(first.MarginNS))
		if err != nil || !reflect.DeepEqual(v, first) {
			t.Fatal("lease replay extended bound", v, err)
		}
	}
	bad := first.Reply
	validity := int64(1000)
	bad.ValidityMS = &validity
	if _, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, first.Request, bad, time.Duration(first.MarginNS)); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("nonce changed", err)
	}
	req, reply := leaseMessages(f, d)
	req.MessageID = first.Request.MessageID
	if _, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, req, reply, time.Millisecond); !errors.Is(err, p.IdentityConflict) {
		t.Fatal("message alias", err)
	}
	req.MessageID = newID()
	reply.ValidityMS = &validity
	second, err := f.s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, req, reply, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if second.DeadlineNS >= first.DeadlineNS {
		t.Fatal("fixture did not shorten renewal")
	}
	stop := stopRequest(c.GlobalStop, d)
	if _, err := f.s.RequestStop(ctx, f.owner, stop); err != nil {
		t.Fatal(err)
	}
	f.s.controlNow = func() time.Time {
		return f.s.controlStart.Add(time.Duration(second.DeadlineNS+second.MarginNS) + time.Nanosecond)
	}
	v, err := f.s.StopStatus(ctx, stop.ID, d.Assignment.Identity.AttemptID)
	if err != nil || v.Status != c.TerminationUnconfirmed {
		t.Fatal("shorter renewal erased prior bound", v, err)
	}
	rowCount(t, f.s, "control_leases", 2)
	rowCount(t, f.s, "control_revocations", 2)
	if err := f.s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, f.s.dir, f.artifacts)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.RecordControlLease(ctx, f.owner, d.ID, p.FencedVersion, first.Request, first.Reply, time.Duration(first.MarginNS)); !errors.Is(err, p.BootMismatch) {
		t.Fatal("old boot renewed", err)
	}
	rowCount(t, s, "control_fenced", 1)
}

func TestControlPreDispatchLatchAndOwnerBoundary(t *testing.T) {
	for _, kind := range []c.Kind{c.PauseTask, c.GlobalStop} {
		t.Run(string(kind), func(t *testing.T) {
			f := dispatchFixtureFor(t, nil)
			request := c.Request{ID: newID(), Kind: kind, Cause: "operator"}
			if kind == c.PauseTask {
				request.TaskID = f.grant.TaskID
			}
			if _, err := f.s.RequestStop(ctx, f.runner, request); err == nil {
				t.Fatal("runner created operator latch")
			}
			rowCount(t, f.s, "control_stops", 0)
			if _, err := f.s.RequestStop(ctx, f.owner, request); err != nil {
				t.Fatal(err)
			}
			_, err := f.s.Dispatch(ctx, f.request)
			requireReason(t, err, "stop_latched")
			dispatchRows(t, f.s, 0)
			rowCount(t, f.s, "control_targets", 0)
			other := otherTaskRequest(t, f)
			_, err = f.s.Dispatch(ctx, other)
			if kind == c.GlobalStop {
				requireReason(t, err, "stop_latched")
			} else if err != nil {
				t.Fatal("task pause suppressed unrelated task", err)
			}
		})
	}
}
