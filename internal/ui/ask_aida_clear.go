package ui

// Patch 29: JT marks a session's Aida bell handled from the TUI.
//
// The 🔔 "notified · waiting for reply" marker (patch 24) used to clear only
// when the hub saw Aida's ack. Ctrl+X in the B dialog now clears it by hand:
// the record gains answered_at plus cleared_by "jt", clear_reason "manual" and
// the dialog's text as an optional note. Aida's equivalent is the MCP tool
// deck_clear_notification (`agent-deck session clear-bell`).

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// aidaBellClearBy is who a TUI clear is recorded as.
const aidaBellClearBy = "jt"

// aidaBellClearMsg is emitted by Ctrl+X in the Ask Aida dialog.
type aidaBellClearMsg struct {
	instanceID string
	note       string
}

// aidaBellClearable reports a recorded ask that is not answered yet. Unlike
// AwaitingReply it ignores the 48 h window and the ring status: an old or
// routed ask can still be marked handled.
func aidaBellClearable(inst *session.Instance) bool {
	if inst == nil {
		return false
	}
	rec, ok := inst.LastAidaAsk()
	return ok && !rec.IsAnswered() && strings.TrimSpace(rec.Ref) != ""
}

// handleAidaBellClear marks the bell handled in memory, clears the row marker
// at once, and returns the SQLite write.
func (h *Home) handleAidaBellClear(msg aidaBellClearMsg) tea.Cmd {
	h.instancesMu.RLock()
	inst := h.instanceByID[msg.instanceID]
	h.instancesMu.RUnlock()
	if inst == nil {
		h.setError(fmt.Errorf("%s", "Mark handled: that session no longer exists"))
		return nil
	}
	rec, ok := inst.LastAidaAsk()
	if !ok || rec.IsAnswered() {
		h.setError(fmt.Errorf("Nothing to clear on %q: no waiting Aida bell", inst.Title))
		return nil
	}
	record, changed := inst.ClearLastAidaAsk(rec.Ref, askAidaNow(), aidaBellClearBy, "manual", msg.note)
	if !changed {
		h.setError(fmt.Errorf("Nothing to clear on %q: no waiting Aida bell", inst.Title))
		return nil
	}
	if parsed, ok := session.ParseAidaAsk(record); ok {
		h.patchAidaAskInSnapshot(inst.ID, parsed, true)
	}
	h.setError(fmt.Errorf("Marked handled — 🔔 cleared on %q", inst.Title))
	return persistLastAidaAskCmd(inst.ID, record)
}
