package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"syscall"
	"time"

	"github.com/rcrespodev/neugram/internal/store"
)

// ErrAddrInUse means another serve won the race for the port, which is the
// daemon's lock (docs/specs/hooks.md).
var ErrAddrInUse = errors.New("address already in use")

const idleTimeout = 60 * time.Second

var shutdownGrace = 5 * time.Second

// keepOpen makes http.Server.Shutdown leave the port bound: Run releases it
// only after the store is closed, so a replacement never meets a live writer.
type keepOpen struct{ *net.TCPListener }

func (k keepOpen) Close() error { return k.SetDeadline(time.Now()) }

// Run binds addr before opening opts.DBPath, then serves until ctx ends or a
// client calls POST /shutdown. ready, when not nil, receives the bound address.
func Run(ctx context.Context, addr string, opts Options, ready func(net.Addr)) error {
	nl, err := net.Listen("tcp", addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return ErrAddrInUse
	}
	if err != nil {
		return err
	}
	ln := nl.(*net.TCPListener)
	defer ln.Close()
	st, err := store.Open(opts.DBPath)
	if err != nil {
		return err
	}
	defer func() {
		if opts.afterStoreClose != nil {
			opts.afterStoreClose()
		}
	}()
	defer st.Close()

	s := New(st, opts)
	var inflight sync.WaitGroup
	handler := s.Handler()
	if opts.wrapHandler != nil {
		handler = opts.wrapHandler(handler)
	}
	srv := &http.Server{Handler: trackInflight(&inflight, handler), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: idleTimeout}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(keepOpen{ln}) }()
	if ready != nil {
		ready(ln.Addr())
	}

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	case <-s.Stopping():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		srv.Close()
		waitFor(&inflight, shutdownGrace)
		return err
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func trackInflight(wg *sync.WaitGroup, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wg.Add(1)
		defer wg.Done()
		h.ServeHTTP(w, r)
	})
}

func waitFor(wg *sync.WaitGroup, d time.Duration) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
	}
}
