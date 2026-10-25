package server

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunWaitsForInflightHandlersWhenGraceExpires(t *testing.T) {
	old := shutdownGrace
	shutdownGrace = 100 * time.Millisecond
	t.Cleanup(func() { shutdownGrace = old })

	var finished atomic.Bool
	var finishedAtClose bool
	started := make(chan struct{})
	opts := Options{Version: "v", DBPath: filepath.Join(t.TempDir(), "neugram.db")}
	opts.wrapHandler = func(next http.Handler) http.Handler {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			time.Sleep(50 * time.Millisecond)
			finished.Store(true)
		})
		mux.Handle("/", next)
		return mux
	}
	opts.afterStoreClose = func() { finishedAtClose = finished.Load() }
	addr := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), "127.0.0.1:0", opts, func(a net.Addr) { addr <- a.String() })
	}()
	a := <-addr
	go http.Get("http://" + a + "/slow")
	<-started
	resp, err := http.Post("http://"+a+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after /shutdown")
	}
	if !finishedAtClose {
		t.Fatal("store closed while a handler was still running")
	}
}
