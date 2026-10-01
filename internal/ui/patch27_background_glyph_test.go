// Patch 27: a session whose turn is done with only background shells left
// (substate background-work) gets its own row glyph and preview label.
package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestPatch27_RowStatusGlyph_BackgroundWork(t *testing.T) {
	cases := []struct {
		name     string
		status   session.Status
		substate session.Substate
		archived bool
		want     string
	}{
		{"waiting + background-work", session.StatusWaiting, session.SubstateBackgroundWork, false, backgroundWorkGlyph},
		{"idle + background-work", session.StatusIdle, session.SubstateBackgroundWork, false, backgroundWorkGlyph},
		{"running + stale background-work", session.StatusRunning, session.SubstateBackgroundWork, false, "●"},
		{"error + stale background-work", session.StatusError, session.SubstateBackgroundWork, false, "✕"},
		{"archived + background-work", session.StatusWaiting, session.SubstateBackgroundWork, true, "■"},
		{"waiting plain", session.StatusWaiting, session.SubstateIdleAtEmptyPrompt, false, "◐"},
		{"idle plain", session.StatusIdle, session.SubstateNone, false, "○"},
		{"running plain", session.StatusRunning, session.SubstateRunning, false, "●"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			icon, _ := rowStatusGlyph(c.status, c.substate, c.archived)
			if icon != c.want {
				t.Fatalf("rowStatusGlyph = %q, want %q", icon, c.want)
			}
		})
	}
}

func TestPatch27_BackgroundWorkStyleDistinct(t *testing.T) {
	_, bg := rowStatusGlyph(session.StatusWaiting, session.SubstateBackgroundWork, false)
	_, waiting := rowStatusGlyph(session.StatusWaiting, session.SubstateNone, false)
	_, running := rowStatusGlyph(session.StatusRunning, session.SubstateNone, false)
	if bg.GetForeground() == waiting.GetForeground() || bg.GetForeground() == running.GetForeground() {
		t.Fatal("background-work glyph colour must differ from waiting and running")
	}
}

func TestPatch27_ClaudeConnectionStatusLine(t *testing.T) {
	text, _ := claudeConnectionStatusLine(false, session.StatusWaiting, session.SubstateBackgroundWork)
	if !strings.HasPrefix(text, backgroundWorkGlyph) || !strings.Contains(text, "bg shells") {
		t.Fatalf("background-work status line = %q, want ◌ … bg shells", text)
	}
	text, _ = claudeConnectionStatusLine(false, session.StatusWaiting, session.SubstateNone)
	if text != "● Connected" {
		t.Fatalf("plain status line = %q, want ● Connected", text)
	}
	text, _ = claudeConnectionStatusLine(true, session.StatusWaiting, session.SubstateBackgroundWork)
	if text != "■ Archived" {
		t.Fatalf("archived status line = %q, want ■ Archived", text)
	}
	text, _ = claudeConnectionStatusLine(false, session.StatusRunning, session.SubstateBackgroundWork)
	if text != "● Connected" {
		t.Fatalf("running with stale substate = %q, want ● Connected", text)
	}
}
