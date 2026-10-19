package cli

import (
	"bytes"
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
