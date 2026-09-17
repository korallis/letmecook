// Command gafferd composes provisional owner and execution transport seams.
// Installation is explicit flags only; --fixture retains disposable public demos.
package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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

	g "github.com/korallis/letmecook/internal/authority"
	"github.com/korallis/letmecook/internal/backup"
	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/isolation"
	"github.com/korallis/letmecook/internal/jobs"
	"github.com/korallis/letmecook/internal/notify"
	"github.com/korallis/letmecook/internal/reconcile"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
	"github.com/korallis/letmecook/internal/verification"
)

type profiles []string

func (p *profiles) String() string { return strings.Join(*p, ",") }
func (p *profiles) Set(value string) error {
	if !g.ValidActor(value) || slices.Contains(*p, value) {
		return i.Invalid
	}
	*p = append(*p, value)
	return nil
}
func listenAddress(value string, plaintext bool) bool {
	host, port, err := net.SplitHostPort(value)
	n, portErr := strconv.Atoi(port)
	return err == nil && net.ParseIP(host) != nil && (!plaintext || host == "127.0.0.1") && portErr == nil && n >= 0 && n <= 65535 && strconv.Itoa(n) == port
}

// prepareVerificationState keeps the verifier's canary root explicit and private.
// Preserve the installation rule that the parent already exists, and never chmod
// an operator's directory to make a failed preflight appear qualified.
func prepareVerificationState(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) {
		return fmt.Errorf("verification state directory %q must be absolute, clean and non-root", path)
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return fmt.Errorf("verification state parent %q must already exist: %w", filepath.Dir(path), err)
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return fmt.Errorf("create verification state directory %q with required mode 0700: %w", path, err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect verification state directory %q: %w", path, err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("verification state directory %q must be a real directory with mode 0700 (no group/other permission bits); existing permissions were not changed", path)
	}
	return nil
}

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("gafferd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fixture := flags.Bool("fixture", false, "fresh disposable public fixture, no persistent state")
	state := flags.String("state-dir", "", "absolute private local state directory")
	artifacts := flags.String("artifacts-dir", "", "absolute private local artifact directory")
	listen := flags.String("listen", "", "explicit IP:port; plaintext restricted to 127.0.0.1")
	tlsCert := flags.String("tls-cert", "", "server certificate PEM")
	tlsKey := flags.String("tls-key", "", "private server key PEM, mode 0600")
	executionListen := flags.String("execution-listen", "", "execution IP:port, required with TLS; no plaintext or fixture mode")
	var developmentProfiles profiles
	flags.Var(&developmentProfiles, "allow-development-profile", "explicit development profile ID; repeatable; default none")
	verificationProfile := flags.String("verification-isolation-profile", "unqualified", "verification isolation profile; default refuses execution")
	autoRetry := flags.Bool("auto-retry", false, "opt in to evidence-gated automatic retries for infrastructure failures")
	gatewayPath := flags.String("gateway-config", "", "explicit gateway-config-v1 JSON path; no credential is read")
	backupDir := flags.String("backup-dir", "", "absolute clean backup destination root")
	endpoint := flags.String("endpoint", "", "operator-selected HTTPS origin, matching server certificate")
	bootstrap := flags.String("bootstrap-owner-cert", "", "local one-use owner certificate pin; no listener")
	recoverOwner := flags.String("recover-owner-cert", "", "local replacement owner; revokes all identities/invites; no listener")
	if err = flags.Parse(args); err != nil {
		return i.Invalid
	}
	admission := sc.AdmissionPolicy{DevelopmentProfiles: slices.Clone([]string(developmentProfiles))}
	if flags.NArg() != 0 {
		return fmt.Errorf("positional arguments are not supported")
	}
	installSeams := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "execution-listen", "allow-development-profile", "verification-isolation-profile", "auto-retry", "gateway-config", "backup-dir":
			installSeams = true
		}
	})
	if *backupDir != "" && (!filepath.IsAbs(*backupDir) || filepath.Clean(*backupDir) != *backupDir || strings.ContainsRune(*backupDir, 0)) {
		return i.Invalid
	}
	if *verificationProfile != "unqualified" && (*verificationProfile != "macos-sandbox-exec-dev" || !slices.Contains(developmentProfiles, *verificationProfile)) {
		return i.Invalid
	}
	var gateway inference.Gateway
	if *gatewayPath != "" {
		gateway, err = inference.LoadGatewayConfig(*gatewayPath)
		if err != nil {
			return err
		}
	}
	secure := *tlsCert != "" || *tlsKey != "" || *endpoint != ""
	localIdentity := *bootstrap != "" || *recoverOwner != ""
	if localIdentity {
		if *fixture || secure || installSeams || *listen != "" || *state == "" || *artifacts == "" || (*bootstrap != "" && *recoverOwner != "") {
			return i.Invalid
		}
		path := *bootstrap
		if *recoverOwner != "" {
			path = *recoverOwner
		}
		fingerprint, readErr := i.ReadCertificate(path)
		if readErr != nil {
			return readErr
		}
		var s *store.Store
		s, err = store.Open(ctx, *state, *artifacts)
		if err != nil {
			return i.Unavailable
		}
		defer func() { err = errors.Join(err, s.Close()) }()
		if err = s.BootstrapOwner(ctx, fingerprint, *recoverOwner != ""); err != nil {
			return err
		}
		_, err = fmt.Fprintln(out, "owner identity committed; HTTPS required")
		return err
	}
	if *fixture {
		if *state != "" || *artifacts != "" || *listen != "" || secure || installSeams {
			return fmt.Errorf("fixture mode cannot use install configuration")
		}
		*listen = "127.0.0.1:0"
	} else if *state == "" || *artifacts == "" || *listen == "" {
		return fmt.Errorf("configure --state-dir, --artifacts-dir and --listen; or use --fixture for disposable public data")
	}
	if !listenAddress(*listen, !secure) {
		return fmt.Errorf("listen requires explicit IP:port; plaintext requires 127.0.0.1")
	}
	if (*executionListen != "") != secure || (secure && !listenAddress(*executionListen, false)) {
		return i.Invalid
	}
	var tlsConfig, executionTLS *tls.Config
	if secure {
		if *tlsCert == "" || *tlsKey == "" || *endpoint == "" {
			return i.Invalid
		}
		executionTLS, err = i.ExecutionTLS(*tlsCert, *tlsKey)
		if err != nil {
			return err
		}
		tlsConfig, err = i.ServerTLS(*tlsCert, *tlsKey)
		if err != nil {
			return err
		}
		u, e := url.Parse(*endpoint)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || tlsConfig.Certificates[0].Leaf.VerifyHostname(u.Hostname()) != nil {
			return i.Invalid
		}
	}
	if secure && *verificationProfile == isolation.DevelopmentProfileID {
		if err = prepareVerificationState(*state); err != nil {
			return err
		}
	}
	var s *store.Store
	if *fixture {
		s, err = store.New(ctx)
	} else {
		// Development profiles named on the command line are the only admission
		// relaxation; the default policy refuses every unsupported profile.
		s, err = store.OpenWithOptions(ctx, *state, *artifacts, store.Options{Admission: admission})
	}
	if err != nil {
		return err
	}
	defer func() {
		if *fixture {
			err = errors.Join(err, s.Dispose())
		} else {
			err = errors.Join(err, s.Close())
		}
	}()
	// Reconcile durable state before either listener can admit work. The sweep
	// lifetime is joined before the store closes, including on startup failures.
	reconcileDeps := reconcile.Deps{Store: s, Now: time.Now, AutoRetry: *autoRetry}
	if _, err = reconcile.Startup(ctx, reconcileDeps); err != nil {
		return err
	}
	sweepCtx, stopSweeps := context.WithCancel(ctx)
	sweepsDone := make(chan struct{})
	go func() {
		defer close(sweepsDone)
		reconcile.RunSweeps(sweepCtx, reconcileDeps, 5*time.Second)
	}()
	defer func() { stopSweeps(); <-sweepsDone }()
	d := httpapi.Deps{Store: s, Sinks: s.Streams(), Hub: &notify.Hub{}, Gateway: gateway, Policy: admission, Reconcile: reconcile.NewReader(s)}
	if secure {
		// The listener pin is the SHA-256 of the server leaf DER; identity.Fingerprint
		// validates client leaves only, so the digest is computed directly.
		sum := sha256.Sum256(executionTLS.Certificates[0].Leaf.Raw)
		d.DaemonFingerprint = hex.EncodeToString(sum[:])
	}
	if secure {
		if *backupDir != "" {
			d.Backup, err = backup.NewService(s, *artifacts, filepath.Join(*state, "streams"), *backupDir)
			if err != nil {
				return err
			}
		}
		var verifier verification.Profile = verification.Unqualified{}
		if *verificationProfile == isolation.DevelopmentProfileID {
			secretPaths := []string{}
			for _, path := range []string{*tlsKey, *tlsCert, *gatewayPath, *backupDir, *artifacts, filepath.Join(*state, "state.db"), filepath.Join(*state, "state.db-wal"), filepath.Join(*state, "state.db-shm"), filepath.Join(*state, "streams")} {
				if path == "" {
					continue
				}
				absolute, e := filepath.Abs(path)
				if e != nil {
					return e
				}
				secretPaths = append(secretPaths, absolute)
			}
			// Do not deny StateDir itself: verifier checkouts live in StateDir/verify.
			profile, e := isolation.DevelopmentProfile(isolation.SandboxConfig{BoundaryPort: 0, CredentialPath: gateway.CredentialRef.Path, SecretPaths: secretPaths})
			if e != nil {
				return e
			}
			verifier, err = verification.NewDevelopmentProfile(profile, time.Hour, *state)
			if err != nil {
				return fmt.Errorf("development verification refused for state directory %q: requires a canonical directory with mode 0700 outside system-temp exception roots: %w", *state, err)
			}
		}
		handlers := httpapi.WorkflowJobHandlers(d, httpapi.VerificationOptions{StateDir: *state, Profile: verifier})
		if d.Backup != nil {
			handlers["backup"] = func(ctx context.Context, j jobs.Job) (json.RawMessage, error) {
				manifest, e := d.Backup.Create(ctx, j.SubjectID)
				if e != nil {
					return nil, e
				}
				return json.Marshal(manifest)
			}
		}
		worker, e := jobs.NewWorker(ctx, s, handlers)
		if e != nil {
			return e
		}
		d.Jobs = worker
		defer func() { err = errors.Join(err, worker.Close()) }()
	}
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	var executionServer *http.Server
	var executionListener net.Listener
	if secure {
		raw, listenErr := net.Listen("tcp", *executionListen)
		if listenErr != nil {
			return listenErr
		}
		executionListener = httpapi.ExecutionListener(raw, executionTLS, 5*time.Second)
		defer executionListener.Close()
		executionHandler, handlerErr := httpapi.NewExecution(s, d)
		if handlerErr != nil {
			return handlerErr
		}
		executionServer = &http.Server{Handler: executionHandler, ConnContext: httpapi.ExecutionConnContext, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 150 * time.Second, WriteTimeout: 150 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
		defer executionServer.Close()
	}
	var handler http.Handler
	if secure {
		handler, err = httpapi.NewTLSWithDeps(s, *endpoint, d)
	} else {
		handler, err = httpapi.New(s, listener.Addr())
	}
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 2)
	if executionServer != nil {
		go func() { done <- executionServer.Serve(executionListener) }()
	}
	go func() {
		if secure {
			done <- server.Serve(tls.NewListener(listener, tlsConfig))
		} else {
			done <- server.Serve(listener)
		}
	}()
	defer server.Close()
	mode := "store-only"
	if *fixture {
		mode = "fixture-only"
	}
	origin := "http://" + listener.Addr().String()
	if secure {
		origin = *endpoint
	}
	if _, err = fmt.Fprintf(out, "%s %s/api/v1/status\n", mode, origin); err != nil {
		return err
	}
	if executionListener != nil {
		if _, err = fmt.Fprintf(out, "execution https://%s/x/v1\n", executionListener.Addr().String()); err != nil {
			return err
		}
	}
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ownerErr := server.Shutdown(shutdown)
		if executionServer != nil {
			return errors.Join(ownerErr, executionServer.Shutdown(shutdown))
		}
		return ownerErr
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		// Preserve actionable install diagnostics. Gateway configuration errors
		// are sanitized by the loader; never print the configuration itself.
		fmt.Fprintf(os.Stderr, "gafferd startup or shutdown failed: %v\n", err)
		os.Exit(1)
	}
}
