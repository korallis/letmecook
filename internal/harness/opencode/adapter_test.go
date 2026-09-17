//go:build darwin || linux

package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	p "github.com/korallis/letmecook/schemas/execution"
)

// A real subprocess, not an in-process mock. Production adapter argv/environment
// and launcher wrapping are exercised unchanged, including signal escalation.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(Version)
		return
	}
	if len(os.Args) > 2 && os.Args[1] == "run" {
		os.Exit(helperOpenCode(os.Args[2]))
	}
	os.Exit(m.Run())
}
func helperOpenCode(brief string) int {
	event := func(kind string, part any) {
		json.NewEncoder(os.Stdout).Encode(map[string]any{"type": kind, "timestamp": time.Now().UnixMilli(), "part": part})
	}
	if brief == "ignore-term" {
		signal.Ignore(syscall.SIGTERM)
	}
	if brief == "hang" || brief == "ignore-term" {
		event("step_start", map[string]string{"type": "step-start"})
		for {
			time.Sleep(time.Second)
		}
	}
	if brief == "malformed" {
		fmt.Println(`{"type":`)
		return 0
	}
	if brief == "huge-output" {
		fmt.Fprint(os.Stdout, strings.Repeat("x", 128<<10))
		return 0
	}
	if brief == "approval" {
		event("approval_required", map[string]string{})
		return 1
	}
	var config struct {
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
			Models map[string]any `json:"models"`
		} `json:"provider"`
	}
	raw, err := os.ReadFile(os.Getenv("OPENCODE_CONFIG"))
	if err != nil || json.Unmarshal(raw, &config) != nil {
		return 2
	}
	provider := config.Provider["gaffer"]
	model := ""
	for key := range provider.Models {
		model = key
	}
	endpoint := provider.Options.BaseURL + "/chat/completions"
	protocol := "chat_completions"
	if provider.NPM == "@ai-sdk/openai" {
		endpoint = provider.Options.BaseURL + "/responses"
		protocol = "responses"
	}
	if provider.NPM == "@ai-sdk/anthropic" {
		endpoint = provider.Options.BaseURL + "/messages"
		protocol = "messages"
	}
	if os.Getenv("OPENCODE_DISABLE_PROJECT_CONFIG") != "1" {
		endpoint = provider.Options.BaseURL + "/unexpected"
	}
	body := `{"model":"` + model + `","stream":true}`
	req, _ := http.NewRequest("POST", endpoint, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if protocol == "messages" {
		req.Header.Set("x-api-key", os.Getenv("GAFFER_ATTEMPT_TOKEN"))
	} else {
		req.Header.Set("Authorization", "Bearer "+os.Getenv("GAFFER_ATTEMPT_TOKEN"))
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 3
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return 4
	}
	event("step_start", map[string]string{"type": "step-start"})
	if brief == "print-token" {
		fmt.Fprintln(os.Stderr, "native "+os.Getenv("GAFFER_ATTEMPT_TOKEN"))
		event("text", map[string]string{"text": os.Getenv("GAFFER_ATTEMPT_TOKEN")})
	} else {
		fmt.Fprintln(os.Stderr, "native diagnostic")
		event("text", map[string]string{"text": "ok"})
	}
	if brief == "fail" {
		return 9
	}
	event("step_finish", map[string]any{"reason": "stop", "tokens": map[string]any{"input": 1, "output": 1, "cache": map[string]int{"read": 0, "write": 0}}})
	fmt.Fprintln(os.Stderr, "after final step")
	return 0
}

type testLauncher struct {
	mu        sync.Mutex
	env, args []string
	calls     int
	remove    []string
	deny      bool
}

