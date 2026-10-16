package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// accountPillsForInstance returns account slot ids, their picker labels, and the
// selected slot id (empty = inherit). Selection is always by slot id, never by
// list index.
func accountPillsForInstance(cfg *session.UserConfig, harness, selected string, accounts []string) (opts, labels []string, selection string) {
	opts = append([]string{""}, accounts...)
	labels = make([]string, len(opts))
	labels[0] = session.AccountSlotPickerLabel(cfg, harness, "")
	for i, name := range accounts {
		labels[i+1] = session.AccountSlotPickerLabel(cfg, harness, name)
	}
	selection = selected
	found := selected == ""
	for _, name := range accounts {
		found = found || name == selected
	}
	if selected != "" && !found {
		opts = append(opts, selected)
		labels = append(labels, session.AccountSlotPickerLabel(cfg, harness, selected)+" (not configured)")
	}
	return opts, labels, selection
}

func accountSelectionIndex(opts []string, selection string) int {
	for i, opt := range opts {
		if opt == selection {
			return i
		}
	}
	if len(opts) > 0 {
		return 0
	}
	return -1
}

func (f *editField) syncAccountCursor() {
	if f.key != session.FieldAccount {
		return
	}
	f.pillCursor = accountSelectionIndex(f.pillOptions, f.pillSelection)
}

func (f *editField) stepAccountSelection(delta int) {
	if len(f.pillOptions) == 0 {
		return
	}
	idx := accountSelectionIndex(f.pillOptions, f.pillSelection)
	if idx < 0 {
		idx = 0
	}
	idx = (idx + delta) % len(f.pillOptions)
	if idx < 0 {
		idx += len(f.pillOptions)
	}
	f.pillSelection = f.pillOptions[idx]
	f.pillCursor = idx
}

// renderAccountPickerList draws one account per row with the full row
// highlighted for the selected slot. Labels are truncated with an ellipsis
// rather than wrapping mid-entry.
func renderAccountPickerList(labels []string, selectedIndex int, width int) string {
	if len(labels) == 0 {
		return ""
	}
	selectedStyle := lipgloss.NewStyle().Foreground(ColorBg).Background(ColorAccent).Bold(true)
	idleStyle := lipgloss.NewStyle().Foreground(ColorTextDim)
	budget := width
	if budget < 8 {
		budget = 8
	}
	var lines []string
	for i, label := range labels {
		text := label
		if budget > 4 {
			text = cellTruncate(label, budget-4, "…")
		}
		var line string
		if i == selectedIndex {
			line = selectedStyle.Render("▶ " + text)
		} else {
			line = "  " + idleStyle.Render(text)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}
