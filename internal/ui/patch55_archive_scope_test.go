package ui

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 55 (archive scope): ^ cycles Current -> Archived -> All, the filter
// bar's scope control is clickable, and in the All scope archived rows share
// the list with active ones, counting/filtering/hiding as stopped and carrying
// a dim [archived] badge.

// p55Home has three active and two archived sessions. z-run is archived with
// a stale "running" status (archiving freezes the stored status).
func p55Home(t *testing.T) *Home {
	t.Helper()
	h := NewHome()
	t.Cleanup(func() {
		h.cancel()
		if h.storage != nil {
			_ = h.storage.Close()
		}
	})
	h.width, h.height = 200, 40
	h.initialLoading = false
	mk := func(id, tool string, st session.Status, archived bool) *session.Instance {
		inst := session.NewInstanceWithTool(id, t.TempDir(), tool)
		inst.Status = st
		if archived {
			inst.ArchivedAt = time.Now()
		}
		return inst
	}
	insts := []*session.Instance{
		mk("a-run", "claude", session.StatusRunning, false),
		mk("a-wait", "grok", session.StatusWaiting, false),
		mk("a-stop", "claude", session.StatusStopped, false),
		mk("z-run", "claude", session.StatusRunning, true),
		mk("z-idle", "grok", session.StatusIdle, true),
	}
	h.instancesMu.Lock()
	h.instances = insts
	for _, inst := range insts {
		h.instanceByID[inst.ID] = inst
	}
	h.instancesMu.Unlock()
	h.groupTree = session.NewGroupTree(h.instances)
	h.rebuildFlatItems()
	h.refreshSessionRenderSnapshot(nil)
	h.cachedStatusCounts.valid.Store(false)
	return h
}

func p55Titles(h *Home) []string {
	var out []string
	for _, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil {
			out = append(out, it.Session.Title)
		}
	}
	sort.Strings(out)
	return out
}

func p55Want(t *testing.T, h *Home, label string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if got := p55Titles(h); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s: rows = %v, want %v", label, got, want)
	}
}

func TestPatch55_CaretCyclesCurrentArchivedAll(t *testing.T) {
	h := p55Home(t)
	if h.archiveScope() != archiveScopeCurrent {
		t.Fatalf("default scope = %v, want current", h.archiveScope())
	}
	steps := []struct {
		scope  archiveScope
		filter session.Status
		all    bool
	}{
		{archiveScopeArchived, FilterModeArchived, false},
		{archiveScopeAll, "", true},
		{archiveScopeCurrent, "", false},
		{archiveScopeArchived, FilterModeArchived, false},
	}
	for i, s := range steps {
		h = chipKey(h, "^")
		if h.archiveScope() != s.scope || h.statusFilter != s.filter || h.archiveScopeAll != s.all {
			t.Fatalf("step %d: scope=%v filter=%q all=%v, want %v/%q/%v",
				i, h.archiveScope(), h.statusFilter, h.archiveScopeAll, s.scope, s.filter, s.all)
		}
	}
}

// A scope change keeps the selected row when it is still visible.
func TestPatch55_CycleKeepsSelection(t *testing.T) {
	h := p55Home(t)
	for i, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil && it.Session.Title == "a-wait" {
			h.cursor = i
		}
	}
	h.selectArchiveScope(archiveScopeAll)
	if it := h.flatItems[h.cursor]; it.Session == nil || it.Session.Title != "a-wait" {
		t.Fatalf("selection lost entering All scope: cursor on %+v", it)
	}
}

func TestPatch55_FlatItemsFollowTheScope(t *testing.T) {
	h := p55Home(t)
	p55Want(t, h, "current", "a-run", "a-wait", "a-stop")
	h.selectArchiveScope(archiveScopeArchived)
	p55Want(t, h, "archived", "z-run", "z-idle")
	h.selectArchiveScope(archiveScopeAll)
	p55Want(t, h, "all", "a-run", "a-wait", "a-stop", "z-run", "z-idle")
	h.selectArchiveScope(archiveScopeCurrent)
	p55Want(t, h, "current again", "a-run", "a-wait", "a-stop")
}

