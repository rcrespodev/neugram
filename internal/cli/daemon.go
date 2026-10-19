package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rcrespodev/neugram/internal/server"
)

const (
	defaultAddr    = "127.0.0.1:7438"
	daemonWait     = 2 * time.Second
	daemonPoll     = 50 * time.Millisecond
	requestTimeout = 10 * time.Second
)

// neugramHome is ~/.neugram unless NEUGRAM_HOME points elsewhere.
func neugramHome() (string, error) {
	if h := os.Getenv("NEUGRAM_HOME"); h != "" {
		return expandHome(h)
	}
	return expandHome("~/.neugram")
}

func hooksLog() (string, error) {
	home, err := neugramHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hooks.log"), nil
}

func defaultDB() string {
	home, err := neugramHome()
	if err != nil {
		return "~/.neugram/neugram.db"
	}
	return filepath.Join(home, "neugram.db")
}

func daemonAddr() string {
	if a := os.Getenv("NEUGRAM_ADDR"); a != "" {
		return a
	}
	return defaultAddr
}

// buildVersion is what /health reports; the revision makes a rebuilt binary
// replace a resident daemon of the same release.
func buildVersion() string { return Version + "+" + revision() }

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", daemonAddr(), "address to listen on")
	db := fs.String("db", defaultDB(), "neugram database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := expandHome(*db)
	if err != nil {
		fmt.Fprintf(stderr, "neugram: %v\n", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logPath, err := hooksLog()
	if err != nil {
		fmt.Fprintf(stderr, "neugram: %v\n", err)
		return 1
	}
	opts := server.Options{Version: buildVersion(), DBPath: path, LogPath: logPath}
	err = server.Run(ctx, *addr, opts, func(a net.Addr) {
		fmt.Fprintf(stderr, "neugram serve: %s escuchando en %s (db %s)\n", buildVersion(), a, path)
	})
	if errors.Is(err, server.ErrAddrInUse) {
		fmt.Fprintf(stderr, "neugram serve: %s ya está tomado; otro serve corre, salgo sin tocar nada\n", *addr)
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "neugram serve: %v\n", err)
		return 1
	}
	return 0
}

type client struct {
	addr string
	http *http.Client
}

func newClient() *client {
	return &client{addr: daemonAddr(), http: &http.Client{Timeout: requestTimeout}}
}

// apiError is the error envelope of docs/specs/api.md.
type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e *apiError) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + "\n  " + e.Hint
}

func (c *client) url(path string) string { return "http://" + c.addr + path }

func (c *client) get(path string, out any) error {
	resp, err := c.http.Get(c.url(path))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var env struct{ Error apiError }
		if json.Unmarshal(body, &env) != nil || env.Error.Code == "" {
			return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
		}
		return &env.Error
	}
	return json.Unmarshal(body, out)
}

type health struct {
	Version string `json:"version"`
}

func (c *client) health() (health, error) {
	var h health
	hc := &http.Client{Timeout: daemonPoll * 4}
	resp, err := hc.Get(c.url("/health"))
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	return h, json.NewDecoder(resp.Body).Decode(&h)
}

// startDaemon launches `neugram serve` detached; tests replace it.
var startDaemon = func(addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := neugramHome()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(home, "serve.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	cmd := exec.Command(exe, "serve", "--addr", addr, "--db", defaultDB())
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// ensureDaemon follows the lifecycle of docs/specs/hooks.md: a daemon of
// another version is asked to stop and replaced.
func (c *client) ensureDaemon() error {
	want := buildVersion()
	h, err := c.health()
	if err == nil && h.Version == want {
		return nil
	}
	if err == nil {
		if resp, err := c.http.Post(c.url("/shutdown"), "application/json", bytes.NewReader(nil)); err == nil {
			resp.Body.Close()
		}
		if !c.waitFor(func(h health, err error) bool { return err != nil || h.Version == want }) {
			return fmt.Errorf("el daemon en %s (versión %s) no se detuvo", c.addr, h.Version)
		}
		if h, err := c.health(); err == nil && h.Version == want {
			return nil
		}
	}
	if err := startDaemon(c.addr); err != nil {
		return fmt.Errorf("no pude lanzar neugram serve: %w", err)
	}
	if !c.waitFor(func(h health, err error) bool { return err == nil && h.Version == want }) {
		return fmt.Errorf("el daemon no respondió en %s después de %s", c.addr, daemonWait)
	}
	return nil
}

func (c *client) waitFor(done func(health, error) bool) bool {
	deadline := time.Now().Add(daemonWait)
	for {
		if done(c.health()) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(daemonPoll)
	}
}
