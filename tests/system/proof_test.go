//go:build system

// Package system proves the installed binaries, not their in-process substitutes.
// Product packages are deliberately not imported. JSON is observed at public
// interfaces; SQL is limited to the read-only invariants in invariants_test.go.
package system

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type object = map[string]any

const devProfile = "macos-sandbox-exec-dev"
const wireVersion = "execution-channel-provisional-v1"

var checkout, binDir, runRoot, outputFile string
var report = object{"version": "gaffer-m1-proof-v1", "qualification": "development", "supported": false, "scenarios": []*scenario{}, "recovery_samples": []object{}}
var scenarios []*scenario
var reportMu sync.Mutex

type step struct {
	At         string `json:"at"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	DurationMS int64  `json:"duration_ms"`
	Data       any    `json:"data"`
}
type assertion struct {
	Name     string `json:"name"`
	Pass     bool   `json:"pass"`
	Observed any    `json:"observed"`
}
type scenario struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Root       string      `json:"root"`
	Started    string      `json:"started"`
	DurationMS int64       `json:"duration_ms"`
	Steps      []step      `json:"steps"`
	Assertions []assertion `json:"assertions"`
	mu         sync.Mutex
}

func (s *scenario) add(kind, name string, started time.Time, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Steps = append(s.Steps, step{time.Now().UTC().Format(time.RFC3339Nano), kind, name, time.Since(started).Milliseconds(), data})
}
func (s *scenario) check(t *testing.T, name string, ok bool, observed any) bool {
	t.Helper()
	s.mu.Lock()
	s.Assertions = append(s.Assertions, assertion{name, ok, observed})
	s.mu.Unlock()
	if !ok {
		t.Errorf("%s: %s", name, compact(redact(observed)))
	}
	return ok
}
func (s *scenario) require(t *testing.T, name string, ok bool, observed any) {
	t.Helper()
	if !s.check(t, name, ok, observed) {
		t.FailNow()
	}
}
func compact(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 1800 {
		return string(b[:1800]) + "...[bounded]"
	}
	return string(b)
}
func obj(v any) object { o, _ := v.(map[string]any); return o }
func arr(v any) []any  { a, _ := v.([]any); return a }
func str(v any) string { s, _ := v.(string); return s }
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	}
	return 0
}
func boolean(v any) bool { b, _ := v.(bool); return b }
func parse(b []byte) any {
	var v any
	if json.Unmarshal(b, &v) != nil {
		return string(b)
	}
	return v
}
func uuid() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}
func readJSON(path string) any {
	b, e := os.ReadFile(path)
	if e != nil {
		return object{"read_error": e.Error()}
	}
	return parse(b)
}
func sortedSet(a []string) []string {
	sort.Strings(a)
	r := []string{}
	for _, s := range a {
		if len(r) == 0 || r[len(r)-1] != s {
			r = append(r, s)
		}
	}
	return r
}
func intent(flow, step string) string {
	b := sha256.Sum256([]byte(flow + ":" + step))
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
func percentile(a []int64, p float64) any {
	if len(a) == 0 {
		return nil
	}
	b := append([]int64{}, a...)
	sort.Slice(b, func(i, j int) bool { return b[i] < b[j] })
	return b[int(math.Ceil(float64(len(b))*p))-1]
}

// Redaction is applied recursively before publication, never after a secret has
// reached the repository. Live config and key bytes are never put in evidence.
var urlPattern = regexp.MustCompile(`https?://[^\s/"\\]+`)
var userPathPattern = regexp.MustCompile(`/Users/[^\s"'<>\\]+`)

func redact(v any) any {
	b, _ := json.Marshal(v)
	var n any
	_ = json.Unmarshal(b, &n)
	var walk func(any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if strings.EqualFold(k, "token") || strings.EqualFold(k, "apiKey") || strings.EqualFold(k, "authorization") || strings.EqualFold(k, "credential") {
					x[k] = "<redacted>"
				} else {
					x[k] = walk(val)
				}
			}
			return x
		case []any:
			for i := range x {
				x[i] = walk(x[i])
			}
			return x
		case string:
			// Command output can be JSON or NDJSON inside a JSON string. Redact
			// those objects too, before any log or assertion can persist a token.
			lines := strings.Split(x, "\n")
			for i, line := range lines {
				trim := strings.TrimSpace(line)
				if strings.HasPrefix(trim, "{") || strings.HasPrefix(trim, "[") {
					var nested any
					if json.Unmarshal([]byte(trim), &nested) == nil {
						clean, _ := json.Marshal(walk(nested))
						lines[i] = string(clean)
					}
				}
			}
			x = strings.Join(lines, "\n")
			x = strings.ReplaceAll(x, runRoot, "<RUN_ROOT>")
			x = strings.ReplaceAll(x, checkout, "<checkout>")
			if p := os.Getenv("GAFFER_GATEWAY_CONFIG"); p != "" {
				x = strings.ReplaceAll(x, p, "<gateway-config>")
			}
			x = urlPattern.ReplaceAllString(x, "<gateway>")
			x = userPathPattern.ReplaceAllString(x, "<user-path>")
			if host, _ := os.Hostname(); host != "" {
				x = strings.ReplaceAll(x, host, "<host>")
			}
			return strings.ReplaceAll(x, "<RUN_ROOT>", runRoot)
		default:
			return x
		}
	}
	return walk(n)
}
func saveReport() error {
	reportMu.Lock()
	defer reportMu.Unlock()
	report["scenarios"] = scenarios
	report["finished"] = time.Now().UTC().Format(time.RFC3339Nano)
	b, err := json.Marshal(redact(report))
	if err != nil {
		return err
	}
	return os.WriteFile(outputFile, append(b, '\n'), 0600)
}
func commandEnv() []string {
	env := []string{}
	for _, s := range os.Environ() {
		if !strings.HasPrefix(s, "GAFFER_") && !strings.HasPrefix(s, "GIT_CONFIG_") {
			env = append(env, s)
		}
	}
	return append(env, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
}

type result struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Exit   int    `json:"exit"`
	Value  any    `json:"value,omitempty"`
}

