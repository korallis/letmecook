package fake

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDeleteModeHonorsEachEditDiscriminator(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed-delete-and-content", true: "missing-delete"}[missing], func(t *testing.T) {
			root := t.TempDir()
			if !missing {
				if err := os.WriteFile(filepath.Join(root, "obsolete.txt"), []byte("old"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("before"), 0600); err != nil {
				t.Fatal(err)
			}
			spec := jobSpec{Root: root, Settings: Settings{Mode: "delete", Edits: []Edit{{Path: "obsolete.txt", Delete: true}, {Path: "README.md", Content: "after"}}}}
			raw, _ := json.Marshal(spec)
			path := filepath.Join(t.TempDir(), "spec.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			code := Job(path, &output)
			if missing {
				if code != 4 || !bytes.Contains(output.Bytes(), []byte("edit_failed")) {
					t.Fatal(code, output.String())
				}
				return
			}
			if code != 0 {
				t.Fatal(code, output.String())
			}
			if _, err := os.Stat(filepath.Join(root, "obsolete.txt")); !os.IsNotExist(err) {
				t.Fatal("delete not applied", err)
			}
			if got, err := os.ReadFile(filepath.Join(root, "README.md")); err != nil || string(got) != "after" {
				t.Fatal("content edit was deleted", string(got), err)
			}
		})
	}
}