func TestPatch55_StatusFilterInAllScopeExcludesArchivedRows(t *testing.T) {
	h := p55Home(t)
	h.selectArchiveScope(archiveScopeAll)
	h.statusFilter = session.StatusRunning
	h.rebuildFlatItems()
	// z-run's stored status is "running" but it is archived: it must not pass.
	p55Want(t, h, "running filter", "a-run")
	h.statusFilter = FilterModeActive
	h.rebuildFlatItems()
	p55Want(t, h, "open filter", "a-run", "a-wait")
	// Archived rows count as stopped, but no status filter shows them.
	h.statusFilter = session.StatusStopped
	h.rebuildFlatItems()
	p55Want(t, h, "stopped filter", "a-stop")
	h.activeFilterExcludes = map[session.Status]bool{}
	h.statusFilter = FilterModeActive
	h.rebuildFlatItems()
	p55Want(t, h, "open filter, nothing excluded", "a-run", "a-wait", "a-stop")
	if !h.showingAllArchiveScope() {
		t.Fatal("a status filter must not leave the All scope")
	}
}

func TestPatch55_HiddenStoppedChipHidesArchivedRowsInAllScope(t *testing.T) {
	h := p55Home(t)
	h.selectArchiveScope(archiveScopeAll)
	h.toggleHiddenStatus(session.StatusStopped)
	p55Want(t, h, "stopped hidden", "a-run", "a-wait")
	h.toggleHiddenStatus(session.StatusStopped)
	h.toggleHiddenStatus(session.StatusRunning)
	// z-run counts as stopped, so hiding running must keep it.
	p55Want(t, h, "running hidden", "a-wait", "a-stop", "z-run", "z-idle")
}

func TestPatch55_ProviderFilterInAllScopeKeepsArchivedRows(t *testing.T) {
	h := p55Home(t)
	h.selectArchiveScope(archiveScopeAll)
	h.toggleProviderFilter("grok")
	p55Want(t, h, "grok in all", "a-wait", "z-idle")
	h.selectArchiveScope(archiveScopeCurrent)
	p55Want(t, h, "grok in current", "a-wait")
	h.selectArchiveScope(archiveScopeArchived)
	p55Want(t, h, "grok in archived", "z-idle")
}

func TestPatch55_CountsInAllScopeTreatArchivedAsStopped(t *testing.T) {
	h := p55Home(t)
	type c5 [5]int
	count := func() c5 {
		r, w, i, s, e := h.countSessionStatuses()
		return c5{r, w, i, s, e}
	}
	if got := count(); got != (c5{1, 1, 0, 1, 0}) {
		t.Fatalf("current counts = %v", got)
	}
	// No cache invalidation: the cache is keyed by scope.
	h.selectArchiveScope(archiveScopeAll)
	if got := count(); got != (c5{1, 1, 0, 3, 0}) {
		t.Fatalf("all counts = %v, want archived rows as stopped", got)
	}
	h.selectArchiveScope(archiveScopeArchived)
	if got := count(); got != (c5{1, 0, 1, 0, 0}) {
		t.Fatalf("archived counts = %v", got)
	}
	// Provider counts are live sessions only: archived rows are not live in All.
	h.selectArchiveScope(archiveScopeAll)
	for _, pc := range h.providerCounts() {
		if pc.live != 1 || (pc.tool == "claude" && pc.running != 1) {
			t.Fatalf("all-scope provider counts = %+v", h.providerCounts())
		}
	}
	// The pill names the scope.
	if bar := ansi.Strip(h.renderFilterBar()); !strings.Contains(bar, "All+archived") {
		t.Fatalf("All-scope pill should say so: %q", bar)
	}
}

func TestPatch55_ArchivedBadgeOnlyInAllScope(t *testing.T) {
	for _, wrap := range []bool{false, true} {
		h := p55Home(t)
		h.wrapRows = wrap
		render := func() string { return ansi.Strip(h.renderSessionList(120, 30)) }
		if strings.Contains(render(), "[archived]") {
			t.Fatalf("wrap=%v: badge in current scope", wrap)
		}
		h.selectArchiveScope(archiveScopeArchived)
		if strings.Contains(render(), "[archived]") {
			t.Fatalf("wrap=%v: badge in archived scope", wrap)
		}
		h.selectArchiveScope(archiveScopeAll)
		out := render()
		if n := strings.Count(out, "[archived]"); n != 2 {
			t.Fatalf("wrap=%v: want 2 [archived] badges in all scope, got %d:\n%s", wrap, n, out)
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "a-stop") && strings.Contains(line, "[archived]") {
				t.Fatalf("wrap=%v: active row badged: %q", wrap, line)
			}
		}
	}
}

