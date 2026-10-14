package session

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGracefulStopExitSubmitsAndWaits(t *testing.T) {
	oldNow, oldSleep, oldAlive := gracefulStopNow, gracefulStopSleep, gracefulStopAlive
	defer func() { gracefulStopNow, gracefulStopSleep, gracefulStopAlive = oldNow, oldSleep, oldAlive }()
	now := time.Unix(100, 0)
	gracefulStopNow = func() time.Time { return now }
	gracefulStopSleep = func(d time.Duration) { now = now.Add(d) }
	probes := 0
	gracefulStopAlive = func(int) bool { probes++; return probes < 4 }
	pane := &scriptedSwitchPane{frames: []string{claudeSwitchFrame("")}}
	if !gracefulStopExit(pane, "claude", stopExitCommand("claude"), now.Add(time.Second)) {
		t.Fatal("expected graceful exit")
	}
	if pane.exitKeys != 1 || !pane.forgot {
		t.Fatalf("exit was not submitted and confirmed: %+v", pane)
	}
}

func TestGracefulStopExitTimesOutWithoutClobberingDraft(t *testing.T) {
	oldNow, oldSleep, oldAlive := gracefulStopNow, gracefulStopSleep, gracefulStopAlive
	defer func() { gracefulStopNow, gracefulStopSleep, gracefulStopAlive = oldNow, oldSleep, oldAlive }()
	now := time.Unix(100, 0)
	gracefulStopNow = func() time.Time { return now }
	gracefulStopSleep = func(d time.Duration) { now = now.Add(d) }
	gracefulStopAlive = func(int) bool { return true }
	pane := &scriptedSwitchPane{frames: []string{claudeSwitchFrame("unsent draft")}}
	if gracefulStopExit(pane, "claude", stopExitCommand("claude"), now.Add(400*time.Millisecond)) {
		t.Fatal("busy agent must not be considered exited")
	}
	if pane.exitKeys != 0 || len(pane.typed) != 0 {
		t.Fatalf("draft was disturbed: %+v", pane)
	}
}

func TestStopWithOptionsAlreadyDead(t *testing.T) {
	isolateGracefulStopTestHome(t)
	inst := NewInstance("already-dead", t.TempDir())
	outcome, err := inst.StopWithOptions(StopOptions{})
	if err != nil || outcome != StopAlreadyDead || inst.Status != StatusStopped {
		t.Fatalf("outcome=%s status=%s err=%v", outcome, inst.Status, err)
	}
}

func TestStopWithOptionsTimeoutKillsThrowawayPane(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	isolateGracefulStopTestHome(t)
	inst := NewInstance("graceful-stop-timeout", t.TempDir())
	inst.Account = ""
	inst.Tool = "shell"
	inst.Command = "sleep 60"
	if err := inst.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = inst.StopWithOptions(StopOptions{Force: true}) })
	// The throwaway pane has no Claude composer. Exit submission is withheld,
	// then the bounded timeout must invoke the synchronous kill path.
	inst.Tool = "claude"
	outcome, err := inst.StopWithOptions(StopOptions{Timeout: 100 * time.Millisecond})
	if err != nil || outcome != StopTimeoutKilled || inst.Exists() {
		t.Fatalf("outcome=%s alive=%t err=%v", outcome, inst.Exists(), err)
	}
}

func isolateGracefulStopTestHome(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "xdg"))
	t.Setenv("AGENTDECK_ACCOUNT", "")
}

func TestStopExitCommands(t *testing.T) {
	for tool, want := range map[string]string{"claude": "ctrl-d-twice", "codex": "/exit", "grok": "/exit", "gemini": "/quit", "shell": ""} {
		if got := stopExitCommand(tool); got != want {
			t.Errorf("%s: got %q want %q", tool, got, want)
		}
	}
}
