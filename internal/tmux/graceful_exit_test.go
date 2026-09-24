package tmux

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

// TestTypeCommand_AgentExitsAndTeardownStillSucceeds is the graceful-stop
// path end to end against a real (test-isolated) tmux server: the pane process
// exits on the typed command, and the teardown that always follows must still
// report success even though the positive liveness cache was warmed moments
// before the pane vanished.
func TestTypeCommand_AgentExitsAndTeardownStillSucceeds(t *testing.T) {
	skipIfNoTmuxBinary(t)
	s := NewSession("agent-deck-graceful-exit", t.TempDir())
	// The pane process IS the stand-in agent, as in production where the pane
	// command ends in `exec claude …`: it exits 0 on the exact line "/exit".
	// (Session.Start types its command into a login shell under test, which
	// would outlive the agent.)
	if err := s.runBoundedRun("new-session", "-d", "-s", s.Name, `sh -c 'while read l; do [ "$l" = /exit ] && exit 0; done'`); err != nil {
		t.Skipf("could not start tmux session in this environment: %v", err)
	}
	t.Cleanup(func() { _ = s.Kill() })

	pid, err := s.PanePID()
	if err != nil {
		t.Fatalf("PanePID: %v", err)
	}
	// What the CLI does while loading sessions: a positive cache entry that
	// Exists() will trust for sessionCacheTTL.
	RefreshSessionCache()

	if err := s.TypeCommand("/exit"); err != nil {
		t.Fatalf("TypeCommand: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane process %d did not exit after /exit", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}

	s.ForgetCachedExistence()
	if err := s.KillAndWait(); err != nil {
		t.Fatalf("teardown after a graceful exit must succeed, got: %v", err)
	}
}

func TestTypeCommand_RefusesMultiLine(t *testing.T) {
	s := NewSession("agent-deck-graceful-multiline", t.TempDir())
	for _, c := range []string{"", "/exit\n", "a\rb"} {
		if err := s.TypeCommand(c); err == nil {
			t.Fatalf("TypeCommand(%q) should refuse", c)
		}
	}
}
