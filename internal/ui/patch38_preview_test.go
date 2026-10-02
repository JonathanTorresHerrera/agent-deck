package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 38 (responsive): collapsible preview sections (agent / Analytics /
// Notes / Output), global + persisted, toggled by clicking the divider row.

const p38PreviewConfig = "[preview]\nshow_output = true\nshow_analytics = true\nshow_notes = true\n"

// p38PreviewHome builds two Claude sessions with notes and a long terminal
// capture, cursor on the first, in the dual layout.
func p38PreviewHome(t *testing.T) *Home {
	t.Helper()
	forceTrueColorProfile()
	home := setXDGTestHome(t)
	writeXDGTestConfig(t, home, p38PreviewConfig)

	h := NewHome()
	h.width, h.height = 160, 40
	h.initialLoading = false

	var insts []*session.Instance
	for i := 0; i < 2; i++ {
		inst := session.NewInstanceWithTool(fmt.Sprintf("p38-sess-%d", i), t.TempDir(), "claude")
		inst.Status = session.StatusIdle
		inst.Notes = fmt.Sprintf("NOTE-BODY-%d", i)
		insts = append(insts, inst)
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

	var out []string
	for i := 0; i < 200; i++ {
		out = append(out, fmt.Sprintf("out-line-%03d", i))
	}
	h.previewCacheMu.Lock()
	for _, inst := range insts {
		h.previewCache[inst.ID] = strings.Join(out, "\n")
	}
	h.previewCacheMu.Unlock()

	p38SelectSession(t, h, 0)
	return h
}

func p38SelectSession(t *testing.T, h *Home, n int) {
	t.Helper()
	seen := 0
	for i, it := range h.flatItems {
		if it.Type == session.ItemTypeSession && it.Session != nil {
			if seen == n {
				h.cursor = i
				return
			}
			seen++
		}
	}
	t.Fatalf("session %d not found", n)
}

func p38Plain(h *Home) string { return ansi.Strip(h.View()) }

func p38OutputLines(plain string) int { return strings.Count(plain, "out-line-") }

func TestPatch38_PreviewSectionsRenderWithFoldMarkers(t *testing.T) {
	h := p38PreviewHome(t)
	plain := p38Plain(h)
	for _, want := range []string{"▾ Claude", "▾ Analytics", "▾ Notes", "▾ Output", "NOTE-BODY-0", "Loading analytics...", "out-line-"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("expanded preview missing %q:\n%s", want, plain)
		}
	}
	for _, key := range previewSectionKeys {
		if _, ok := h.previewSectionScreenY(key); !ok {
			t.Fatalf("no hitbox recorded for section %q", key)
		}
	}
}

func TestPatch38_CollapsingAnalyticsDropsBodyAndGivesOutputMoreLines(t *testing.T) {
	h := p38PreviewHome(t)
	before := p38Plain(h)
	outBefore := p38OutputLines(before)
	if !strings.Contains(before, "Loading analytics...") || outBefore == 0 {
		t.Fatalf("setup: analytics body / output missing (output lines %d)", outBefore)
	}

	h.togglePreviewSection(previewSectionAnalytics)
	after := p38Plain(h)
	if strings.Contains(after, "Loading analytics...") {
		t.Fatalf("collapsed Analytics must not render its body:\n%s", after)
	}
	if !strings.Contains(after, "▸ Analytics") {
		t.Fatalf("collapsed divider must carry the ▸ marker:\n%s", after)
	}
	if outAfter := p38OutputLines(after); outAfter <= outBefore {
		t.Fatalf("Output must gain lines when Analytics collapses: before %d, after %d", outBefore, outAfter)
	}
}

func TestPatch38_EachSectionCollapsesToItsDividerOnly(t *testing.T) {
	cases := []struct {
		key, label, body string
	}{
		{previewSectionAgent, "Claude", "Not connected"},
		{previewSectionAnalytics, "Analytics", "Loading analytics..."},
		{previewSectionNotes, "Notes", "NOTE-BODY-0"},
		{previewSectionOutput, "Output", "out-line-"},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			h := p38PreviewHome(t)
			if !strings.Contains(p38Plain(h), c.body) {
				t.Fatalf("fixture does not render %q when expanded", c.body)
			}
			h.togglePreviewSection(c.key)
			plain := p38Plain(h)
			if strings.Contains(plain, c.body) {
				t.Fatalf("collapsed %s still renders %q:\n%s", c.key, c.body, plain)
			}
			if !strings.Contains(plain, "▸ "+c.label) {
				t.Fatalf("collapsed %s must keep its ▸ divider:\n%s", c.key, plain)
			}
			h.togglePreviewSection(c.key)
			if !strings.Contains(p38Plain(h), c.body) {
				t.Fatalf("expanding %s must bring the body back", c.key)
			}
		})
	}
}

