package fake

import (
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	h "github.com/korallis/letmecook/internal/harness"
)

func TestScannerFinishesWithoutConsumerAndRetainsFullOutput(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(map[bool]string{false: "consumer_stopped", true: "retention_full"}[large], func(t *testing.T) {
			count := "256"
			if large {
				count = "20000"
			}
			// More than the old 16-event channel capacity, through real OS pipes. No
			// consumer runs until both scanners have completed, as after spool_full.
			cmd := exec.Command("/usr/bin/awk", "BEGIN { for(i=0;i<"+count+";i++) { printf \"{\\\"kind\\\":\\\"activity\\\",\\\"text\\\":\\\"\"; for(j=0;j<1024;j++) printf \"x\"; print \"\\\"}\" } }")
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { cmd.Process.Kill() })
			stream := &events{changed: make(chan struct{}), done: make(chan struct{})}
			finished := make(chan struct{})
			go func() {
				stream.scan(stdout, false)
				cmd.Wait()
				stream.mu.Lock()
				close(stream.done)
				stream.notify()
				stream.mu.Unlock()
				close(finished)
			}()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("scanner stranded without event consumer")
			}
			if stream.bytes > maxRetainedBytes || stream.overflow != large {
				t.Fatalf("retention bytes=%d overflow=%v", stream.bytes, stream.overflow)
			}
			adapter := New("unused")
			adapter.runs["run"] = stream
			if err := adapter.Release(context.Background(), h.RunHandle{ID: "run"}, 0); err == nil {
				t.Fatal("incomplete run released")
			}
			var last int64
			for {
				ev, err := stream.Next(context.Background())
				if err == io.EOF {
					break
				}
				if err != nil {
					if !large {
						t.Fatal(err)
					}
					break
				}
				last = ev.Sequence
			}
			err = adapter.Release(context.Background(), h.RunHandle{ID: "run"}, last)
			if large {
				if err == nil || len(adapter.runs) != 1 {
					t.Fatal("full output discarded to release")
				}
			} else if err != nil || len(adapter.runs) != 0 {
				t.Fatal("completed run not pruned", err)
			}
		})
	}
}
