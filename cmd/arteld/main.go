// arteld is the local Artel API daemon.
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
	"path/filepath"
	"syscall"
	"time"

	"github.com/lllypuk/artel/internal/api"
	"github.com/lllypuk/artel/internal/local"
	"github.com/lllypuk/artel/internal/store"
)

func serve(ctx context.Context, args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet("arteld", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	data := flags.String("data-dir", "", "private data directory")
	tokenPath := flags.String("token-file", "", "private owner credential file")
	address := flags.String("listen", "127.0.0.1:7331", "numeric loopback address")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		_, _ = fmt.Fprintln(errOut, "arteld: invalid arguments")
		return 2
	}
	if err := local.ListenAddress(*address); err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld:", err)
		return 1
	}
	paths, err := local.Resolve(*data, *tokenPath)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld:", err)
		return 1
	}
	lock, err := local.Acquire(paths.DataDir)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld:", err)
		return 1
	}
	defer func() { _ = lock.Close() }()
	token, err := local.InitToken(paths.TokenFile)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld:", err)
		return 1
	}
	database, err := store.Open(ctx, filepath.Join(paths.DataDir, "artel.sqlite"))
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld: cannot initialize database")
		return 1
	}
	defer func() { _ = database.Close() }()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", *address)
	if err != nil {
		_, _ = fmt.Fprintln(errOut, "arteld: cannot bind local API")
		return 1
	}
	server := &http.Server{Handler: api.SecureHandler(token, listener.Addr().String()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if shutdownErr := server.Shutdown(shutdown); shutdownErr != nil {
				_ = server.Close()
			}
		case <-done:
		}
	}()
	_, _ = fmt.Fprintln(out, "arteld: listening", listener.Addr().String())
	err = server.Serve(listener)
	close(done)
	<-stopped
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		_, _ = fmt.Fprintln(errOut, "arteld: unable to serve local API")
		return 1
	}
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := serve(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