func execute(timeout time.Duration, name string, args ...string) result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = commandEnv()
	var out, errout bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errout
	e := cmd.Run()
	r := result{Stdout: out.String(), Stderr: errout.String()}
	if e != nil {
		r.Exit = 1
		var ee *exec.ExitError
		if errors.As(e, &ee) {
			r.Exit = ee.ExitCode()
		}
		if ctx.Err() != nil {
			r.Stderr += "\ncommand deadline exceeded"
		}
	}
	r.Value = parse(out.Bytes())
	return r
}
func (s *scenario) command(timeout time.Duration, name string, args ...string) result {
	now := time.Now()
	r := execute(timeout, name, args...)
	logged := r
	if _, ok := r.Value.(map[string]any); ok {
		logged.Stdout = "[structured JSON retained in value]"
	}
	s.add("command", filepath.Base(name), now, object{"args": args, "result": logged})
	return r
}
func TestMain(m *testing.M) {
	checkout = os.Getenv("GAFFER_PROOF_CHECKOUT")
	if checkout == "" {
		wd, _ := os.Getwd()
		checkout = filepath.Clean(filepath.Join(wd, "../.."))
	}
	binDir = os.Getenv("GAFFER_PROOF_BIN")
	if binDir == "" {
		binDir = "/private/tmp/gaffer-m1-bin"
	}
	runRoot = os.Getenv("GAFFER_SYSTEM_ROOT")
	if runRoot == "" {
		home, _ := os.UserHomeDir()
		runRoot = filepath.Join(home, ".gaffer-system", time.Now().UTC().Format("20060102T150405")+"-"+uuid()[:8])
	}
	outputFile = os.Getenv("GAFFER_PROOF_JSON")
	if outputFile == "" {
		outputFile = filepath.Join(checkout, "docs/evidence/m1-acceptance-run.json")
	}
	if e := os.MkdirAll(runRoot, 0700); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(2)
	}
	_ = os.Chmod(runRoot, 0700)
	report["started"] = time.Now().UTC().Format(time.RFC3339Nano)
	report["run_root"] = runRoot
	report["scenario_filter"] = os.Getenv("GAFFER_SCENARIOS")
	report["head"] = strings.TrimSpace(execute(time.Minute, "git", "-C", checkout, "rev-parse", "HEAD").Stdout)
	env := object{}
	for _, c := range [][]string{{"sw_vers"}, {"uname", "-a"}, {"go", "version"}, {"node", "--version"}} {
		r := execute(time.Minute, c[0], c[1:]...)
		env[strings.Join(c, " ")] = r
	}
	if b, e := os.ReadFile("/Users/leebarry/.opencode/bin/opencode"); e == nil {
		env["opencode_sha256"] = digest(b)
	}
	report["environment"] = env
	code := m.Run()
	if len(scenarios) == 0 {
		os.Exit(code) // Compilation/helper checks must not replace acceptance evidence.
	}
	report["exit"] = code
	if e := saveReport(); e != nil {
		fmt.Fprintln(os.Stderr, "evidence write:", e)
		code = 1
	}
	fmt.Println("\nSCENARIO STATUS DURATION_MS EVIDENCE")
	for i, s := range scenarios {
		fmt.Printf("%s %s %d /scenarios/%d\n", s.ID, s.Status, s.DurationMS, i)
	}
	fmt.Println("run JSON:", outputFile)
	os.Exit(code)
}
func runScenario(t *testing.T, id, name string, fn func(*testing.T, *scenario)) {
	filter := os.Getenv("GAFFER_SCENARIOS")
	if filter != "" && !strings.Contains(","+filter+",", ","+id+",") {
		return
	}
	s := &scenario{ID: id, Name: name, Root: filepath.Join(runRoot, id), Started: time.Now().UTC().Format(time.RFC3339Nano), Steps: []step{}, Assertions: []assertion{}}
	scenarios = append(scenarios, s)
	t.Run(id, func(t *testing.T) {
		start := time.Now()
		t.Cleanup(func() {
			s.DurationMS = time.Since(start).Milliseconds()
			s.Status = "PASS"
			if t.Failed() {
				s.Status = "FAIL"
			} else if t.Skipped() {
				s.Status = "SKIP"
			}
			_ = saveReport()
		})
		if e := os.MkdirAll(s.Root, 0700); e != nil {
			s.require(t, "create fresh private root", false, e.Error())
		}
		fn(t, s)
	})
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.b.Len() < 1<<20 {
		_, _ = b.b.Write(p)
	}
	return len(p), nil
}
func (b *safeBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

type process struct {
	cmd      *exec.Cmd
	out, err safeBuffer
	done     chan struct{}
	wait     error
	once     sync.Once
}

func startProcess(name string, args ...string) (*process, error) {
	p := &process{done: make(chan struct{})}
	p.cmd = exec.Command(name, args...)
	p.cmd.Env = commandEnv()
	p.cmd.Stdout = &p.out
	p.cmd.Stderr = &p.err
	if e := p.cmd.Start(); e != nil {
		return nil, e
	}
	go func() { p.wait = p.cmd.Wait(); close(p.done) }()
	return p, nil
}
func (p *process) alive() bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	default:
		return true
	}
}
func (p *process) stop(kill bool) {
	if p == nil {
		return
	}
	p.once.Do(func() {
		if p.alive() {
			if kill {
				_ = p.cmd.Process.Kill()
			} else {
				_ = p.cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-p.done:
			case <-time.After(8 * time.Second):
				_ = p.cmd.Process.Kill()
				<-p.done
			}
		}
	})
}
func (p *process) logs() object {
	if p == nil {
		return object{}
	}
	return object{"pid": p.cmd.Process.Pid, "stdout": p.out.String(), "stderr": p.err.String(), "alive": p.alive()}
}
func freeAddress(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := l.Addr().String()
	_ = l.Close()
	return a
}
func waitFor(timeout time.Duration, fn func() bool) bool {
	end := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(end) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// TLS uses public certificates generated by the CLI. A normal RootCAs verifier
// plus exact DER pin is used; InsecureSkipVerify is never enabled.
func tlsConfig(t *testing.T, root, role, alpn string) *tls.Config {
	t.Helper()
	cert, e := tls.LoadX509KeyPair(filepath.Join(root, role+".crt"), filepath.Join(root, role+".key"))
	if e != nil {
		t.Fatal(e)
	}
	ca, e := os.ReadFile(filepath.Join(root, "daemon.crt"))
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("daemon certificate")
	}
	block, _ := pem.Decode(ca)
	pin := digest(block.Bytes)
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, Certificates: []tls.Certificate{cert}, NextProtos: []string{alpn}, VerifyConnection: func(c tls.ConnectionState) error {
		if len(c.PeerCertificates) != 1 || digest(c.PeerCertificates[0].Raw) != pin || c.NegotiatedProtocol != alpn {
			return errors.New("pin or ALPN mismatch")
		}
		return nil
	}}
}
func httpClient(t *testing.T, root, role, alpn string) *http.Client {
	cfg := tlsConfig(t, root, role, alpn)
	tr := &http.Transport{Proxy: nil, TLSClientConfig: cfg, ForceAttemptHTTP2: false}
	if alpn != "http/1.1" {
		tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			d := tls.Dialer{Config: cfg}
			return d.DialContext(ctx, network, addr)
		}
	}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
}
func request(client *http.Client, method, endpoint string, body any, headers map[string]string) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return 0, nil, e
		}
		rd = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, endpoint, rd)
	if e != nil {
		return 0, nil, e
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, e := client.Do(req)
	if e != nil {
		return 0, nil, e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, e
}
func pinFile(path string) string {
	b, _ := os.ReadFile(path)
	p, _ := pem.Decode(b)
	if p == nil {
		return ""
	}
	return digest(p.Bytes)
}
func quotePath(p string) string { return (&url.URL{Path: p}).EscapedPath() }
