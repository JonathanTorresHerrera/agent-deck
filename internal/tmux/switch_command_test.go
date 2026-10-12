package tmux

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func TestSwitchTypeCommandExitsAndTeardownSucceeds(t *testing.T) {
	skipIfNoTmuxBinary(t)
	s := NewSession("agent-deck-switch-exit", t.TempDir())
	if err := s.runBoundedRun("new-session", "-d", "-s", s.Name, `sh -c 'while read l; do [ "$l" = /exit ] && exit 0; done'`); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	t.Cleanup(func() { _ = s.Kill() })
	pid, err := s.PanePID()
	if err != nil {
		t.Fatal(err)
	}
	RefreshSessionCache()
	if err := s.TypeCommand("/exit"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane process %d did not exit", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.ForgetCachedExistence()
	if err := s.KillAndWait(); err != nil {
		t.Fatalf("teardown after agent exit: %v", err)
	}
}

func TestSwitchTypeCommandRejectsMultiline(t *testing.T) {
	s := NewSession("agent-deck-switch-command", t.TempDir())
	for _, command := range []string{"", "/exit\n", "a\rb"} {
		if err := s.TypeCommand(command); err == nil {
			t.Fatalf("TypeCommand(%q) accepted invalid command", command)
		}
	}
}

func TestSwitchTypeCommandCheckWithholdsEnter(t *testing.T) {
	skipIfNoTmuxBinary(t)
	s := NewSession("agent-deck-switch-guard", t.TempDir())
	if err := s.runBoundedRun("new-session", "-d", "-s", s.Name, `sh -c 'while read l; do [ "$l" = /exit ] && exit 0; done'`); err != nil {
		t.Skipf("tmux unavailable: %v", err)
	}
	t.Cleanup(func() { _ = s.Kill() })
	if err := s.TypeCommandChecked("/exit", func(string) error { return errors.New("pane changed") }); err == nil {
		t.Fatal("guard refusal was ignored")
	}
	if !s.Exists() {
		t.Fatal("Enter was sent despite the guard refusal")
	}
}