func TestPatch38_AgentSectionCollapsesForOtherTools(t *testing.T) {
	h := p38PreviewHome(t)
	for _, inst := range h.instances {
		inst.Tool = "codex"
	}
	h.refreshSessionRenderSnapshot(nil)
	plain := p38Plain(h)
	if !strings.Contains(plain, "▾ Codex") {
		t.Fatalf("codex section should be collapsible too:\n%s", plain)
	}
	h.togglePreviewSection(previewSectionAgent)
	plain = p38Plain(h)
	if !strings.Contains(plain, "▸ Codex") {
		t.Fatalf("collapsed codex divider missing:\n%s", plain)
	}
	if _, ok := h.previewSectionScreenY(previewSectionAgent); !ok {
		t.Fatal("collapsed agent divider must still be clickable")
	}
}

func TestPatch38_CollapsedStateIsGlobalAndPersists(t *testing.T) {
	h := p38PreviewHome(t)
	h.togglePreviewSection(previewSectionAnalytics)
	h.togglePreviewSection(previewSectionOutput)

	// Global: switching the selected session keeps it.
	p38SelectSession(t, h, 1)
	plain := p38Plain(h)
	if !strings.Contains(plain, "NOTE-BODY-1") {
		t.Fatalf("second session not selected:\n%s", plain)
	}
	if strings.Contains(plain, "Loading analytics...") || strings.Contains(plain, "out-line-") {
		t.Fatalf("collapsed sections must stay collapsed for another session:\n%s", plain)
	}

	// Persisted: config.toml carries it, and a fresh Home reads it back.
	session.ClearUserConfigCache()
	cfg, err := session.LoadUserConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if want := []string{previewSectionAnalytics, previewSectionOutput}; !reflect.DeepEqual(cfg.Preview.CollapsedSections, want) {
		t.Fatalf("collapsed_sections on disk = %v, want %v", cfg.Preview.CollapsedSections, want)
	}
	fresh := NewHome()
	if !fresh.previewSectionCollapsed(previewSectionAnalytics) || !fresh.previewSectionCollapsed(previewSectionOutput) ||
		fresh.previewSectionCollapsed(previewSectionNotes) {
		t.Fatalf("NewHome must load the collapsed set: %v", fresh.collapsedSections)
	}

	// A settings-panel save keeps it (merge starts from disk).
	panel := NewSettingsPanel()
	panel.LoadConfig(cfg)
	merged, err := session.MergePanelConfigOntoDisk(panel.GetConfig())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !reflect.DeepEqual(merged.Preview.CollapsedSections, cfg.Preview.CollapsedSections) {
		t.Fatalf("panel merge dropped collapsed_sections: %v", merged.Preview.CollapsedSections)
	}

	// Expanding everything clears the key.
	h.togglePreviewSection(previewSectionAnalytics)
	h.togglePreviewSection(previewSectionOutput)
	session.ClearUserConfigCache()
	cfg, _ = session.LoadUserConfig()
	if len(cfg.Preview.CollapsedSections) != 0 {
		t.Fatalf("all expanded should persist an empty list, got %v", cfg.Preview.CollapsedSections)
	}
}

func TestPatch38_UnknownCollapsedKeysAreIgnored(t *testing.T) {
	h := &Home{}
	h.loadCollapsedSections(session.PreviewSettings{CollapsedSections: []string{"bogus", "notes"}})
	if !h.previewSectionCollapsed(previewSectionNotes) || h.previewSectionCollapsed("bogus") {
		t.Fatalf("unexpected state: %v", h.collapsedSections)
	}
}

func p38Click(h *Home, x, y int) *Home {
	model, _ := h.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	return model.(*Home)
}

