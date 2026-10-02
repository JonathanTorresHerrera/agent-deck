package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 38 (responsive): session rows are title-first with a compact " @slot"
// account tag, and the " (Aida)" title suffix is hidden by default.

func p38Row(h *Home, title, account string, listWidth int) string {
	inst := &session.Instance{ID: "p38-row", Title: title, Account: account}
	state := sessionRenderState{
		status: session.StatusIdle, tool: "claude", title: title,
		account: account, accountDisplay: newAccountPresentation(account, true),
	}
	return ansi.Strip(p31RenderRow(h, inst, state, listWidth))
}

func TestPatch38_RowShowsCompactAccountTagAndFullTitle(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140}
	title := "WO-OMNI-GUIDED-HELP-01"
	row := p38Row(h, title, "claude-2", 100)
	if !strings.Contains(row, title) {
		t.Fatalf("full title missing: %q", row)
	}
	if !strings.Contains(row, " @claude-2") {
		t.Fatalf("compact tag ' @claude-2' missing: %q", row)
	}
	for _, old := range []string{"[account:", `"claude-2"`} {
		if strings.Contains(row, old) {
			t.Fatalf("row still carries the long account form %q: %q", old, row)
		}
	}
	// The long form is untouched for any other surface.
	if got := newAccountPresentation("claude-2", true).badge; got != ` [account:"claude-2"]` {
		t.Fatalf("long badge form changed: %q", got)
	}
}

func TestPatch38_NarrowRowKeepsTitleAndDropsTagWhole(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140}
	title := "WO-OMNI-GUIDED-HELP-01"
	sawTitleNoTag, sawBoth := false, false
	for w := 20; w <= 100; w++ {
		row := p38Row(h, title, "claude-2", w)
		hasTitle := strings.Contains(row, title)
		hasTag := strings.Contains(row, " @claude-2")
		if hasTag && !hasTitle {
			t.Fatalf("listWidth %d: tag shown while the title is cut: %q", w, row)
		}
		if strings.Contains(row, "@") && !hasTag {
			t.Fatalf("listWidth %d: tag shown as a stub: %q", w, row)
		}
		if cellWidth(row) > w {
			t.Fatalf("listWidth %d: row is %d cells", w, cellWidth(row))
		}
		if hasTitle && !hasTag {
			sawTitleNoTag = true
		}
		if hasTitle && hasTag {
			sawBoth = true
		}
	}
	if !sawTitleNoTag || !sawBoth {
		t.Fatalf("expected a width with title only (%v) and a width with title+tag (%v)", sawTitleNoTag, sawBoth)
	}
	// And at a tight width the tag is gone entirely while the title survives.
	row := p38Row(h, title, "claude-2", 40)
	if strings.Contains(row, "@") {
		t.Fatalf("narrow row should have dropped the tag: %q", row)
	}
	if !strings.Contains(row, "WO-OMNI") {
		t.Fatalf("title must survive: %q", row)
	}
}

func TestPatch38_CompactTagIsEscaped(t *testing.T) {
	p := newAccountPresentation("a\x1b]0;x\ay", true)
	if strings.ContainsAny(p.compact, "\x1b\a") {
		t.Fatalf("compact tag leaks control characters: %q", p.compact)
	}
	if !strings.HasPrefix(p.compact, " @") || p.compactWidth != cellWidth(p.compact) {
		t.Fatalf("compact form wrong: %q width %d", p.compact, p.compactWidth)
	}
	if (newAccountPresentation("", false) != accountPresentation{}) {
		t.Fatal("hidden presentation must stay the zero value")
	}
	if got := newAccountPresentation("", true).compact; got != " @inherited" {
		t.Fatalf("inherited compact = %q", got)
	}
}

// --- (Aida) suffix ----------------------------------------------------------

func TestPatch38_AidaSuffixHiddenByDefaultShownWhenEnabled(t *testing.T) {
	forceTrueColorProfile()
	title := "WO-OMNI-GUIDED-HELP-01 (Aida)"
	h := &Home{width: 140}
	row := p38Row(h, title, "", 100)
	if strings.Contains(row, "(Aida)") || !strings.Contains(row, "WO-OMNI-GUIDED-HELP-01") {
		t.Fatalf("suffix must be hidden by default: %q", row)
	}
	h.showAidaSuffix = true
	if row := p38Row(h, title, "", 100); !strings.Contains(row, "WO-OMNI-GUIDED-HELP-01 (Aida)") {
		t.Fatalf("show_aida_suffix=true must keep the suffix: %q", row)
	}
}

