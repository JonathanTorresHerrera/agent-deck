package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

func TestEditSessionDialogAccountPickerGoldenNarrowWidth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	dir1 := filepath.Join(home, "c1")
	dir2 := filepath.Join(home, "c2")
	for _, d := range []string{dir1, dir2} {
		requireDir(t, d)
	}
	cfg := &session.UserConfig{Profiles: map[string]session.ProfileSettings{
		"claude-1": {Label: "Seat one", Claude: session.ProfileClaudeSettings{ConfigDir: dir1}},
		"claude-2": {Label: "Seat two", Claude: session.ProfileClaudeSettings{ConfigDir: dir2}},
	}}
	if err := session.SaveUserConfig(cfg); err != nil {
		t.Fatal(err)
	}

	d := NewEditSessionDialog()
	d.SetSize(70, 28)
	d.Show(session.NewInstanceWithTool("narrow", home, "claude"))
	idx := accountFieldIndex(t, d)
	d.focusIndex = idx

	view := stripAnsi(d.View())
	if !strings.Contains(view, "Account for selected harness") {
		t.Fatalf("missing account header in narrow view:\n%s", view)
	}
	for _, want := range []string{"default", "Seat one", "Seat two"} {
		if !strings.Contains(view, want) {
			t.Fatalf("narrow view missing %q:\n%s", want, view)
		}
	}
	lines := strings.Split(view, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Seat one") || strings.Contains(line, "Seat two") {
			if strings.Count(line, "▶") > 1 {
				t.Fatalf("multiple highlights on one line: %q", line)
			}
		}
	}
}

func requireDir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}
