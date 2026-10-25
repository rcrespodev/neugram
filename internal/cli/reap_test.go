package cli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStartReapedLeavesNoZombie(t *testing.T) {
	pid, err := startReaped(exec.Command("true"))
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err == nil && strings.Contains(string(b), ") Z ") {
		t.Fatalf("child stayed a zombie: %s", b)
	}
}
