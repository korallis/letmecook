package notify

import (
	"sync"
	"testing"
)

func TestHubCoalescesAndCancels(t *testing.T) {
	var h Hub
	ch, cancel := h.Subscribe("runner")
	all, stopAll := h.Subscribe("")
	defer stopAll()
	other, stopOther := h.Subscribe("other")
	defer stopOther()
	h.Notify("runner")
	h.Notify("runner")
	for _, target := range []<-chan struct{}{ch, all} {
		select {
		case <-target:
		default:
			t.Fatal("missed wakeup")
		}
		select {
		case <-target:
			t.Fatal("did not coalesce")
		default:
		}
	}
	select {
	case <-other:
		t.Fatal("woke unrelated subscriber")
	default:
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 100 {
				h.Notify("runner")
			}
		})
	}
	wg.Go(func() { cancel(); cancel() })
	wg.Wait()
	// Cancel may leave one buffered hint before the closed channel.
	for range ch {
	}
	if len(h.subscribers["runner"]) != 0 {
		t.Fatal("subscriber leaked")
	}
	var absent *Hub
	done, noOp := absent.Subscribe("runner")
	noOp()
	absent.Notify("runner")
	if _, ok := <-done; ok {
		t.Fatal("nil hub open")
	}
}
