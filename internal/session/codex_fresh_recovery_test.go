package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFreshCodexSwitchEmptyAndHistoryBranches(t *testing.T) {
	for _, kind := range []string{"empty", "subagent-only", "main-history", "archived-history", "unreadable"} {
		t.Run(kind, func(t *testing.T) {
			home := withTempHome(t)
			source := filepath.Join(home, "codex")
			target := filepath.Join(home, "work")
			t.Setenv("CODEX_HOME", source)
			noPaneProcess(t)
			cwd := t.TempDir()
			now := time.Now()
			switch kind {
			case "subagent-only":
				writeRolloutSource(t, source, "01a15339-548f-77f1-9731-429baf710746", cwd, "", `{"subagent":{"thread_spawn":{"parent_thread_id":"01a14c0c-85c6-7310-b63a-3e450ab5c59a"}}}`, "", now)
			case "main-history":
				writeRollout(t, source, "01a14c0c-85c6-7310-b63a-3e450ab5c59a", cwd, "", "", now.Add(-365*24*time.Hour))
			case "archived-history":
				p := writeRollout(t, source, "01a14c0c-85c6-7310-b63a-3e450ab5c59a", cwd, "", "", now.Add(-365*24*time.Hour))
				archive := filepath.Join(source, "archived_sessions")
				require.NoError(t, os.MkdirAll(archive, 0700))
				require.NoError(t, os.Rename(p, filepath.Join(archive, filepath.Base(p))))
			case "unreadable":
				p := writeRollout(t, source, "01a14c0c-85c6-7310-b63a-3e450ab5c59a", cwd, "", "", now)
				require.NoError(t, os.WriteFile(p, []byte("incomplete"), 0600))
			}
			cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: target}}}}
			inst := &Instance{ID: "fresh-" + kind, Title: "iOS Ops Dev", Tool: "codex", Command: "codex", ProjectPath: cwd}
			store := newTestStorage(t)
			require.NoError(t, store.Save([]*Instance{inst}))
			oldRunning, oldStop, oldStart := nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart
			t.Cleanup(func() { nativeSwitchRunning, nativeSwitchStop, nativeSwitchStart = oldRunning, oldStop, oldStart })
			starts, stops := 0, 0
			nativeSwitchRunning = func(*Instance) bool { return true }
			nativeSwitchStop = func(*Instance) error { stops++; return nil }
			nativeSwitchStart = func(i *Instance) error {
				starts++
				require.Equal(t, "work", i.Account)
				require.Empty(t, i.CodexSessionID)
				return nil
			}
			result, err := ExecuteHarnessSwitch(cfg, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, Storage: store, Trigger: "tui"})
			if kind == "main-history" || kind == "archived-history" || kind == "unreadable" {
				require.Error(t, err)
				require.Equal(t, 0, stops)
				require.Equal(t, 0, starts)
				require.Empty(t, inst.Account)
				return
			}
			require.NoError(t, err)
			require.True(t, result.Committed)
			require.True(t, result.Restarted)
			require.Equal(t, "fresh session, nothing migrated", result.Conversation)
			require.Equal(t, 1, starts)
			require.Equal(t, 1, stops)
			require.Empty(t, result.DestinationPath)
			rows, err := store.Load()
			require.NoError(t, err)
			require.Equal(t, "work", rows[0].Account)
		})
	}
}

func TestCodexRecoveryNeverLaunchesBlankOverRecordedHistory(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", home)
	cwd := t.TempDir()
	now := time.Now()
	main := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	child := "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"
	writeRollout(t, home, main, cwd, "", "", now.Add(-365*24*time.Hour))
	writeRolloutSource(t, home, child, cwd, "", `{"subagent":{"thread_spawn":{"parent_thread_id":"`+main+`"}}}`, "", now)
	inst := &Instance{ID: "recovery", Tool: "codex", Command: "codex", ProjectPath: cwd, LastStartedAt: now.Add(-time.Hour)}
	require.ErrorContains(t, inst.guardCodexRecovery(true), "without an exact main conversation ID")
	require.Empty(t, inst.CodexSessionID)
	WriteHookSessionAnchor(inst.ID, main)
	require.NoError(t, inst.guardCodexRecovery(true))
	require.Equal(t, main, inst.CodexSessionID)
	require.Contains(t, inst.buildCodexCommand("codex"), "resume "+main)
	inst.CodexSessionID = child
	require.ErrorContains(t, inst.guardCodexRecovery(true), "not a verified main thread")
	inst.CodexSessionID = "01a154eb-0000-7000-8000-000000000000"
	require.ErrorContains(t, inst.Start(), "recorded conversation")
	require.Equal(t, "01a154eb-0000-7000-8000-000000000000", inst.CodexSessionID)
}