func TestPatch55_UnarchiveKeyWorksInAllScope(t *testing.T) {
	h := p55Home(t)
	h.selectArchiveScope(archiveScopeAll)
	for i, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil && it.Session.Title == "z-idle" {
			h.cursor = i
		}
	}
	h = chipKey(h, "U")
	if !h.confirmDialog.IsVisible() {
		t.Fatal("shift+u on an archived row in the All scope should offer to unarchive")
	}
}

func TestPatch55_ScopeRestoredFromUIState(t *testing.T) {
	h := p55Home(t)
	h.applyUIState(uiState{ArchiveScopeAll: true})
	if h.archiveScope() != archiveScopeAll {
		t.Fatalf("scope = %v, want all", h.archiveScope())
	}
	h.applyUIState(uiState{ArchiveScopeAll: true, StatusFilter: string(FilterModeArchived)})
	if h.archiveScope() != archiveScopeArchived || h.archiveScopeAll {
		t.Fatalf("archived filter must win: scope=%v all=%v", h.archiveScope(), h.archiveScopeAll)
	}
}

// The scope control shows all three words wherever the hint keeps it, and a
// click on a word selects that scope. Columns are found in the rendered text,
// not recomputed with the implementation's arithmetic.
func TestPatch55_ScopeControlRendersAndClicks(t *testing.T) {
	forceTrueColorProfile()
	const control = "current·archived·all"
	shown, dropped := 0, 0
	for _, width := range []int{220, 160, 140, 120, 110, 100, 90, 80, 70, 60} {
		h := p55Home(t)
		h.width = width
		plain := ansi.Strip(strings.Split(h.View(), "\n")[1])
		var scopeBoxes []statusChipHitbox
		for _, c := range h.chipHitboxes {
			if c.scopeTarget {
				scopeBoxes = append(scopeBoxes, c)
			}
		}
		idx := strings.Index(plain, control)
		if idx < 0 {
			dropped++
			if len(scopeBoxes) != 0 {
				t.Fatalf("width %d: control not shown but hitboxes recorded: %+v (%q)", width, scopeBoxes, plain)
			}
			continue
		}
		shown++
		col := cellWidth(plain[:idx])
		for i, word := range archiveScopeNames {
			// Column of this word inside the rendered bar.
			off := strings.Index(control, word)
			if word == "all" {
				off = strings.LastIndex(control, word)
			}
			x := col + cellWidth(control[:off])
			var hit *statusChipHitbox
			for j := range scopeBoxes {
				if scopeBoxes[j].scope == archiveScope(i) {
					hit = &scopeBoxes[j]
				}
			}
			if hit == nil || hit.x0 != x || hit.x1 != x+cellWidth(word) || hit.y != statusChipFilterBarY {
				t.Fatalf("width %d: %s hitbox %+v, want [%d,%d) in %q", width, word, hit, x, x+cellWidth(word), plain)
			}
			if got := ansi.Cut(plain, hit.x0, hit.x1); got != word {
				t.Fatalf("width %d: %s hitbox covers %q", width, word, got)
			}
		}
		// Click each word (rightmost first) and check the scope follows.
		for _, target := range []archiveScope{archiveScopeAll, archiveScopeArchived, archiveScopeCurrent} {
			_ = h.View()
			var x int
			found := false
			for _, c := range h.chipHitboxes {
				if c.scopeTarget && c.scope == target {
					x, found = c.x0, true
				}
			}
			if !found {
				t.Fatalf("width %d: no hitbox for %v after re-render", width, target)
			}
			h = chipClick(h, x+1, statusChipFilterBarY)
			if h.archiveScope() != target {
				t.Fatalf("width %d: click on %s gave scope %v", width, archiveScopeNames[target], h.archiveScope())
			}
		}
	}
	if shown == 0 || dropped == 0 {
		t.Fatalf("want widths that both show (%d) and drop (%d) the control", shown, dropped)
	}
}
