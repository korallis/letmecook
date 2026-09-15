// Command gafferd serves local, non-executing workflow metadata.
// Installation is explicit flags only; --fixture retains disposable public demos.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/httpapi"
	"github.com/korallis/letmecook/internal/store"
)

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	flags := flag.NewFlagSet("gafferd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	fixture := flags.Bool("fixture", false, "fresh disposable public fixture, no persistent state")
	state := flags.String("state-dir", "", "absolute private local state directory")
	artifacts := flags.String("artifacts-dir", "", "absolute private local artifact directory (custody not implemented)")
	listen := flags.String("listen", "", "127.0.0.1:<port>; 0 selects an ephemeral port")
	if err = flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("positional arguments are not supported")
	}
	if *fixture {
		if *state != "" || *artifacts != "" || *listen != "" {
			return fmt.Errorf("fixture mode cannot use install configuration")
		}
		*listen = "127.0.0.1:0"
	} else if *state == "" || *artifacts == "" || *listen == "" {
		return fmt.Errorf("configure --state-dir, --artifacts-dir and --listen; or use --fixture for disposable public data")
	}
	host, port, splitErr := net.SplitHostPort(*listen)
	n, portErr := strconv.Atoi(port)
	if splitErr != nil || host != "127.0.0.1" || portErr != nil || n < 0 || n > 65535 || strconv.Itoa(n) != port {
		return fmt.Errorf("listen must be 127.0.0.1:<port> (0..65535); remote access requires future authentication")
	}
	listener, err := net.Listen("tcp4", *listen)
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
	handler, err := httpapi.New(s, listener.Addr())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer server.Close()
	mode := "store-only"
	if *fixture {
		mode = "fixture-only"
	}
	if _, err = fmt.Fprintf(out, "%s http://%s/api/v1/status\n", mode, listener.Addr()); err != nil {
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
