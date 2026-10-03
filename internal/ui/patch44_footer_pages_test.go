package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

func newPatch44Home(t *testing.T, width int) *Home {
	t.Helper()
	home := NewHome()
	t.Cleanup(func() {
		home.cancel()
		if home.storage != nil {
			_ = home.storage.Close()
		}
	})
	home.width = width
	home.height = 40
	home.footerMode = session.FooterPaged
	return home
}

func patch44Select(home *Home, inst *session.Instance) {
	home.flatItems = []session.Item{{Type: session.ItemTypeSession, Session: inst}}
	home.cursor = 0
}

// The bug: at 146 columns the full footer reflowed when Fork's 5-minute
// window closed or a reload briefly cleared the session ID. The paged footer
// must render the same slots in the same places in all three states.
func TestPatch44_FooterDoesNotReflowOnSessionState(t *testing.T) {
	home := newPatch44Home(t, 146)
	states := []*session.Instance{
		{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid", ClaudeDetectedAt: time.Now()},
		{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid", ClaudeDetectedAt: time.Now().Add(-6 * time.Minute)},
		{ID: "a", Title: "A", Tool: "claude"},
	}
	var first string
	for i, inst := range states {
		patch44Select(home, inst)
		got := tmux.StripANSI(home.renderHelpBar())
		if i == 0 {
			first = got
			if !strings.Contains(got, "Fork") || !strings.Contains(got, "Fresh") {
				t.Fatalf("fork and fresh slots missing:\n%s", got)
			}
			continue
		}
		if got != first {
			t.Fatalf("state %d reflowed the footer:\n%s\nvs\n%s", i, first, got)
		}
	}
}

func TestPatch44_PageTwoShowsTheRest(t *testing.T) {
	home := newPatch44Home(t, 146)
	patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid"})
	p1 := tmux.StripANSI(home.renderHelpBar())
	if strings.Contains(p1, "Rename") || !strings.Contains(p1, ". Page 1/2") {
		t.Fatalf("page 1 should hold the default page-1 actions and the page indicator:\n%s", p1)
	}
	if _, cmd := home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")}); cmd != nil {
		_ = cmd
	}
	p2 := tmux.StripANSI(home.renderHelpBar())
	for _, want := range []string{"Rename", "Del", ". Page 2/2"} {
		if !strings.Contains(p2, want) {
			t.Fatalf("page 2 missing %q:\n%s", want, p2)
		}
	}
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(".")})
	if got := tmux.StripANSI(home.renderHelpBar()); got != p1 {
		t.Fatalf("second . should return to page 1:\n%s", got)
	}
}

func TestPatch44_FooterFitsEveryWidth(t *testing.T) {
	for _, w := range []int{70, 80, 90, 100, 101, 120, 146, 159, 160, 200, 220} {
		home := newPatch44Home(t, w)
		patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid"})
		for page := 0; page < 2; page++ {
			home.footerPage = page
			for _, line := range strings.Split(home.renderHelpBar(), "\n") {
				if lw := lipgloss.Width(line); lw > w {
					t.Fatalf("width %d page %d: line is %d cells:\n%s", w, page, lw, tmux.StripANSI(line))
				}
			}
		}
	}
}

// Page-1 actions that do not fit move to the front of page 2 instead of being
// cut off.
func TestPatch44_NarrowPageOneSpillsToPageTwo(t *testing.T) {
	home := newPatch44Home(t, 80)
	patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid"})
	p1 := tmux.StripANSI(home.renderHelpBar())
	if strings.Contains(p1, "MCP") {
		t.Fatalf("80 columns cannot hold every page-1 action:\n%s", p1)
	}
	home.footerPage = 1
	p2 := tmux.StripANSI(home.renderHelpBar())
	if !strings.Contains(p2, "MCP") {
		t.Fatalf("the spilled page-1 actions should lead page 2:\n%s", p2)
	}
}

