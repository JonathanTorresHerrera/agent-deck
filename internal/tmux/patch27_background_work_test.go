// Patch 27: "turn done, only background shells running" is waiting, not
// running. The detector must tell background SHELLS (a dev server or tail can
// run forever, so the session would never settle) apart from a background
// AGENT the turn is awaiting (Claude resumes by itself when it reports, so the
// session is genuinely still working).
package tmux

import (
	"strings"
	"testing"
	"time"
)

func TestPatch27_ClaudeBackgroundWorkKind(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    BackgroundWorkKind
	}{
		{"shells only (completion line + footer)", paneShellsStillRunning, BackgroundWorkShells},
		{"shells only (footer only)", paneIdleNoBackground + "\n  ⏵⏵ bypass permissions on · 3 shells · ← for agents", BackgroundWorkShells},
		{"agent line + shell footer -> agent wins", paneAwaitingAgent, BackgroundWorkAgent},
		{"agent line, no shells", strings.Replace(paneAwaitingAgent, "· 1 shell ·", "·", 1), BackgroundWorkAgent},
		{"neither", paneIdleNoBackground, BackgroundWorkNone},
		{"completed agent row, nothing pending", paneCompletedAgentRowNoBackground, BackgroundWorkNone},
		{"empty", "", BackgroundWorkNone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeBackgroundWorkKind(c.content); got != c.want {
				t.Fatalf("claudeBackgroundWorkKind = %v, want %v", got, c.want)
			}
			// The bool view stays "any background work" for every other caller.
			if got, want := claudeBackgroundWorkPending(c.content), c.want != BackgroundWorkNone; got != want {
				t.Fatalf("claudeBackgroundWorkPending = %v, want %v", got, want)
			}
		})
	}
}

func TestPatch27_ClaudeBackgroundWorkKind_IgnoresScrollbackProse(t *testing.T) {
	content := "I launched 3 shells still running earlier.\n" +
		"✻ Waiting for 1 background agent to finish\n" +
		"  ⏵⏵ bypass permissions on · 2 shells · ← for agents\n"
	for i := 0; i < 40; i++ {
		content += "line of unrelated transcript output here\n"
	}
	content += paneIdleNoBackground
	if got := claudeBackgroundWorkKind(content); got != BackgroundWorkNone {
		t.Fatalf("scrollback beyond the scan bound must not count, got %v", got)
	}
}

func TestPatch27_ClassifySubstate_BackgroundWork(t *testing.T) {
	d := NewPromptDetector("claude")
	if got := d.ClassifySubstate(paneShellsStillRunning); got != SubstateBackgroundWork {
		t.Fatalf("shells only at prompt: got %q, want %q", got, SubstateBackgroundWork)
	}
	if got := d.ClassifySubstate(paneAwaitingAgent); got == SubstateBackgroundWork {
		t.Fatalf("awaited background agent must not classify as %q", got)
	}
	if got := d.ClassifySubstate(paneIdleNoBackground); got != SubstateIdleAtEmptyPrompt {
		t.Fatalf("plain idle: got %q, want %q", got, SubstateIdleAtEmptyPrompt)
	}
	// A live busy cue still wins over a shells footer.
	if got := d.ClassifySubstate(paneSingleShell); got != SubstateRunning {
		t.Fatalf("busy with a shell: got %q, want %q", got, SubstateRunning)
	}
	if string(SubstateBackgroundWork) != "background-work" {
		t.Fatalf("serialized substate = %q, want background-work", SubstateBackgroundWork)
	}
}

// newPatch27Session builds a Claude session whose CapturePane is served from the
// 500ms capture cache, so no tmux server is touched.
func newPatch27Session(content string) *Session {
	s := &Session{Name: "patch27-test", DisplayName: "patch27", detectedTool: "claude"}
	s.cacheContent = content
	s.cacheTime = time.Now()
	return s
}

func TestPatch27_SessionBackgroundWork_ProbeAndSubstateRefresh(t *testing.T) {
	cases := []struct {
		name     string
		content  string
		wantKind BackgroundWorkKind
		wantSub  Substate
	}{
		{"shells", paneShellsStillRunning, BackgroundWorkShells, SubstateBackgroundWork},
		{"agent", paneAwaitingAgent, BackgroundWorkAgent, SubstateIdleAtEmptyPrompt},
		{"none", paneIdleNoBackground, BackgroundWorkNone, SubstateIdleAtEmptyPrompt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newPatch27Session(c.content)
			if got := s.BackgroundWork(); got != c.wantKind {
				t.Fatalf("BackgroundWork() = %v, want %v", got, c.wantKind)
			}
			if got, want := s.BackgroundWorkPending(), c.wantKind != BackgroundWorkNone; got != want {
				t.Fatalf("BackgroundWorkPending() = %v, want %v", got, want)
			}
			// The hook fast path never reaches GetStatus, so the probe must keep
			// the cached substate (TUI row, transition events) fresh itself.
			if got := s.CachedSubstate(); got != c.wantSub {
				t.Fatalf("CachedSubstate() after probe = %q, want %q", got, c.wantSub)
			}
		})
	}
}

func TestPatch27_MarkBackgroundWorkActive_OnlyForAgent(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"shells only -> not forced active", paneShellsStillRunning, false},
		{"agent -> forced active", paneAwaitingAgent, true},
		{"none -> not forced active", paneIdleNoBackground, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &Session{DisplayName: "patch27", detectedTool: "claude"}
			s.mu.Lock()
			s.ensureStateTrackerLocked()
			s.stateTracker.acknowledged = true
			got := s.markBackgroundWorkActiveLocked(c.content, 1, "patch27")
			acked := s.stateTracker.acknowledged
			s.mu.Unlock()
			if got != c.want {
				t.Fatalf("markBackgroundWorkActiveLocked = %v, want %v", got, c.want)
			}
			// Shells-only must leave the acknowledged flag alone so the
			// prompt path can still settle to idle.
			if !c.want && !acked {
				t.Fatal("acknowledged was cleared for a non-agent pane")
			}
		})
	}
}
