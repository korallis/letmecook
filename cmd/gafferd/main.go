// Command gafferd serves local, non-executing workflow metadata.
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
	"strconv"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/httpapi"
	i "github.com/korallis/letmecook/internal/identity"
	"github.com/korallis/letmecook/internal/store"
)

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("gafferd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fixture := flags.Bool("fixture", false, "fresh disposable public fixture, no persistent state")
	state := flags.String("state-dir", "", "absolute private local state directory")
	artifacts := flags.String("artifacts-dir", "", "absolute private local artifact directory (custody not implemented)")
	listen := flags.String("listen", "", "explicit IP:port; plaintext restricted to 127.0.0.1")
	tlsCert := flags.String("tls-cert", "", "server certificate PEM")
	tlsKey := flags.String("tls-key", "", "private server key PEM, mode 0600")
	endpoint := flags.String("endpoint", "", "operator-selected HTTPS origin, matching server certificate")
	bootstrap := flags.String("bootstrap-owner-cert", "", "local one-use owner certificate pin; no listener")
	recoverOwner := flags.String("recover-owner-cert", "", "local replacement owner; revokes all identities/invites; no listener")
	if err = flags.Parse(args); err != nil {
		return i.Invalid
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("positional arguments are not supported")
	}
	secure := *tlsCert != "" || *tlsKey != "" || *endpoint != ""
	localIdentity := *bootstrap != "" || *recoverOwner != ""
	if localIdentity {
		if *fixture || secure || *listen != "" || *state == "" || *artifacts == "" || (*bootstrap != "" && *recoverOwner != "") {
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
		if *state != "" || *artifacts != "" || *listen != "" || secure {
			return fmt.Errorf("fixture mode cannot use install configuration")
		}
		*listen = "127.0.0.1:0"
	} else if *state == "" || *artifacts == "" || *listen == "" {
		return fmt.Errorf("configure --state-dir, --artifacts-dir and --listen; or use --fixture for disposable public data")
	}
	host, port, splitErr := net.SplitHostPort(*listen)
	n, portErr := strconv.Atoi(port)
	if splitErr != nil || net.ParseIP(host) == nil || (!secure && host != "127.0.0.1") || portErr != nil || n < 0 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("listen requires explicit IP:port; plaintext requires 127.0.0.1")
	}
	var tlsConfig *tls.Config
	if secure {
		if *tlsCert == "" || *tlsKey == "" || *endpoint == "" {
			return i.Invalid
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
	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	var s *store.Store
	if *fixture {
		s, err = store.New(ctx)
	} else {
		s, err = store.Open(ctx, *state, *artifacts)
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
	var handler http.Handler
	if secure {
		handler, err = httpapi.NewTLS(s, *endpoint)
	} else {
		handler, err = httpapi.New(s, listener.Addr())
	}
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096, ErrorLog: log.New(io.Discard, "", 0)}
	done := make(chan error, 1)
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
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
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
