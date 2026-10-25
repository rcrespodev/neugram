package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRunClosesTheDatabaseBeforeReleasingThePort(t *testing.T) {
	addr := make(chan string, 2)
	var bindErr error
	opts := Options{Version: "v", DBPath: filepath.Join(t.TempDir(), "neugram.db")}
	opts.afterStoreClose = func() {
		ln, err := net.Listen("tcp", <-addr)
		if err == nil {
			ln.Close()
		}
		bindErr = err
	}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), "127.0.0.1:0", opts, func(a net.Addr) { addr <- a.String(); addr <- a.String() })
	}()
	a := <-addr
	resp, err := http.Post("http://"+a+"/shutdown", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after /shutdown")
	}
	if !errors.Is(bindErr, syscall.EADDRINUSE) {
		t.Fatalf("bind after store close: err = %v, want EADDRINUSE", bindErr)
	}
}
