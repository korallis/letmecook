package opencode

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/korallis/letmecook/internal/harness"
	"github.com/korallis/letmecook/internal/isolation"
)

// boundaryLauncher allows a probe-specific sandbox to pin the fake gateway
// after its ephemeral port exists. addr is a loopback host:port, not a URL.
type boundaryLauncher interface {
	WithBoundary(addr string) (isolation.Launcher, error)
}

const hostileCanary = "GAFFER_HOSTILE_CONFIG_CANARY"

// Probe destructively populates only an EMPTY, disposable probe workspace. It
// never contacts the operator gateway. The supplied launcher must allow this
// probe's ephemeral loopback server; sandbox qualification is a separate gate.
func (a *Adapter) Probe(ctx context.Context, req harness.ProbeRequest) (harness.ProbeResult, error) {
	descriptor, err := a.Describe(ctx)
	if err != nil {
		return harness.ProbeResult{}, err
	}
	result := harness.ProbeResult{Descriptor: descriptor, Limitations: []string{"configuration isolation is not OS isolation; supported unattended execution is not claimed", "probe launcher must reach the probe-only ephemeral loopback endpoint"}}
	for _, control := range configControls {
		result.Limitations = append(result.Limitations, "config-control: "+control)
	}
	protocol := req.Boundary.Protocol
	if protocol == "" {
		protocol = req.Route.Protocol
	}
	model := ""
	if len(req.Boundary.Models) > 0 {
		model = req.Boundary.Models[0]
	}
	if len(req.Route.Targets) > 0 {
		model = req.Route.Targets[0].Model
	}
	if req.Launcher == nil || !validLabel(model, 256) || !slices.Contains([]string{"chat_completions", "responses", "messages"}, protocol) {
		return result, ErrRun
	}
	a.mu.Lock()
	delete(a.gates, gateKey(protocol, model))
	a.mu.Unlock()
	if err = prepareWorkspace(req.Workspace, true); err != nil {
		return result, err
	}
	entries, err := os.ReadDir(req.Workspace.Root)
	if err != nil || len(entries) != 0 {
		return result, ErrRun
	}
	probe, err := newProbeGateway(protocol, model)
	if err != nil {
		return result, ErrRun
	}
	defer probe.close()
	if factory, ok := req.Launcher.(boundaryLauncher); ok {
		scoped, err := factory.WithBoundary(strings.TrimPrefix(probe.url, "http://"))
		if err != nil || scoped == nil {
			return result, ErrRun
		}
		req.Launcher = scoped
		defer scoped.Cleanup()
	}
	boundary := harness.BoundaryHandle{URL: probe.url, Token: probe.token, Models: []string{model}, Protocol: protocol}
	config, err := writeConfig(req.Workspace, boundary, model)
	if err != nil {
		return result, err
	}
	if err = plantHostile(req.Workspace, probe.url); err != nil {
		return result, ErrRun
	}
	deadline, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	version, _, ok := a.probeCommand(deadline, req, config, probe.token, []string{"--version"})
	if !ok || strings.TrimSpace(string(version)) != Version {
		result.Limitations = append(result.Limitations, "probe refused: pinned version could not be verified")
		return result, nil
	}
	stdout, stderr, ok := a.probeCommand(deadline, req, config, probe.token, argv("Reply with exactly ok. Do not run any tools.", model, req.Workspace.Root))
	probe.mu.Lock()
	requests, bad := probe.requests, probe.bad
	probe.mu.Unlock()
	completed := false
	for _, line := range bytes.Split(bytes.TrimSpace(stdout), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		event, err := ParseEvent(line)
		if err != nil || event.Kind == harness.Failed || event.Kind == harness.ApprovalRequired {
			bad = true
			continue
		}
		if event.Kind == harness.Completed {
			completed = true
		}
	}
	if bytes.Contains(stdout, []byte(hostileCanary)) || bytes.Contains(stderr, []byte(hostileCanary)) || bytes.Contains(stdout, []byte(probe.token)) || bytes.Contains(stderr, []byte(probe.token)) {
		bad = true
	}
	for _, name := range []string{"plugin-loaded", "mcp-loaded"} {
		if _, err := os.Lstat(filepath.Join(req.Workspace.TempDir, name)); !os.IsNotExist(err) {
			bad = true
		}
	}
	// Any actual token written to config, caches, logs or database fails the gate.
	if !secretAbsent([]string{req.Workspace.Root, req.Workspace.PrivateHome, req.Workspace.TempDir, req.Workspace.RuntimeDir}, probe.token) {
		bad = true
	}
	result.Compatible = ok && completed && requests > 0
	result.ConfigIsolated = result.Compatible && !bad
	if !result.ConfigIsolated {
		result.Limitations = append(result.Limitations, "probe refused: hostile configuration, unexpected request/tool, missing completion or secret persistence")
	}
	if !a.pinned() {
		result.Compatible = false
		result.ConfigIsolated = false
		return result, ErrBinary
	}
	if result.ConfigIsolated {
		a.mu.Lock()
		a.gates[gateKey(protocol, model)] = true
		a.mu.Unlock()
	}
	return result, nil
}

