// Patch 27: Claude's Stop hook ("waiting") with only background SHELLS left is
// waiting (or idle once acknowledged), so the normal running->waiting
// transition fires the finished notification. An awaited background AGENT
// still keeps the session running.
package session

import (
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func TestPatch27_ClaudeStopHookStatus(t *testing.T) {
	cases := []struct {
		name  string
		kind  tmux.BackgroundWorkKind
		acked bool
		want  Status
	}{
		{"stop + shells -> waiting", tmux.BackgroundWorkShells, false, StatusWaiting},
		{"stop + shells + acknowledged -> idle", tmux.BackgroundWorkShells, true, StatusIdle},
		{"stop + agent -> running", tmux.BackgroundWorkAgent, false, StatusRunning},
		{"stop + agent + acknowledged -> running", tmux.BackgroundWorkAgent, true, StatusRunning},
		{"plain stop -> waiting", tmux.BackgroundWorkNone, false, StatusWaiting},
		{"plain stop + acknowledged -> idle", tmux.BackgroundWorkNone, true, StatusIdle},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeStopHookStatus(c.kind, c.acked); got != c.want {
				t.Fatalf("claudeStopHookStatus(%v, %v) = %q, want %q", c.kind, c.acked, got, c.want)
			}
		})
	}
}

func TestPatch27_SubstateBackgroundWorkSerialized(t *testing.T) {
	if string(SubstateBackgroundWork) != "background-work" {
		t.Fatalf("SubstateBackgroundWork = %q, want background-work", SubstateBackgroundWork)
	}
}
