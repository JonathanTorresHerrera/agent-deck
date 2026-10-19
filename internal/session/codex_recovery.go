package session

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// codexHasMainHistory checks all dates, not just the recent discovery window.
// Failure to read a rollout is uncertainty, never evidence of an empty session.
func codexHasMainHistory(inst *Instance) (bool, error) {
	if !inst.CodexRolloutIsResolvableLocally() {
		return false, fmt.Errorf("Codex history is not locally resolvable")
	}
	// A process-owned rollout can prove the active source home differs from
	// the configured/default seat. Never prove freshness in the observer's home.
	sourceHome := inst.ResolvedCodexHome()
	for _, dir := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(sourceHome, dir)
		found := false
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			head, ok := readCodexRolloutHead(path)
			if !ok {
				return fmt.Errorf("cannot verify Codex rollout metadata: %s", path)
			}
			if !head.Subagent && (head.Cwd == "" || canonicalDir(head.Cwd) == canonicalDir(inst.EffectiveWorkingDir())) {
				found = true
				return filepath.SkipAll
			}
			return nil
		})
		if err != nil || found {
			return found, err
		}
	}
	return false, nil
}

// guardCodexRecovery runs before any process is replaced. A restart may resume
// the recorded main thread, or an exact instance sidecar; cwd/mtime alone is
// never authority to adopt another conversation or silently launch blank.
func (i *Instance) guardCodexRecovery(restarting bool) error {
	if !IsCodexCompatible(i.Tool) || !i.CodexRolloutIsResolvableLocally() {
		return nil
	}
	id := i.CodexSessionID
	if id == "" {
		id = ReadHookSessionAnchor(i.ID)
	}
	if id != "" {
		if err := validateExactSessionID(id); err != nil {
			return fmt.Errorf("refusing Codex recovery: invalid recorded conversation ID: %w", err)
		}
		p := codexRolloutPathInHome(id, i.getCodexHomeDir())
		if p == "" {
			return fmt.Errorf("refusing Codex recovery: recorded conversation %s is unavailable in CODEX_HOME=%s; restore its rollout before restarting", id, i.getCodexHomeDir())
		}
		head, ok := readCodexRolloutHead(p)
		if !ok || head.Subagent || (head.Cwd != "" && canonicalDir(head.Cwd) != canonicalDir(i.EffectiveWorkingDir())) {
			return fmt.Errorf("refusing Codex recovery: recorded conversation is not a verified main thread for this session")
		}
		i.CodexSessionID = id
		return nil
	}
	if !restarting && (i.codexFreshStart || (i.addedThisProcess && i.LastStartedAt.IsZero() && i.CodexDetectedAt.IsZero())) {
		return nil
	}
	history, err := codexHasMainHistory(i)
	if err != nil {
		return fmt.Errorf("refusing Codex recovery: %w", err)
	}
	if history || !i.CodexDetectedAt.IsZero() {
		return fmt.Errorf("refusing Codex recovery without an exact main conversation ID; history exists and a blank launch would lose continuity")
	}
	return nil
}

func executeFreshCodexSwitch(inst *Instance, preview *SwitchPreview, opts HarnessSwitchOptions, path string, prior *switchJournal) (*HarnessSwitchResult, error) {
	if prior != nil {
		return nil, fmt.Errorf("refusing fresh Codex switch with an incomplete conversation journal")
	}
	wasRunning := nativeSwitchRunning(inst)
	source := identityForInstance(inst)
	j := &switchJournal{Version: switchJournalVersion, OperationID: switchOperationIDForRequest(inst.ID, switchRequestGeneration(source, opts.Target, opts.NoStart)), State: switchPrepared, Source: source, Target: targetIdentityFor(inst, preview), WasRunning: wasRunning, NoStart: opts.NoStart, RequestGeneration: switchRequestGeneration(source, opts.Target, opts.NoStart)}
	j.Target.CodexHome = ExpandPath(preview.TargetAccountDir)
	if preview.TargetAccount == "" {
		j.Target.CodexHome = CodexHomeDirForInstance(defaultSwitchTargetInstance(inst, preview.TargetHarness))
	}
	if j.Target.CodexHome == "" {
		return nil, fmt.Errorf("target Codex home is unavailable")
	}
	if opts.progress != nil {
		j.OperationID = opts.progress.OperationID
		opts.progress.JournalPath = path
	}
	if err := harnessSwitchJournalWrite(path, j); err != nil {
		return nil, err
	}
	if wasRunning {
		setSwitchStep(opts, "stopping")
		if err := nativeSwitchStop(inst); err != nil {
			return switchFailedOnStop(path, j, inst, wasRunning, err)
		}
		// A first turn may have flushed while we waited for the graceful stop.
		history, err := codexHasMainHistory(inst)
		if err != nil || history || inst.CodexSessionID != "" {
			return switchFailedAfterStop(path, j, inst, wasRunning, fmt.Errorf("source gained history while stopping; retry with its exact Codex conversation ID"))
		}
	}
	setSwitchStep(opts, "switching_account")
	acknowledged, err := commitNativeSwitchAccount(path, j, inst, preview.TargetAccount, wasRunning, opts.Storage)
	if err != nil {
		return nil, err
	}
	result := nativeHarnessSwitchResult(&HarnessSwitchResult{Preview: preview, OldTool: inst.Tool, NewTool: inst.Tool, OldAccount: source.Account, NewAccount: inst.Account, Continuity: "native", Conversation: "fresh session, nothing migrated", FreshSession: true, Committed: true}, j.Source, j.Target)
	result.nativeJournalPath, result.nativeStorageAcknowledgement = path, acknowledged
	if wasRunning && !opts.NoStart {
		inst.codexFreshStart = true
		defer func() { inst.codexFreshStart = false }()
		setSwitchStep(opts, "resuming")
		if err := startNativeSwitchInstance(inst); err != nil {
			result.Committed = false
			restoreSwitchIdentity(inst, source, source.Command)
			rollback := revertNativeSwitchStorage(opts.Storage, inst, acknowledged, j.Target, source)
			if rollback == nil {
				rollback = startNativeSwitchInstance(inst)
			}
			j.State, j.Failure = switchFailed, err.Error()
			_ = writeSwitchJournal(path, j)
			if rollback != nil {
				return result, fmt.Errorf("%w; source rollback: %v", err, rollback)
			}
			return result, err
		}
		result.Restarted = true
	}
	return result, nil
}