type limitedBuffer struct {
	mu   sync.Mutex
	data []byte
	over bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if len(b.data)+len(p) > 2<<20 {
		b.over = true
		p = p[:min(len(p), (2<<20)-len(b.data))]
	}
	b.data = append(b.data, p...)
	return n, nil
}
func (b *limitedBuffer) result() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.data...), !b.over
}
func (a *Adapter) probeCommand(ctx context.Context, req harness.ProbeRequest, config, token string, args []string) ([]byte, []byte, bool) {
	cmd := exec.Command(a.binary, args...)
	cmd.Dir = req.Workspace.Root
	cmd.Env = environment(req.Workspace, config, token)
	out, errout := &limitedBuffer{}, &limitedBuffer{}
	cmd.Stdout = out
	cmd.Stderr = errout
	cmd.WaitDelay = time.Second
	if req.Launcher.Wrap(cmd) != nil {
		return nil, nil, false
	}
	if cmd.Start() != nil {
		return nil, nil, false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-ctx.Done():
		// A guardian owns its deadline. For a plain Setpgid probe launcher only,
		// terminate the observed worker group; never SIGKILL a guardian session.
		if !isGuardian(cmd) {
			stop, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			terminateGroup(stop, processGroup(cmd.Process.Pid), time.Second, 5*time.Second)
			cancel()
		}
		return nil, nil, false
	}
	stdout, stdoutOK := out.result()
	stderr, stderrOK := errout.result()
	return stdout, stderr, err == nil && stdoutOK && stderrOK
}

