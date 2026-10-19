package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func switchRotationRollout(t *testing.T, home, id, cwd string, started time.Time) string {
	t.Helper()
	path := writeRollout(t, home, id, cwd, "", "", started)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	body = []byte(strings.Replace(string(body), "2026-09-23T07:59:36.742Z", started.UTC().Format(time.RFC3339Nano), 1))
	require.NoError(t, os.WriteFile(path, body, 0600))
	return path
}

func TestCodexSwitchRotationRefreshesCachedIdentity(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	noPaneProcess(t)
	now := time.Now()
	cwd := t.TempDir()
	old, current := "01a14c0c-85c6-7310-b63a-3e450ab5c59a", "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"
	switchRotationRollout(t, filepath.Join(home, "codex"), old, cwd, now.Add(-48*time.Hour))
	switchRotationRollout(t, filepath.Join(home, "codex"), current, cwd, now.Add(-time.Minute))
	inst := &Instance{ID: "rotation", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: old, LastStartedAt: now.Add(-time.Hour)}
	inst.Status = StatusWaiting
	_ = inst.UpdateStatus()
	require.Equal(t, current, inst.CodexSessionID, "status refresh must follow rotation even without hooks or an open-file probe")
}

func TestCodexSwitchRotationPreflightUsesCurrentUserThread(t *testing.T) {
	home := withTempHome(t)
	sourceHome := filepath.Join(home, "codex")
	t.Setenv("CODEX_HOME", sourceHome)
	noPaneProcess(t)
	now := time.Now()
	cwd := t.TempDir()
	old, current := "01a14c0c-85c6-7310-b63a-3e450ab5c59a", "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"
	switchRotationRollout(t, sourceHome, old, cwd, now.Add(-48*time.Hour))
	switchRotationRollout(t, sourceHome, current, cwd, now.Add(-time.Minute))
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: filepath.Join(home, "work")}}}}
	inst := &Instance{ID: "preflight", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: old, LastStartedAt: now.Add(-time.Hour)}
	storage := newTestStorage(t)
	require.NoError(t, storage.Save([]*Instance{inst}))
	result, err := ExecuteHarnessSwitch(cfg, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, NoStart: true, Storage: storage})
	require.NoError(t, err)
	require.Equal(t, current, inst.CodexSessionID)
	require.Contains(t, result.DestinationPath, current)
	rows, err := storage.Load()
	require.NoError(t, err)
	require.Equal(t, current, rows[0].CodexSessionID)
	require.Equal(t, "work", rows[0].Account)
}

func TestCodexSwitchRotationAmbiguityRefusesBeforeInstall(t *testing.T) {
	home := withTempHome(t)
	sourceHome := filepath.Join(home, "codex")
	t.Setenv("CODEX_HOME", sourceHome)
	noPaneProcess(t)
	now := time.Now()
	cwd := t.TempDir()
	old := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	switchRotationRollout(t, sourceHome, old, cwd, now.Add(-48*time.Hour))
	for _, id := range []string{"01a15339-548f-77f1-9731-429baf710746", "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"} {
		switchRotationRollout(t, sourceHome, id, cwd, now.Add(-time.Minute))
	}
	targetHome := filepath.Join(home, "work")
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: targetHome}}}}
	inst := &Instance{ID: "ambiguous", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: old, LastStartedAt: now.Add(-time.Hour)}
	_, err := ExecuteHarnessSwitch(cfg, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, NoStart: true})
	require.ErrorContains(t, err, "ambiguous Codex session ID")
	_, err = os.Stat(filepath.Join(targetHome, "sessions"))
	require.True(t, os.IsNotExist(err), "ambiguous switch must not stage into the target")
}

func TestCodexSwitchDefaultHomeDoesNotBorrowObserverSeat(t *testing.T) {
	home := withTempHome(t)
	borrowed := filepath.Join(home, "codex-2")
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"codex-2": {Codex: ProfileCodexSettings{ConfigDir: borrowed}}}}
	require.NoError(t, SaveUserConfig(cfg))
	t.Setenv("CODEX_HOME", borrowed)
	inst := &Instance{ID: "default", Tool: "codex", Command: "codex"}
	require.Equal(t, filepath.Join(home, ".codex"), inst.ResolvedCodexHome())
	require.Contains(t, inst.buildCodexCommand("codex"), "CODEX_HOME="+filepath.Join(home, ".codex"))
}

func TestCodexSwitchIdentityConflictRestoresAndRetries(t *testing.T) {
	home := withTempHome(t)
	sourceHome := filepath.Join(home, ".codex")
	targetHome := filepath.Join(home, "work")
	t.Setenv("CODEX_HOME", sourceHome)
	noPaneProcess(t)
	now := time.Now()
	cwd := t.TempDir()
	sid := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	switchRotationRollout(t, sourceHome, sid, cwd, now.Add(-time.Minute))
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: targetHome}}}}
	require.NoError(t, SaveUserConfig(cfg))
	inst := &Instance{ID: "retry", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: sid, LastStartedAt: now.Add(-time.Hour)}
	storage := newTestStorage(t)
	require.NoError(t, storage.Save([]*Instance{inst}))
	oldRunning, oldStop, oldStart := nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart
	t.Cleanup(func() { nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart = oldRunning, oldStop, oldStart })
	nativeSwitchRunning = func(*Instance) bool { return true }
	nativeSwitchStart = func(i *Instance) error {
		require.Equal(t, sid, i.CodexSessionID)
		require.Equal(t, sourceHome, i.ResolvedCodexHome())
		return nil
	}
	// A status/identity writer advances the registry after preflight. The CAS
	// must refuse, restore the source, and remove only this operation's copy.
	nativeSwitchStop = func(*Instance) error {
		rows, err := storage.Load()
		require.NoError(t, err)
		rows[0].CodexSessionID = "01a15339-548f-77f1-9731-429baf710746"
		require.NoError(t, storage.Save(rows))
		return nil
	}
	opts := HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, NoStart: true, Storage: storage}
	_, err := ExecuteHarnessSwitch(cfg, inst, opts)
	require.ErrorContains(t, err, "identity conflict")
	require.Equal(t, sid, inst.CodexSessionID)
	require.Empty(t, inst.Account)
	require.Equal(t, sourceHome, inst.ResolvedCodexHome())
	matches, _ := filepath.Glob(filepath.Join(targetHome, "sessions", "*", "*", "*", "*.jsonl"))
	require.Empty(t, matches, "failed switch must not leave an installed rollout")
	nativeSwitchStop = func(*Instance) error { return nil }
	result, err := ExecuteHarnessSwitch(cfg, inst, opts)
	require.NoError(t, err)
	require.True(t, result.Committed)
	require.Equal(t, "work", inst.Account)
	require.Equal(t, sid, inst.CodexSessionID)
}

func TestCodexSwitchIgnoresTheIncidentSubagentThreads(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", home)
	noPaneProcess(t)
	now := time.Now()
	cwd := t.TempDir()
	sid := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	switchRotationRollout(t, home, sid, cwd, now.Add(-time.Hour))
	for _, child := range []string{"01a15339-548f-77f1-9731-429baf710746", "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"} {
		writeRollout(t, home, child, cwd, sid, "", now)
	}
	inst := &Instance{ID: "subagents", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: sid, LastStartedAt: now.Add(-2 * time.Hour)}
	inst.UpdateCodexSession(nil)
	require.Equal(t, sid, inst.CodexSessionID, "the incident's newer rollouts are child reviewers, not main-thread rotations")
}