func TestCodexNewestResolversExcludeNestedSubagentSource(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", home)
	noPaneProcess(t)
	cwd := t.TempDir()
	now := time.Now()
	main := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	child := "01a15413-dbc2-7cd1-ba3b-7e3d425a8b0c"
	mainPath := writeRollout(t, home, main, cwd, "", "", now.Add(-time.Minute))
	childPath := writeRolloutSource(t, home, child, cwd, "", `{"subagent":{"thread_spawn":{"parent_thread_id":"`+main+`"}}}`, "", now)
	require.Equal(t, "subagent", readCodexRolloutThreadMeta(childPath).ThreadSource)
	codexThreadMetaCache.Delete(child)
	require.True(t, CodexSubagentThread(child, home))
	inst := &Instance{ID: "nested", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: main, CreatedAt: now.Add(-time.Hour)}
	require.Equal(t, main, inst.queryCodexSession(nil, false))
	require.Equal(t, mainPath, CodexLiveRolloutPath(inst, nil))
	candidates := codexUserRolloutsForCwd(home, cwd, inst.CreatedAt, nil)
	require.Len(t, candidates, 1)
	require.Equal(t, main, candidates[0].id)
	stubCodexPaneOpenPaths(t, []string{mainPath, childPath}, nil)
	id, err := resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, main, id)
	require.Equal(t, main, inst.LiveCodexThreadID())
	require.False(t, strings.Contains(inst.buildCodexCommand("codex"), child))
}

func TestCodexRecoveryExplicitFreshRestartRemainsAvailable(t *testing.T) {
	home := withTempHome(t)
	t.Setenv("CODEX_HOME", home)
	cwd := t.TempDir()
	main := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	writeRollout(t, home, main, cwd, "", "", time.Now())
	inst := &Instance{ID: "deliberate-fresh", Tool: "codex", Command: "codex", ProjectPath: cwd, CodexSessionID: main}
	WriteHookSessionAnchor(inst.ID, main)
	inst.clearSessionBindingForFreshStart()
	require.Empty(t, ReadHookSessionAnchor(inst.ID))
	require.Empty(t, inst.CodexSessionID)
	require.ErrorContains(t, inst.guardCodexRecovery(false), "without an exact main conversation ID")
	inst.codexFreshStart = true
	require.NoError(t, inst.guardCodexRecovery(false))
	require.Empty(t, inst.CodexSessionID)
}

func TestFreshCodexSwitchChecksTheLiveSourceHome(t *testing.T) {
	home := withTempHome(t)
	configured := filepath.Join(home, "configured")
	actual := filepath.Join(home, "actual")
	t.Setenv("CODEX_HOME", configured)
	cwd := t.TempDir()
	id := "01a14c0c-85c6-7310-b63a-3e450ab5c59a"
	p := writeRollout(t, actual, id, cwd, "", "", time.Now())
	stubCodexPaneOpenPaths(t, []string{p}, nil)
	cfg := &UserConfig{Profiles: map[string]ProfileSettings{"work": {Codex: ProfileCodexSettings{ConfigDir: filepath.Join(home, "work")}}}}
	inst := &Instance{ID: "live-home-history", Tool: "codex", Command: "codex", ProjectPath: cwd}
	result, err := ExecuteHarnessSwitch(cfg, inst, HarnessSwitchOptions{Target: SwitchPreviewTarget{Harness: "codex", Account: "work"}, NoStart: true})
	require.NoError(t, err)
	require.False(t, result.FreshSession, "a live main rollout must migrate rather than restart blank")
	require.Equal(t, id, inst.CodexSessionID)
	require.Equal(t, "work", inst.Account)
	require.FileExists(t, result.DestinationPath)
	require.Contains(t, result.DestinationPath, filepath.Join(home, "work", "sessions"))
}
