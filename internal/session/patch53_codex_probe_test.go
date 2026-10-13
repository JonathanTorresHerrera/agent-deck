package session

import (
	"os/exec"
	"testing"
	"time"
)

// Patch 53: the Codex process probe reads /proc/<pid>/cmdline instead of
// forking `ps -p` per process, with the same answer.
func TestPatch53_CodexPIDCheckFromProc(t *testing.T) {
	if !procFSAvailable() {
		t.Skip("no /proc")
	}
	cmd := exec.Command("sh", "-c", "sleep 30 # fake-CODEX-worker")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() }()
	time.Sleep(50 * time.Millisecond)

	if ok, err := isLikelyCodexProcessPID(cmd.Process.Pid); err != nil || !ok {
		t.Fatalf("codex child: ok=%v err=%v", ok, err)
	}
	plain := exec.Command("sleep", "30")
	if err := plain.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Process.Kill(); _, _ = plain.Process.Wait() }()
	if ok, err := isLikelyCodexProcessPID(plain.Process.Pid); err != nil || ok {
		t.Fatalf("plain sleep must not look like codex: ok=%v err=%v", ok, err)
	}

	// A process that is gone is an error, as `ps -p` reported it.
	gone := exec.Command("true")
	if err := gone.Run(); err != nil {
		t.Fatal(err)
	}
	if _, err := isLikelyCodexProcessPID(gone.Process.Pid); err == nil {
		t.Fatal("exited pid must report an error")
	}
}
