package ui

import (
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func providerTestHome(t *testing.T) *Home {
	t.Helper()
	home := NewHome()
	t.Cleanup(func() {
		home.cancel()
		if home.storage != nil {
			_ = home.storage.Close()
		}
	})
	home.width, home.height = 200, 40
	home.initialLoading = false
	mk := func(id, tool string, st session.Status) *session.Instance {
		inst := session.NewInstanceWithTool(id, t.TempDir(), tool)
		inst.Status = st
		return inst
	}
	insts := []*session.Instance{
		mk("claude-run", "claude", session.StatusRunning),
		mk("claude-wait", "claude", session.StatusWaiting),
		mk("grok-wait", "grok", session.StatusWaiting),
		mk("grok-idle", "grok", session.StatusIdle),
		mk("codex-wait", "codex", session.StatusWaiting),
	}
	home.instancesMu.Lock()
	home.instances = insts
	for _, inst := range insts {
		home.instanceByID[inst.ID] = inst
	}
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree(home.instances)
	home.rebuildFlatItems()
	home.refreshSessionRenderSnapshot(nil)
	home.cachedStatusCounts.valid.Store(false)
	return home
}

func findProviderChip(h *Home, key string) *statusChipHitbox {
	for i := range h.chipHitboxes {
		if h.chipHitboxes[i].provider == key {
			return &h.chipHitboxes[i]
		}
	}
	return nil
}

func TestPatch45_ClickProviderFiltersAndStacksWithStatus(t *testing.T) {
	home := providerTestHome(t)
	_ = home.View()
	c := findProviderChip(home, "grok")
	if c == nil {
		t.Fatalf("no click target for grok in %+v", home.chipHitboxes)
	}
	home = chipClick(home, c.x0, c.y)
	if got := chipSessionIDs(home); len(got) != 2 || !got["grok-wait"] || !got["grok-idle"] {
		t.Fatalf("grok filter shows %v", got)
	}
	// The provider pill is highlighted and its hitbox lines up with its text.
	bar := stripAnsi(home.renderFilterBar())
	c = findProviderChip(home, "grok")
	if c == nil || !strings.Contains(string([]rune(bar)[c.x0:c.x1]), "Grok") {
		t.Fatalf("grok hitbox %+v does not cover its text in %q", c, bar)
	}

	// Stack a status: hide waiting -> only the idle grok session remains.
	_ = home.View()
	if w := findChip(home, session.StatusWaiting, statusChipFilterBarY); w != nil {
		home = chipClick(home, w.x0, w.y)
	} else {
		t.Fatal("no waiting chip")
	}
	if got := chipSessionIDs(home); len(got) != 1 || !got["grok-idle"] {
		t.Fatalf("grok + waiting hidden shows %v", got)
	}

	// Clicking the provider again clears only the provider filter.
	_ = home.View()
	c = findProviderChip(home, "grok")
	home = chipClick(home, c.x0, c.y)
	if got := chipSessionIDs(home); got["grok-wait"] || !got["claude-run"] || !got["grok-idle"] {
		t.Fatalf("after clearing provider: %v", got)
	}

	// All clears everything, provider included.
	_ = home.View()
	home.toggleProviderFilter("codex")
	home.resetStatusChips()
	if home.providerFilter != "" || len(chipSessionIDs(home)) != 5 {
		t.Fatalf("All left provider=%q, %v", home.providerFilter, chipSessionIDs(home))
	}
}

func TestPatch45_FilteredProviderStaysClickableWithNoLiveSession(t *testing.T) {
	home := providerTestHome(t)
	home.toggleProviderFilter("gemini")
	_ = home.View()
	if findProviderChip(home, "gemini") == nil {
		t.Fatal("a filtered provider with no live sessions must keep a pill to clear it")
	}
	if len(chipSessionIDs(home)) != 0 {
		t.Fatalf("gemini filter should show no sessions: %v", chipSessionIDs(home))
	}
}

func TestPatch45_BadgeAfterStatusGlyph(t *testing.T) {
	home := providerTestHome(t)
	home.showProviderBadge = true
	var b strings.Builder
	snap := home.getSessionRenderSnapshot()
	renderGrok := func(width int) string {
		b.Reset()
		for _, it := range home.flatItems {
			if it.Type == session.ItemTypeSession && it.Session.ID == home.instances[2].ID {
				home.renderSessionItem(&b, it, false, snap, width)
			}
		}
		return stripAnsi(b.String())
	}
	for _, width := range []int{80, 31} { // 31 columns approximates preview_pct=74 at 120 columns.
		if row := renderGrok(width); !strings.Contains(row, "◐ X ") {
			t.Fatalf("grok row at width %d should read '◐ X <title>': %q", width, row)
		}
	}
	home.showProviderBadge = false
	if row := renderGrok(31); strings.Contains(row, " X ") {
		t.Fatalf("badge must be opt-in: %q", row)
	}
}

func TestPatch45_BadgeLettersAreDistinct(t *testing.T) {
	seen := map[string]string{}
	for _, k := range []string{"claude", "codex", "grok", "gemini", "shell", "opencode"} {
		l := providerBadgeLetter(k)
		if prev, dup := seen[l]; dup {
			t.Fatalf("%s and %s share badge %q", prev, k, l)
		}
		seen[l] = k
		if cellWidth(l) != 1 {
			t.Fatalf("%s badge %q is not one cell", k, l)
		}
	}
}