func (l *testLauncher) Wrap(cmd *exec.Cmd) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if cmd.Stdout == nil || cmd.Stderr == nil || cmd.Dir == "" || len(cmd.Env) == 0 {
		panic("command not configured before Wrap")
	}
	if l.deny {
		return isolation.ErrExecutionUnqualified
	}
	for _, key := range l.remove {
		cmd.Env = slices.DeleteFunc(cmd.Env, func(v string) bool { return strings.HasPrefix(v, key+"=") })
	}
	l.env = slices.Clone(cmd.Env)
	l.args = slices.Clone(cmd.Args)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return nil
}
func (l *testLauncher) Observation() isolation.Observation {
	return isolation.Observation{Limitations: []string{"test-only-unconfined"}}
}
func (l *testLauncher) Cleanup() error { return nil }
func workspace(t *testing.T) harness.Workspace {
	t.Helper()
	root := t.TempDir()
	w := harness.Workspace{Root: filepath.Join(root, "repo"), PrivateHome: filepath.Join(root, "home"), TempDir: filepath.Join(root, "tmp"), RuntimeDir: filepath.Join(root, "runtime")}
	for _, dir := range []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return w
}
func helperAdapter(t *testing.T, protocol string) (*Adapter, *testLauncher) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a, err := New(bin)
	if err != nil {
		t.Fatal(err)
	}
	l := &testLauncher{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := a.Probe(ctx, harness.ProbeRequest{Workspace: workspace(t), Boundary: harness.BoundaryHandle{Protocol: protocol, Models: []string{"model-a"}}, Launcher: l})
	if err != nil || !result.Compatible || !result.ConfigIsolated {
		t.Fatalf("helper config gate: %+v %v", result, err)
	}
	return a, l
}
func runRequest(t *testing.T, l *testLauncher, gateway *probeGateway, brief string) harness.RunRequest {
	t.Helper()
	return harness.RunRequest{Workspace: workspace(t), Brief: brief, Boundary: harness.BoundaryHandle{URL: gateway.url, Token: gateway.token, Models: []string{"model-a"}, Protocol: gateway.protocol}, Limits: harness.Limits{MaxStdoutBytes: 1 << 20}, Launcher: l}
}
func collect(t *testing.T, a *Adapter, h harness.RunHandle) []harness.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := a.Events(ctx, h, 0)
	if err != nil {
		t.Fatal(err)
	}
	var events []harness.Event
	for {
		event, err := stream.Next(ctx)
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
}

