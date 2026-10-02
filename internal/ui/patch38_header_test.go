package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/sysinfo"
)

// Patch 38 (responsive): header line 1 fits the terminal by degrading in a
// fixed order; the filter bar never cuts a pill; both lines count only the
// rendered archive partition.

func p38SysStats() sysinfo.Stats {
	return sysinfo.Stats{
		CPU:     sysinfo.CPUStat{Available: true, UsagePercent: 12},
		Memory:  sysinfo.MemStat{Available: true, UsedBytes: 8 << 30, TotalBytes: 16 << 30, UsagePercent: 50},
		Disk:    sysinfo.DiskStat{Available: true, UsedBytes: 200 << 30, TotalBytes: 500 << 30, UsagePercent: 40},
		Network: sysinfo.NetworkStat{Available: true, RxBytesPerSec: 1500, TxBytesPerSec: 700},
	}
}

// p38HeaderHome is the 6-session chip fixture with system stats and a cost
// segment, so every degradation step has something to drop.
func p38HeaderHome(t *testing.T, width int) *Home {
	t.Helper()
	h := chipTestHome(t)
	h.sysStatsCollector = sysinfo.NewStaticCollector(p38SysStats())
	h.costLineTemplate = "today {cost_today}"
	h.costToday.Store(12_340_000)
	h.setP38Width(width)
	return h
}

func (h *Home) setP38Width(width int) {
	h.width, h.height = width, 40
	h.cachedStatusCounts.valid.Store(false)
}

func p38ViewLines(h *Home) []string {
	return strings.Split(h.View(), "\n")
}

// p38HeaderFlags reports which droppable parts of header line 1 are visible,
// in drop order: network, disk, ram, cpu, cost, long status words.
func p38HeaderFlags(plain string) [6]bool {
	return [6]bool{
		strings.Contains(plain, "⇅"),
		strings.Contains(plain, "▪"),
		strings.Contains(plain, "⛁"),
		strings.Contains(plain, "⚙"),
		strings.Contains(plain, "today $"),
		strings.Contains(plain, "running"),
	}
}

func TestPatch38_HeaderLine1FitsAndDegradesInOrder(t *testing.T) {
	forceTrueColorProfile()
	counts := []string{"● 1", "◐ 2", "○ 1", "■ 1", "✕ 1"}
	version := "v" + Version

	var atWide, atNarrow [6]bool
	for width := 200; width >= 80; width -= 2 {
		h := p38HeaderHome(t, width)
		line := p38ViewLines(h)[0]
		plain := ansi.Strip(line)

		if w := lipgloss.Width(line); w > width {
			t.Fatalf("width %d: header line 1 is %d cells wide", width, w)
		}
		if strings.ContainsRune(plain, 0x1b) {
			t.Fatalf("width %d: stray escape after stripping ANSI: %q", width, plain)
		}
		if !strings.Contains(plain, version) {
			t.Fatalf("width %d: version badge %q missing: %q", width, version, plain)
		}
		for _, c := range counts {
			if !strings.Contains(plain, c) {
				t.Fatalf("width %d: status count %q missing: %q", width, c, plain)
			}
		}

		// Drop order: network, disk, ram, cpu, cost, words. A part may only
		// be present while every part dropped LATER is present too.
		f := p38HeaderFlags(plain)
		for i := 0; i < len(f); i++ {
			if !f[i] {
				continue
			}
			for j := i + 1; j < len(f); j++ {
				if !f[j] {
					t.Fatalf("width %d: part %d present but later part %d dropped (flags %v): %q", width, i, j, f, plain)
				}
			}
		}
		if width == 200 {
			atWide = f
		}
		if width == 80 {
			atNarrow = f
		}
	}
	for _, ok := range atWide {
		if !ok {
			t.Fatalf("at width 200 everything should fit, flags %v", atWide)
		}
	}
	if atNarrow == atWide {
		t.Fatalf("at width 80 nothing was dropped: %v", atNarrow)
	}
}

// Each stage is reachable and is the one the spec names: forcing one stage at
// a time through the fit function.
func TestPatch38_HeaderStagesAreTheSpecifiedOnes(t *testing.T) {
	forceTrueColorProfile()
	h := p38HeaderHome(t, 400)
	logo := RenderLogoCompact(1, 2, 1)
	title := "Agent Deck"
	badge := "v" + Version
	build := func(width int) string {
		h.width = width
		got := h.fitHeaderLine1(logo, title, badge, 1, 2, 1, 1, 1, "today $12.34")
		return ansi.Strip(got.left)
	}
	full := build(400)
	if f := p38HeaderFlags(full); f != [6]bool{true, true, true, true, true, true} {
		t.Fatalf("wide: %v %q", f, full)
	}
	// Squeeze one cell at a time and record the first width at which each part
	// disappears: network must go first, then disk, then ram, then cpu, then cost.
	firstGone := [6]int{}
	for w := lipgloss.Width(full) + lipgloss.Width(badge) + 2; w >= 40; w-- {
		f := p38HeaderFlags(build(w))
		for i := range f {
			if !f[i] && firstGone[i] == 0 {
				firstGone[i] = w
			}
		}
	}
	for i := 0; i < 5; i++ {
		if firstGone[i] == 0 || firstGone[i+1] == 0 {
			t.Fatalf("a stage was never reached: %v", firstGone)
		}
		if firstGone[i] < firstGone[i+1] {
			t.Fatalf("stage %d should vanish at a wider width than stage %d: %v", i, i+1, firstGone)
		}
	}
	// The short form keeps the glyph and the count.
	short := build(firstGone[5])
	if !strings.Contains(short, "● 1") || strings.Contains(short, "running") {
		t.Fatalf("short form should read '● 1' without words: %q", short)
	}
}

