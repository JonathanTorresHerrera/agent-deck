package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asheshgoplani/agent-deck/internal/session"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/require"
)

func TestNewDialogCodexAccountDefaultLabelsAndKeyboard(t *testing.T) {
	home := setXDGTestHome(t)
	t.Setenv("JEV_ROUTER_HOME", filepath.Join(home, "router"))
	writeXDGTestConfig(t, home, `[codex]
config_dir = "/synced-default"
[profiles.codex-1.codex]
config_dir = "/named-codex"
[profiles.codex-other.codex]
config_dir = "/other-codex"
[profiles.claude-key.claude]
config_dir = "/named-claude"
`)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "router"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "router", "accounts.json"), []byte(`{"defaultSlots":{"codex":"slot-codex","claude":"slot-claude"},"slots":[{"id":"slot-codex","provider":"codex","deckAccount":"codex-1"},{"id":"slot-claude","provider":"claude","deckAccount":"claude-key"}]}`), 0600))
	bin := filepath.Join(home, "jev-accounts")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\ncat <<'JSON'\n"+`{"meters":[{"slot":"slot-codex","displayName":"codex-1","provider":"codex","deckAccount":"codex-1","identity":{"email":"dev@example.test"}},{"slot":"slot-claude","displayName":"claude-1","provider":"claude","deckAccount":"claude-key","identity":{"email":"ops@example.test"}}]}`+"\nJSON\n"), 0700))
	t.Setenv("JEV_ACCOUNTS_BIN", bin)
	d := NewNewDialog()
	d.SetDefaultTool("codex")
	d.SetSize(80, 40)
	d.Show()
	require.Equal(t, "codex-1", d.GetCodexAccount())
	require.Equal(t, "claude-key", d.claudeOptions.GetAccount())
	require.Equal(t, []string{"codex-1", "codex-other"}, d.codexOptions.accounts)
	require.Contains(t, stripAnsi(d.codexOptions.View()), "dev@example.test")
	require.Contains(t, stripAnsi(d.claudeOptions.View()), "claude-1 · ops@example.test")
	p := d.codexOptions
	p.Focus()
	require.True(t, p.AtTop())
	require.False(t, p.AtBottom())
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	require.False(t, p.AtTop())
	require.True(t, p.AtBottom())
	require.Contains(t, stripAnsi(p.View()), "▶ Account:")
	p.Update(tea.KeyMsg{Type: tea.KeyLeft})
	require.Empty(t, d.GetCodexAccount())
	p.Update(tea.KeyMsg{Type: tea.KeyRight})
	require.Equal(t, "codex-1", d.GetCodexAccount())
	d.nameInput.SetValue("demo")
	d.pathInput.SetValue(home)
	snap := d.saveSnapshot()
	p.SetAccount("codex-other")
	d.restoreSnapshot(snap)
	require.Equal(t, "codex-1", d.GetCodexAccount())
	d.Hide()
	d.Show()
	require.Equal(t, "codex-1", d.GetCodexAccount())
	require.True(t, strings.Contains(d.View(), "Account:"))
}

func TestCodexAccountPickerAbsentRegistryInherits(t *testing.T) {
	home := setXDGTestHome(t)
	t.Setenv("JEV_ROUTER_HOME", filepath.Join(home, "absent"))
	t.Setenv("JEV_ACCOUNTS_BIN", filepath.Join(home, "absent-bin"))
	writeXDGTestConfig(t, home, `[profiles.codex-1.codex]
config_dir = "/codex-1"
`)
	d := NewNewDialog()
	d.SetDefaultTool("codex")
	d.Show()
	require.Empty(t, d.GetCodexAccount())
	require.Equal(t, []string{"codex-1"}, d.codexOptions.accounts)
	// Selecting a profile remains a profile key consumed by normal launch.
	d.codexOptions.SetAccount("codex-1")
	inst := &session.Instance{Tool: "codex", Account: d.GetCodexAccount()}
	require.Equal(t, "/codex-1", inst.ResolvedCodexHome())
}

func TestNewDialogAccountIdentityRemainsVisibleAt80x24(t *testing.T) {
	forceTrueColorProfile()
	for _, provider := range []string{"claude", "codex"} {
		t.Run(provider, func(t *testing.T) {
			d := goldenDialog(t)
			d.SetDefaultTool(provider)
			d.SetSize(80, 24)
			names := []string{"one", "two", "three", "four", "five", "six"}
			labels := []string{"inherit/default", "slot-1 · one@example.test", "slot-2 · two@example.test", "slot-3 · three@example.test", "slot-4 · four@example.test", "slot-5 · five@example.test", "slot-6 · six@example.test"}
			if provider == "claude" {
				d.claudeOptions.SetAccounts(names)
				d.claudeOptions.accountLabels = labels
				d.claudeOptions.SetAccount("six")
			} else {
				d.codexOptions.SetAccounts(names)
				d.codexOptions.accountLabels = labels
				d.codexOptions.SetAccount("six")
			}
			d.rebuildFocusTargets()
			flowFocus(t, d, focusOptions)
			d.toolOptions.FocusLast()
			view := stripAnsi(d.View())
			require.Contains(t, view, "six@example.test")
			require.Contains(t, view, "Create session")
		})
	}
}
