package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"time"
)

// StartPprof starts a separate HTTP listener for Go pprof (loopback by default).
// Returns a shutdown function; no-op when disabled.
func StartPprof(ctx context.Context, log *slog.Logger, enabled bool, listenAddr string) (func(context.Context) error, error) {
	if !enabled {
		return func(context.Context) error { return nil }, nil
	}
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		addr = "127.0.0.1:6060"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("pprof: invalid PPROF_LISTEN_ADDR %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("pprof: PPROF_LISTEN_ADDR must bind to loopback, got %q", addr)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		if log != nil {
			log.Info("pprof server listening", "addr", addr)
		}
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	shutdown := func(shutdownCtx context.Context) error {
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		select {
		case err := <-errCh:
			if err != nil {
				return err
			}
		default:
		}
		return nil
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(shutdownCtx)
	}()

	return shutdown, nil
}