func TestPatch38_HeaderChipHitboxesFollowDegradedLayout(t *testing.T) {
	forceTrueColorProfile()
	glyph := map[session.Status]string{
		session.StatusRunning: "●", session.StatusWaiting: "◐", session.StatusIdle: "○",
		session.StatusStopped: "■", session.StatusError: "✕",
	}
	for _, width := range []int{200, 150, 120, 100, 80} {
		h := p38HeaderHome(t, width)
		lines := p38ViewLines(h)
		plain := ansi.Strip(lines[0])
		seen := 0
		for st, g := range glyph {
			c := findChip(h, st, statusChipSummaryY)
			if c == nil {
				t.Fatalf("width %d: no summary hitbox for %s", width, st)
			}
			seen++
			seg := ansi.Cut(plain, c.x0, c.x1)
			if !strings.Contains(seg, g) {
				t.Fatalf("width %d: hitbox [%d,%d) for %s covers %q, want glyph %s (line %q)", width, c.x0, c.x1, st, seg, g, plain)
			}
			// And clicking it toggles that status.
			before := h.statusHidden(st)
			h = chipClick(h, c.x0, statusChipSummaryY)
			if h.statusHidden(st) == before {
				t.Fatalf("width %d: click on %s chip did not toggle it", width, st)
			}
			h.toggleHiddenStatus(st) // restore
		}
		if seen != 5 {
			t.Fatalf("width %d: want 5 summary hitboxes, got %d", width, seen)
		}
	}
}

func TestPatch38_FilterBarNeverCutsAPill(t *testing.T) {
	forceTrueColorProfile()
	glyph := map[session.Status]string{
		session.StatusRunning: "●", session.StatusWaiting: "◐", session.StatusIdle: "○",
		session.StatusStopped: "■", session.StatusError: "✕",
	}
	var hintAtWide, hintAtNarrow string
	for width := 120; width >= 50; width-- {
		h := p38HeaderHome(t, width)
		line := p38ViewLines(h)[1]
		plain := ansi.Strip(line)
		if w := lipgloss.Width(line); w > width {
			t.Fatalf("width %d: filter bar %d cells", width, w)
		}
		for st, g := range glyph {
			c := findChip(h, st, statusChipFilterBarY)
			if c == nil {
				t.Fatalf("width %d: no pill hitbox for %s", width, st)
			}
			if c.x1 > width {
				t.Fatalf("width %d: pill for %s ends at %d (cut by the width)", width, st, c.x1)
			}
			seg := ansi.Cut(plain, c.x0, c.x1)
			if !strings.Contains(seg, g) || !strings.Contains(seg, " ") {
				t.Fatalf("width %d: pill hitbox for %s covers %q", width, st, seg)
			}
		}
		if a := findAllChip(h); a == nil || !strings.Contains(ansi.Cut(plain, a.x0, a.x1), "All") {
			t.Fatalf("width %d: All pill hitbox wrong: %+v in %q", width, a, plain)
		}
		if width == 120 {
			hintAtWide = plain
		}
		if width == 70 {
			hintAtNarrow = plain
		}
	}
	if !strings.Contains(hintAtWide, "archived") {
		t.Fatalf("wide filter bar should keep its hint: %q", hintAtWide)
	}
	if strings.Contains(hintAtNarrow, "time") {
		t.Fatalf("narrow filter bar should have dropped the time part first: %q", hintAtNarrow)
	}
}

// The filter-hint tier test was dropped on v1.16.22: upstream's
// fitFilterBarHint (1.16.13 #2313) already drops whole hint segments from the
// low-priority end, so patch 38's own tiers are not ported.
// TestPatch38_FilterBarNeverCutsAPill still pins the behaviour that matters.

