package store

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
	"github.com/korallis/letmecook/internal/runstream"
	p "github.com/korallis/letmecook/schemas/execution"
)

func TestS1Review4AppendAdmittedBeforeCancelLandsAfter(t *testing.T) {
	testAppendBeforeCancellation(t, false)
}

func TestIntReviewAppendAdmittedBeforeCancelLandsWhilePinned(t *testing.T) {
	testAppendBeforeCancellation(t, true)
}

func testAppendBeforeCancellation(t *testing.T, fenceFirst bool) {
	t.Helper()
	x := executionFixtureFor(t, nil)
	lease := x.lease(t)
	x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
	x.propose(t, p.Running, launchedEvidence())
	attempt := x.d.Assignment.Identity.AttemptID
	stopID := execwire.ExpiryStopID(lease.Request.Nonce)
	if !fenceFirst {
		stop := stopRequest(c.CancelAttempt, x.d)
		stopID = stop.ID
		if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
			t.Fatal(err)
		}
		x.propose(t, p.Stopping, RuntimeEvidence{Kind: "stop"})
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var fired atomic.Bool
	x.s.controlHook = func(step string) error {
		if step == "append_stream_io" && fired.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	records := spooledRecords(t, x.d.Assignment.Identity, "late")
	appendDone := make(chan error, 1)
	go func() {
		ack, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records)
		if err == nil && ack.Through != 1 {
			err = errors.New("admitted append did not reach watermark 1")
		}
		appendDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("append did not reach pre-I/O barrier")
	}
	evidence := terminatedFor(&x, stopID, "quiescent")
	cancelDone := make(chan error, 1)
	go func() {
		if fenceFirst {
			if _, err := x.s.FenceAttempt(ctx, attempt, "lease_expired"); err != nil {
				cancelDone <- err
				return
			}
		}
		reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, evidence, quiescent())
		if err == nil && !reply.Released {
			err = errors.New("quiescent cancellation did not release")
		}
		cancelDone <- err
	}()
	select {
	case err := <-cancelDone:
		t.Fatal("cancellation passed an admitted append before I/O", err)
	case <-time.After(100 * time.Millisecond):
	}
	// A cancellation waiting on the attempt must not hold s.mu: unrelated
	// control reads still complete, proving the established lock order.
	readDone := make(chan error, 1)
	go func() {
		_, err := x.s.ExecutionSession(ctx, x.runner, x.session.SessionID)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation held the store lock while waiting for append")
	}
	unblock()
	for _, done := range []chan error{appendDone, cancelDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("append/cancellation failed to complete")
		}
	}
	x.s.controlHook = nil
	state, _ := attemptRow(t, x.s, attempt)
	if state != p.Cancelled && state != p.Expired {
		t.Fatal("not terminal", state)
	}
	if _, err := x.s.SetPaused(ctx, x.owner, newID(), true, "backup", false); err != nil {
		t.Fatal(err)
	}
	unpin, err := x.s.PinArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	defer unpin()
	before, err := x.s.Streams().Watermark(attempt)
	if err != nil || before.Through != 1 {
		t.Fatal(before, err)
	}
	// No append is left in flight when terminalization makes the pin legal.
	after, err := x.s.Streams().Watermark(attempt)
	if err != nil || after != before {
		t.Fatal("watermark changed under backup pin", before, after, err)
	}
	unpin()
	if _, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records); err != nil {
		t.Fatal("retained replay refused", err)
	}
	if _, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, spooledRecords(t, x.d.Assignment.Identity, "late", "new")[1:]); !errors.Is(err, p.StaleAttempt) {
		t.Fatal("new append after terminalization accepted", err)
	}
}

func TestS1Review4LockOrderingStress(t *testing.T) {
	x := executionFixtureFor(t, nil)
	x.run(t, "seed")
	attempt := x.d.Assignment.Identity.AttemptID
	var wg sync.WaitGroup
	stopCh := make(chan struct{})
	noise := spooledRecords(t, x.d.Assignment.Identity, "n")
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
				}
				// Foreign attempts: distinct locks, admission refused (unknown
				// attempt) but the lock path is exercised.
				_, _ = x.s.AppendStream(ctx, x.runner, x.session.SessionID, newID(), noise)
				_, _ = x.s.ExecutionSession(ctx, x.runner, x.session.SessionID)
			}
		}()
	}
	_, custody := x.upload(t, "succeeded", map[string]string{"a.txt": "bytes"})
	reply, err := x.s.FinalizeAttempt(ctx, x.runner, x.session.SessionID, attempt, x.completion(t, custody.Receipt.ReceiptID, 1))
	close(stopCh)
	wg.Wait()
	if err != nil || reply.Outcome != "succeeded" {
		t.Fatal(reply, err)
	}
}

