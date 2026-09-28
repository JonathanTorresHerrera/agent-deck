// Patch 23: Enter in the "/" search lands the cursor on the match.
//
// JT typed a Deck ID into "/" and pressed Enter; the overlay closed and the
// cursor did not move (2026-09-28). Two causes: Enter on no match closed the
// overlay silently, and a match hidden by the status (!/@), time (*) or
// archive (^) filter was never found in flatItems, so the cursor stayed put.
package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func patch23Home(t *testing.T) (*Home, *session.Instance, *session.Instance) {
	t.Helper()
	home := NewHome()
	home.width, home.height = 120, 40
	a := session.NewInstanceWithTool("alpha", t.TempDir(), "claude")
	b := session.NewInstanceWithTool("bravo target", t.TempDir(), "claude")
	a.Status = session.StatusRunning
	b.Status = session.StatusIdle
	home.instancesMu.Lock()
	home.instances = []*session.Instance{a, b}
	home.instanceByID[a.ID] = a
	home.instanceByID[b.ID] = b
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree(home.instances)
	home.rebuildFlatItems()
	return home, a, b
}

func searchFor(home *Home, query string) {
	home.search.SetItems(home.instances)
	home.search.Show()
	for _, r := range query {
		home.handleSearchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func cursorSessionID(home *Home) string {
	if home.cursor < 0 || home.cursor >= len(home.flatItems) {
		return ""
	}
	if s := home.flatItems[home.cursor].Session; s != nil {
		return s.ID
	}
	return ""
}

func TestPatch23_EnterOnDeckIDLandsOnTheSession(t *testing.T) {
	home, _, b := patch23Home(t)
	searchFor(home, b.ID)
	home.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if home.search.IsVisible() {
		t.Fatal("search should close after a successful Enter")
	}
	if got := cursorSessionID(home); got != b.ID {
		t.Errorf("cursor on %q, want %q", got, b.ID)
	}
}

func TestPatch23_EnterWithNoMatchStaysOpenAndSaysSo(t *testing.T) {
	home, _, _ := patch23Home(t)
	searchFor(home, "zz-no-such-session")
	home.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !home.search.IsVisible() {
		t.Fatal("Enter with no match must keep the search open")
	}
	if got := statusText(home); !strings.Contains(got, "No session matches") {
		t.Errorf("status = %q, want a no-match message", got)
	}
}

func TestPatch23_EnterClearsAStatusFilterHidingTheMatch(t *testing.T) {
	home, _, b := patch23Home(t)
	home.statusFilter = session.StatusRunning // "!" — hides the idle target
	home.rebuildFlatItems()
	searchFor(home, "bravo")
	home.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got := cursorSessionID(home); got != b.ID {
		t.Fatalf("cursor on %q, want %q (filter should have been cleared)", got, b.ID)
	}
	if home.statusFilter != "" {
		t.Errorf("statusFilter = %q, want cleared", home.statusFilter)
	}
	if got := statusText(home); !strings.Contains(got, "filter") {
		t.Errorf("status = %q, want a note that a filter was cleared", got)
	}
}

func TestPatch23_EnterClearsATimeFilterHidingTheMatch(t *testing.T) {
	home, _, b := patch23Home(t)
	home.timeFilter = session.TimeFilterToday
	b.CreatedAt = b.CreatedAt.AddDate(0, 0, -30)
	b.LastAccessedAt = b.CreatedAt
	home.rebuildFlatItems()
	searchFor(home, "bravo")
	home.handleSearchKey(tea.KeyMsg{Type: tea.KeyEnter})
	if got := cursorSessionID(home); got != b.ID {
		t.Fatalf("cursor on %q, want %q", got, b.ID)
	}
	if home.timeFilter != session.TimeFilterAll {
		t.Errorf("timeFilter = %v, want all", home.timeFilter)
	}
}
