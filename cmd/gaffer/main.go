// Command gaffer is the provisional owner CLI. All network access is explicit,
// pinned mTLS; no credential discovery, proxies, or execution authority is implied.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const cliVersion = "gaffer-cli-v1"
const maxReply = 1 << 20

type globals struct {
	Endpoint, Cert, Key, DaemonFingerprint, MessageID string
	JSON                                              bool
	Timeout                                           time.Duration
	Out, Err                                          io.Writer
}

// S4/S6 add command families from init functions in their own files.
var commands = map[string]func(context.Context, globals, []string) int{
	"help":     helpCommand,
	"version":  versionCommand,
	"identity": identityCommand,
}

type cliError struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func exitCode(status int, code string) int {
	if code == "reconciliation_required" {
		return 4
	}
	if status >= 500 || status == 0 {
		return 3
	}
	if status == 400 {
		return 2
	}
	return 1
}
func fail(g globals, status int, code, detail string) int {
	if g.JSON {
		json.NewEncoder(g.Err).Encode(struct {
			Version string   `json:"version"`
			Error   cliError `json:"error"`
		}{cliVersion, cliError{status, code, detail}})
	} else {
		fmt.Fprintf(g.Err, "gaffer: %s: %s\n", code, detail)
	}
	return exitCode(status, code)
}
func emit(g globals, command string, result any) int {
	var err error
	if g.JSON {
		err = json.NewEncoder(g.Out).Encode(struct {
			Version   string `json:"version"`
			Command   string `json:"command"`
			MessageID string `json:"message_id"`
			Result    any    `json:"result"`
		}{cliVersion, command, g.MessageID, result})
	} else if text, ok := result.(string); ok {
		_, err = fmt.Fprintln(g.Out, text)
	} else {
		err = json.NewEncoder(g.Out).Encode(result)
	}
	if err != nil {
		return fail(g, 0, "output_unavailable", "could not write command output")
	}
	return 0
}
func newID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func validID(s string) bool {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' || s[14] != '4' || !strings.ContainsRune("89ab", rune(s[19])) {
		return false
	}
	compact := strings.ReplaceAll(s, "-", "")
	b, err := hex.DecodeString(compact)
	return err == nil && len(b) == 16 && strings.ToLower(s) == s
}
func validPin(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && strings.ToLower(s) == s
}

// parseGlobals accepts global flags before or after the command, leaving command
// flags intact. Environment supplies defaults only; explicit flags always win.
func parseGlobals(args []string, out, errOut io.Writer) (globals, []string, error) {
	g := globals{Endpoint: os.Getenv("GAFFER_ENDPOINT"), Cert: os.Getenv("GAFFER_CERT"), Key: os.Getenv("GAFFER_KEY"), DaemonFingerprint: os.Getenv("GAFFER_DAEMON_FINGERPRINT"), MessageID: os.Getenv("GAFFER_MESSAGE_ID"), Timeout: 30 * time.Second, Out: out, Err: errOut}
	jsonDefault := os.Getenv("GAFFER_JSON")
	if jsonDefault != "" {
		var err error
		g.JSON, err = strconv.ParseBool(jsonDefault)
		if err != nil {
			return g, nil, err
		}
	}
	if raw := os.Getenv("GAFFER_TIMEOUT"); raw != "" {
		var err error
		g.Timeout, err = time.ParseDuration(raw)
		if err != nil {
			return g, nil, err
		}
	}
	fs := flag.NewFlagSet("gaffer", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&g.Endpoint, "endpoint", g.Endpoint, "owner HTTPS origin")
	fs.StringVar(&g.Cert, "cert", g.Cert, "client leaf certificate PEM")
	fs.StringVar(&g.Key, "key", g.Key, "private client key PEM, mode 0600")
	fs.StringVar(&g.DaemonFingerprint, "daemon-fingerprint", g.DaemonFingerprint, "SHA-256 daemon leaf DER pin")
	fs.StringVar(&g.MessageID, "message-id", g.MessageID, "stable command intent UUIDv4")
	fs.BoolVar(&g.JSON, "json", g.JSON, "machine-readable envelope")
	fs.DurationVar(&g.Timeout, "timeout", g.Timeout, "request timeout")
	var globalArgs, rest []string
	for n := 0; n < len(args); n++ {
		arg := args[n]
		if arg == "--" {
			rest = append(rest, args[n+1:]...)
			break
		}
		name, _, _ := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || fs.Lookup(name) == nil {
			rest = append(rest, arg)
			continue
		}
		globalArgs = append(globalArgs, arg)
		if !strings.Contains(arg, "=") && name != "json" {
			if n+1 >= len(args) {
				return g, nil, errors.New("missing global value")
			}
			n++
			globalArgs = append(globalArgs, args[n])
		}
	}
	if err := fs.Parse(globalArgs); err != nil {
		return g, nil, err
	}
	if g.Timeout <= 0 || g.Timeout > 24*time.Hour || (g.MessageID != "" && !validID(g.MessageID)) {
		return g, nil, errors.New("invalid global")
	}
	if g.MessageID == "" {
		g.MessageID = newID()
	}
	return g, rest, nil
}
func run(ctx context.Context, args []string, out, errOut io.Writer) int {
	g, rest, err := parseGlobals(args, out, errOut)
	if err != nil {
		return fail(g, 400, "invalid_arguments", "invalid command-line arguments")
	}
	if len(rest) == 0 {
		return helpCommand(ctx, g, nil)
	}
	command, ok := commands[rest[0]]
	if !ok {
		return fail(g, 400, "unknown_command", "use gaffer help for available commands")
	}
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	return command(ctx, g, rest[1:])
}
func helpCommand(ctx context.Context, g globals, args []string) int {
	if len(args) != 0 {
		return fail(g, 400, "invalid_arguments", "help takes no arguments")
	}
	return emit(g, "help", "gaffer (provisional)\nCommands: help, version, identity keygen, identity self\nGlobals: --endpoint --cert --key --daemon-fingerprint --json --message-id --timeout\nidentity keygen: --cert FILE --key FILE [--name NAME] [--days 30] [--server --host HOST]\nWorkflow, execution, verification and backup commands arrive in later slices.")
}
func versionCommand(ctx context.Context, g globals, args []string) int {
	if len(args) != 0 {
		return fail(g, 400, "invalid_arguments", "version takes no arguments")
	}
	return emit(g, "version", cliVersion)
}
func identityCommand(ctx context.Context, g globals, args []string) int {
	if len(args) == 0 {
		return fail(g, 400, "invalid_arguments", "identity requires keygen or self")
	}
	switch args[0] {
	case "keygen":
		return identityKeygen(ctx, g, args[1:])
	case "self":
		return identitySelf(ctx, g, args[1:])
	default:
		return fail(g, 400, "unknown_command", "identity subcommand is not implemented")
	}
}

