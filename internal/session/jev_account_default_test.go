package session

import (
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestJevDefaultAccountUsesProviderSlotMapping(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("JEV_ROUTER_HOME", filepath.Join(home, "router"))
	require.Empty(t, JevDefaultAccount("codex"))
	require.NoError(t, os.MkdirAll(filepath.Join(home, "router"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "router", "accounts.json"), []byte(`{"defaultSlots":{"codex":"logical-codex","claude":"logical-claude"},"slots":[{"id":"logical-codex","provider":"codex","deckAccount":"codex-1"},{"id":"logical-claude","provider":"claude","deckAccount":"claude-key"}]}`), 0600))
	require.Equal(t, "codex-1", JevDefaultAccount("codex"))
	require.Equal(t, "claude-key", JevDefaultAccount("claude"))
	require.Empty(t, JevDefaultAccount("gemini"))
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"codex-1": {Codex: ProfileCodexSettings{ConfigDir: "/tmp/codex-1"}}, "claude-key": {Claude: ProfileClaudeSettings{ConfigDir: "/tmp/claude"}}}}
	require.Equal(t, []string{"codex-1"}, ConfiguredCodexAccountNames(cfg))
}
