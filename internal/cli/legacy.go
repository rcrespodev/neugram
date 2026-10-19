package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rcrespodev/neugram/internal/doctor"
	"github.com/rcrespodev/neugram/internal/legacy"
)

const defaultEngramDB = "~/.engram/engram.db"

func runImportLegacy(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-legacy", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dryRun := fs.Bool("dry-run", false, "verify and print the summary without writing")
	engramDB := fs.String("engram-db", defaultEngramDB, "legacy Engram database, opened read-only")
	db := fs.String("db", defaultDB(), "target neugram database")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	legacyPath, err := expandHome(*engramDB)
	if err != nil {
		fmt.Fprintf(stderr, "neugram: %v\n", err)
		return 1
	}
	target, err := expandHome(*db)
	if err != nil {
		fmt.Fprintf(stderr, "neugram: %v\n", err)
		return 1
	}

	sum, err := legacy.Import(context.Background(), legacy.Options{LegacyPath: legacyPath, TargetPath: target, DryRun: *dryRun})
	if sum != nil {
		sum.WriteChecks(stdout)
	}
	if errors.Is(err, legacy.ErrVerification) {
		fmt.Fprintln(stderr, "neugram: import-legacy abortado: una verificación de procedencia falló; no se escribió nada")
		return 1
	}
	if err != nil {
		fmt.Fprintf(stderr, "neugram: import-legacy: %v; no se escribió nada\n", err)
		return 1
	}
	fmt.Fprintln(stdout)
	if *dryRun {
		fmt.Fprintf(stdout, "dry-run: %s → %s, sin escribir\n\n", legacyPath, target)
	} else {
		fmt.Fprintf(stdout, "importado: %s → %s\n\n", legacyPath, target)
	}
	sum.Write(stdout)
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", defaultDB(), "neugram database to check")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	path, err := expandHome(*db)
	if err != nil {
		fmt.Fprintf(stderr, "neugram: %v\n", err)
		return 1
	}
	results, err := doctor.Run(context.Background(), path)
	if err != nil {
		fmt.Fprintf(stderr, "neugram: doctor: %v\n", err)
		return 1
	}
	doctor.Write(stdout, results)
	if doctor.Failed(results) {
		return 1
	}
	return 0
}

func expandHome(p string) (string, error) {
	if p != "~" && !strings.HasPrefix(p, "~/") {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~")), nil
}
