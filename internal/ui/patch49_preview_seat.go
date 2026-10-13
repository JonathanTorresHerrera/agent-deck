package ui

// Patch 49 (preview seat). The PREVIEW pane's tool section header names the
// seat the session runs on — "Claude @claude-3", "Codex @codex-2" — so the
// seat is readable for the selected session without the list's row tag. A
// session with no stored seat shows the default login it runs on (patch 47's
// defaultSeatLabel), regardless of [display] show_inherited_account, which
// only governs the list rows.

import "github.com/asheshgoplani/agent-deck/internal/session"

func previewSeatSuffix(inst *session.Instance) string {
	if inst == nil {
		return ""
	}
	seat := inst.Account
	if seat == "" {
		seat = defaultSeatLabel(inst.Tool, inst.GroupPath)
	}
	if seat == "" {
		return ""
	}
	return " @" + seat
}
