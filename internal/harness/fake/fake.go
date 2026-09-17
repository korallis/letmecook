// Package fake implements deterministic jobs as real sandboxed subprocesses.
package fake

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/closedjson"
	h "github.com/korallis/letmecook/internal/harness"
)

type Edit struct {
	Path          string `json:"path"`
	Content       string `json:"content,omitempty"`
	ContentBase64 string `json:"content_base64,omitempty"`
	Delete        bool   `json:"delete,omitempty"`
}
type Settings struct {
	Mode        string `json:"mode"`
	Edits       []Edit `json:"edits,omitempty"`
	DelayMS     int64  `json:"delay_ms,omitempty"`
	StreamBytes int64  `json:"stream_bytes,omitempty"`
}
type Script struct {
	Attempts []Settings `json:"attempts"`
}

func Decode(data []byte, attempt int) (Settings, error) {
	var s Settings
	if len(data) == 0 {
		return Settings{Mode: "noop"}, nil
	}
	if err := closedjson.Decode(data, &s, 65536, nil); err != nil {
		var script Script
		if e := closedjson.Decode(data, &script, 65536, nil); e != nil || attempt < 0 || len(script.Attempts) == 0 {
			return s, errors.New("invalid fake settings")
		}
		s = script.Attempts[min(attempt, len(script.Attempts)-1)]
	}
	if !strings.Contains("|edit|noop|delete|create_empty|binary_edit|hang|crash|approval|ignore_term|huge_output|exit_nonzero|fork_child|detached_child|crash_after_edit|", "|"+s.Mode+"|") || s.Mode == "" || s.DelayMS < 0 || s.DelayMS > 300000 || s.StreamBytes < 0 || s.StreamBytes > 64<<20 {
		return s, errors.New("invalid fake settings")
	}
	for _, e := range s.Edits {
		if e.Path == "" || !filepath.IsLocal(e.Path) || filepath.Clean(e.Path) != e.Path || strings.Contains(e.Path, "\\") || e.Path == ".git" || strings.HasPrefix(e.Path, ".git/") {
			return s, errors.New("invalid fake edit path")
		}
		if e.ContentBase64 != "" {
			if _, err := base64.StdEncoding.DecodeString(e.ContentBase64); err != nil {
				return s, err
			}
		}
	}
	return s, nil
}

const MaxRetainedRuns = 8
const maxRetainedBytes = 8 << 20

type Harness struct {
	Binary string
	mu     sync.Mutex
	runs   map[string]*events
}

func New(binary string) *Harness { return &Harness{Binary: binary, runs: map[string]*events{}} }
func (f *Harness) Describe(context.Context) (h.Descriptor, error) {
	b, e := os.ReadFile(f.Binary)
	if e != nil {
		return h.Descriptor{}, e
	}
	d := sha256.Sum256(b)
	return h.Descriptor{Name: "fake", Version: "fake-v1", BinaryDigest: hex.EncodeToString(d[:]), Protocols: []string{"responses"}, StructuredOutput: true, Approval: true, Sandbox: true}, nil
}
func (f *Harness) Probe(ctx context.Context, r h.ProbeRequest) (h.ProbeResult, error) {
	d, e := f.Describe(ctx)
	return h.ProbeResult{Descriptor: d, Compatible: e == nil, ConfigIsolated: true, Limitations: []string{"fake harness; no gateway or inference used"}}, e
}

type jobSpec struct {
	Root     string   `json:"root"`
	Settings Settings `json:"settings"`
}