func TestPatch38_ClickingDividerTogglesAndOtherRowsDoNot(t *testing.T) {
	h := p38PreviewHome(t)
	_ = h.View()

	y, ok := h.previewSectionScreenY(previewSectionAnalytics)
	if !ok {
		t.Fatal("no analytics divider hitbox")
	}
	// The recorded row really is the Analytics divider on screen.
	lines := strings.Split(p38Plain(h), "\n")
	if !strings.Contains(lines[y], "Analytics") {
		t.Fatalf("row %d is %q, not the Analytics divider", y, lines[y])
	}
	px := h.previewPaneX + 4

	// A click on a non-divider preview row (the row under the divider) does nothing.
	h = p38Click(h, px, y+1)
	if h.previewSectionCollapsed(previewSectionAnalytics) {
		t.Fatal("click below the divider toggled the section")
	}
	// A click on the same row but inside the sessions list does nothing to it.
	h = p38Click(h, 3, y)
	if h.previewSectionCollapsed(previewSectionAnalytics) {
		t.Fatal("click in the sessions pane toggled a preview section")
	}

	// A click on the divider collapses; the next frame re-records the hitboxes.
	h = p38Click(h, px, y)
	if !h.previewSectionCollapsed(previewSectionAnalytics) {
		t.Fatal("click on the divider did not collapse Analytics")
	}
	plain := p38Plain(h)
	if strings.Contains(plain, "Loading analytics...") || !strings.Contains(plain, "▸ Analytics") {
		t.Fatalf("collapsed render wrong:\n%s", plain)
	}
	y2, ok := h.previewSectionScreenY(previewSectionAnalytics)
	if !ok {
		t.Fatal("collapsed divider lost its hitbox")
	}
	// Click again expands.
	h = p38Click(h, px, y2)
	if h.previewSectionCollapsed(previewSectionAnalytics) {
		t.Fatal("second click did not expand Analytics")
	}

	// The Output divider works through the real message path too.
	_ = h.View()
	yo, ok := h.previewSectionScreenY(previewSectionOutput)
	if !ok {
		t.Fatal("no output divider hitbox")
	}
	h = p38Click(h, px, yo)
	if !h.previewSectionCollapsed(previewSectionOutput) {
		t.Fatal("click on the Output divider did not collapse it")
	}
}

func TestPatch38_HitboxesOnlyForDividersActuallyOnScreen(t *testing.T) {
	h := p38PreviewHome(t)
	h.height = 22 // short pane: lower sections get cut off
	_ = h.View()
	rows := h.previewPaneRows
	for _, hit := range h.previewSectionHits {
		y, ok := h.previewSectionScreenY(hit.key)
		if hit.row >= rows {
			if ok {
				t.Fatalf("divider %q at row %d is below the %d visible rows but is clickable", hit.key, hit.row, rows)
			}
			continue
		}
		if !ok {
			t.Fatalf("visible divider %q not clickable", hit.key)
		}
		if _, hitOK := h.previewSectionAt(h.previewPaneX, y); !hitOK {
			t.Fatalf("previewSectionAt disagrees with the hitbox for %q", hit.key)
		}
	}
	// A click on a row past the bottom of the pane maps to nothing.
	if _, ok := h.previewSectionAt(h.previewPaneX, h.getListContentStartY()+rows+3); ok {
		t.Fatal("row below the pane must not hit a section")
	}
}

func TestPatch38_StackedLayoutHitboxesMatchScreen(t *testing.T) {
	h := p38PreviewHome(t)
	h.width, h.height = 70, 50 // stacked layout (50-79 cols)
	if h.getLayoutMode() != LayoutModeStacked {
		t.Fatalf("layout mode is %v, want stacked", h.getLayoutMode())
	}
	for _, key := range []string{previewSectionAnalytics, previewSectionOutput} {
		lines := strings.Split(p38Plain(h), "\n")
		y, ok := h.previewSectionScreenY(key)
		if !ok {
			t.Fatalf("no hitbox for %q in stacked layout", key)
		}
		label := map[string]string{previewSectionAnalytics: "Analytics", previewSectionOutput: "Output"}[key]
		if y >= len(lines) || !strings.Contains(lines[y], label) {
			t.Fatalf("stacked hitbox for %q at row %d lands on %q", key, y, lines[min(y, len(lines)-1)])
		}
		h = p38Click(h, 5, y)
		if !h.previewSectionCollapsed(key) {
			t.Fatalf("stacked click on %q did not toggle it", key)
		}
		_ = h.View()
	}
}

func TestPatch38_NoPreviewNoHitboxes(t *testing.T) {
	h := p38PreviewHome(t)
	h.width, h.height = 40, 30 // single column: no preview pane
	_ = h.View()
	if h.previewPaneRows != 0 || len(h.previewSectionHits) != 0 {
		t.Fatalf("single-column layout must clear preview hitboxes (rows=%d hits=%d)", h.previewPaneRows, len(h.previewSectionHits))
	}
	if _, ok := h.previewSectionAt(10, 10); ok {
		t.Fatal("no preview, nothing to click")
	}
}
