package tmux

import (
	"errors"
	"strings"
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

func TestSwitchTypeCommandClearsOnlyItsOwnDraftAfterGuardRefusal(t *testing.T) {
	skipIfNoTmuxBinary(t)
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "injected", true: "operator-edited"}[changed], func(t *testing.T) {
			s := NewSession("agent-deck-switch-cleanup", t.TempDir())
			if err := s.runBoundedRun("new-session", "-d", "-s", s.Name, `sh -c 'while read l; do [ "$l" = /exit ] && exit 0; done'`); err != nil {
				t.Skipf("tmux unavailable: %v", err)
			}
			t.Cleanup(func() { _ = s.Kill() })
			match := func(raw string) bool { return strings.HasSuffix(strings.TrimSpace(raw), "/exit") }
			err := s.TypeCommandChecked("/exit", func(string) error {
				if changed {
					if err := s.sendKeysToTarget(s.Name, "operator"); err != nil {
						return err
					}
				}
				return errors.New("pane changed")
			}, match)
			if err == nil || !strings.Contains(err.Error(), "pane changed") {
				t.Fatalf("guard error was lost: %v", err)
			}
			raw, err := s.CapturePaneFresh()
			if err != nil {
				t.Fatal(err)
			}
			if changed && !strings.HasSuffix(strings.TrimSpace(raw), "/exitoperator") {
				t.Fatalf("operator edit was cleared: %q", raw)
			}
			if !changed && match(raw) {
				t.Fatalf("injected command remained in composer: %q", raw)
			}
		})
	}
}
