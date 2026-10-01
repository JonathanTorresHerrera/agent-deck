package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 28 (status chips): the header status chips are clickable. A click on a
// status chip hides/shows that status (several may be off at once), a click on
// "All" resets every toggle and the single-status filter, and the keyboard
// filter keys keep their single-status behaviour while clearing the toggles.

func chipTestHome(t *testing.T) *Home {
	t.Helper()
	home := NewHome()
	home.width, home.height = 160, 40
	home.initialLoading = false

	mk := func(id string, st session.Status) *session.Instance {
		inst := session.NewInstanceWithTool(id, t.TempDir(), "claude")
		inst.Status = st
		return inst
	}
	insts := []*session.Instance{
		mk("run-1", session.StatusRunning),
		mk("wait-1", session.StatusWaiting),
		mk("wait-2", session.StatusWaiting),
		mk("idle-1", session.StatusIdle),
		mk("stop-1", session.StatusStopped),
		mk("err-1", session.StatusError),
	}
	home.instancesMu.Lock()
	home.instances = insts
	for _, inst := range insts {
		home.instanceByID[inst.ID] = inst
	}
	home.instancesMu.Unlock()
	home.groupTree = session.NewGroupTree(home.instances)
	home.rebuildFlatItems()
	// The header counts come from the render snapshot, normally refreshed by
	// the tick loop; refresh it by hand and drop the 500ms count cache.
	home.refreshSessionRenderSnapshot(nil)
	home.cachedStatusCounts.valid.Store(false)
	return home
}

func chipSessionIDs(h *Home) map[string]bool {
	ids := map[string]bool{}
	for _, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil {
			ids[it.Session.Title] = true
		}
	}
	return ids
}

func chipClick(h *Home, x, y int) *Home {
	model, _ := h.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return model.(*Home)
}

func chipKey(h *Home, r string) *Home {
	model, _ := h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)})
	return model.(*Home)
}

// findChip returns the hitbox for a status chip on the given row (the filter
// bar or the summary line), or nil.
func findChip(h *Home, status session.Status, y int) *statusChipHitbox {
	for i := range h.chipHitboxes {
		c := &h.chipHitboxes[i]
		if !c.all && c.status == status && c.y == y {
			return c
		}
	}
	return nil
}

func findAllChip(h *Home) *statusChipHitbox {
	for i := range h.chipHitboxes {
		if h.chipHitboxes[i].all {
			return &h.chipHitboxes[i]
		}
	}
	return nil
}

func TestPatch28_ToggleHidesAndRestores(t *testing.T) {
	h := chipTestHome(t)
	if got := len(chipSessionIDs(h)); got != 6 {
		t.Fatalf("setup: want 6 sessions, got %d", got)
	}

	h.toggleHiddenStatus(session.StatusWaiting)
	ids := chipSessionIDs(h)
	if ids["wait-1"] || ids["wait-2"] {
		t.Fatalf("waiting sessions still listed after toggling waiting off: %v", ids)
	}
	if !ids["run-1"] || !ids["idle-1"] || !ids["stop-1"] || !ids["err-1"] {
		t.Fatalf("non-waiting sessions must stay: %v", ids)
	}

	h.toggleHiddenStatus(session.StatusWaiting)
	if got := len(chipSessionIDs(h)); got != 6 {
		t.Fatalf("toggling waiting back on should restore all 6, got %d", got)
	}
}

func TestPatch28_TwoStatusesOff(t *testing.T) {
	h := chipTestHome(t)
	h.toggleHiddenStatus(session.StatusStopped)
	h.toggleHiddenStatus(session.StatusError)
	ids := chipSessionIDs(h)
	if ids["stop-1"] || ids["err-1"] {
		t.Fatalf("stopped/error should be hidden: %v", ids)
	}
	if len(ids) != 4 {
		t.Fatalf("want 4 sessions left, got %v", ids)
	}
	h.toggleHiddenStatus(session.StatusStopped)
	ids = chipSessionIDs(h)
	if !ids["stop-1"] || ids["err-1"] {
		t.Fatalf("only stopped should return: %v", ids)
	}
}

