// Package client is the HTTP client of the neugram daemon shared by the CLI,
// the MCP server and the hooks.
package client

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DefaultAddr    = "127.0.0.1:7438"
	requestTimeout = 10 * time.Second
	healthTimeout  = 200 * time.Millisecond
)

// ExpandHome resolves a leading "~" to the user's home directory.
func ExpandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}

// Home is ~/.neugram unless NEUGRAM_HOME points elsewhere.
func Home() (string, error) {
	if h := os.Getenv("NEUGRAM_HOME"); h != "" {
		return ExpandHome(h)
	}
	return ExpandHome("~/.neugram")
}

// DefaultDB is the database the daemon opens unless told otherwise.
func DefaultDB() string {
	home, err := Home()
	if err != nil {
		return "~/.neugram/neugram.db"
	}
	return filepath.Join(home, "neugram.db")
}

// Addr is NEUGRAM_ADDR or the default daemon address.
func Addr() string {
	if a := os.Getenv("NEUGRAM_ADDR"); a != "" {
		return a
	}
	return DefaultAddr
}

// Client talks to one daemon address; Version is the build the caller wants
// the daemon to be.
type Client struct {
	Addr    string
	Version string
	// Budget bounds how long EnsureDaemon waits for the daemon to change state.
	Budget time.Duration
	HTTP   *http.Client
	// Logf receives lifecycle notices; nil discards them.
	Logf func(format string, args ...any)
}

func New(version string) *Client {
	return &Client{
		Addr:    Addr(),
		Version: version,
		Budget:  2 * time.Second,
		HTTP:    &http.Client{Timeout: requestTimeout},
	}
}

// APIError is the error envelope of docs/specs/api.md.
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

func (e *APIError) Error() string {
	if e.Hint == "" {
		return e.Message
	}
	return e.Message + "\n  " + e.Hint
}

func (c *Client) url(path string) string { return "http://" + c.Addr + path }

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// Get decodes the JSON response of GET path into out.
func (c *Client) Get(path string, out any) error {
	resp, err := c.HTTP.Get(c.url(path))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var env struct{ Error APIError }
		if json.Unmarshal(body, &env) != nil || env.Error.Code == "" {
			return fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
		}
		return &env.Error
	}
	return json.Unmarshal(body, out)
}