func TestAdapterProtocolsAndEventLifecycle(t *testing.T) {
	for _, protocol := range []string{"chat_completions", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			a, l := helperAdapter(t, protocol)
			gateway, err := newProbeGateway(protocol, "model-a")
			if err != nil {
				t.Fatal(err)
			}
			defer gateway.close()
			req := runRequest(t, l, gateway, "normal")
			handle, err := a.Start(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if handle.PID <= 1 || handle.PGID != handle.PID || handle.ID == "" || handle.StartUnixNS <= 0 {
				t.Fatalf("bad handle %+v", handle)
			}
			events := collect(t, a, handle)
			if len(events) < 4 || events[len(events)-1].Kind != harness.Completed {
				t.Fatalf("events %+v", events)
			}
			foundNative := false
			for i, event := range events {
				if event.Sequence != int64(i+1) || len(event.Raw) > harness.MaxRawBytes {
					t.Fatal("event bounds")
				}
				if event.Native {
					var text string
					if json.Unmarshal(event.Raw, &text) != nil {
						t.Fatal("invalid native JSON")
					}
					if strings.Contains(text, "after final step\n") {
						foundNative = true
					}
				}
			}
			if !foundNative {
				t.Fatal("stderr after terminal model step was dropped")
			}
			stream, err := a.Events(context.Background(), handle, events[1].Sequence)
			if err != nil {
				t.Fatal(err)
			}
			event, err := stream.Next(context.Background())
			if err != nil || event.Sequence != 3 {
				t.Fatal("event replay")
			}
			l.mu.Lock()
			gotArgs, gotEnv := slices.Clone(l.args), slices.Clone(l.env)
			l.mu.Unlock()
			wantArgs := append([]string{a.binary}, argv(req.Brief, "model-a", req.Workspace.Root)...)
			if !slices.Equal(gotArgs, wantArgs) {
				t.Fatalf("argv: %q", gotArgs)
			}
			if !slices.Equal(gotEnv, environment(req.Workspace, filepath.Join(req.Workspace.RuntimeDir, "opencode.json"), gateway.token)) {
				t.Fatal("ambient environment inherited")
			}
			raw, err := os.ReadFile(filepath.Join(req.Workspace.RuntimeDir, "opencode.json"))
			if err != nil || bytes.Contains(raw, []byte(gateway.token)) {
				t.Fatal("token in config")
			}
		})
	}
}
func TestStartRequiresCurrentSuccessfulProbe(t *testing.T) {
	bin, _ := os.Executable()
	a, err := New(bin)
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := newProbeGateway("chat_completions", "model-a")
	if err != nil {
		t.Fatal(err)
	}
	defer gateway.close()
	req := runRequest(t, &testLauncher{}, gateway, "normal")
	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrConfigIsolation) {
		t.Fatal("unprobed adapter launched", err)
	}
	result, err := a.Probe(context.Background(), harness.ProbeRequest{Workspace: workspace(t), Boundary: req.Boundary, Launcher: &testLauncher{remove: []string{"OPENCODE_DISABLE_PROJECT_CONFIG"}}})
	if err != nil || result.ConfigIsolated {
		t.Fatal("hostile config probe should fail", err)
	}
	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrConfigIsolation) {
		t.Fatal("failed probe permitted launch")
	}
	a, l := helperAdapter(t, "chat_completions")
	req = runRequest(t, l, gateway, "normal")
	a.digest = strings.Repeat("0", 64)
	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrBinary) {
		t.Fatal("changed digest permitted launch")
	}
}
func TestRefusingLauncherUnsafeWorkspaceAndSettings(t *testing.T) {
	a, _ := helperAdapter(t, "chat_completions")
	gateway, _ := newProbeGateway("chat_completions", "model-a")
	defer gateway.close()
	l := &testLauncher{deny: true}
	req := runRequest(t, l, gateway, "normal")
	if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrRun) {
		t.Fatal("launcher bypassed")
	}
	for _, mutate := range []func(*harness.RunRequest){
		func(r *harness.RunRequest) { r.Launcher = nil },
		func(r *harness.RunRequest) { r.Workspace.PrivateHome = r.Workspace.Root },
		func(r *harness.RunRequest) { r.Settings = json.RawMessage(`{"plugin":"malicious"}`) },
		func(r *harness.RunRequest) { r.Boundary.URL = "https://gateway.example" },
		func(r *harness.RunRequest) { r.Boundary.URL = "http://localhost:1234" },
		func(r *harness.RunRequest) { r.Boundary.URL = "http://127.0.0.1:1234/?token=value" },
		func(r *harness.RunRequest) {
			os.WriteFile(filepath.Join(r.Workspace.PrivateHome, "opencode.json"), []byte(`{}`), 0600)
		},
	} {
		req = runRequest(t, &testLauncher{}, gateway, "normal")
		mutate(&req)
		if _, err := a.Start(context.Background(), req); !errors.Is(err, ErrRun) {
			t.Fatal("unsafe run admitted", err)
		}
	}
}
func TestCancellationEscalatesAndObservesPGID(t *testing.T) {
	for _, brief := range []string{"hang", "ignore-term"} {
		t.Run(brief, func(t *testing.T) {
			a, l := helperAdapter(t, "chat_completions")
			a.grace = 50 * time.Millisecond
			a.killWait = 2 * time.Second
			gateway, _ := newProbeGateway("chat_completions", "model-a")
			defer gateway.close()
			handle, err := a.Start(context.Background(), runRequest(t, l, gateway, brief))
			if err != nil {
				t.Fatal(err)
			}
			stream, _ := a.Events(context.Background(), handle, 0)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			first, err := stream.Next(ctx)
			if err != nil || first.Kind != harness.Started {
				t.Fatal("child not ready", err)
			}
			result, err := a.Cancel(ctx, handle)
			if err != nil || result.ConfirmedProcess != "terminated" || result.ObservedUnixNS <= 0 || result.RemoteWork != "unknown" {
				t.Fatalf("cancel observation %+v %v", result, err)
			}
			if result.Escalated != (brief == "ignore-term") {
				t.Fatal("wrong escalation", result)
			}
			events := collect(t, a, handle)
			if events[len(events)-1].Kind != harness.Failed {
				t.Fatal("terminated worker completed")
			}
			handle.GuardianPID = handle.PGID
			result, err = a.Cancel(ctx, handle)
			if err != nil || result.ConfirmedProcess != "unknown" {
				t.Fatal("guardian group treated as worker")
			}
		})
	}
}
func TestFailedOutputDoesNotInventSuccess(t *testing.T) {
	for _, brief := range []string{"fail", "malformed", "huge-output", "approval"} {
		t.Run(brief, func(t *testing.T) {
			a, l := helperAdapter(t, "chat_completions")
			gateway, _ := newProbeGateway("chat_completions", "model-a")
			defer gateway.close()
			req := runRequest(t, l, gateway, brief)
			req.Limits.MaxStdoutBytes = 8192
			h, err := a.Start(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			events := collect(t, a, h)
			if events[len(events)-1].Kind != harness.Failed {
				t.Fatal("failure reported as completion")
			}
			if brief == "approval" && !slices.ContainsFunc(events, func(e harness.Event) bool { return e.Kind == harness.ApprovalRequired }) {
				t.Fatal("approval not surfaced")
			}
			a.mu.Lock()
			r := a.runs[h.ID]
			a.mu.Unlock()
			select {
			case <-r.done:
			case <-time.After(5 * time.Second):
				t.Fatal("subprocess did not exit")
			}
		})
	}
}

type testReceipts struct {
	mu     sync.Mutex
	values []inference.Receipt
	file   string
}

func (s *testReceipts) Reserve(_ context.Context, r inference.Receipt) error  { return s.append(r) }
func (s *testReceipts) Complete(_ context.Context, r inference.Receipt) error { return s.append(r) }
func (s *testReceipts) append(r inference.Receipt) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values = append(s.values, r)
	f, err := os.OpenFile(s.file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = json.NewEncoder(f).Encode(r); err != nil {
		return err
	}
	return f.Sync()
}
func TestGatewayCredentialAbsentFromChildWorkspaceAndJournals(t *testing.T) {
	const credential = "synthetic-credential-must-not-enter-worker"
	t.Setenv("OPENAI_API_KEY", credential)
	t.Setenv("ANTHROPIC_API_KEY", credential)
	t.Setenv("HTTP_PROXY", "http://untrusted.example")
	a, l := helperAdapter(t, "chat_completions")
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+credential {
			t.Error("boundary did not inject credential")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, probeResponse("chat_completions", "model-a"))
	}))
	defer gateway.Close()
	protected := t.TempDir()
	credentialFile := filepath.Join(protected, "credential")
	os.WriteFile(credentialFile, []byte(credential), 0600)
	ca := filepath.Join(protected, "ca.pem")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: gateway.Certificate().Raw}), 0600)
	w := workspace(t)
	journalDir := t.TempDir()
	sink := &testReceipts{file: filepath.Join(journalDir, "receipts.jsonl")}
	token := "synthetic-scoped-token-000000000000000"
	scope := inference.Scope{Identity: p.Identity{Generation: "11111111-1111-4111-8111-111111111111", TaskID: "22222222-2222-4222-8222-222222222222", AttemptID: "33333333-3333-4333-8333-333333333333", Epoch: 1}, Token: token, Models: []string{"model-a"}, Protocols: []string{"chat_completions"}, Gateway: inference.Gateway{BaseURL: gateway.URL, CABundle: ca, CredentialRef: inference.CredentialRef{Kind: "file", Path: credentialFile}, Models: []string{"model-a"}, Protocols: []string{"chat_completions"}}, Limits: inference.Limits{Requests: 2, RequestBytes: 1 << 20, ResponseBytes: 1 << 20, RequestTimeout: 5 * time.Second}}
	b, err := inference.Start(context.Background(), scope, sink)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	h, err := a.Start(context.Background(), harness.RunRequest{Workspace: w, Brief: "print-token", Boundary: harness.BoundaryHandle{URL: "http://" + b.Addr(), Token: token, Models: []string{"model-a"}, Protocol: "chat_completions"}, Launcher: l, Limits: harness.Limits{MaxStdoutBytes: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	events := collect(t, a, h)
	data, _ := json.Marshal(events)
	if err := os.WriteFile(filepath.Join(journalDir, "events.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	env := strings.Join(l.env, "\n")
	l.mu.Unlock()
	if strings.Contains(env, credential) || strings.Count(env, token) != 1 || !strings.Contains(env, "GAFFER_ATTEMPT_TOKEN="+token) {
		t.Fatal("unexpected child secret environment")
	}
	state := b.Close(context.Background())
	if !state.Quiescent || state.Reservations != 1 {
		t.Fatalf("receipt state %+v", state)
	}
	roots := []string{w.Root, w.PrivateHome, w.TempDir, w.RuntimeDir, journalDir}
	for _, secret := range []string{credential, token} {
		if !secretAbsent(roots, secret) {
			t.Fatal("secret found by recursive streaming grep")
		}
	}
	if !bytes.Contains(data, []byte("[redacted-attempt-token]")) {
		t.Fatal("token output was not visibly redacted")
	}
}

func TestRealOpenCodeConfigIsolation(t *testing.T) {
	bin := os.Getenv("GAFFER_OPENCODE_BIN")
	if bin == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no HOME for optional pinned binary")
		}
		bin = filepath.Join(home, ".opencode", "bin", "opencode")
	}
	if _, err := os.Stat(bin); err != nil {
		t.Skip("pinned OpenCode binary unavailable; set GAFFER_OPENCODE_BIN")
	}
	for _, protocol := range []string{"chat_completions", "responses", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			a, err := New(bin)
			if err != nil {
				t.Fatal(err)
			}
			probeWorkspace := workspace(t)
			ambient := filepath.Join(probeWorkspace.RuntimeDir, "hostile-home")
			t.Setenv("HOME", ambient)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(ambient, ".config"))
			t.Setenv("OPENCODE_CONFIG", filepath.Join(ambient, ".config", "opencode", "opencode.json"))
			result, err := a.Probe(context.Background(), harness.ProbeRequest{Workspace: probeWorkspace, Boundary: harness.BoundaryHandle{Models: []string{"model-a"}, Protocol: protocol}, Launcher: &testLauncher{}})
			if err != nil || !result.Compatible || !result.ConfigIsolated {
				t.Fatalf("real pinned probe: %+v; %v", result, err)
			}
			t.Logf("OpenCode %s: %s hostile config isolation passed; binary sha256=%s", Version, protocol, result.Descriptor.BinaryDigest)
		})
	}
	t.Run("prescribed-environment-refused", func(t *testing.T) {
		a, err := New(bin)
		if err != nil {
			t.Fatal(err)
		}
		remove := []string{}
		for _, v := range configControls {
			remove = append(remove, strings.SplitN(v, "=", 2)[0])
		}
		result, err := a.Probe(context.Background(), harness.ProbeRequest{Workspace: workspace(t), Boundary: harness.BoundaryHandle{Models: []string{"model-a"}, Protocol: "chat_completions"}, Launcher: &testLauncher{remove: remove}})
		if err != nil || result.ConfigIsolated {
			t.Fatal("prescribed environment unexpectedly passed hostile project gate", err)
		}
		gateway, _ := newProbeGateway("chat_completions", "model-a")
		defer gateway.close()
		if _, err := a.Start(context.Background(), runRequest(t, &testLauncher{}, gateway, "normal")); !errors.Is(err, ErrConfigIsolation) {
			t.Fatal("real binary failed gate did not prevent Start")
		}
		t.Log("OpenCode 1.18.31 prescribed environment fails closed despite --pure")
	})
}