func TestPatch44_NormalizeLayout(t *testing.T) {
	got := normalizeFooterLayout([]string{"restart", "bogus", "restart", "attach", "toggle_group", "--", "rename", "--"}, footerKindSession)
	if got[0] != "restart" || got[1] != "attach" || got[2] != footerPageBreak || got[3] != "rename" {
		t.Fatalf("saved order not kept: %v", got)
	}
	breaks, seen := 0, map[string]bool{}
	for _, id := range got {
		if id == footerPageBreak {
			breaks++
			continue
		}
		if seen[id] {
			t.Fatalf("duplicate %q in %v", id, got)
		}
		seen[id] = true
	}
	if breaks != 1 || seen["bogus"] || seen["toggle_group"] {
		t.Fatalf("bad normalize: %v", got)
	}
	for _, id := range defaultFooterLayout(footerKindSession) {
		if id != footerPageBreak && !seen[id] {
			t.Fatalf("missing action %q appended nowhere: %v", id, got)
		}
	}
	if d := normalizeFooterLayout(nil, footerKindGroup); d[len(d)-1] != footerPageBreak {
		t.Fatalf("group default keeps everything on page 1: %v", d)
	}
}

func TestPatch44_MenuMoveAndSwapPage(t *testing.T) {
	home := newPatch44Home(t, 146)
	patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid"})
	home.handleMainKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":")})
	if !home.footerMenu.open || home.footerMenu.layout[home.footerMenu.cursor] != footerActAttach {
		t.Fatalf("menu should open on the first action: %+v", home.footerMenu)
	}
	m := &home.footerMenu
	// Send attach to page 2: it lands right after the break.
	home.footerMenuSwapPage()
	bi := indexOf(m.layout, footerPageBreak)
	if m.layout[bi+1] != footerActAttach || m.cursor != bi+1 {
		t.Fatalf("swap to page 2: %v cursor %d", m.layout, m.cursor)
	}
	// And back: right before the break.
	home.footerMenuSwapPage()
	bi = indexOf(m.layout, footerPageBreak)
	if m.layout[bi-1] != footerActAttach || m.cursor != bi-1 {
		t.Fatalf("swap to page 1: %v cursor %d", m.layout, m.cursor)
	}
	// Moving down across the break puts it on page 2.
	home.footerMenuMove(1)
	if indexOf(m.layout, footerActAttach) != indexOf(m.layout, footerPageBreak)+1 {
		t.Fatalf("move across the break: %v", m.layout)
	}
	if !m.dirty {
		t.Fatal("edits must mark the menu dirty")
	}
	// The menu view lists both pages and never overflows.
	view := home.renderFooterMenu()
	plain := tmux.StripANSI(view)
	if !strings.Contains(plain, "Page 1") || !strings.Contains(plain, "Page 2") {
		t.Fatalf("menu view:\n%s", plain)
	}
	for _, line := range strings.Split(view, "\n") {
		if lipgloss.Width(line) > home.width {
			t.Fatalf("menu line too wide: %q", tmux.StripANSI(line))
		}
	}
	// Esc without saving side effects in this test: drop the dirty flag first.
	m.dirty = false
	home.handleFooterMenuKey(tea.KeyMsg{Type: tea.KeyEsc})
	if home.footerMenu.open {
		t.Fatal("esc should close the menu")
	}
}

func TestPatch44_MenuWindowsOnShortTerminal(t *testing.T) {
	home := newPatch44Home(t, 120)
	home.height = 15
	patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude"})
	home.openFooterMenu()
	for i := 0; i < 30; i++ {
		home.footerMenu.cursor = home.footerMenuStep(home.footerMenu.cursor, 1)
	}
	last := home.footerMenu.layout[home.footerMenu.cursor]
	a := footerCatalogByID()[last]
	plain := tmux.StripANSI(home.renderFooterMenu())
	if !strings.Contains(plain, "▸") || !strings.Contains(plain, a.label) {
		t.Fatalf("cursor row %q must stay visible:\n%s", a.label, plain)
	}
}

func TestPatch44_KeyMsgRoundTrip(t *testing.T) {
	for _, k := range []string{"enter", "tab", "R", "T", "ctrl+y", "alt+a", ".", "H"} {
		if got := footerKeyMsg(k).String(); got != k {
			t.Fatalf("footerKeyMsg(%q).String() = %q", k, got)
		}
	}
}

func TestPatch44_FullFooterUnchangedByDefault(t *testing.T) {
	home := newPatch44Home(t, 146)
	home.footerMode = session.FooterFull
	patch44Select(home, &session.Instance{ID: "a", Title: "A", Tool: "claude", ClaudeSessionID: "sid"})
	if got := tmux.StripANSI(home.renderHelpBar()); strings.Contains(got, "Page 1/2") {
		t.Fatalf("the default footer must stay upstream's:\n%s", got)
	}
}

func indexOf(l []string, v string) int {
	for i, x := range l {
		if x == v {
			return i
		}
	}
	return -1
}