func (f *Harness) Start(ctx context.Context, r h.RunRequest) (h.RunHandle, error) {
	if err := ctx.Err(); err != nil {
		return h.RunHandle{}, err
	}
	if r.Launcher == nil {
		return h.RunHandle{}, errors.New("launcher required")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.runs) >= MaxRetainedRuns || f.runs[r.Identity.AttemptID] != nil {
		return h.RunHandle{}, errors.New("fake retained-run limit or duplicate identity")
	}
	s, e := Decode(r.Settings, 0)
	if e != nil {
		return h.RunHandle{}, e
	}
	b, _ := json.Marshal(jobSpec{r.Workspace.Root, s})
	spec := filepath.Join(r.Workspace.RuntimeDir, "fake-spec.json")
	if e = os.WriteFile(spec, b, 0600); e != nil {
		return h.RunHandle{}, e
	}
	cmd := exec.Command(f.Binary, "fake-job", "--spec", spec)
	cmd.Dir = r.Workspace.Root
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + r.Workspace.PrivateHome, "XDG_CONFIG_HOME=" + r.Workspace.PrivateHome + "/.config", "XDG_DATA_HOME=" + r.Workspace.PrivateHome + "/.local/share", "XDG_STATE_HOME=" + r.Workspace.PrivateHome + "/.local/state", "XDG_CACHE_HOME=" + r.Workspace.PrivateHome + "/.cache", "TMPDIR=" + r.Workspace.TempDir, "TERM=dumb", "NO_COLOR=1"}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return h.RunHandle{}, e
	}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		stdout.Close()
		return h.RunHandle{}, e
	}
	if e = r.Launcher.Wrap(cmd); e != nil {
		stdout.Close()
		stderr.Close()
		return h.RunHandle{}, e
	}
	if e = cmd.Start(); e != nil {
		stdout.Close()
		stderr.Close()
		return h.RunHandle{}, e
	}
	stream := &events{changed: make(chan struct{}), done: make(chan struct{})}
	id := r.Identity.AttemptID
	f.runs[id] = stream
	go func() {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); stream.scan(stdout, false) }()
		go func() { defer wg.Done(); stream.scan(stderr, true) }()
		wg.Wait()
		err := cmd.Wait()
		if err != nil {
			stream.append(item{event: h.Event{Kind: h.Failed, At: time.Now(), Summary: "harness_crash", Raw: json.RawMessage(`{"kind":"failed","reason":"harness_crash"}`)}})
		}
		stream.mu.Lock()
		close(stream.done)
		stream.notify()
		stream.mu.Unlock()
	}()
	return h.RunHandle{ID: id, PID: cmd.Process.Pid, PGID: cmd.Process.Pid}, nil
}
func (f *Harness) Events(ctx context.Context, r h.RunHandle, after int64) (h.EventStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.runs[r.ID]
	if v == nil || after != 0 {
		return nil, errors.New("fake events not resumable; replay the durable spool")
	}
	return v, nil
}
func (f *Harness) Cancel(context.Context, h.RunHandle) (h.CancelResult, error) {
	return h.CancelResult{ConfirmedProcess: "unknown", RemoteWork: "unknown"}, errors.New("supervisor termination required")
}

type item struct {
	event h.Event
	err   error
}
type events struct {
	mu            sync.Mutex
	rows          []item
	next          int
	bytes         int
	overflow      bool
	changed, done chan struct{}
}

func (e *events) notify() { close(e.changed); e.changed = make(chan struct{}) }
func (e *events) append(v item) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.overflow {
		return false
	}
	n := len(v.event.Raw) + len(v.event.Summary) + 256
	if e.bytes+n > maxRetainedBytes {
		e.overflow = true
		e.rows = append(e.rows, item{err: errors.New("fake output retention full")})
		e.notify()
		return false
	}
	e.bytes += n
	e.rows = append(e.rows, v)
	e.notify()
	return true
}
func (e *events) Next(ctx context.Context) (h.Event, error) {
	for {
		if err := ctx.Err(); err != nil {
			return h.Event{}, err
		}
		e.mu.Lock()
		if e.next < len(e.rows) {
			v := e.rows[e.next]
			e.next++
			v.event.Sequence = int64(e.next)
			e.mu.Unlock()
			return v.event, v.err
		}
		select {
		case <-e.done:
			e.mu.Unlock()
			return h.Event{}, io.EOF
		default:
		}
		changed := e.changed
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return h.Event{}, ctx.Err()
		case <-changed:
		}
	}
}

