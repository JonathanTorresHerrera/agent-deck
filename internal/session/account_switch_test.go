package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const twoAccountConfig = `
[profiles.work.claude]
config_dir = "~/.claude-work"

[profiles.personal.claude]
config_dir = "~/.claude-personal"
`

// SwitchAccount is the single implementation behind the CLI's
// `session switch-account` and the TUI's account row. An unknown slot must
// abort with nothing committed, and the message must name the slots that DO
// exist — otherwise the operator has to go read config.toml to find out.
func TestSwitchAccountRejectsUnknownSlot(t *testing.T) {
	withTempAgentDeckHome(t, twoAccountConfig)
	cfg, err := LoadUserConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	inst := NewInstanceWithTool("unknown-slot", t.TempDir(), "claude")
	result, switchErr := SwitchAccount(cfg, inst, "nope", AccountSwitchOptions{})
	if result != nil {
		t.Fatal("an unknown slot must commit nothing: result must be nil so the caller does not persist")
	}
	if !errors.Is(switchErr, ErrUnknownAccount) {
		t.Fatalf("error must wrap ErrUnknownAccount, got: %v", switchErr)
	}
	for _, want := range []string{"work", "personal"} {
		if !strings.Contains(switchErr.Error(), want) {
			t.Errorf("error must list the configured accounts (missing %q): %v", want, switchErr)
		}
	}
	if inst.Account != "" {
		t.Errorf("account field must be untouched, got %q", inst.Account)
	}
}

// Clearing a named slot must carry the exact Claude conversation into the
// inherited/default home. A plain account-field edit used to restart with an
// empty conversation under the same session ID.
func TestSwitchAccountNamedToDefaultPreservesConversation(t *testing.T) {
	home := withTempAgentDeckHome(t, twoAccountConfig)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cfg, err := LoadUserConfig()
	require.NoError(t, err)
	project := filepath.Join(home, "project")
	const sid = "11111111-2222-3333-4444-555555555555"
	source := writeAccountTranscript(t, home, "work", project, sid,
		`{"sessionId":"`+sid+`","type":"user","timestamp":"2026-10-12T13:00:00.000Z","message":"original context"}`+"\n")
	sourceSidecar := filepath.Join(filepath.Dir(source), sid)
	require.NoError(t, os.MkdirAll(sourceSidecar, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(sourceSidecar, "context.txt"), []byte("companion context"), 0o600))

	inst := &Instance{ID: "named-to-default", Title: "source", ProjectPath: project, Tool: "claude", Command: "claude", Account: "work", ClaudeSessionID: sid, Status: StatusStopped, CreatedAt: time.Now()}
	preview := PreviewSwitch(cfg, inst, SwitchPreviewTarget{Harness: "claude", Account: ""})
	require.Nil(t, preview.Refusal)
	require.Equal(t, filepath.Join(home, ".claude"), preview.TargetAccountDir)

	oldRunning, oldStop, oldStart := nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart
	nativeSwitchRunning = func(*Instance) bool { return false }
	nativeSwitchStop = func(*Instance) error { return nil }
	nativeSwitchStart = func(*Instance) error { return nil }
	t.Cleanup(func() { nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart = oldRunning, oldStop, oldStart })
	result, err := SwitchAccount(cfg, inst, "", AccountSwitchOptions{NoRestart: true})
	require.NoError(t, err)
	require.Equal(t, "", inst.Account)
	require.Equal(t, "work", result.OldAccount)
	require.Equal(t, "", result.NewAccount)
	dest := filepath.Join(home, ".claude", "projects", ConvertToClaudeDirName(project), sid+".jsonl")
	sourceBytes, err := os.ReadFile(source)
	require.NoError(t, err)
	destBytes, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, sourceBytes, destBytes)
	sidecar, err := os.ReadFile(filepath.Join(filepath.Dir(dest), sid, "context.txt"))
	require.NoError(t, err)
	require.Equal(t, "companion context", string(sidecar))
}

func TestSwitchAccountCodexNamedToDefaultPreservesRollout(t *testing.T) {
	home := withTempAgentDeckHome(t, `
[profiles.work.codex]
config_dir = "~/.codex-work"
`)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("CODEX_HOME", "")
	cfg, err := LoadUserConfig()
	require.NoError(t, err)
	const sid = "11111111-2222-3333-4444-555555555555"
	sourceHome := filepath.Join(home, ".codex-work")
	seedCodex155Rollout(t, sourceHome, sid, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	inst := &Instance{ID: "codex-named-to-default", Title: "source", ProjectPath: filepath.Join(home, "project"),
		Tool: "codex", Command: "codex", Account: "work", CodexSessionID: sid, Status: StatusStopped, CreatedAt: time.Now()}
	preview := PreviewSwitch(cfg, inst, SwitchPreviewTarget{Harness: "codex", Account: ""})
	require.Nil(t, preview.Refusal)
	require.Equal(t, filepath.Join(home, ".codex"), preview.TargetAccountDir)

	oldRunning, oldStop, oldStart := nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart
	nativeSwitchRunning = func(*Instance) bool { return false }
	nativeSwitchStop = func(*Instance) error { return nil }
	nativeSwitchStart = func(*Instance) error { return nil }
	t.Cleanup(func() { nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart = oldRunning, oldStop, oldStart })
	result, err := SwitchAccount(cfg, inst, "", AccountSwitchOptions{NoRestart: true})
	require.NoError(t, err)
	require.Equal(t, "", inst.Account)
	require.Equal(t, "work", result.OldAccount)
	require.Equal(t, "", result.NewAccount)
	sourcePath := codexRolloutPathInHome(sid, sourceHome)
	destPath := codexRolloutPathInHome(sid, filepath.Join(home, ".codex"))
	sourceBytes, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	destBytes, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, sourceBytes, destBytes)
}

