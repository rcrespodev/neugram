package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const daemonPoll = 50 * time.Millisecond

// Health is the /health response.
type Health struct {
	Version string `json:"version"`
}

func (c *Client) Health() (Health, error) {
	var h Health
	hc := &http.Client{Timeout: healthTimeout}
	resp, err := hc.Get(c.url("/health"))
	if err != nil {
		return h, err
	}
	defer resp.Body.Close()
	return h, json.NewDecoder(resp.Body).Decode(&h)
}

// StartDaemon launches `neugram serve` detached; tests replace it.
var StartDaemon = func(addr string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := Home()
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
	cmd := exec.Command(exe, "serve", "--addr", addr, "--db", DefaultDB())
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	_, err = startReaped(cmd)
	return err
}

// startReaped starts cmd and returns its pid, waiting for it in the
// background: Release alone leaves a zombie while this process lives (neugram mcp).
func startReaped(cmd *exec.Cmd) (int, error) {
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait()
	return cmd.Process.Pid, nil
}

// Policy says what EnsureDaemon may do to a live daemon of another version.
type Policy struct {
	// Replace asks that daemon to stop and starts this build's. Without it
	// the live daemon is used as is and the mismatch is only logged.
	Replace bool
}

const worktreeBuildMarker = "-wt-"

// replaceable reports whether this build may stop a live daemon running
// other. A worktree build must not take the shared daemon from a main build
// unless NEUGRAM_ADDR was set explicitly.
func (c *Client) replaceable(other string) bool {
	if strings.Contains(c.Version, worktreeBuildMarker) && !strings.Contains(other, worktreeBuildMarker) {
		return os.Getenv("NEUGRAM_ADDR") != ""
	}
	return true
}

// EnsureDaemon follows the lifecycle of docs/specs/hooks.md: with
// Policy.Replace a daemon of another version is asked to stop and replaced.
func (c *Client) EnsureDaemon(p Policy) error {
	want := c.Version
	h, err := c.Health()
	if err == nil && h.Version == want {
		return nil
	}
	if err == nil {
		if !p.Replace || !c.replaceable(h.Version) {
			c.logf("daemon en %s corre la versión %s, este binario es %s; no lo reemplazo", c.Addr, h.Version, want)
			return nil
		}
		if resp, err := c.HTTP.Post(c.url("/shutdown"), "application/json", bytes.NewReader(nil)); err == nil {
			resp.Body.Close()
		}
		if !c.waitFor(func(h Health, err error) bool { return err != nil || h.Version == want }) {
			return fmt.Errorf("el daemon en %s (versión %s) no se detuvo", c.Addr, h.Version)
		}
		if h, err := c.Health(); err == nil && h.Version == want {
			return nil
		}
	}
	if err := StartDaemon(c.Addr); err != nil {
		return fmt.Errorf("no pude lanzar neugram serve: %w", err)
	}
	if !c.waitFor(func(h Health, err error) bool { return err == nil && h.Version == want }) {
		return fmt.Errorf("el daemon no respondió en %s después de %s", c.Addr, c.Budget)
	}
	return nil
}

func (c *Client) waitFor(done func(Health, error) bool) bool {
	deadline := time.Now().Add(c.Budget)
	for {
		if done(c.Health()) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(daemonPoll)
	}
}