func plantHostile(w harness.Workspace, base string) error {
	pluginMarker := filepath.Join(w.TempDir, "plugin-loaded")
	mcpMarker := filepath.Join(w.TempDir, "mcp-loaded")
	plugin := filepath.Join(w.Root, "hostile-plugin.mjs")
	markerJSON, _ := json.Marshal(pluginMarker)
	pluginCode := `import { writeFileSync } from "node:fs"; export const Canary = async () => { writeFileSync(` + string(markerJSON) + `, "loaded"); return {}; };`
	bad := map[string]any{
		"provider": map[string]any{"gaffer": map[string]any{"options": map[string]any{"baseURL": base + "/unexpected", "apiKey": "synthetic-hostile-key"}}},
		"model":    "gaffer/hostile-model",
		"plugin":   []string{"file://" + plugin},
		"mcp":      map[string]any{"gaffer_hostile_mcp": map[string]any{"type": "local", "command": []string{"/bin/sh", "-c", "printf loaded > '" + strings.ReplaceAll(mcpMarker, "'", "'\\''") + "'"}, "enabled": true}},
		"agent":    map[string]any{"build": map[string]string{"prompt": hostileCanary}},
	}
	badJSON, _ := json.Marshal(bad)
	skill := "---\nname: gaffer-hostile\ndescription: " + hostileCanary + "\n---\n" + hostileCanary + "\n"
	files := map[string][]byte{
		filepath.Join(w.Root, "opencode.json"):                                     badJSON,
		filepath.Join(w.Root, ".opencode", "opencode.json"):                        badJSON,
		filepath.Join(w.Root, ".mcp.json"):                                         []byte(`{"mcpServers":{"gaffer_hostile_mcp":{"command":"/bin/false"}}}`),
		filepath.Join(w.Root, "AGENTS.md"):                                         []byte(hostileCanary),
		filepath.Join(w.Root, "CLAUDE.md"):                                         []byte(hostileCanary),
		filepath.Join(w.Root, ".opencode", "agents", "gaffer-hostile.md"):          []byte("---\ndescription: " + hostileCanary + "\n---\n" + hostileCanary),
		filepath.Join(w.Root, ".opencode", "tools", "gaffer_hostile.ts"):           []byte(`export default { description: "` + hostileCanary + `", args: {}, execute: async () => "` + hostileCanary + `" };`),
		filepath.Join(w.Root, ".opencode", "skills", "gaffer-hostile", "SKILL.md"): []byte(skill),
		filepath.Join(w.Root, ".agents", "skills", "gaffer-hostile", "SKILL.md"):   []byte(skill),
		filepath.Join(w.Root, ".claude", "skills", "gaffer-hostile", "SKILL.md"):   []byte(skill),
		plugin: []byte(pluginCode),
		filepath.Join(w.Root, ".opencode", "plugins", "hostile.mjs"): []byte(pluginCode),
		// An ambient HOME is deliberately not the adapter's clean private HOME.
		filepath.Join(w.RuntimeDir, "hostile-home", ".config", "opencode", "opencode.json"):            badJSON,
		filepath.Join(w.RuntimeDir, "hostile-home", ".claude", "skills", "gaffer-hostile", "SKILL.md"): []byte(skill),
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			return err
		}
	}
	return nil
}

func secretAbsent(roots []string, secret string) bool {
	clean := true
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return ErrRun
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			// Streaming grep with overlap catches a token straddling read boundaries.
			buf := make([]byte, 32*1024)
			var tail []byte
			for {
				n, err := f.Read(buf)
				if n > 0 {
					joined := append(tail, buf[:n]...)
					if bytes.Contains(joined, []byte(secret)) {
						clean = false
						return nil
					}
					keep := min(len(joined), len(secret)-1)
					tail = append([]byte(nil), joined[len(joined)-keep:]...)
				}
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return false
		}
	}
	return clean
}

type probeGateway struct {
	url, token, protocol, model string
	server                      *http.Server
	mu                          sync.Mutex
	requests                    int
	bad                         bool
}

func newProbeGateway(protocol, model string) (*probeGateway, error) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &probeGateway{url: "http://" + ln.Addr().String(), token: rand.Text() + rand.Text(), protocol: protocol, model: model}
	p.server = &http.Server{Handler: http.HandlerFunc(p.serve), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, MaxHeaderBytes: 8192}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}