// Release never drops bytes to make a full spool look complete. Full/error runs
// remain bounded and retained; normal completed runs are pruned after custody.
func (f *Harness) Release(ctx context.Context, handle h.RunHandle, through int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.runs[handle.ID]
	if e == nil {
		return errors.New("unknown fake run")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.done:
	default:
		return errors.New("fake run still active")
	}
	if e.overflow || through != int64(len(e.rows)) || e.next != len(e.rows) {
		return errors.New("fake output not fully retained")
	}
	delete(f.runs, handle.ID)
	return nil
}
func (e *events) scan(r io.ReadCloser, stderr bool) {
	defer r.Close()
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), h.MaxRawBytes)
	s.Split(func(data []byte, eof bool) (int, []byte, error) {
		if n := bytes.IndexByte(data, '\n'); n >= 0 {
			return n + 1, data[:n+1], nil
		}
		if eof && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for s.Scan() {
		raw := append([]byte(nil), s.Bytes()...)
		var v struct {
			Kind string `json:"kind"`
			Text string `json:"text,omitempty"`
			PID  int    `json:"pid,omitempty"`
		}
		native := stderr
		if stderr || json.Unmarshal(raw, &v) != nil {
			native = true
			b, _ := json.Marshal(string(raw))
			raw = b
			v.Kind = h.Activity
			v.Text = "stderr"
		}
		if !e.append(item{event: h.Event{Kind: v.Kind, At: time.Now(), Summary: v.Text, Raw: raw, Native: native}}) {
			return
		}
	}
	if err := s.Err(); err != nil {
		e.append(item{err: err})
	}
}

// Job executes one trusted synthetic spec inside the launcher. It is not an API
// for accepting network commands; the supervisor owns the spec and workspace.
func Job(path string, out io.Writer) int {
	b, e := os.ReadFile(path)
	var spec jobSpec
	if e != nil || closedjson.Decode(b, &spec, 65536, nil) != nil {
		return 2
	}
	sb, _ := json.Marshal(spec.Settings)
	s, e := Decode(sb, 0)
	if e != nil {
		return 2
	}
	root, e := filepath.EvalSymlinks(spec.Root)
	if e != nil {
		return 2
	}
	enc := json.NewEncoder(out)
	emit := func(kind, text string) {
		_ = enc.Encode(struct {
			Kind string `json:"kind"`
			Text string `json:"text,omitempty"`
		}{kind, text})
	}
	if s.Mode == "ignore_term" {
		signal.Ignore(syscall.SIGTERM)
	}
	emit(h.Started, "fake started")
	if s.DelayMS > 0 {
		time.Sleep(time.Duration(s.DelayMS) * time.Millisecond)
	}
	switch s.Mode {
	case "hang", "ignore_term":
		for {
			time.Sleep(time.Second)
		}
	case "approval":
		emit(h.ApprovalRequired, "approval_required")
		for {
			time.Sleep(time.Second)
		}
	case "crash":
		return 17
	case "fork_child", "detached_child":
		bin, _ := os.Executable()
		cmd := exec.Command(bin, "fake-job", "--child")
		cmd.Env = os.Environ()
		cmd.Stdout = out
		cmd.Stderr = out
		if s.Mode == "detached_child" {
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		}
		if e := cmd.Start(); e != nil {
			return 3
		}
		_ = enc.Encode(struct {
			Kind string `json:"kind"`
			PID  int    `json:"pid"`
		}{h.Activity, cmd.Process.Pid})
		_ = cmd.Wait()
		return 0
	case "huge_output":
		n := s.StreamBytes
		if n == 0 {
			n = 8 << 20
		}
		for n > 0 {
			size := min(n, 16<<10)
			emit(h.Activity, strings.Repeat("x", int(size)))
			n -= size
		}
	case "exit_nonzero":
		emit(h.Failed, "exit_nonzero")
		return 23
	}
	if s.Mode == "edit" || s.Mode == "delete" || s.Mode == "create_empty" || s.Mode == "binary_edit" || s.Mode == "crash_after_edit" {
		for _, edit := range s.Edits {
			target := filepath.Join(root, edit.Path)
			if e := os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return 4
			}
			parent, e := filepath.EvalSymlinks(filepath.Dir(target))
			if e != nil || parent != root && !strings.HasPrefix(parent, root+string(os.PathSeparator)) {
				return 4
			}
			if st, e := os.Lstat(target); e == nil && st.Mode()&os.ModeSymlink != 0 {
				return 4
			}
			if edit.Delete || s.Mode == "delete" {
				e = os.Remove(target)
			} else {
				content := []byte(edit.Content)
				if edit.ContentBase64 != "" {
					content, _ = base64.StdEncoding.DecodeString(edit.ContentBase64)
				}
				e = os.WriteFile(target, content, 0600)
			}
			if e != nil {
				emit(h.Failed, "edit_failed")
				return 4
			}
		}
	}
	if s.Mode == "crash_after_edit" {
		return 19
	}
	emit(h.Completed, "fake completed")
	return 0
}
func Child() {
	signal.Ignore(syscall.SIGTERM)
	for {
		time.Sleep(time.Second)
	}
}

var _ h.Harness = (*Harness)(nil)
