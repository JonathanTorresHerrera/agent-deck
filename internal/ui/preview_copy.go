package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/clipboard"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// buildSessionInfoForCopy produces a plain-text payload of the right-pane
// preview values (#791): Repo / Path / Branch for worktree sessions, the
// full project-path list for multi-repo sessions, or just Path for plain
// sessions. The shape is deliberately label-prefixed and newline-separated
// so a paste lands cleanly in a shell prompt, an issue tracker, or a doc
// without further editing.
func buildSessionInfoForCopy(inst *session.Instance) string {
	if inst == nil {
		return ""
	}

	var b strings.Builder

	// Patch 17: the session name leads the block. Without it a paste named
	// the session only by an opaque UUID and a directory many sessions share.
	// Skipped when blank, like the Session line below, so no dangling label.
	if name := strings.TrimSpace(inst.Title); name != "" {
		fmt.Fprintf(&b, "Name: %s\n", name)
	}
	// Patch 20: the Deck's own instance ID, right after the name. It is the
	// exact key Aida's deck tools look a session up by; the "Session:" line
	// below is the tool's (Claude/Codex/...) ID, a different thing.
	if id := strings.TrimSpace(inst.ID); id != "" {
		fmt.Fprintf(&b, "Deck ID: %s\n", id)
	}

	if inst.IsMultiRepo() {
		b.WriteString("Paths:\n")
		for i, p := range inst.AllProjectPaths() {
			fmt.Fprintf(&b, "  %d. %s\n", i+1, p)
		}
	} else if inst.IsWorktree() {
		if inst.WorktreeRepoRoot != "" {
			fmt.Fprintf(&b, "Repo: %s\n", inst.WorktreeRepoRoot)
		}
		path := inst.WorktreePath
		if path == "" {
			path = inst.ProjectPath
		}
		if path != "" {
			fmt.Fprintf(&b, "Path: %s\n", path)
		}
		if inst.WorktreeBranch != "" {
			fmt.Fprintf(&b, "Branch: %s\n", inst.WorktreeBranch)
		}
	} else if inst.ProjectPath != "" {
		fmt.Fprintf(&b, "Path: %s\n", inst.ProjectPath)
	}

	// Session ID line (matches the "Session:" value shown in the preview pane),
	// emitted only when the tool has a detected session so empty sessions don't
	// produce a dangling label.
	if id := inst.DisplaySessionID(); id != "" {
		fmt.Fprintf(&b, "Session: %s\n", id)
	}

	return strings.TrimRight(b.String(), "\n")
}

// deckIDPreviewLine is the preview header's Deck ID line (patch 20), or "" when
// the instance has no ID. Every tool section below prints the tool's own
// session ID; this is the Deck's, shown once for every session type.
func deckIDPreviewLine(inst *session.Instance) string {
	if inst == nil || strings.TrimSpace(inst.ID) == "" {
		return ""
	}
	return "🆔 Deck ID: " + strings.TrimSpace(inst.ID)
}

// lastAidaAskPreviewLine is the preview header's "asked Aida" line (patch 21),
// shown under the Deck ID line, or "" when the session has no last_aida_ask
// record. The list row shows only patch 24's 🔔 marker, and only while the
// ask is unanswered.
func lastAidaAskPreviewLine(inst *session.Instance) string {
	if inst == nil {
		return ""
	}
	rec, ok := inst.LastAidaAsk()
	if !ok {
		return ""
	}
	// Patch 24: an answered ask leads with the answer; an unanswered one that
	// rang Aida says it is still waiting.
	var line string
	if rec.IsCleared() {
		// Patch 29: cleared by hand, not by Aida's ack.
		line = "✅ Marked handled by " + rec.ClearedBy + ": " + formatActivityStamp(rec.AnsweredAt, false) +
			" (asked " + formatRelativeTime(rec.At) + ")"
		if rec.Note != "" {
			line += " — " + rec.Note
		}
	} else if answeredAt, answered := rec.Answered(); answered {
		line = "✅ Aida answered: " + formatActivityStamp(answeredAt, false) +
			" (asked " + formatRelativeTime(rec.At) + ")"
	} else {
		line = "🔔 asked Aida: " + formatActivityStamp(rec.At, false) + " — " + aidaAskWord(rec.Status)
		if rec.AwaitingReply(askAidaNow()) {
			line += " · waiting for reply"
		}
	}
	if rec.Fallback {
		line += " · #aida-ops fallback"
	}
	return line
}

// copySessionInfo returns a tea.Cmd that copies the preview pane's
// session-info payload (#791) to the system clipboard, mirroring the
// fallback chain used by copySessionOutput.
func (h *Home) copySessionInfo(inst *session.Instance) tea.Cmd {
	return func() tea.Msg {
		payload := buildSessionInfoForCopy(inst)
		if payload == "" {
			return copyResultMsg{err: fmt.Errorf("no session info to copy")}
		}

		termInfo := tmux.GetTerminalInfo()
		result, err := clipboard.Copy(payload, termInfo.SupportsOSC52)
		if err != nil {
			return copyResultMsg{err: fmt.Errorf("clipboard: %w", err)}
		}
		return copyResultMsg{
			sessionTitle: inst.Title,
			lineCount:    result.LineCount,
		}
	}
}

// copyPreviewField returns a tea.Cmd that copies a single PREVIEW value picked
// from the copy picker. It reports the field by name so the confirmation reads
// "Copied Session ID to clipboard" rather than a line count, which is the only
// useful feedback when the payload is one short value.
func (h *Home) copyPreviewField(field copyField, sessionTitle string) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(field.value) == "" {
			return copyResultMsg{err: fmt.Errorf("nothing to copy for %s", field.label)}
		}

		termInfo := tmux.GetTerminalInfo()
		result, err := clipboard.Copy(field.value, termInfo.SupportsOSC52)
		if err != nil {
			return copyResultMsg{err: fmt.Errorf("clipboard: %w", err)}
		}
		return copyResultMsg{
			sessionTitle: sessionTitle,
			lineCount:    result.LineCount,
			fieldLabel:   field.label,
		}
	}
}
