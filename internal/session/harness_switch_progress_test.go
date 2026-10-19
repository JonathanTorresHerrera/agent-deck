package session

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHarnessSwitchFailureCarriesDurableCopyableProgress(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	noPaneProcess(t)
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: filepath.Join(home, "work")}}}}
	inst := &Instance{ID: "progress", Title: "Aida fixture", Tool: "codex", Command: "codex", ProjectPath: t.TempDir()}
	_, err := ExecuteHarnessSwitch(cfg, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, NoStart: true})
	require.Error(t, err)
	var failure *HarnessSwitchFailure
	require.True(t, errors.As(err, &failure))
	p := failure.Progress
	require.Len(t, p.Steps, 6)
	require.Equal(t, "failed", p.Status)
	require.Equal(t, "resolve_session_id", p.FailedStep)
	require.NotEmpty(t, p.OperationID)
	require.NotEmpty(t, p.LatestLogEntry)
	require.FileExists(t, p.LogPath)
	require.FileExists(t, p.ProgressPath)
	saved, readErr := ReadHarnessSwitchProgress(inst.ID)
	require.NoError(t, readErr)
	require.Equal(t, p.OperationID, saved.OperationID)
	for _, want := range []string{p.OperationID, "Resolving session ID", p.LogPath, p.ProgressPath} {
		require.True(t, strings.Contains(p.Text(), want))
	}
}

func TestResumedCodexVerificationRequiresTargetHomeProcessEvidence(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", home)
	const sid = "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	cwd := t.TempDir()
	path := switchRotationRollout(t, home, sid, cwd, time.Now())
	inst := &Instance{ID: "verify", Tool: "codex", Account: "work", ProjectPath: cwd, CodexSessionID: sid, runtimeCodexHome: home}
	stubCodexPaneOpenPaths(t, []string{path}, nil)
	require.True(t, observeNativeCodexResume(inst, sid, home))
	stubCodexPaneOpenPaths(t, []string{filepath.Join(t.TempDir(), filepath.Base(path))}, nil)
	require.False(t, observeNativeCodexResume(inst, sid, home), "a matching ID open in another account home is not target evidence")
}

func TestMCPProgressTracksPhasesBeforeAndAfterDeckLifecycle(t *testing.T) {
	withTempHome(t)
	inst := &Instance{ID: "mcp-progress", Title: "fixture", Tool: "codex", Command: "codex", ProjectPath: t.TempDir()}
	p, err := RecordHarnessSwitchProgress(inst, "mcp-operation", "codex-4", "waiting_for_idle", "running", "")
	require.NoError(t, err)
	require.Equal(t, "running", p.Status)
	p, err = RecordHarnessSwitchProgress(inst, "mcp-operation", "codex-4", "stopping", "running", "")
	require.NoError(t, err)
	require.Equal(t, "completed", p.Steps[0].Status)
	inst.Account = "codex-4"
	p, err = RecordHarnessSwitchProgress(inst, "mcp-operation", "codex-4", "verifying", "success", "")
	require.NoError(t, err)
	require.Equal(t, "success", p.Status)
	require.Equal(t, "codex-4", p.FinalAccount)
	require.Equal(t, "mcp-operation", p.OperationID)
}