func TestPatch38_AidaSuffixOnlyStrippedWhenTrailingAndExact(t *testing.T) {
	forceTrueColorProfile()
	h := &Home{width: 140}
	for _, title := range []string{"Fix (Aida) bug", "work (aida)", "work(Aida)", "(Aida) work"} {
		if row := p38Row(h, title, "", 100); !strings.Contains(row, title) {
			t.Fatalf("title %q must be untouched, row %q", title, row)
		}
	}
	if row := p38Row(h, "trailing spaces (Aida)   ", "", 100); strings.Contains(row, "(Aida)") {
		t.Fatalf("trailing spaces after the suffix must not defeat the strip: %q", row)
	}
	if got := stripAidaSuffix(" (Aida)"); got != " (Aida)" {
		t.Fatalf("a title that is only the suffix is kept: %q", got)
	}
}

func TestPatch38_AidaSuffixHiddenReclaimsWidthAndNeverEditsTitle(t *testing.T) {
	forceTrueColorProfile()
	inst := &session.Instance{ID: "p38-aida", Title: "REVIEW-THE-THING-NOW (Aida)"}
	state := sessionRenderState{status: session.StatusIdle, tool: "claude", title: inst.Title}
	hidden, shown := &Home{width: 60}, &Home{width: 60, showAidaSuffix: true}
	// Find a width where the suffixed title is truncated but the bare one fits.
	found := false
	for w := 24; w <= 60; w++ {
		hr := ansi.Strip(p31RenderRow(hidden, inst, state, w))
		sr := ansi.Strip(p31RenderRow(shown, inst, state, w))
		if strings.Contains(hr, "REVIEW-THE-THING-NOW") && !strings.Contains(sr, "REVIEW-THE-THING-NOW (Aida)") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("hiding the suffix should give the title its cells back at some width")
	}
	if inst.Title != "REVIEW-THE-THING-NOW (Aida)" {
		t.Fatalf("inst.Title must never be modified: %q", inst.Title)
	}
}

func TestPatch38_AidaSuffixSettingAppliesFromConfig(t *testing.T) {
	h := &Home{}
	h.applyListDisplaySettings(session.DisplaySettings{ShowAidaSuffix: true})
	if !h.showAidaSuffix {
		t.Fatal("applyListDisplaySettings must read show_aida_suffix")
	}
	h.applyListDisplaySettings(session.DisplaySettings{})
	if h.showAidaSuffix {
		t.Fatal("default must be hidden")
	}

	// Startup read-back through NewHome.
	home := setXDGTestHome(t)
	writeXDGTestConfig(t, home, "[display]\nshow_aida_suffix = true\n")
	if !NewHome().showAidaSuffix {
		t.Fatal("NewHome must read [display] show_aida_suffix at startup")
	}
}

func TestPatch38_AidaSuffixSettingsPanelToggleLoadsAndPersists(t *testing.T) {
	isolatePatch31Config(t)

	loaded := NewSettingsPanel()
	loaded.LoadConfig(&session.UserConfig{Display: session.DisplaySettings{ShowAidaSuffix: true}})
	if !loaded.showAidaSuffix {
		t.Fatal("panel must load show_aida_suffix")
	}

	panel := NewSettingsPanel()
	panel.Show()
	panel.cursor = int(SettingShowAidaSuffix)
	if panel.showAidaSuffix {
		t.Fatal("must default to false")
	}
	if !strings.Contains(panel.View(), `Show "(Aida)" title suffix`) {
		t.Fatal("settings view must list the Aida suffix checkbox")
	}
	if _, _, changed := panel.Update(tea.KeyMsg{Type: tea.KeySpace}); !changed || !panel.showAidaSuffix {
		t.Fatalf("Space must flip it on (changed=%v value=%v)", changed, panel.showAidaSuffix)
	}
	if !panel.GetConfig().Display.ShowAidaSuffix {
		t.Fatal("GetConfig must carry the value")
	}
	merged, err := session.MergePanelConfigOntoDisk(panel.GetConfig())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := session.SaveUserConfig(merged); err != nil {
		t.Fatalf("save: %v", err)
	}
	session.ClearUserConfigCache()
	reloaded, err := session.LoadUserConfig()
	if err != nil || !reloaded.Display.ShowAidaSuffix {
		t.Fatalf("value did not survive save + reload (err=%v)", err)
	}
	_, _, _ = panel.Update(tea.KeyMsg{Type: tea.KeySpace})
	if panel.showAidaSuffix {
		t.Fatal("second toggle must flip back off")
	}
}

func TestPatch38_SettingsCountMatchesEnumTail(t *testing.T) {
	if int(SettingPrivacy) != settingsCount-1 { // v1.16.22: PRIVACY is the last section
		t.Fatalf("settingsCount=%d but last setting index is %d", settingsCount, int(SettingPrivacy))
	}
	if int(SettingShowAidaSuffix) != int(SettingShowEmptyGroups)+1 {
		t.Fatal("Aida suffix row must directly follow the patch-31 display rows")
	}
}
