package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountSlotPickerLabelPrefersConfiguredLabel(t *testing.T) {
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{
		"claude-c0e6cb2b": {Label: "Work laptop", Claude: ProfileClaudeSettings{ConfigDir: "/tmp/x"}},
	}}
	require.Equal(t, "Work laptop", AccountSlotPickerLabel(cfg, "claude", "claude-c0e6cb2b"))
}

func TestAccountSlotPickerLabelFallsBackToEmail(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"aida@example.com"}}`), 0o600))
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{
		"claude-1": {Claude: ProfileClaudeSettings{ConfigDir: dir}},
	}}
	require.Equal(t, "aida@example.com", AccountSlotPickerLabel(cfg, "claude", "claude-1"))
}

func TestRegisterAccountProviderSlotStoresFriendlyLabel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	dir := filepath.Join(home, ".claude-new")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{}}
	name, err := RegisterAccountProviderSlot(cfg, "claude", "", dir, "Aida personal")
	require.NoError(t, err)
	require.NotEmpty(t, name)
	loaded, err := LoadUserConfig()
	require.NoError(t, err)
	require.Equal(t, "Aida personal", loaded.Profiles[name].Label)
	require.Equal(t, dir, loaded.Profiles[name].Claude.ConfigDir)
}
