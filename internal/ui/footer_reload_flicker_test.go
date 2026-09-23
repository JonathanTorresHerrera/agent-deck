package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// footerTestHome builds a Home showing a Claude session row, the context
// with the most footer hints, at the given width.
func footerTestHome(width int) *Home {
	home := NewHome()
	home.width = width
	home.height = 69
	home.footerMode = session.FooterFull
	s := &session.Instance{ID: "footer-1", Title: "T", Tool: "claude", ClaudeSessionID: "abc"}
	home.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: s}}
	home.cursor = 0
	return home
}

// TestFullFooterDoesNotShiftDuringReload pins the flicker fix: a storage
// reload (which fires on every state.db write from any session) must not
// change the footer, or the whole bar jumps sideways and back.
func TestFullFooterDoesNotShiftDuringReload(t *testing.T) {
	for _, w := range []int{120, 187, 260} {
		home := footerTestHome(w)
		idle := home.renderHelpBar()
		home.isReloading = true
		reloading := home.renderHelpBar()
		if idle != reloading {
			t.Errorf("width %d: footer changed during reload\nidle:      %q\nreloading: %q", w, idle, reloading)
		}
	}
}

// TestFullFooterFitsWholeHints checks an overflowing full footer drops whole
// hints instead of cutting the last one mid-word, and keeps the help key.
func TestFullFooterFitsWholeHints(t *testing.T) {
	for _, w := range []int{101, 150, 187} {
		home := footerTestHome(w)
		lines := strings.Split(home.renderHelpBar(), "\n")
		content := lines[len(lines)-1]
		if got := lipgloss.Width(content); got > w {
			t.Errorf("width %d: footer content is %d cells wide", w, got)
		}
		if helpKey := home.actionKey(hotkeyHelp); helpKey != "" && !strings.Contains(content, helpKey+" Help") {
			t.Errorf("width %d: footer lost the help key: %q", w, content)
		}
		// Every hint label that appears must appear whole: the content must
		// not end in a partial label such as "Mov" (from "Move").
		plain := strings.TrimRight(stripFooterANSI(content), " ")
		for _, label := range []string{"Move", "Delete", "Close", "Rename", "Send", "Copy pane", "Copy ID/path", "Restart Fresh"} {
			for i := 1; i < len(label); i++ {
				if strings.HasSuffix(plain, " "+label[:i]) {
					t.Errorf("width %d: footer ends in truncated label %q: %q", w, label[:i], plain)
				}
			}
		}
	}
}

func stripFooterANSI(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		if r == 0x1b {
			esc = true
			continue
		}
		if esc {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				esc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
