package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestVersionPrintsBuildVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "neugram "+Version+" (") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestNoArgsPrintsUsageAndFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(errOut.String(), "usage: neugram") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestUnknownCommandFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"nope"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(errOut.String(), `unknown command "nope"`) {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestImportLegacyRejectsUnknownFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"import-legacy", "--nope"}, &out, &errOut); code != 2 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestImportLegacyMissingFileFails(t *testing.T) {
	var out, errOut bytes.Buffer
	dir := t.TempDir()
	code := Run([]string{"import-legacy", "--dry-run", "--engram-db", dir + "/missing.db", "--db", dir + "/n.db"}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "no se escribió nada") {
		t.Fatalf("exit code = %d, stderr = %q", code, errOut.String())
	}
}

func TestDoctorOnMissingDatabaseFails(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Run([]string{"doctor", "--db", t.TempDir() + "/n.db"}, &out, &errOut); code != 1 {
		t.Fatalf("exit code = %d, stdout = %q", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got, _ := expandHome("~/.neugram/neugram.db"); got != home+"/.neugram/neugram.db" {
		t.Errorf("got %q", got)
	}
	if got, _ := expandHome("/abs/x.db"); got != "/abs/x.db" {
		t.Errorf("got %q", got)
	}
}
