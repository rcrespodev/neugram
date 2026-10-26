package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/rcrespodev/neugram/internal/client"
	"github.com/rcrespodev/neugram/internal/server"
)

func hooksLog() (string, error) {
	home, err := client.Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "hooks.log"), nil
}

func defaultDB() string { return client.DefaultDB() }

// buildVersion is what /health reports; the revision makes a rebuilt binary
// replace a resident daemon of the same release.
func buildVersion() string { return Version + "+" + revision() }

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", client.Addr(), "address to listen on")
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

func newClient() *client.Client { return client.New(buildVersion()) }
