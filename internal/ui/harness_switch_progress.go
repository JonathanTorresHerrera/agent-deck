package ui

import (
	"fmt"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/clipboard"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
	tea "github.com/charmbracelet/bubbletea"
)

func (h *Home) presentHarnessSwitchProgress(p *session.HarnessSwitchProgress) {
	if p == nil {
		return
	}
	if h.switchProgressSeen == nil {
		h.switchProgressSeen = make(map[string]string)
	}
	key := fmt.Sprintf("%s:%d:%s", p.OperationID, p.Sequence, p.UpdatedAt.Format(time.RFC3339Nano))
	if h.switchProgressSeen[p.InstanceID] == key && p.Status != "running" {
		return
	}
	// Preserve unrelated confirmation decisions. Switch notices can be refreshed
	// while the instance and its pane are replaced by a registry watcher.
	if h.confirmDialog.IsVisible() && h.confirmDialog.GetConfirmType() != ConfirmNotice {
		return
	}
	title := "Account switch in progress"
	switch p.Status {
	case "success":
		title = "Account switch verified"
	case "failed":
		title = "Account switch failed"
	case "pending":
		title = "Account switch pending verification"
	}
	h.confirmDialog.ShowNotice(title, p.Text())
	h.switchProgressSeen[p.InstanceID] = key
}

func (h *Home) pollHarnessSwitchProgress() {
	// Follow operations launched in this TUI even when selection moves away.
	for id := range h.resumingSessions {
		if p, err := session.ReadHarnessSwitchProgress(id); err == nil {
			h.presentHarnessSwitchProgress(p)
			return
		}
	}
	// MCP switches share the same durable receipt. Polling the selected row
	// makes their progress and final diagnostics visible without a TUI restart.
	if inst := h.getSelectedSession(); inst != nil {
		if p, err := session.ReadHarnessSwitchProgress(inst.ID); err == nil {
			h.presentHarnessSwitchProgress(p)
		}
	}
}

func (h *Home) copySwitchNotice() tea.Cmd {
	text := h.confirmDialog.noticeBody
	return func() tea.Msg {
		copyText := h.paneClipboard
		if copyText == nil {
			copyText = clipboard.Copy
		}
		result, err := copyText(text, tmux.GetTerminalInfo().SupportsOSC52)
		if err != nil {
			return copyPaneResultMsg{sessionTitle: "Switch diagnostics", err: err}
		}
		return copyPaneResultMsg{sessionTitle: "Switch diagnostics", lineCount: result.LineCount}
	}
}