func TestPatch28_AllClearsToggleSetAndStatusFilter(t *testing.T) {
	h := chipTestHome(t)
	h.toggleHiddenStatus(session.StatusIdle)
	h.toggleHiddenStatus(session.StatusRunning)
	h.statusFilter = session.StatusWaiting
	h.resetStatusChips()
	if h.statusFilter != "" {
		t.Fatalf("statusFilter = %q, want empty", h.statusFilter)
	}
	if len(h.hiddenStatuses) != 0 {
		t.Fatalf("hiddenStatuses = %v, want empty", h.hiddenStatuses)
	}
	if got := len(chipSessionIDs(h)); got != 6 {
		t.Fatalf("want all 6 sessions back, got %d", got)
	}
}

func TestPatch28_KeyboardFilterClearsToggleSet(t *testing.T) {
	for _, key := range []string{"!", "@", "#", "&", "0"} {
		h := chipTestHome(t)
		h.toggleHiddenStatus(session.StatusStopped)
		h.toggleHiddenStatus(session.StatusIdle)
		h = chipKey(h, key)
		if len(h.hiddenStatuses) != 0 {
			t.Fatalf("key %q: hiddenStatuses = %v, want cleared", key, h.hiddenStatuses)
		}
	}
	// And the key's own single-status behaviour is intact.
	h := chipTestHome(t)
	h.toggleHiddenStatus(session.StatusStopped)
	h = chipKey(h, "!")
	if h.statusFilter != session.StatusRunning {
		t.Fatalf("! should set running filter, got %q", h.statusFilter)
	}
	ids := chipSessionIDs(h)
	if len(ids) != 1 || !ids["run-1"] {
		t.Fatalf("running filter should list only run-1, got %v", ids)
	}
}

func TestPatch28_ArchivedViewIgnoresToggles(t *testing.T) {
	h := chipTestHome(t)
	h.instancesMu.Lock()
	for _, inst := range h.instances {
		if inst.Title == "stop-1" {
			inst.ArchivedAt = time.Now()
		}
	}
	h.instancesMu.Unlock()
	h.groupTree = session.NewGroupTree(h.instances)

	h.toggleHiddenStatus(session.StatusStopped)
	h.statusFilter = FilterModeArchived
	h.rebuildFlatItems()
	if !chipSessionIDs(h)["stop-1"] {
		t.Fatalf("archived view must ignore the toggle set; got %v", chipSessionIDs(h))
	}
}

func TestPatch28_HidingEverythingIsSafe(t *testing.T) {
	h := chipTestHome(t)
	h.cursor = 5
	for _, st := range []session.Status{
		session.StatusRunning, session.StatusWaiting, session.StatusIdle,
		session.StatusStopped, session.StatusError,
	} {
		h.toggleHiddenStatus(st)
	}
	if len(h.hiddenStatuses) != 5 {
		t.Fatalf("toggle set must NOT auto-clear, got %v", h.hiddenStatuses)
	}
	if n := len(chipSessionIDs(h)); n != 0 {
		t.Fatalf("want no sessions listed, got %d", n)
	}
	if len(h.flatItems) != 0 {
		if h.cursor < 0 || h.cursor >= len(h.flatItems) {
			t.Fatalf("cursor %d out of range for %d items", h.cursor, len(h.flatItems))
		}
	} else if h.cursor != 0 {
		t.Fatalf("cursor = %d on empty list, want 0", h.cursor)
	}
	// Must still render, and the chips must still be there to click back.
	_ = h.View()
	if findChip(h, session.StatusRunning, statusChipFilterBarY) == nil {
		t.Fatalf("chips must remain after everything is hidden")
	}
	h.toggleHiddenStatus(session.StatusRunning)
	if !chipSessionIDs(h)["run-1"] {
		t.Fatalf("un-hiding running should bring run-1 back")
	}
}

func TestPatch28_ClickFilterBarChipTogglesStatus(t *testing.T) {
	h := chipTestHome(t)
	_ = h.View()

	c := findChip(h, session.StatusWaiting, statusChipFilterBarY)
	if c == nil {
		t.Fatalf("no waiting chip hitbox recorded: %+v", h.chipHitboxes)
	}
	if c.x1 <= c.x0 {
		t.Fatalf("empty hitbox %+v", c)
	}

	h = chipClick(h, c.x0, c.y)
	if ids := chipSessionIDs(h); ids["wait-1"] || ids["wait-2"] {
		t.Fatalf("click on waiting chip should hide waiting: %v", ids)
	}

	// Chip positions can shift when it renders as off; re-render and re-find.
	_ = h.View()
	c = findChip(h, session.StatusWaiting, statusChipFilterBarY)
	if c == nil {
		t.Fatalf("waiting chip vanished after being turned off")
	}
	h = chipClick(h, c.x1-1, c.y) // last cell of the chip
	if ids := chipSessionIDs(h); !ids["wait-1"] || !ids["wait-2"] {
		t.Fatalf("second click should restore waiting: %v", ids)
	}
}