func TestRealOpenCodeControlAblation(t *testing.T) {
	if os.Getenv("GAFFER_OPENCODE_ABLATION") != "1" {
		t.Skip("explicit one-off control ablation")
	}
	home, _ := os.UserHomeDir()
	bin := filepath.Join(home, ".opencode", "bin", "opencode")
	for _, remove := range [][]string{{"OPENCODE_DISABLE_PROJECT_CONFIG"}, {"OPENCODE_DISABLE_EXTERNAL_SKILLS"}, {"OPENCODE_DISABLE_CLAUDE_CODE"}, {"OPENCODE_DISABLE_DEFAULT_PLUGINS"}, {"OPENCODE_DISABLE_EXTERNAL_SKILLS", "OPENCODE_DISABLE_CLAUDE_CODE", "OPENCODE_DISABLE_DEFAULT_PLUGINS"}} {
		t.Run(strings.Join(remove, "+"), func(t *testing.T) {
			a, err := New(bin)
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Probe(context.Background(), harness.ProbeRequest{Workspace: workspace(t), Boundary: harness.BoundaryHandle{Models: []string{"model-a"}, Protocol: "chat_completions"}, Launcher: &testLauncher{remove: remove}})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("Compatible=%t ConfigIsolated=%t", result.Compatible, result.ConfigIsolated)
		})
	}
}

type scopedProbeLauncher struct {
	testLauncher
	called bool
	addr   string
	child  *testLauncher
}

func (l *scopedProbeLauncher) WithBoundary(addr string) (isolation.Launcher, error) {
	l.called = true
	l.addr = addr
	l.child = &testLauncher{}
	return l.child, nil
}
func TestProbeRebindsOptionalBoundaryLauncher(t *testing.T) {
	binary, _ := os.Executable()
	a, err := New(binary)
	if err != nil {
		t.Fatal(err)
	}
	launcher := &scopedProbeLauncher{}
	result, err := a.Probe(context.Background(), harness.ProbeRequest{Workspace: workspace(t), Boundary: harness.BoundaryHandle{Protocol: "chat_completions", Models: []string{"model-a"}}, Launcher: launcher})
	if err != nil || !result.ConfigIsolated || !launcher.called || !strings.HasPrefix(launcher.addr, "127.0.0.1:") || launcher.child.calls != 2 || launcher.testLauncher.calls != 0 {
		t.Fatal("probe launcher was not bound to its endpoint", err)
	}
}
