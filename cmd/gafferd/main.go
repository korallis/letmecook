// Command gafferd composes provisional owner and execution transport seams.
// Installation is explicit flags only; --fixture retains disposable public demos.
package main

import (
	"context"
	"crypto/tls"
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
	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/inference"
	"github.com/korallis/letmecook/internal/notify"
	"github.com/korallis/letmecook/internal/reconcile"
	sc "github.com/korallis/letmecook/internal/scheduler"
	"github.com/korallis/letmecook/internal/store"
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
	autoRetry := flags.Bool("auto-retry", false, "opt in to evidence-gated automatic retries (reconcile implementation pending)")
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
	// The refusing S0 seam is explicit. S5 replaces it with real recovery;
	// any other startup error prevents either listener from opening.
	if _, err = reconcile.Startup(ctx, reconcile.Deps{Store: s, Now: time.Now, AutoRetry: *autoRetry}); err != nil && !errors.Is(err, reconcile.ErrNotImplemented) {
		return err
	}
	err = nil
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	d := httpapi.Deps{Store: s, Hub: &notify.Hub{}, Gateway: gateway, Policy: admission}
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
		// Startup inputs/paths and library errors may contain sensitive material.
		fmt.Fprintln(os.Stderr, "gafferd startup or shutdown failed")
		os.Exit(1)
	}
}
