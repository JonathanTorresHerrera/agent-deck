package session

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type scriptedSwitchPane struct {
	frames     []string
	reads      int
	typed      []string
	forgot     bool
	captureErr error
}

func (p *scriptedSwitchPane) CapturePaneFresh() (string, error) {
	if p.captureErr != nil {
		return "", p.captureErr
	}
	i := p.reads
	p.reads++
	if i >= len(p.frames) {
		i = len(p.frames) - 1
	}
	return p.frames[i], nil
}
func (p *scriptedSwitchPane) PanePID() (int, error) { return 42, nil }
func (p *scriptedSwitchPane) TypeCommandChecked(s string, check func(string) error) error {
	if err := check(claudeSwitchFrame(s)); err != nil {
		return err
	}
	p.typed = append(p.typed, s)
	return nil
}
func (p *scriptedSwitchPane) ForgetCachedExistence() { p.forgot = true }

func claudeSwitchFrame(draft string) string {
	border := strings.Repeat("─", 40)
	return "prior output\n" + border + "\n❯ " + draft + "\n" + border + "\n"
}

func TestGracefulSwitchExitWaitsForIdleAndAgentExit(t *testing.T) {
	oldNow, oldSleep, oldAlive := switchExitNow, switchExitSleep, switchExitAlive
	defer func() { switchExitNow, switchExitSleep, switchExitAlive = oldNow, oldSleep, oldAlive }()
	now := time.Unix(100, 0)
	switchExitNow = func() time.Time { return now }
	switchExitSleep = func(d time.Duration) { now = now.Add(d) }
	probes := 0
	switchExitAlive = func(int) bool { probes++; return probes < 3 }
	pane := &scriptedSwitchPane{frames: []string{claudeSwitchFrame("unsent draft"), claudeSwitchFrame("")}}
	if err := gracefulSwitchExit(pane, "claude", nil); err != nil {
		t.Fatal(err)
	}
	if pane.reads != 2 || len(pane.typed) != 1 || pane.typed[0] != "/exit" || !pane.forgot {
		t.Fatalf("wrong switch exit sequence: %+v", pane)
	}
}

func TestGracefulSwitchExitRefusesUnsafeComposer(t *testing.T) {
	oldNow, oldSleep := switchExitNow, switchExitSleep
	defer func() { switchExitNow, switchExitSleep = oldNow, oldSleep }()
	now := time.Unix(100, 0)
	switchExitNow = func() time.Time { return now }
	switchExitSleep = func(d time.Duration) { now = now.Add(d) }
	pane := &scriptedSwitchPane{frames: []string{claudeSwitchFrame("operator draft")}}
	if err := gracefulSwitchExit(pane, "claude", nil); err == nil || !strings.Contains(err.Error(), "did not become idle") {
		t.Fatalf("wanted idle refusal, got %v", err)
	}
	if len(pane.typed) != 0 || pane.forgot {
		t.Fatal("unsafe composer was mutated")
	}
	pane = &scriptedSwitchPane{captureErr: errors.New("pane unavailable")}
	if err := gracefulSwitchExit(pane, "claude", nil); err == nil || len(pane.typed) != 0 {
		t.Fatalf("unreadable pane must refuse before typing: %v", err)
	}
}

func TestSwitchComposerReadyRejectsBusyCodex(t *testing.T) {
	frame := "• Working (6s • esc to interrupt)\n› Ask Codex to do anything\n  model footer\n"
	if ready, _ := switchComposerReady(frame, "codex"); ready {
		t.Fatal("busy Codex composer read as idle")
	}
	frame = "› Ask Codex to do anything\n  model footer\n"
	if ready, reason := switchComposerReady(frame, "codex"); !ready {
		t.Fatalf("idle Codex composer refused: %s", reason)
	}
}

func TestGracefulSwitchRefusesExitToShellBeforeTyping(t *testing.T) {
	inst := NewInstanceWithTool("switch-shell", t.TempDir(), "claude")
	enabled := true
	inst.ExitToShell = &enabled
	if err := gracefulSwitchStop(inst); err == nil || !strings.Contains(err.Error(), "exit_to_shell=false") {
		t.Fatalf("exit_to_shell must refuse before touching a pane: %v", err)
	}
}