type hosts []string

func (h *hosts) String() string { return strings.Join(*h, ",") }
func (h *hosts) Set(s string) error {
	if s == "" || strings.ContainsAny(s, " /\\\t\r\n\x00") {
		return errors.New("invalid host")
	}
	*h = append(*h, s)
	return nil
}
func identityKeygen(ctx context.Context, g globals, args []string) int {
	fs := flag.NewFlagSet("identity keygen", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "gaffer-client", "display name, never authority")
	days := fs.Int("days", 30, "certificate validity in days (1..365)")
	server := fs.Bool("server", false, "generate a daemon server leaf instead of a client leaf")
	var names hosts
	fs.Var(&names, "host", "server DNS name or IP SAN; repeatable")
	if fs.Parse(args) != nil || fs.NArg() != 0 || g.Cert == "" || g.Key == "" || *days < 1 || *days > 365 || len(*name) > 128 || (*server && len(names) == 0) || (!*server && len(names) != 0) {
		return fail(g, 400, "invalid_arguments", "keygen requires distinct new --cert and --key paths; servers also require --host")
	}
	certPath, e1 := filepath.Abs(g.Cert)
	keyPath, e2 := filepath.Abs(g.Key)
	if e1 != nil || e2 != nil || certPath == keyPath {
		return fail(g, 400, "invalid_arguments", "certificate and key paths must differ")
	}
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fail(g, 0, "keygen_unavailable", "could not generate identity")
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fail(g, 0, "keygen_unavailable", "could not generate identity")
	}
	serial.Add(serial, big.NewInt(1))
	now := time.Now()
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: *name}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Duration(*days) * 24 * time.Hour), BasicConstraintsValid: true, IsCA: false, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	if *server {
		template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		for _, h := range names {
			if ip := net.ParseIP(h); ip != nil {
				template.IPAddresses = append(template.IPAddresses, ip)
			} else {
				template.DNSNames = append(template.DNSNames, h)
			}
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, pub, key)
	if err != nil {
		return fail(g, 0, "keygen_unavailable", "could not generate certificate")
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fail(g, 0, "keygen_unavailable", "could not encode identity")
	}
	if ctx.Err() != nil {
		return fail(g, 0, "cancelled", "command deadline or cancellation")
	}
	if err := writeIdentityPair(keyPath, certPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); err != nil {
		return fail(g, 1, "identity_write_refused", "identity files must be new and writable; nothing is overwritten")
	}
	sum := sha256.Sum256(der)
	fingerprint := hex.EncodeToString(sum[:])
	if !g.JSON {
		return emit(g, "identity.keygen", fingerprint)
	}
	return emit(g, "identity.keygen", struct {
		Fingerprint string `json:"fingerprint"`
		Cert        string `json:"cert"`
		Key         string `json:"key"`
	}{fingerprint, g.Cert, g.Key})
}
func writeIdentityPair(keyPath, certPath string, key, cert []byte) (err error) {
	private, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	public, err := os.OpenFile(certPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		private.Close()
		os.Remove(keyPath)
		return err
	}
	defer func() {
		private.Close()
		public.Close()
		if err != nil {
			os.Remove(keyPath)
			os.Remove(certPath)
		}
	}()
	if err = private.Chmod(0600); err != nil {
		return err
	}
	if err = public.Chmod(0644); err != nil {
		return err
	}
	if _, err = private.Write(key); err != nil {
		return err
	}
	if err = private.Sync(); err != nil {
		return err
	}
	if _, err = public.Write(cert); err != nil {
		return err
	}
	if err = public.Sync(); err != nil {
		return err
	}
	if err = private.Close(); err != nil {
		return err
	}
	if err = public.Close(); err != nil {
		return err
	}
	for _, dir := range []string{filepath.Dir(keyPath), filepath.Dir(certPath)} {
		f, e := os.Open(dir)
		if e != nil {
			return e
		}
		e = f.Sync()
		closeErr := f.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// ownerClient pins the exact server leaf and also verifies validity, serverAuth
// and the configured endpoint hostname. The fingerprint is explicit trust, not
// trust-on-first-use. VerifyConnection replaces (never skips) PKI verification.
func ownerClient(g globals) (*http.Client, error) {
	u, err := url.Parse(g.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.String() != g.Endpoint || !validPin(g.DaemonFingerprint) {
		return nil, errors.New("invalid endpoint or pin")
	}
	info, err := os.Lstat(g.Key)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return nil, errors.New("invalid client key")
	}
	cert, err := tls.LoadX509KeyPair(g.Cert, g.Key)
	if err != nil || cert.Leaf == nil || len(cert.Certificate) != 1 || cert.Leaf.IsCA || cert.Leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !slices.Contains(cert.Leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) {
		return nil, errors.New("invalid client identity")
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AddCert(cert.Leaf)
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{Roots: clientRoots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, errors.New("invalid client identity")
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}, NextProtos: []string{"http/1.1"}, InsecureSkipVerify: true} // verified below against the explicit pin
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.PeerCertificates) == 0 || state.NegotiatedProtocol != "http/1.1" {
			return errors.New("daemon identity denied")
		}
		leaf := state.PeerCertificates[0]
		sum := sha256.Sum256(leaf.Raw)
		fp := hex.EncodeToString(sum[:])
		if subtle.ConstantTimeCompare([]byte(fp), []byte(g.DaemonFingerprint)) != 1 || leaf.IsCA || leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0 || !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) {
			return errors.New("daemon identity denied")
		}
		roots := x509.NewCertPool()
		roots.AddCert(leaf)
		_, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: u.Hostname(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
		return err
	}
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: false, TLSClientConfig: cfg, TLSHandshakeTimeout: g.Timeout, ResponseHeaderTimeout: g.Timeout, MaxResponseHeaderBytes: 8192}
	return &http.Client{Transport: transport, Timeout: g.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, nil
}
func identitySelf(ctx context.Context, g globals, args []string) int {
	if len(args) != 0 {
		return fail(g, 400, "invalid_arguments", "identity self takes no arguments")
	}
	client, err := ownerClient(g)
	if err != nil {
		return fail(g, 400, "invalid_configuration", "explicit HTTPS endpoint, client certificate/key and daemon fingerprint are required")
	}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", g.Endpoint+"/api/v1/identity/self", nil)
	if err != nil {
		return fail(g, 400, "invalid_configuration", "invalid owner endpoint")
	}
	reply, err := client.Do(req)
	if err != nil {
		return fail(g, 0, "daemon_unavailable", "pinned mTLS request failed")
	}
	defer reply.Body.Close()
	body, err := io.ReadAll(io.LimitReader(reply.Body, maxReply+1))
	if err != nil || len(body) > maxReply || !json.Valid(body) {
		return fail(g, 0, "invalid_response", "daemon response was not bounded JSON")
	}
	if reply.StatusCode != 200 {
		var errorBody struct {
			Error  string `json:"error"`
			Detail string `json:"detail"`
		}
		json.Unmarshal(body, &errorBody)
		if errorBody.Error == "" {
			errorBody.Error = "request_refused"
		}
		return fail(g, reply.StatusCode, errorBody.Error, errorBody.Detail)
	}
	return emit(g, "identity.self", json.RawMessage(body))
}
func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
