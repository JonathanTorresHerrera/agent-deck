package tmux

import (
	"regexp"
	"strings"
)

// Claude Code's at-the-prompt indicators that the FOREGROUND turn finished but
// background work is still in flight. Claude prints these on the
// turn-completion (✻) summary line and in the footer:
//
//	✻ Churned for 6m 24s · 2 shells still running
//	✻ Waiting for 1 background agent to finish
//	⏵⏵ bypass permissions on · 2 shells · ← for agents   (footer; segment present iff shells>0)
//
// run_in_background shells and a background agent the turn awaits are the two
// "still working after Stop" cases. See the background-work-stop-signal
// investigation (issue: bg-only sessions flagged yellow + notified).
//
// Patch 27: the two cases are matched separately because they mean different
// things. An awaited background AGENT is genuinely still working: Claude
// resumes by itself when the agent reports. A background SHELL can be a dev
// server or a tail that never exits, so treating it as "still running" kept
// sessions green forever with no finished notification. Shells-only is now
// waiting (substate background-work); only the agent case stays running.
var (
	claudeBackgroundAgentRe = regexp.MustCompile(`(?i)` +
		`waiting\s+for\s+\d+\s+background\s+agents?\s+to\s+finish`) // completion line: background agent
	claudeBackgroundShellsRe = regexp.MustCompile(`(?i)` +
		`\d+\s+shells?\s+still\s+running` + // completion line: shells
		`|·\s*\d+\s+shells?\s*·`) // footer shell counter
)

// backgroundWorkScanLines bounds the scan to the pane tail (completion line +
// input box + footer) so a transcript that merely mentions "shells" in prose
// further up the scrollback cannot trip the detector.
const backgroundWorkScanLines = 20

// BackgroundWorkKind says which kind of background work a Claude pane shows at
// the prompt (Patch 27).
type BackgroundWorkKind int

const (
	// BackgroundWorkNone: nothing in flight.
	BackgroundWorkNone BackgroundWorkKind = iota
	// BackgroundWorkShells: only run_in_background shells are still running.
	// The turn is done; the session is waiting (substate background-work).
	BackgroundWorkShells
	// BackgroundWorkAgent: the turn is awaiting a background agent. Wins over
	// shells, because Claude resumes on its own when the agent reports.
	BackgroundWorkAgent
)

func (k BackgroundWorkKind) String() string {
	switch k {
	case BackgroundWorkShells:
		return "shells"
	case BackgroundWorkAgent:
		return "agent"
	default:
		return "none"
	}
}

// claudeBackgroundWorkKind classifies the (ANSI-stripped) Claude pane tail.
// Pure and Claude-shaped; callers gate it to Claude sessions. Agent beats
// shells: a pane awaiting an agent usually also shows a shell footer.
func claudeBackgroundWorkKind(content string) BackgroundWorkKind {
	if content == "" {
		return BackgroundWorkNone
	}
	recent := strings.Join(lastNLines(content, backgroundWorkScanLines), "\n")
	switch {
	case claudeBackgroundAgentRe.MatchString(recent):
		return BackgroundWorkAgent
	case claudeBackgroundShellsRe.MatchString(recent):
		return BackgroundWorkShells
	default:
		return BackgroundWorkNone
	}
}

// claudeBackgroundWorkPending reports whether the (ANSI-stripped) Claude pane
// content shows ANY in-flight background work at the prompt (shells or an
// awaited agent). Pure and Claude-shaped; callers gate it to Claude sessions.
func claudeBackgroundWorkPending(content string) bool {
	return claudeBackgroundWorkKind(content) != BackgroundWorkNone
}