func TestPatch28_ClickSummaryLineTogglesStatus(t *testing.T) {
	h := chipTestHome(t)
	_ = h.View()
	c := findChip(h, session.StatusError, statusChipSummaryY)
	if c == nil {
		t.Fatalf("no error segment hitbox on the summary line: %+v", h.chipHitboxes)
	}
	h = chipClick(h, c.x0, c.y)
	if chipSessionIDs(h)["err-1"] {
		t.Fatalf("clicking the error summary segment should hide errors")
	}
}

func TestPatch28_ClickAllResets(t *testing.T) {
	h := chipTestHome(t)
	h.toggleHiddenStatus(session.StatusIdle)
	h.toggleHiddenStatus(session.StatusStopped)
	_ = h.View()
	c := findAllChip(h)
	if c == nil {
		t.Fatalf("no All hitbox: %+v", h.chipHitboxes)
	}
	h = chipClick(h, c.x0, c.y)
	if len(h.hiddenStatuses) != 0 || h.statusFilter != "" {
		t.Fatalf("All should reset: hidden=%v filter=%q", h.hiddenStatuses, h.statusFilter)
	}
	if got := len(chipSessionIDs(h)); got != 6 {
		t.Fatalf("want 6 sessions, got %d", got)
	}
}

func TestPatch28_ClickHintTextAndGapsDoNothing(t *testing.T) {
	h := chipTestHome(t)
	_ = h.View()

	// Furthest recorded chip edge; everything to the right on the chip row is
	// the "!@#& filter • ..." hint.
	maxX := 0
	for _, c := range h.chipHitboxes {
		if c.y == statusChipFilterBarY && c.x1 > maxX {
			maxX = c.x1
		}
	}
	if maxX == 0 {
		t.Fatalf("no chip hitboxes recorded")
	}
	before := len(chipSessionIDs(h))
	for _, x := range []int{maxX + 2, maxX + 6, maxX + 12} {
		h = chipClick(h, x, statusChipFilterBarY)
		if len(h.hiddenStatuses) != 0 || h.statusFilter != "" {
			t.Fatalf("click on hint text at x=%d changed filter state: hidden=%v filter=%q", x, h.hiddenStatuses, h.statusFilter)
		}
	}
	if got := len(chipSessionIDs(h)); got != before {
		t.Fatalf("list changed after hint clicks: %d -> %d", before, got)
	}
}

// The recorded columns must line up with what is actually drawn: the cells in
// [x0, x1) of the rendered row hold that chip's own label.
func TestPatch28_HitboxesMatchRenderedCells(t *testing.T) {
	h := chipTestHome(t)
	h.toggleHiddenStatus(session.StatusIdle) // an off chip must align too
	lines := strings.Split(stripAnsi(h.View()), "\n")

	check := func(status session.Status, y int, want string) {
		t.Helper()
		c := findChip(h, status, y)
		if c == nil {
			t.Fatalf("no hitbox for %s on row %d", status, y)
		}
		cells := []rune(lines[y])
		if c.x1 > len(cells) {
			t.Fatalf("hitbox %+v beyond row width %d", c, len(cells))
		}
		if got := string(cells[c.x0:c.x1]); !strings.Contains(got, want) {
			t.Fatalf("%s row %d cells [%d,%d) = %q, want it to contain %q", status, y, c.x0, c.x1, got, want)
		}
	}
	check(session.StatusRunning, statusChipFilterBarY, "● 1")
	check(session.StatusWaiting, statusChipFilterBarY, "◐ 2")
	check(session.StatusIdle, statusChipFilterBarY, "○ 1")
	check(session.StatusStopped, statusChipFilterBarY, "■ 1")
	check(session.StatusError, statusChipFilterBarY, "✕ 1")
	check(session.StatusWaiting, statusChipSummaryY, "◐ 2 waiting")
	check(session.StatusError, statusChipSummaryY, "✕ 1 error")
}
