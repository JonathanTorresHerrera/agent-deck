package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Patch 31 (settings density): Settings panel wiring for [display] density /
// show_tool_label / show_inherited_account / show_empty_groups and
// [ui] preview_pct / preview_orientation. Mirrors
// settings_panel_timestamps_test.go.

func isolatePatch31Config(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)
}

func TestPatch31Settings_LoadConfig(t *testing.T) {
	panel := NewSettingsPanel()
	panel.LoadConfig(&session.UserConfig{
		Display: session.DisplaySettings{
			Density:              "spacious",
			ShowToolLabel:        true,
			ShowInheritedAccount: true,
			ShowEmptyGroups:      true,
		},
		UI: session.UISettings{PreviewPct: 40, PreviewOrientation: "below"},
	})
	if densityValues[panel.density] != session.DensitySpacious {
		t.Errorf("density = %q, want spacious", densityValues[panel.density])
	}
	if !panel.showToolLabel || !panel.showInheritedAccount || !panel.showEmptyGroups {
		t.Errorf("bools not loaded: tool=%v inherited=%v empty=%v",
			panel.showToolLabel, panel.showInheritedAccount, panel.showEmptyGroups)
	}
	if panel.previewPct != 40 {
		t.Errorf("previewPct = %d, want 40", panel.previewPct)
	}
	if previewOrientationValues[panel.previewOrientation] != session.PreviewOrientationBelow {
		t.Errorf("previewOrientation = %q, want below", previewOrientationValues[panel.previewOrientation])
	}

	// Empty config: comfortable, all toggles off, preview defaults.
	p2 := NewSettingsPanel()
	p2.LoadConfig(&session.UserConfig{})
	if densityValues[p2.density] != session.DensityComfortable {
		t.Errorf("default density = %q, want comfortable", densityValues[p2.density])
	}
	if p2.showToolLabel || p2.showInheritedAccount || p2.showEmptyGroups {
		t.Error("tool label / inherited account / empty groups must default to false")
	}
	if p2.previewPct != session.DefaultPreviewPct {
		t.Errorf("default previewPct = %d, want %d", p2.previewPct, session.DefaultPreviewPct)
	}
	if previewOrientationValues[p2.previewOrientation] != session.PreviewOrientationRight {
		t.Errorf("default previewOrientation = %q, want right", previewOrientationValues[p2.previewOrientation])
	}
}

func TestPatch31Settings_ToggleBoolsAndPersist(t *testing.T) {
	isolatePatch31Config(t)

	cases := []struct {
		name    string
		setting SettingType
		read    func(*SettingsPanel) bool
		disk    func(*session.UserConfig) bool
	}{
		{"show tool label", SettingShowToolLabel,
			func(p *SettingsPanel) bool { return p.showToolLabel },
			func(c *session.UserConfig) bool { return c.Display.ShowToolLabel }},
		{"show inherited account", SettingShowInheritedAccount,
			func(p *SettingsPanel) bool { return p.showInheritedAccount },
			func(c *session.UserConfig) bool { return c.Display.ShowInheritedAccount }},
		{"show empty groups", SettingShowEmptyGroups,
			func(p *SettingsPanel) bool { return p.showEmptyGroups },
			func(c *session.UserConfig) bool { return c.Display.ShowEmptyGroups }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			panel := NewSettingsPanel()
			panel.Show()
			panel.cursor = int(tc.setting)
			if tc.read(panel) {
				t.Fatal("precondition: must default to false")
			}
			if _, _, changed := panel.Update(tea.KeyMsg{Type: tea.KeySpace}); !changed {
				t.Error("Space must report changed=true")
			}
			if !tc.read(panel) {
				t.Fatal("Space must flip the value to true")
			}
			if !tc.disk(panel.GetConfig()) {
				t.Fatal("GetConfig must propagate the value into Display")
			}

			// Full persist round trip through the merge + save + reload path.
			merged, err := session.MergePanelConfigOntoDisk(panel.GetConfig())
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if err := session.SaveUserConfig(merged); err != nil {
				t.Fatalf("save: %v", err)
			}
			session.ClearUserConfigCache()
			reloaded, err := session.LoadUserConfig()
			if err != nil {
				t.Fatalf("reload: %v", err)
			}
			if !tc.disk(reloaded) {
				t.Fatal("value did not survive save + reload")
			}

			// Second toggle flips it back.
			_, _, _ = panel.Update(tea.KeyMsg{Type: tea.KeySpace})
			if tc.read(panel) {
				t.Error("second toggle must flip back to false")
			}
		})
	}
}

