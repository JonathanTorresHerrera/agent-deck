package session

import (
	"reflect"
	"testing"
)

// Patch 38 (responsive): [display] show_aida_suffix is panel-managed (the merge
// must copy it, true AND false), while [preview] collapsed_sections is written
// straight to disk by the TUI and must survive any panel save.

func TestPatch38_MergePropagatesShowAidaSuffix(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	on, err := MergePanelConfigOntoDisk(&UserConfig{Display: DisplaySettings{ShowAidaSuffix: true}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if !on.Display.ShowAidaSuffix {
		t.Fatal("merge dropped Display.ShowAidaSuffix=true")
	}
	if err := SaveUserConfig(on); err != nil {
		t.Fatalf("save: %v", err)
	}
	ClearUserConfigCache()
	reloaded, err := LoadUserConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Display.ShowAidaSuffix {
		t.Fatal("show_aida_suffix did not survive save + reload")
	}

	off, err := MergePanelConfigOntoDisk(&UserConfig{Display: DisplaySettings{ShowAidaSuffix: false}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if off.Display.ShowAidaSuffix {
		t.Fatal("merge failed to propagate ShowAidaSuffix=false (toggle stuck on)")
	}
}

func TestPatch38_ShowAidaSuffixDefaultsFalse(t *testing.T) {
	if (DisplaySettings{}).ShowAidaSuffix {
		t.Fatal("show_aida_suffix must default to false (suffix hidden)")
	}
}

func TestPatch38_MergeKeepsCollapsedSectionsFromDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	base, err := LoadUserConfig()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	disk := *base
	disk.Preview.CollapsedSections = []string{"analytics", "output"}
	if err := SaveUserConfig(&disk); err != nil {
		t.Fatalf("save: %v", err)
	}
	ClearUserConfigCache()

	// A panel config knows nothing about collapsed_sections.
	merged, err := MergePanelConfigOntoDisk(&UserConfig{Preview: PreviewSettings{NotesOutputSplit: 0.4}})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if want := []string{"analytics", "output"}; !reflect.DeepEqual(merged.Preview.CollapsedSections, want) {
		t.Fatalf("collapsed_sections after panel merge = %v, want %v", merged.Preview.CollapsedSections, want)
	}
	if err := SaveUserConfig(merged); err != nil {
		t.Fatalf("save merged: %v", err)
	}
	ClearUserConfigCache()
	reloaded, err := LoadUserConfig()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if want := []string{"analytics", "output"}; !reflect.DeepEqual(reloaded.Preview.CollapsedSections, want) {
		t.Fatalf("collapsed_sections after save+reload = %v, want %v", reloaded.Preview.CollapsedSections, want)
	}
}