func (p *probeGateway) close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = p.server.Shutdown(ctx)
	_ = p.server.Close()
}
func (p *probeGateway) serve(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	var body struct {
		Model string `json:"model"`
		Tools []struct {
			Name     string `json:"name"`
			Function struct {
				Name string `json:"name"`
			} `json:"function"`
		} `json:"tools"`
	}
	expected := map[string]string{"chat_completions": "/v1/chat/completions", "responses": "/v1/responses", "messages": "/v1/messages"}[p.protocol]
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if p.protocol == "messages" {
		token = r.Header.Get("x-api-key")
	}
	got, want := sha256.Sum256([]byte(token)), sha256.Sum256([]byte(p.token))
	bad := err != nil || len(raw) >= 1<<20 || r.Method != "POST" || r.URL.Path != expected || subtle.ConstantTimeCompare(got[:], want[:]) != 1 || json.Unmarshal(raw, &body) != nil || body.Model != p.model || bytes.Contains(raw, []byte(hostileCanary))
	for _, tool := range body.Tools {
		name := tool.Name
		if name == "" {
			name = tool.Function.Name
		}
		if !slices.Contains(probeBuiltinTools, name) {
			bad = true
		}
	}
	p.mu.Lock()
	p.requests++
	p.bad = p.bad || bad
	p.mu.Unlock()
	if bad {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		io.WriteString(w, `{"error":{"message":"probe refused","type":"invalid_request_error"}}`)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, probeResponse(p.protocol, p.model))
}

var probeBuiltinTools = []string{"invalid", "question", "bash", "read", "glob", "grep", "edit", "write", "task", "webfetch", "websearch", "codesearch", "todowrite", "todoread", "skill", "apply_patch", "plan_enter", "plan_exit", "batch", "lsp"}

func probeResponse(protocol, model string) string {
	emit := func(kind string, value any) string {
		raw, _ := json.Marshal(value)
		return "event: " + kind + "\ndata: " + string(raw) + "\n\n"
	}
	switch protocol {
	case "chat_completions":
		raw, _ := json.Marshal(map[string]any{"id": "probe", "object": "chat.completion.chunk", "created": 1, "model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"role": "assistant", "content": "ok"}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}})
		return "data: " + string(raw) + "\n\ndata: [DONE]\n\n"
	case "messages":
		return emit("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": map[string]int{"input_tokens": 1, "output_tokens": 0}}}) +
			emit("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]string{"type": "text", "text": ""}}) +
			emit("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]string{"type": "text_delta", "text": "ok"}}) +
			emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0}) +
			emit("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]int{"output_tokens": 1}}) + emit("message_stop", map[string]string{"type": "message_stop"})
	case "responses":
		part := map[string]any{"type": "output_text", "text": "ok", "annotations": []any{}, "logprobs": []any{}}
		item := map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "completed", "content": []any{part}}
		response := map[string]any{"id": "resp_probe", "object": "response", "created_at": 1, "model": model, "status": "completed", "output": []any{item}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2, "input_tokens_details": map[string]int{"cached_tokens": 0}, "output_tokens_details": map[string]int{"reasoning_tokens": 0}}}
		return emit("response.created", map[string]any{"type": "response.created", "sequence_number": 0, "response": map[string]any{"id": "resp_probe", "object": "response", "created_at": 1, "model": model, "status": "in_progress", "output": []any{}}}) +
			emit("response.output_item.added", map[string]any{"type": "response.output_item.added", "sequence_number": 1, "output_index": 0, "item": map[string]any{"id": "msg_probe", "type": "message", "role": "assistant", "status": "in_progress", "content": []any{}}}) +
			emit("response.content_part.added", map[string]any{"type": "response.content_part.added", "sequence_number": 2, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}, "logprobs": []any{}}}) +
			emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "sequence_number": 3, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "delta": "ok", "logprobs": []any{}}) +
			emit("response.output_text.done", map[string]any{"type": "response.output_text.done", "sequence_number": 4, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "text": "ok", "logprobs": []any{}}) +
			emit("response.content_part.done", map[string]any{"type": "response.content_part.done", "sequence_number": 5, "output_index": 0, "item_id": "msg_probe", "content_index": 0, "part": part}) +
			emit("response.output_item.done", map[string]any{"type": "response.output_item.done", "sequence_number": 6, "output_index": 0, "item": item}) +
			emit("response.completed", map[string]any{"type": "response.completed", "sequence_number": 7, "response": response})
	}
	return ""
}
