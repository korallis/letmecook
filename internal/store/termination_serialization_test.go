package store

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	c "github.com/korallis/letmecook/internal/control"
	"github.com/korallis/letmecook/internal/execwire"
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
