package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// resolveCurrentCodexIdentity never ranks unrelated conversations by mtime.
// A live process-owned user thread wins; otherwise exactly one newer user
// rollout in this launch's cwd/home may advance the cached binding. Subagents,
// exec/title threads, peer-owned IDs and copied historical files cannot bind.
func resolveCurrentCodexIdentity(inst *Instance, excluded map[string]bool) (string, error) {
	if inst == nil || !inst.CodexRolloutIsResolvableLocally() {
		return "", fmt.Errorf("Codex identity is not locally resolvable")
	}
	home := inst.getCodexHomeDir()
	if liveHome := codexProcessHome(inst); liveHome != "" {
		home = liveHome
	}
	cwd := canonicalDir(inst.EffectiveWorkingDir())
	valid := func(path string) bool {
		head, ok := readCodexRolloutHead(path)
		return ok && head.UserThread && !excluded[head.ID] && canonicalDir(head.Cwd) == cwd && validateExactSessionID(head.ID) == nil
	}
	if live := inst.LiveCodexThreadID(); live != "" && !excluded[live] {
		if path := codexRolloutPathInHome(live, home); path != "" && valid(path) {
			return live, nil
		}
	}
	since := inst.LastStartedAt
	if since.IsZero() {
		since = inst.CreatedAt
	}
	// Without a launch boundary, a cold legacy record can only retain its exact
	// existing ID. It cannot adopt a conversation from an unscoped cwd scan.
	var candidates []string
	if !since.IsZero() {
		for _, c := range codexUserRolloutsForCwd(home, cwd, since, excluded) {
			if c.id == inst.CodexSessionID || !valid(c.path) {
				continue
			}
			started := codexRolloutStartedAt(c.path)
			if started.IsZero() || started.Before(since) {
				continue
			}
			candidates = append(candidates, c.id)
		}
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("ambiguous Codex session ID: %d user rollouts started after %s in %s (CODEX_HOME=%s); refusing to guess", len(candidates), since.Format(time.RFC3339), cwd, home)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if inst.CodexSessionID != "" {
		path := codexRolloutPathInHome(inst.CodexSessionID, home)
		if path != "" {
			head, ok := readCodexRolloutHead(path)
			// Legacy exact-ID artifacts may omit cwd/source; an explicit subagent is
			// always rejected, including when it was previously cached.
			if ok && !head.Subagent && (head.Cwd == "" || canonicalDir(head.Cwd) == cwd) && !excluded[inst.CodexSessionID] {
				return inst.CodexSessionID, nil
			}
		}
	}
	return "", fmt.Errorf("no exact Codex user session ID in CODEX_HOME=%s for %s; hooks may be missing or awaiting trust review", home, cwd)
}

func codexRolloutStartedAt(path string) time.Time {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 4096), 4<<20)
	if !sc.Scan() {
		return time.Time{}
	}
	var head struct {
		Timestamp string `json:"timestamp"`
		Payload   struct {
			Timestamp string `json:"timestamp"`
		} `json:"payload"`
	}
	if json.Unmarshal(sc.Bytes(), &head) != nil {
		return time.Time{}
	}
	stamp := head.Payload.Timestamp
	if stamp == "" {
		stamp = head.Timestamp
	}
	t, _ := time.Parse(time.RFC3339Nano, stamp)
	return t
}

// A reporting CLI inherits the caller's seat. That environment must not
// silently turn an account="" instance into a different configured account.
func (i *Instance) defaultCodexHome() string {
	ambient := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if i != nil && i.Account == "" && ambient != "" {
		if cfg, err := LoadUserConfig(); err == nil && cfg != nil {
			for name := range cfg.Profiles {
				if dir := cfg.GetProfileCodexConfigDir(name); dir != "" && canonicalDir(dir) == canonicalDir(ambient) {
					if cfg.Codex.ConfigDir != "" {
						return ExpandPath(cfg.Codex.ConfigDir)
					}
					home, _ := os.UserHomeDir()
					return filepath.Join(home, ".codex")
				}
			}
		}
	}
	return getCodexHomeDir()
}

