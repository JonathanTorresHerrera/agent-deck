package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func readSwitchAttempts(t *testing.T, home string) []SwitchAttempt {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".agent-deck", "switch-attempts.jsonl"))
	require.NoError(t, err)
	var out []SwitchAttempt
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var a SwitchAttempt
		require.NoError(t, json.Unmarshal([]byte(line), &a))
		out = append(out, a)
	}
	return out
}
func TestSwitchAttemptLogsEarlyFailuresAndRetriesExactlyOnce(t *testing.T) {
	home := withTempHome(t)
	inst := &Instance{ID: "receipt", Title: "iOS Ops Dev", Tool: "codex", Account: "source", Command: "codex"}
	for i := 0; i < 2; i++ {
		_, err := ExecuteHarnessSwitch(nil, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "unknown"}, Trigger: "tui"})
		require.Error(t, err)
		require.NotEmpty(t, SwitchAttemptID(err))
		require.Contains(t, err.Error(), "Account switch failed (log ")
	}
	rows := readSwitchAttempts(t, home)
	require.Len(t, rows, 2)
	require.NotEqual(t, rows[0].ID, rows[1].ID)
	for _, r := range rows {
		require.Equal(t, "receipt", r.SessionID)
		require.Equal(t, "iOS Ops Dev", r.Title)
		require.Equal(t, "codex", r.Tool)
		require.Equal(t, "source", r.From)
		require.Equal(t, "unknown", r.To)
		require.Equal(t, "tui", r.Trigger)
		require.Equal(t, "failed", r.Outcome)
		require.NotEmpty(t, r.Step)
		require.NotEmpty(t, r.Error)
		require.False(t, r.Timestamp.IsZero())
	}
}
func TestSwitchAttemptLogsQueuedSourceLockFailure(t *testing.T) {
	home := withTempHome(t)
	inst := &Instance{ID: "contended", Tool: "codex"}
	lock, err := acquireHarnessSwitchSourceLock(inst.ID)
	require.NoError(t, err)
	defer lock.Release()
	done := make(chan error, 1)
	go func() { _, callErr := ExecuteHarnessSwitch(nil, inst, HarnessSwitchOptions{}); done <- callErr }()
	select {
	case <-done:
		t.Fatal("source lock was bypassed")
	case <-time.After(30 * time.Millisecond):
	}
	lock.Release()
	select {
	case err = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("queued switch did not complete")
	}
	require.Error(t, err)
	rows := readSwitchAttempts(t, home)
	require.Len(t, rows, 1)
	require.Equal(t, rows[0].ID, SwitchAttemptID(err))
}
func TestSwitchAttemptCompatibilityFailureRetainsSentinel(t *testing.T) {
	home := withTempHome(t)
	inst := &Instance{ID: "compat", Tool: "claude"}
	_, err := SwitchAccount(&UserConfig{}, inst, "unknown", AccountSwitchOptions{Trigger: "rotator"})
	require.ErrorIs(t, err, ErrUnknownAccount)
	require.Len(t, readSwitchAttempts(t, home), 1)
	require.Equal(t, "rotator", readSwitchAttempts(t, home)[0].Trigger)
}
func TestSwitchAttemptExternalOwnerDoesNotDuplicateReceipt(t *testing.T) {
	home := withTempHome(t)
	inst := &Instance{ID: "external", Tool: "codex"}
	_, err := ExecuteHarnessSwitch(nil, inst, HarnessSwitchOptions{AttemptID: "mcp-attempt", Trigger: "mcp"})
	require.Equal(t, "mcp-attempt", SwitchAttemptID(err))
	_, statErr := os.Stat(filepath.Join(home, ".agent-deck", "switch-attempts.jsonl"))
	require.True(t, errors.Is(statErr, os.ErrNotExist))
}

func TestSwitchAttemptCompatibilityRestartFailureRetainsID(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	noPaneProcess(t)
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: filepath.Join(home, "work")}}}}
	inst := &Instance{ID: "compat-restart", Tool: "codex", Command: "codex", ProjectPath: t.TempDir()}
	oldRunning, oldStop, oldStart := nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart
	t.Cleanup(func() { nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart = oldRunning, oldStop, oldStart })
	nativeSwitchRunning = func(*Instance) bool { return true }
	nativeSwitchStop = func(*Instance) error { return nil }
	nativeSwitchStart = func(i *Instance) error {
		if i.Account == "work" {
			return errors.New("target launch failed")
		}
		return nil
	}
	_, err := SwitchAccount(cfg, inst, "work", AccountSwitchOptions{})
	require.ErrorIs(t, err, ErrAccountSwitchRestartFailed)
	rows := readSwitchAttempts(t, home)
	require.Len(t, rows, 1)
	require.Equal(t, rows[0].ID, SwitchAttemptID(err))
	require.NotEmpty(t, SwitchAttemptID(err))
}