// p38ArchiveHome has, per status, a different number of active and archived
// sessions: running 1/2, waiting 2/1, idle 3/1, stopped 1/4, error 2/3.
func p38ArchiveHome(t *testing.T) *Home {
	t.Helper()
	h := NewHome()
	h.width, h.height = 160, 40
	h.initialLoading = false
	var insts []*session.Instance
	add := func(st session.Status, active, archived int) {
		for i := 0; i < active+archived; i++ {
			inst := session.NewInstanceWithTool(fmt.Sprintf("%s-%d", st, i), t.TempDir(), "claude")
			inst.Status = st
			if i >= active {
				inst.ArchivedAt = time.Now()
			}
			insts = append(insts, inst)
		}
	}
	add(session.StatusRunning, 1, 2)
	add(session.StatusWaiting, 2, 1)
	add(session.StatusIdle, 3, 1)
	add(session.StatusStopped, 1, 4)
	add(session.StatusError, 2, 3)
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

func p38PillCount(t *testing.T, h *Home, plain string, st session.Status, glyph string) string {
	t.Helper()
	c := findChip(h, st, statusChipFilterBarY)
	if c == nil {
		t.Fatalf("no pill for %s", st)
	}
	return strings.TrimSpace(ansi.Cut(plain, c.x0, c.x1))
}

func TestPatch38_HeaderAndPillsCountOnlyTheRenderedPartition(t *testing.T) {
	forceTrueColorProfile()
	h := p38ArchiveHome(t)

	type want struct{ running, waiting, idle, stopped, errored int }
	check := func(label string, w want) {
		t.Helper()
		r, wa, i, s, e := h.countSessionStatuses()
		if (want{r, wa, i, s, e}) != w {
			t.Fatalf("%s: countSessionStatuses = %d/%d/%d/%d/%d, want %+v", label, r, wa, i, s, e, w)
		}
		lines := p38ViewLines(h)
		head := ansi.Strip(lines[0])
		bar := ansi.Strip(lines[1])
		for _, c := range []struct {
			glyph string
			n     int
			st    session.Status
		}{
			{"●", w.running, session.StatusRunning}, {"◐", w.waiting, session.StatusWaiting},
			{"○", w.idle, session.StatusIdle}, {"■", w.stopped, session.StatusStopped},
			{"✕", w.errored, session.StatusError},
		} {
			wantSeg := fmt.Sprintf("%s %d", c.glyph, c.n)
			if !strings.Contains(head, wantSeg) {
				t.Fatalf("%s: header line missing %q: %q", label, wantSeg, head)
			}
			if got := p38PillCount(t, h, bar, c.st, c.glyph); got != wantSeg {
				t.Fatalf("%s: pill for %s reads %q, want %q (bar %q)", label, c.st, got, wantSeg, bar)
			}
		}
	}

	check("active view", want{1, 2, 3, 1, 2})

	// Toggle the archived view without touching the cache: the counts must
	// follow the partition immediately, not after the 500ms cache window.
	h.statusFilter = FilterModeArchived
	h.rebuildFlatItems()
	check("archived view", want{2, 1, 1, 4, 3})

	h.statusFilter = ""
	h.rebuildFlatItems()
	check("back to active view", want{1, 2, 3, 1, 2})
}

func TestPatch38_ChipClickStillWorksWithArchivedSessionsPresent(t *testing.T) {
	forceTrueColorProfile()
	h := p38ArchiveHome(t)
	_ = h.View()
	c := findChip(h, session.StatusWaiting, statusChipFilterBarY)
	if c == nil {
		t.Fatal("no waiting pill")
	}
	h = chipClick(h, c.x0, statusChipFilterBarY)
	if !h.statusHidden(session.StatusWaiting) {
		t.Fatal("click did not hide waiting")
	}
	for _, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil && it.Session.Status == session.StatusWaiting {
			t.Fatalf("waiting session %s still listed", it.Session.Title)
		}
	}
	// The chip stays (struck through) with the same active-only count.
	lines := p38ViewLines(h)
	if got := p38PillCount(t, h, ansi.Strip(lines[1]), session.StatusWaiting, "◐"); got != "◐ 2" {
		t.Fatalf("hidden waiting pill reads %q, want %q", got, "◐ 2")
	}
}

// A header chip whose columns overlap the pane separator must be clickable, not
// swallowed by the divider-drag grab (which belongs to the content rows only).
func TestPatch38_HeaderRowClickInSeparatorColumnsDoesNotGrabDivider(t *testing.T) {
	forceTrueColorProfile()
	h := p38HeaderHome(t, 100)
	_ = h.View()
	x := h.sessionsPaneWidth() // first separator column
	if !h.isOnDivider(x) {
		t.Fatalf("setup: column %d should be on the divider", x)
	}
	for _, y := range []int{statusChipSummaryY, statusChipFilterBarY} {
		h = chipClick(h, x, y)
		if h.draggingDivider {
			t.Fatalf("a click on header row %d grabbed the divider", y)
		}
	}
	// Inside the content area the grab still works.
	h = chipClick(h, x, h.getListContentStartY())
	if !h.draggingDivider {
		t.Fatal("a click on the separator in the content rows must still grab the divider")
	}
}
