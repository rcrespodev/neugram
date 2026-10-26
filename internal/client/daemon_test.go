package client

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeDaemon struct {
	version   atomic.Value
	shutdowns atomic.Int32
	starts    atomic.Int32
	client    *Client
}

func newFakeDaemon(t *testing.T, running, want string) *fakeDaemon {
	t.Helper()
	d := &fakeDaemon{}
	d.version.Store(running)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		v := d.version.Load().(string)
		if v == "" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"version": v})
	})
	mux.HandleFunc("POST /shutdown", func(w http.ResponseWriter, r *http.Request) {
		d.shutdowns.Add(1)
		d.version.Store("")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	prev := StartDaemon
	StartDaemon = func(string) error {
		d.starts.Add(1)
		d.version.Store(want)
		return nil
	}
	t.Cleanup(func() { StartDaemon = prev })
	d.client = New(want)
	d.client.Addr = strings.TrimPrefix(srv.URL, "http://")
	return d
}

func TestReplaceShutsDownADaemonOfAnotherVersionAndStartsOurs(t *testing.T) {
	d := newFakeDaemon(t, "old", "new")
	if err := d.client.EnsureDaemon(Policy{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if d.shutdowns.Load() != 1 || d.starts.Load() != 1 {
		t.Fatalf("shutdowns %d, starts %d", d.shutdowns.Load(), d.starts.Load())
	}
}

func TestWithoutReplaceALiveDaemonOfAnotherVersionIsLeftAlone(t *testing.T) {
	d := newFakeDaemon(t, "old", "new")
	var logged int
	d.client.Logf = func(string, ...any) { logged++ }
	if err := d.client.EnsureDaemon(Policy{}); err != nil {
		t.Fatal(err)
	}
	if d.shutdowns.Load() != 0 || d.starts.Load() != 0 || logged != 1 {
		t.Fatalf("shutdowns %d, starts %d, logged %d", d.shutdowns.Load(), d.starts.Load(), logged)
	}
}

func TestWithoutReplaceAMissingDaemonIsStarted(t *testing.T) {
	d := newFakeDaemon(t, "", "new")
	if err := d.client.EnsureDaemon(Policy{}); err != nil {
		t.Fatal(err)
	}
	if d.starts.Load() != 1 {
		t.Fatalf("starts = %d", d.starts.Load())
	}
}

func TestWorktreeBuildDoesNotReplaceAMainDaemonWithoutExplicitAddr(t *testing.T) {
	t.Setenv("NEUGRAM_ADDR", "")
	d := newFakeDaemon(t, "0.1+abc", "0.1+abc-wt-feature")
	if err := d.client.EnsureDaemon(Policy{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if d.shutdowns.Load() != 0 || d.starts.Load() != 0 {
		t.Fatalf("shutdowns %d, starts %d", d.shutdowns.Load(), d.starts.Load())
	}
}

func TestWorktreeBuildReplacesWhenAddrIsExplicit(t *testing.T) {
	t.Setenv("NEUGRAM_ADDR", "127.0.0.1:1")
	d := newFakeDaemon(t, "0.1+abc", "0.1+abc-wt-feature")
	if err := d.client.EnsureDaemon(Policy{Replace: true}); err != nil {
		t.Fatal(err)
	}
	if d.shutdowns.Load() != 1 || d.starts.Load() != 1 {
		t.Fatalf("shutdowns %d, starts %d", d.shutdowns.Load(), d.starts.Load())
	}
}

func TestUnstartableDaemonFails(t *testing.T) {
	d := newFakeDaemon(t, "", "new")
	StartDaemon = func(string) error { return errors.New("boom") }
	err := d.client.EnsureDaemon(Policy{})
	if err == nil || !strings.Contains(err.Error(), "no pude lanzar neugram serve") {
		t.Fatalf("err = %v", err)
	}
}

func TestBudgetBoundsTheWaitForAStartedDaemon(t *testing.T) {
	d := newFakeDaemon(t, "", "new")
	StartDaemon = func(string) error { return nil }
	d.client.Budget = 100 * time.Millisecond
	start := time.Now()
	err := d.client.EnsureDaemon(Policy{})
	if err == nil || !strings.Contains(err.Error(), "no respondió") || time.Since(start) > time.Second {
		t.Fatalf("err = %v after %s", err, time.Since(start))
	}
}