// Tools without an account-switch executor must still be refused. Codex is
// intentionally excluded: it now has a supported configured-account path.
func TestSwitchAccountRejectsUnsupportedTool(t *testing.T) {
	withTempAgentDeckHome(t, twoAccountConfig)
	cfg, _ := LoadUserConfig()

	inst := NewInstanceWithTool("shell-session", t.TempDir(), "shell")
	result, switchErr := SwitchAccount(cfg, inst, "work", AccountSwitchOptions{})
	if result != nil {
		t.Fatal("an unsupported-tool session must commit nothing")
	}
	if !errors.Is(switchErr, ErrAccountSwitchUnsupported) {
		t.Fatalf("error must wrap ErrAccountSwitchUnsupported, got: %v", switchErr)
	}
}

// Codex named accounts are supported when the selected target has its own
// configured CODEX_HOME. This no-op path verifies the compatibility adapter
// recognizes the actual Codex target configuration instead of treating every
// non-Claude tool as unsupported.
func TestSwitchAccountSupportsConfiguredCodexTarget(t *testing.T) {
	withTempAgentDeckHome(t, `
[profiles.work.codex]
config_dir = "~/.codex-work"
`)
	cfg, _ := LoadUserConfig()
	inst := NewInstanceWithTool("codex-session", t.TempDir(), "codex")
	inst.Account = "work"

	result, switchErr := SwitchAccount(cfg, inst, "work", AccountSwitchOptions{})
	if switchErr != nil || result == nil || result.NewAccount != "work" || result.Restarted {
		t.Fatalf("configured Codex no-op = result=%#v err=%v", result, switchErr)
	}
	if result.Conversation == "" || !strings.Contains(result.Conversation, "no-op") {
		t.Fatalf("Codex no-op summary = %q", result.Conversation)
	}
}

// A session that has never held a conversation is the common case for the
// TUI picker (switch first, work later). It must commit the account field and
// report that there was nothing to migrate — not fail.
func TestSwitchAccountFreshSessionCommitsSlot(t *testing.T) {
	home := withTempAgentDeckHome(t, twoAccountConfig)
	cfg, _ := LoadUserConfig()

	inst := NewInstanceWithTool("fresh", t.TempDir(), "claude")
	result, switchErr := SwitchAccount(cfg, inst, "work", AccountSwitchOptions{})
	if switchErr != nil {
		t.Fatalf("fresh session switch must succeed, got: %v", switchErr)
	}
	if result.NewAccount != "work" || result.OldAccount != "" {
		t.Errorf("result accounts = %q -> %q, want \"\" -> \"work\"", result.OldAccount, result.NewAccount)
	}
	if inst.Account != "work" {
		t.Errorf("instance account = %q, want \"work\"", inst.Account)
	}
	if result.Restarted {
		t.Error("a session that was not running must not be started by a switch")
	}
	if !strings.Contains(result.Conversation, "no conversation") || !strings.Contains(result.Conversation, "readiness was not asserted") {
		t.Errorf("conversation summary = %q, want fresh-session/no-readiness disclosure", result.Conversation)
	}

	// The resolver must now route this session at the target account's dir.
	if got := GetClaudeConfigDirForInstance(inst); got != filepath.Join(home, ".claude-work") {
		t.Errorf("resolved config dir = %q, want the work account dir", got)
	}
}

// ConfiguredAccountNames is what both the CLI error text and the TUI pickers
// enumerate. A profile without a Claude config_dir is not a Claude account.
func TestConfiguredAccountNamesOnlyListsClaudeSlots(t *testing.T) {
	withTempAgentDeckHome(t, `
[profiles.work.claude]
config_dir = "~/.claude-work"

[profiles.personal.claude]
config_dir = "~/.claude-personal"

[profiles.codexonly.codex]
config_dir = "~/.codex-only"
`)
	cfg, _ := LoadUserConfig()

	got := ConfiguredAccountNames(cfg)
	want := []string{"personal", "work"}
	if len(got) != len(want) {
		t.Fatalf("names = %v, want %v (sorted, claude slots only)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("names = %v, want %v", got, want)
		}
	}
	if ConfiguredAccountNames(nil) != nil {
		t.Error("a nil config must yield no accounts, not panic")
	}
}
