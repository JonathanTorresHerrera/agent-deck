package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestEditSessionDialogAccountSelectionMatchesConfirmation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", home+"/config")
	session.ClearUserConfigCache()
	t.Cleanup(session.ClearUserConfigCache)

	claude1 := filepath.Join(home, "claude-1")
	claude2 := filepath.Join(home, "claude-2")
	for _, d := range []string{claude1, claude2} {
		require.NoError(t, os.MkdirAll(d, 0o700))
	}
	cfg := &session.UserConfig{Profiles: map[string]session.ProfileSettings{
		"claude-1": {Label: "Claude One", Claude: session.ProfileClaudeSettings{ConfigDir: claude1}},
		"claude-2": {Label: "Claude Two", Claude: session.ProfileClaudeSettings{ConfigDir: claude2}},
	}}
	require.NoError(t, session.SaveUserConfig(cfg))

	inst := session.NewInstanceWithTool("acct-sel", home, "claude")
	const originalAccount = "claude-2"
	inst.Account = originalAccount

	homeUI := NewHome()
	homeUI.width, homeUI.height = 120, 40
	homeUI.instancesMu.Lock()
	homeUI.instances = []*session.Instance{inst}
	homeUI.instanceByID[inst.ID] = inst
	homeUI.instancesMu.Unlock()

	d := homeUI.editSessionDialog
	d.SetSize(70, 30)
	d.Show(inst)
	idx := accountFieldIndex(t, d)
	d.focusIndex = idx

	wantIDs := []string{"", "claude-1", "claude-2"}
	for _, wantID := range wantIDs {
		for attempts := 0; attempts < len(d.fields[idx].pillOptions); attempts++ {
			if d.selectedPill(session.FieldAccount) == wantID {
				break
			}
			d.Update(tea.KeyMsg{Type: tea.KeyDown})
		}
		got := d.selectedPill(session.FieldAccount)
		if got != wantID {
			t.Fatalf("selected account id = %q, want %q", got, wantID)
		}
		summary := d.switchSummary()
		if !strings.Contains(summary, session.AccountSlotSummaryLine(cfg, "claude", wantID)) {
			t.Fatalf("summary for %q missing label line:\n%s", wantID, summary)
		}
		if wantID == originalAccount || wantID == "" {
			continue
		}
		_, cmd := homeUI.handleEditSessionDialogKey(tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil || !homeUI.confirmDialog.IsVisible() {
			t.Fatalf("changing to %q must open confirmation", wantID)
		}
		if homeUI.confirmDialog.TargetAccount() != wantID {
			t.Fatalf("confirmation account id = %q, want %q", homeUI.confirmDialog.TargetAccount(), wantID)
		}
		homeUI.handleConfirmDialogKey(tea.KeyMsg{Type: tea.KeyEsc})
	}
}

func TestRenderAccountPickerListNarrowWidthNoMidEntryWrap(t *testing.T) {
	labels := []string{
		"default",
		"claude-1 · Aida@very-long-email-address.example.com",
		"claude-2 · Bob@example.com",
	}
	const width = 70
	view := renderAccountPickerList(labels, 1, width)
	for _, line := range strings.Split(view, "\n") {
		plain := stripAnsi(line)
		require.True(t, utf8.ValidString(line))
		require.LessOrEqual(t, cellWidth(line), width+2, "line too wide: %q", plain)
		require.False(t, strings.HasSuffix(strings.TrimSpace(plain), "claude-"), "wrapped mid-entry: %q", plain)
	}
	require.Contains(t, stripAnsi(view), "▶")
}
