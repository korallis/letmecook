// Command gafferd serves a fresh disposable public fixture. No config files,
// environment options, state paths, inference clients or process runners exist.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/korallis/letmecook/internal/httpapi"
	"github.com/korallis/letmecook/internal/store"
)

func run(ctx context.Context, args []string, out io.Writer) (err error) {
	if len(args) != 0 {
		return fmt.Errorf("fixture-only daemon accepts no arguments, binding, configuration or state import")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := store.New(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Dispose()) }()
	handler, err := httpapi.New(s, listener.Addr())
	if err != nil {
		return err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 2 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 5 * time.Second, MaxHeaderBytes: 4096}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer server.Close()
	if _, err = fmt.Fprintf(out, "fixture-only http://%s/api/v1/status\n", listener.Addr()); err != nil {
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
