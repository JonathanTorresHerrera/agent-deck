package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// YoloOptionsPanel is a UI panel for YOLO/dangerous mode options.
// Used for Gemini and Codex in NewDialog, matching ClaudeOptionsPanel's visual style.
type YoloOptionsPanel struct {
	toolName      string // "Gemini" or "Codex"
	label         string // Checkbox label text
	yoloMode      bool
	focused       bool
	focusIndex    int
	accounts      []string
	accountLabels []string
	accountCursor int
}

// NewYoloOptionsPanel creates a new options panel for a tool with a single YOLO checkbox.
func NewYoloOptionsPanel(toolName, label string) *YoloOptionsPanel {
	return &YoloOptionsPanel{
		toolName: toolName,
		label:    label,
	}
}

// SetDefaults applies default value from config.
func (p *YoloOptionsPanel) SetDefaults(yoloMode bool) {
	p.yoloMode = yoloMode
}

// Focus sets focus to this panel.
func (p *YoloOptionsPanel) Focus() {
	p.focused = true
	p.focusIndex = 0
}

// Blur removes focus from this panel.
func (p *YoloOptionsPanel) Blur() {
	p.focused = false
}

// IsFocused returns true if the panel has focus.
func (p *YoloOptionsPanel) IsFocused() bool {
	return p.focused
}

// GetYoloMode returns the current YOLO mode state.
func (p *YoloOptionsPanel) GetYoloMode() bool {
	return p.yoloMode
}

// AtTop returns true (single element, always at top).
func (p *YoloOptionsPanel) AtTop() bool {
	return p.focusIndex == 0
}

// AtBottom returns true (single element, always at bottom).
func (p *YoloOptionsPanel) AtBottom() bool {
	return len(p.accounts) == 0 || p.focusIndex == 1
}

// FocusLast focuses the single element.
func (p *YoloOptionsPanel) FocusLast() {
	p.focused = true
	if len(p.accounts) > 0 {
		p.focusIndex = 1
	} else {
		p.focusIndex = 0
	}
}

func (p *YoloOptionsPanel) FocusedLine() int {
	if !p.focused {
		return -1
	}
	if p.focusIndex == 1 {
		if p.accountLabels != nil {
			return 3 + p.accountCursor
		}
		return 3
	}
	return 1 // The options header occupies line zero.
}

// Update handles key events.
func (p *YoloOptionsPanel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up", "shift+tab":
			if p.focusIndex > 0 {
				p.focusIndex--
			}
			return nil
		case "down", "tab":
			if len(p.accounts) > 0 && p.focusIndex == 0 {
				p.focusIndex++
			}
			return nil
		case "left", "h", "right", "l":
			if p.focusIndex == 1 && len(p.accounts) > 0 {
				step := 1
				if msg.String() == "left" || msg.String() == "h" {
					step = -1
				}
				span := len(p.accounts) + 1
				p.accountCursor = (p.accountCursor + step + span) % span
			}
			return nil
		case " ", "y":
			if p.focusIndex == 0 {
				p.yoloMode = !p.yoloMode
			} else if len(p.accounts) > 0 {
				p.accountCursor = (p.accountCursor + 1) % (len(p.accounts) + 1)
			}
			return nil
		}
	}
	return nil
}

// View renders the options panel matching ClaudeOptionsPanel's visual style.
func (p *YoloOptionsPanel) View() string {
	headerStyle := lipgloss.NewStyle().Foreground(ColorComment)

	var content string
	content += headerStyle.Render("─ "+p.toolName+" Options ─") + "\n"
	content += renderCheckboxLine(p.label, p.yoloMode, p.focused && p.focusIndex == 0)
	if len(p.accounts) > 0 {
		label := "    Account:"
		if p.focused && p.focusIndex == 1 {
			label = lipgloss.NewStyle().Foreground(ColorAccent).Bold(true).Render("  ▶ Account:")
		}
		labels := p.accountLabels
		if len(labels) != len(p.accounts)+1 {
			labels = accountPillLabels(p.accounts)
		}
		content += label + "\n" + renderNewAccountChoices(labels, p.accountCursor, p.accountLabels != nil)
	}
	return content
}

// SetAccounts is used by Codex; other YOLO panels retain their single row.
func (p *YoloOptionsPanel) SetAccounts(accounts []string) {
	p.accounts = accounts
	p.accountLabels = nil
	if p.accountCursor > len(accounts) {
		p.accountCursor = 0
	}
	if len(accounts) == 0 {
		p.focusIndex = 0
	}
}
func (p *YoloOptionsPanel) SetAccount(account string) {
	p.accountCursor = 0
	for i, name := range p.accounts {
		if name == account {
			p.accountCursor = i + 1
			return
		}
	}
}
func (p *YoloOptionsPanel) GetAccount() string {
	if p.accountCursor <= 0 || p.accountCursor > len(p.accounts) {
		return ""
	}
	return p.accounts[p.accountCursor-1]
}
