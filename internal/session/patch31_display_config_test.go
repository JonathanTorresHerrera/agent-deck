package session

import (
	"testing"

	"github.com/BurntSushi/toml"
)

// Patch 31 (settings density): [display] density / show_tool_label /
// show_inherited_account / show_empty_groups and [ui] preview_pct /
// preview_orientation through MergePanelConfigOntoDisk and the TOML layer.

func TestPatch31_MergeKeepsDisplayFields(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	panel := &UserConfig{Display: DisplaySettings{
		Density:              DensitySpacious,
		ShowToolLabel:        true,
		ShowInheritedAccount: true,
		ShowEmptyGroups:      true,
	}}
	merged, err := MergePanelConfigOntoDisk(panel)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.Display.Density != DensitySpacious {
		t.Errorf("merge dropped Display.Density: %q", merged.Display.Density)
	}
	if !merged.Display.ShowToolLabel || !merged.Display.ShowInheritedAccount || !merged.Display.ShowEmptyGroups {
		t.Errorf("merge dropped a true bool: tool=%v inherited=%v empty=%v",
			merged.Display.ShowToolLabel, merged.Display.ShowInheritedAccount, merged.Display.ShowEmptyGroups)
	}

	// Persist, then merge an all-off panel on top: false must propagate too.
	if err := SaveUserConfig(merged); err != nil {
		t.Fatalf("save: %v", err)
	}
	ClearUserConfigCache()
	off, err := MergePanelConfigOntoDisk(&UserConfig{Display: DisplaySettings{Density: DensityCompact}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if off.Display.Density != DensityCompact {
		t.Errorf("density change not propagated: %q", off.Display.Density)
	}
	if off.Display.ShowToolLabel || off.Display.ShowInheritedAccount || off.Display.ShowEmptyGroups {
		t.Error("false bools failed to propagate — toggles would be stuck on")
	}
}

func TestPatch31_MergeKeepsPreviewSplitAndPosition(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	merged, err := MergePanelConfigOntoDisk(&UserConfig{UI: UISettings{PreviewPct: 40, PreviewOrientation: PreviewOrientationBelow}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if merged.UI.PreviewPct != 40 || merged.UI.PreviewOrientation != PreviewOrientationBelow {
		t.Fatalf("merge dropped preview split/position: pct=%d orient=%q", merged.UI.PreviewPct, merged.UI.PreviewOrientation)
	}

	// A panel that does not manage them (zero/empty) must not reset the disk values.
	if err := SaveUserConfig(merged); err != nil {
		t.Fatalf("save: %v", err)
	}
	ClearUserConfigCache()
	kept, err := MergePanelConfigOntoDisk(&UserConfig{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if kept.UI.PreviewPct != 40 || kept.UI.PreviewOrientation != PreviewOrientationBelow {
		t.Fatalf("empty panel clobbered preview settings: pct=%d orient=%q", kept.UI.PreviewPct, kept.UI.PreviewOrientation)
	}
}

func TestPatch31_GetDensityResolution(t *testing.T) {
	cases := map[string]string{
		"":            DensityComfortable,
		"comfortable": DensityComfortable,
		"compact":     DensityCompact,
		"  Compact ":  DensityCompact,
		"SPACIOUS":    DensitySpacious,
		"bogus":       DensityComfortable,
	}
	for in, want := range cases {
		if got := (DisplaySettings{Density: in}).GetDensity(); got != want {
			t.Errorf("GetDensity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPatch31_TomlKeysAndDefaults(t *testing.T) {
	var cfg UserConfig
	src := `
[display]
density = "compact"
show_tool_label = true
show_inherited_account = true
show_empty_groups = true
`
	if _, err := toml.Decode(src, &cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	d := cfg.Display
	if d.Density != "compact" || !d.ShowToolLabel || !d.ShowInheritedAccount || !d.ShowEmptyGroups {
		t.Fatalf("toml keys not bound: %+v", d)
	}

	var empty UserConfig
	if _, err := toml.Decode("", &empty); err != nil {
		t.Fatalf("decode empty: %v", err)
	}
	if empty.Display.ShowToolLabel || empty.Display.ShowInheritedAccount || empty.Display.ShowEmptyGroups {
		t.Error("tool label / inherited account / empty groups must default to false")
	}
	if empty.Display.GetDensity() != DensityComfortable {
		t.Errorf("default density = %q, want comfortable", empty.Display.GetDensity())
	}
}