// reconcileCodexSwitchBinding reads the durable source before resolving. Its
// scoped CAS repairs only the native ID, so a stale caller cannot overwrite an
// account/path/command change and the subsequent account CAS binds the ID that
// was actually exported. Later concurrent changes still fail that account CAS.
func reconcileCodexSwitchBinding(inst *Instance, storage *Storage) error {
	var source switchIdentity
	var excluded map[string]bool
	if storage != nil {
		rows, err := storage.Load()
		if err != nil {
			return err
		}
		excluded = codexThreadsOwnedByPeers(inst, rows)
		found := false
		for _, row := range rows {
			if row.ID == inst.ID {
				if row.Tool != inst.Tool || row.Account != inst.Account || row.Command != inst.Command || canonicalDir(row.ProjectPath) != canonicalDir(inst.ProjectPath) {
					return fmt.Errorf("Codex switch source changed in registry; reload before retrying")
				}
				source = identityForInstance(row)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("Codex switch source was deleted from registry")
		}
	}
	if home := codexProcessHome(inst); home != "" {
		inst.runtimeCodexHome = home
	}
	id, err := resolveCurrentCodexIdentity(inst, excluded)
	if err != nil {
		return err
	}
	target := identityForInstance(inst)
	target.CodexID, target.SessionID = id, id
	if storage != nil {
		repair := nativeHarnessSwitchResult(&HarnessSwitchResult{Committed: true}, source, target)
		if err := storage.CommitNativeHarnessSwitch(inst, repair); err != nil {
			return fmt.Errorf("persist refreshed Codex identity: %w", err)
		}
	}
	inst.CodexSessionID = id
	return nil
}

// Only undo bytes this operation installed and that no destination process has
// changed. Preserve a pre-existing identical file or any newly written events.
func cleanupFailedCodexInstall(j *switchJournal) error {
	if j == nil || !IsCodexCompatible(j.Source.Tool) || j.Destination == "" || (!j.DestinationCreated && j.DestinationArchived == "") {
		return nil
	}
	hash, err := sha256File(j.Destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if hash != j.SourceSHA256 {
		return fmt.Errorf("destination advanced; preserving %s", j.Destination)
	}
	if err := os.Remove(j.Destination); err != nil {
		return err
	}
	if j.DestinationArchived != "" {
		return os.Rename(j.DestinationArchived, j.Destination)
	}
	return nil
}

// A notify entry is configuration evidence, never a claim that Codex's hook
// trust review was accepted. Trust belongs to Codex and must not be bypassed.
func codexSwitchHookWarnings(sourceHome, targetHome string) []string {
	var warnings []string
	for _, home := range []string{sourceHome, targetHome} {
		data, err := os.ReadFile(filepath.Join(home, "config.toml"))
		if err != nil || !strings.Contains(string(data), "codex-notify") {
			warnings = append(warnings, fmt.Sprintf("Codex hooks not installed in %s; run CODEX_HOME=%s agent-deck codex-hooks install, then review/trust the hook in Codex. Fresh hook verification is unavailable until then.", home, home))
		} else {
			warnings = append(warnings, fmt.Sprintf("Codex notify configured in %s; trust approval is unverified. A 'Hooks need review' dialog must be accepted in that account's Codex TUI.", home))
		}
	}
	return warnings
}

// The old pane has exited before this is called. An exact user rollout held
// open by the replacement pane in the requested home is fresh native evidence
// even when notify hooks are missing or awaiting Codex trust approval.
func observeNativeCodexResume(inst *Instance, id, home string) bool {
	paths, err := codexPaneOpenPaths(inst)
	if err != nil {
		return false
	}
	expected := codexRolloutPathInHome(id, home)
	if expected == "" {
		return false
	}
	head, ok := readCodexRolloutHead(expected)
	if !ok || head.Subagent || (head.Cwd != "" && canonicalDir(head.Cwd) != canonicalDir(inst.EffectiveWorkingDir())) {
		return false
	}
	for _, path := range paths {
		if canonicalDir(path) == canonicalDir(expected) {
			return true
		}
	}
	return false
}

// Read only native process-owned rollout paths. A tmux server's environment
// can predate the shell's CODEX_HOME= assignment and is not launch evidence.
func codexProcessHome(inst *Instance) string {
	if inst == nil || !inst.CodexRolloutIsResolvableLocally() {
		return ""
	}
	paths, err := codexPaneOpenPaths(inst)
	if err != nil {
		return ""
	}
	home := ""
	for _, path := range paths {
		if extractCodexSessionIDFromPath(path) == "" {
			continue
		}
		head, ok := readCodexRolloutHead(path)
		if !ok || !head.UserThread || canonicalDir(head.Cwd) != canonicalDir(inst.EffectiveWorkingDir()) {
			continue
		}
		sessions := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(path))))
		if filepath.Base(sessions) != "sessions" {
			continue
		}
		candidate := filepath.Dir(sessions)
		if home != "" && canonicalDir(home) != canonicalDir(candidate) {
			return ""
		}
		home = candidate
	}
	return home
}

func cachedCodexPeerIDs(inst *Instance) map[string]bool {
	if inst.tmuxSession == nil {
		return nil
	}
	codexOwnershipCache.Lock()
	snapshot := codexOwnershipCache.bySocket[inst.tmuxSession.SocketName]
	codexOwnershipCache.Unlock()
	if snapshot.claims == nil {
		return nil
	}
	snapshot.claims.Lock()
	defer snapshot.claims.Unlock()
	excluded := map[string]bool{}
	for name, id := range snapshot.claims.bySession {
		if name != inst.tmuxSession.Name {
			excluded[id] = true
		}
	}
	return excluded
}