func TestPatch31Settings_AdjustDensityAndPersist(t *testing.T) {
	isolatePatch31Config(t)

	panel := NewSettingsPanel()
	panel.Show()
	panel.cursor = int(SettingDensity)
	if densityValues[panel.density] != session.DensityComfortable {
		t.Fatalf("precondition: density must start comfortable, got %q", densityValues[panel.density])
	}

	_, _, changed := panel.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !changed || densityValues[panel.density] != session.DensitySpacious {
		t.Fatalf("right: changed=%v density=%q, want spacious", changed, densityValues[panel.density])
	}
	// Clamped at the end.
	if _, _, changed = panel.Update(tea.KeyMsg{Type: tea.KeyRight}); changed {
		t.Error("right at the last option must not report a change")
	}
	_, _, _ = panel.Update(tea.KeyMsg{Type: tea.KeyLeft})
	_, _, _ = panel.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if densityValues[panel.density] != session.DensityCompact {
		t.Fatalf("two lefts: density=%q, want compact", densityValues[panel.density])
	}
	if _, _, changed = panel.Update(tea.KeyMsg{Type: tea.KeyLeft}); changed {
		t.Error("left at the first option must not report a change")
	}

	if got := panel.GetConfig().Display.Density; got != session.DensityCompact {
		t.Fatalf("GetConfig density = %q, want compact", got)
	}
	merged, err := session.MergePanelConfigOntoDisk(panel.GetConfig())
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := session.SaveUserConfig(merged); err != nil {
		t.Fatalf("save: %v", err)
	}
	session.ClearUserConfigCache()
	reloaded, _ := session.LoadUserConfig()
	if reloaded.Display.Density != session.DensityCompact {
		t.Fatalf("density did not persist: %q", reloaded.Display.Density)
	}
}

func TestPatch31Settings_PreviewSplitAndPositionAdjustAndPersist(t *testing.T) {
	isolatePatch31Config(t)

	panel := NewSettingsPanel()
	panel.Show()

	panel.cursor = int(SettingPreviewPct)
	if panel.previewPct != session.DefaultPreviewPct {
		t.Fatalf("precondition: previewPct must start at %d, got %d", session.DefaultPreviewPct, panel.previewPct)
	}
	_, _, changed := panel.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if !changed || panel.previewPct != session.DefaultPreviewPct-5 {
		t.Fatalf("left: changed=%v previewPct=%d, want %d", changed, panel.previewPct, session.DefaultPreviewPct-5)
	}
	// Clamp at the 90 ceiling and 10 floor.
	panel.previewPct = 90
	if _, _, changed = panel.Update(tea.KeyMsg{Type: tea.KeyRight}); changed || panel.previewPct != 90 {
		t.Errorf("right at 90: changed=%v previewPct=%d, want unchanged 90", changed, panel.previewPct)
	}
	panel.previewPct = 10
	if _, _, changed = panel.Update(tea.KeyMsg{Type: tea.KeyLeft}); changed || panel.previewPct != 10 {
		t.Errorf("left at 10: changed=%v previewPct=%d, want unchanged 10", changed, panel.previewPct)
	}
	panel.previewPct = 45

	panel.cursor = int(SettingPreviewOrientation)
	_, _, changed = panel.Update(tea.KeyMsg{Type: tea.KeyRight})
	if !changed || previewOrientationValues[panel.previewOrientation] != session.PreviewOrientationBelow {
		t.Fatalf("right: changed=%v orientation=%q, want below", changed, previewOrientationValues[panel.previewOrientation])
	}

	cfg := panel.GetConfig()
	if cfg.UI.PreviewPct != 45 || cfg.UI.PreviewOrientation != session.PreviewOrientationBelow {
		t.Fatalf("GetConfig UI = pct %d orient %q, want 45 / below", cfg.UI.PreviewPct, cfg.UI.PreviewOrientation)
	}
	merged, err := session.MergePanelConfigOntoDisk(cfg)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if err := session.SaveUserConfig(merged); err != nil {
		t.Fatalf("save: %v", err)
	}
	session.ClearUserConfigCache()
	reloaded, _ := session.LoadUserConfig()
	if reloaded.UI.GetPreviewPct() != 45 || reloaded.UI.GetPreviewOrientation() != session.PreviewOrientationBelow {
		t.Fatalf("preview split/position did not persist: pct=%d orient=%q",
			reloaded.UI.GetPreviewPct(), reloaded.UI.GetPreviewOrientation())
	}
}

// Pins cursorToLine for every Patch 31 row, plus the two TOOL PICKER rows that
// shifted down behind them.
func TestPatch31Settings_ScrollMappingBringsEachRowIntoView(t *testing.T) {
	isolatePatch31Config(t)

	rows := []struct {
		setting SettingType
		label   string
	}{
		{SettingDensity, "List density"},
		{SettingShowToolLabel, "Show tool name"},
		{SettingShowInheritedAccount, "Show inherited account"},
		{SettingShowEmptyGroups, "Show empty groups"},
		{SettingPreviewPct, "Preview split"},
		{SettingPreviewOrientation, "Preview position"},
		{SettingShowOnlyInstalledTools, "Show only installed tools"},
		{SettingVisibleTools, "Visible tools"},
	}
	for _, r := range rows {
		panel := NewSettingsPanel()
		panel.SetSize(100, 20)
		panel.Show()
		panel.cursor = int(r.setting)
		view := panel.View()
		if !containsString(view, r.label) {
			t.Errorf("cursorToLine must scroll %q into view for setting %d. Got:\n%s", r.label, r.setting, view)
		}
		if !containsString(view, "more above") && !containsString(view, "more below") {
			t.Fatal("test must run in scroll mode (no scroll indicator rendered)")
		}
	}
}

func TestPatch31Settings_CountMatchesLastEnum(t *testing.T) {
	if int(SettingVisibleTools) != settingsCount-1 {
		t.Fatalf("settingsCount (%d) must be last enum (%d) + 1", settingsCount, int(SettingVisibleTools))
	}
	if int(SettingTheme) != 0 {
		t.Fatal("SettingTheme must stay index 0")
	}
}