// Every recovery writer joins the attempt lock before taking the store lock.
// In particular, a finalizer must see an append's durable watermark, not the
// stale exit watermark that was current when that append was admitted.
func TestReconcileWritersSerializeAdmittedAppend(t *testing.T) {
	t.Run("ReleaseAttempt", func(t *testing.T) {
		x := executionFixtureFor(t, nil)
		attempt := x.d.Assignment.Identity.AttemptID
		lease := x.lease(t)
		x.propose(t, p.Starting, launchIntent(lease.Request.Nonce))
		x.propose(t, p.Running, launchedEvidence())
		stop := stopRequest(c.CancelAttempt, x.d)
		if _, err := x.s.RequestStop(ctx, x.owner, stop); err != nil {
			t.Fatal(err)
		}
		// From running this retains verified evidence but cannot itself release.
		if reply, err := x.s.ReportTermination(ctx, x.runner, x.session.SessionID, p.FencedVersion, terminatedFor(&x, stop.ID, "quiescent"), quiescent()); err != nil || reply.Released {
			t.Fatal(reply, err)
		}
		records := spooledRecords(t, x.d.Assignment.Identity, "late")
		err := appendBeforeReconcile(t, &x, records, 1, func() error {
			out, err := x.s.ReleaseAttempt(ctx, attempt, ReleaseBasis{Kind: BasisObservation, StopID: stop.ID})
			if err == nil && out.Proof.To != p.Cancelled {
				return errors.New("verified reconcile release did not cancel")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		assertReconcileTerminalStream(t, &x, records)
	})
	for _, replay := range []bool{false, true} {
		name := "new-record-refuses-stale-exit"
		if replay {
			name = "retained-replay-finalizes"
		}
		t.Run("CompleteFinalization/"+name, func(t *testing.T) {
			x := executionFixtureFor(t, nil)
			x.run(t, "seed")
			attempt := x.d.Assignment.Identity.AttemptID
			_, custody := x.upload(t, "succeeded", map[string]string{"result.txt": "result"})
			attested := &BoundaryAttestation{RunnerBoot: x.facts.RunnerBoot, ReceiptID: custody.Receipt.ReceiptID, Boundary: quiescent()}
			records := spooledRecords(t, x.d.Assignment.Identity, "seed", "late")
			through := int64(2)
			batch := records[1:]
			if replay {
				through, batch = 1, records[:1]
			}
			err := appendBeforeReconcile(t, &x, batch, through, func() error {
				reply, err := x.s.CompleteFinalization(ctx, attempt, attested)
				if err == nil && (!reply.Released || reply.Outcome != "succeeded") {
					return errors.New("verified finalization did not succeed")
				}
				return err
			})
			if replay {
				if err != nil {
					t.Fatal(err)
				}
				assertReconcileTerminalStream(t, &x, batch)
				return
			}
			if err == nil || !strings.Contains(err.Error(), "reconciliation_required: stream") {
				t.Fatal("finalizer did not recheck the durable append watermark", err)
			}
			if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
				t.Fatal("stale exit evidence terminalized the attempt", state)
			}
			rcReleases(t, x.s, 0)
		})
	}
	t.Run("RecoverResultPending", func(t *testing.T) {
		x := executionFixtureFor(t, nil)
		x.run(t, "seed")
		reopen(t, &x)
		x.session = sessionFor(t, x.dispatchFixture)
		attempt := x.d.Assignment.Identity.AttemptID
		records := spooledRecords(t, x.d.Assignment.Identity, "seed")
		err := appendBeforeReconcile(t, &x, records, 1, func() error {
			m, err := x.s.RecoverResultPending(ctx, attempt, execwire.Journal{})
			if err == nil && (m.From != p.Unknown || m.To != p.ResultPending) {
				return errors.New("recovery did not restore result_pending")
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if state, _ := attemptRow(t, x.s, attempt); state != p.ResultPending {
			t.Fatal(state)
		}
		rcReleases(t, x.s, 0)
	})
}

func appendBeforeReconcile(t *testing.T, x *executionFixture, records []runstream.Record, through int64, mutate func() error) error {
	t.Helper()
	attempt := x.d.Assignment.Identity.AttemptID
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	var fired atomic.Bool
	x.s.controlHook = func(step string) error {
		if step == "append_stream_io" && fired.CompareAndSwap(false, true) {
			close(entered)
			<-release
		}
		return nil
	}
	appendDone := make(chan error, 1)
	go func() {
		ack, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records)
		if err == nil && ack.Through != through {
			err = errors.New("append did not reach the expected watermark")
		}
		appendDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("append did not reach pre-I/O barrier")
	}
	mutated := make(chan error, 1)
	go func() { mutated <- mutate() }()
	select {
	case err := <-mutated:
		unblock()
		<-appendDone
		t.Fatal("reconcile writer passed an admitted append before I/O", err)
	case <-time.After(100 * time.Millisecond):
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := x.s.ExecutionSession(ctx, x.runner, x.session.SessionID)
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile writer held the store lock while waiting for append")
	}
	unblock()
	select {
	case err := <-appendDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("append failed to complete")
	}
	var mutationErr error
	select {
	case mutationErr = <-mutated:
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile writer failed to complete")
	}
	x.s.controlHook = nil
	watermark, err := x.s.Streams().Watermark(attempt)
	if err != nil || watermark.Through != through {
		t.Fatal(watermark, err)
	}
	return mutationErr
}

func assertReconcileTerminalStream(t *testing.T, x *executionFixture, records []runstream.Record) {
	t.Helper()
	attempt := x.d.Assignment.Identity.AttemptID
	if state, _ := attemptRow(t, x.s, attempt); !rcTerminal(state) {
		t.Fatal("not terminal", state)
	}
	rcReleases(t, x.s, 1)
	if _, err := x.s.SetPaused(ctx, x.owner, newID(), true, "backup", false); err != nil {
		t.Fatal(err)
	}
	unpin, err := x.s.PinArtifacts()
	if err != nil {
		t.Fatal(err)
	}
	defer unpin()
	before, err := x.s.Streams().Watermark(attempt)
	if err != nil || before.Through != records[len(records)-1].Sequence {
		t.Fatal(before, err)
	}
	if after, err := x.s.Streams().Watermark(attempt); err != nil || after != before {
		t.Fatal("watermark changed under backup pin", before, after, err)
	}
	unpin()
	if _, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, records); err != nil {
		t.Fatal("retained replay refused", err)
	}
	next := records[len(records)-1]
	next.Sequence++
	if _, err := x.s.AppendStream(ctx, x.runner, x.session.SessionID, attempt, []runstream.Record{next}); !errors.Is(err, p.StaleAttempt) {
		t.Fatal("new record accepted after terminalization", err)
	}
}
