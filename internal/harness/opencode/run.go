package opencode

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/harness"
)

type run struct {
	mu              sync.Mutex
	handle          harness.RunHandle
	cmd             *exec.Cmd
	events          []harness.Event
	changed         chan struct{}
	ended, released bool
	done            chan struct{}
	bytes, maxBytes int64
	token           string
	started         bool
	failure         string
	completion      *harness.Event
}

func (a *Adapter) launch(req harness.RunRequest, config, model string) (harness.RunHandle, error) {
	cmd := exec.Command(a.binary, argv(req.Brief, model, req.Workspace.Root)...)
	cmd.Dir = req.Workspace.Root
	cmd.Env = environment(req.Workspace, config, req.Boundary.Token)
	stdout, out, err := os.Pipe()
	if err != nil {
		return harness.RunHandle{}, ErrRun
	}
	stderr, errout, err := os.Pipe()
	if err != nil {
		stdout.Close()
		out.Close()
		return harness.RunHandle{}, ErrRun
	}
	cmd.Stdout = out
	cmd.Stderr = errout
	// Wrap is the last command mutation. A guardian launcher owns Stdin,
	// ExtraFiles, SysProcAttr and cancellation; never use exec.CommandContext.
	err = req.Launcher.Wrap(cmd)
	started := time.Now().UnixNano()
	if err == nil {
		err = cmd.Start()
	}
	out.Close()
	errout.Close()
	if err != nil {
		stdout.Close()
		stderr.Close()
		return harness.RunHandle{}, ErrRun
	}
	id := rand.Text()
	handle := harness.RunHandle{ID: id, SessionID: id, PID: cmd.Process.Pid, PGID: processGroup(cmd.Process.Pid), StartUnixNS: started}
	r := &run{handle: handle, cmd: cmd, changed: make(chan struct{}), done: make(chan struct{}), maxBytes: req.Limits.MaxStdoutBytes, token: req.Boundary.Token}
	a.mu.Lock()
	a.runs[id] = r
	a.mu.Unlock()
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); defer stdout.Close(); r.read(stdout, false) }()
	go func() { defer readers.Done(); defer stderr.Close(); r.read(stderr, true) }()
	go func() {
		err := cmd.Wait()
		// EOF on both pipes precedes the terminal event. Native stderr after the
		// final model step must be spooled too, and a step alone is not process exit.
		readers.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		defer close(r.done)
		if r.ended {
			return
		}
		event := harness.Event{Kind: harness.Failed, At: time.Now().UTC(), Summary: "opencode exited without completion"}
		if r.failure != "" {
			event.Summary = r.failure
		} else if err != nil {
			event.Summary = "opencode exited unsuccessfully"
		} else if r.completion != nil {
			event = *r.completion
		}
		r.appendLocked(event)
		r.ended = true
		close(r.changed)
		r.changed = make(chan struct{})
	}()
	return handle, nil
}
func (r *run) appendLocked(event harness.Event) {
	event.Sequence = int64(len(r.events) + 1)
	r.events = append(r.events, event)
	close(r.changed)
	r.changed = make(chan struct{})
}
func (r *run) read(reader io.Reader, native bool) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 32*1024), int(r.maxBytes)+1)
	scanner.Split(scanLineWithEnding)
	for scanner.Scan() {
		raw := scanner.Bytes()
		r.mu.Lock()
		r.bytes += int64(len(raw))
		if r.bytes > r.maxBytes {
			r.failLocked("opencode output limit exceeded")
			r.mu.Unlock()
			// Keep draining pipes until the supervisor cancels. Never block a guardian
			// behind a full output pipe or mistake an output failure for termination.
			_, _ = io.Copy(io.Discard, reader)
			return
		}
		// Tokens deliberately never enter returned raw/native events, even if a tool
		// prints its environment. Gateway credentials never enter this process.
		redacted := bytes.ReplaceAll(raw, []byte(r.token), []byte("[redacted-attempt-token]"))
		escaped, _ := json.Marshal(r.token)
		redacted = bytes.ReplaceAll(redacted, escaped[1:len(escaped)-1], []byte("[redacted-attempt-token]"))
		if !r.ended {
			if native {
				for len(redacted) > 0 {
					n := min(len(redacted), 8192)
					encoded, _ := json.Marshal(string(redacted[:n]))
					r.appendLocked(harness.Event{Kind: harness.Activity, At: time.Now().UTC(), Summary: "opencode stderr", Raw: encoded, Native: true})
					redacted = redacted[n:]
				}
			} else {
				event, err := ParseEvent(bytes.TrimSuffix(bytes.TrimSuffix(redacted, []byte("\n")), []byte("\r")))
				if err != nil {
					r.failLocked("opencode malformed event")
				} else {
					if event.Kind == harness.Started {
						if r.started {
							event.Kind = harness.Activity
						}
						r.started = true
					}
					if event.Kind == harness.Completed {
						r.completion = &event
					} else if event.Kind == harness.Failed {
						r.failure = event.Summary
						r.appendLocked(harness.Event{Kind: harness.Activity, At: event.At, Summary: event.Summary, Raw: event.Raw})
					} else {
						r.appendLocked(event)
					}
				}
			}
		}
		r.mu.Unlock()
	}
	if scanner.Err() != nil {
		r.mu.Lock()
		r.failLocked("opencode output unreadable or oversized")
		r.mu.Unlock()
		_, _ = io.Copy(io.Discard, reader)
	}
}
func (r *run) failLocked(reason string) {
	if r.ended {
		return
	}
	r.failure = reason
	r.appendLocked(harness.Event{Kind: harness.Failed, At: time.Now().UTC(), Summary: reason})
	r.ended = true
	close(r.changed)
	r.changed = make(chan struct{})
}

type eventStream struct {
	run   *run
	after int64
}

func (s *eventStream) Next(ctx context.Context) (harness.Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return harness.Event{}, err
		}
		s.run.mu.Lock()
		if s.run.released {
			s.run.mu.Unlock()
			return harness.Event{}, ErrHandle
		}
		if s.after < int64(len(s.run.events)) {
			event := s.run.events[s.after]
			s.after = event.Sequence
			event.Raw = append(json.RawMessage(nil), event.Raw...)
			if event.Usage != nil {
				copy := *event.Usage
				event.Usage = &copy
			}
			s.run.mu.Unlock()
			return event, nil
		}
		ended, changed := s.run.ended, s.run.changed
		s.run.mu.Unlock()
		if ended {
			return harness.Event{}, io.EOF
		}
		select {
		case <-ctx.Done():
			return harness.Event{}, ctx.Err()
		case <-changed:
		}
	}
}

func scanLineWithEnding(data []byte, eof bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if eof && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
