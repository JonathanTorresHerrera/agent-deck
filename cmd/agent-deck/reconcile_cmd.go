package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

type reconcileCounts struct {
	Total                   int                    `json:"total"`
	LiveByStatus            map[session.Status]int `json:"live_by_status"`
	Archived                int                    `json:"archived"`
	ArchivedTmuxAlive       int                    `json:"archived_but_tmux_alive"`
	KilledWithoutSessionEnd int                    `json:"killed_without_session_end"`
	StaleHooks              int                    `json:"stale_hooks"`
	OrphanTmux              int                    `json:"orphan_tmux_sessions"`
}

type reconcileCandidate struct {
	profile      string
	inst         *session.Instance
	missingEnd   bool
	staleHook    bool
	archivedLive bool
}

type reconcilePlan struct {
	counts     reconcileCounts
	candidates []reconcileCandidate
	orchans    []reconcileOrphan
}

type reconcileOrphan struct{ socket, name string }

type reconcileHook struct {
	Status, Event, SessionID, Generation string
	Timestamp                            int64
	Sequence                             uint64
}

func reconcileTerminalEvent(event string) bool {
	switch strings.ToLower(strings.ReplaceAll(event, "_", "")) {
	case "sessionend", "sessionshutdown", "onsessionfinalize":
		return true
	}
	return false
}

func classifyReconcile(rows map[string][]*session.Instance, live map[string][]string, hooks map[string]reconcileHook) reconcilePlan {
	plan := reconcilePlan{counts: reconcileCounts{LiveByStatus: make(map[session.Status]int)}}
	owned := make(map[string]bool)
	profiles := make([]string, 0, len(rows))
	for p := range rows {
		profiles = append(profiles, p)
	}
	sort.Strings(profiles)
	for _, profile := range profiles {
		for _, inst := range rows[profile] {
			plan.counts.Total++
			if inst.IsArchived() {
				plan.counts.Archived++
			}
			pane := inst.GetTmuxSession()
			if pane == nil {
				continue
			}
			socket := inst.TmuxSocketName
			key := socket + "\x00" + pane.Name
			owned[key] = true
			alive := false
			for _, name := range live[socket] {
				if name == pane.Name {
					alive = true
					break
				}
			}
			if alive {
				plan.counts.LiveByStatus[inst.Status]++
			}
			hook, hasHook := hooks[inst.ID]
			hasHook = hasHook && hook.Event != "" && hook.Status != ""
			terminalCurrent := hasHook && reconcileTerminalEvent(hook.Event) &&
				(inst.LastStartedAt.IsZero() || !time.Unix(hook.Timestamp, 0).Before(inst.LastStartedAt.Add(-time.Second)))
			// A missing hook file is ambiguous: the native end hook may have
			// run and its record been cleaned up. Replay only when a
			// nonterminal record proves this generation was tracked.
			missingEnd := !alive && hasHook && !terminalCurrent
			stale := !alive && hasHook && hook.Status != "dead" && !reconcileTerminalEvent(hook.Event)
			archivedLive := alive && inst.IsArchived()
			if missingEnd {
				plan.counts.KilledWithoutSessionEnd++
			}
			if stale {
				plan.counts.StaleHooks++
			}
			if archivedLive {
				plan.counts.ArchivedTmuxAlive++
			}
			if missingEnd || stale || archivedLive || (!alive && inst.Status != session.StatusStopped) {
				plan.candidates = append(plan.candidates, reconcileCandidate{profile, inst, missingEnd, stale, archivedLive})
			}
		}
	}
	for socket, names := range live {
		for _, name := range names {
			if !owned[socket+"\x00"+name] {
				plan.orchans = append(plan.orchans, reconcileOrphan{socket, name})
			}
		}
	}
	sort.Slice(plan.orchans, func(i, j int) bool {
		return plan.orchans[i].socket+plan.orchans[i].name < plan.orchans[j].socket+plan.orchans[j].name
	})
	plan.counts.OrphanTmux = len(plan.orchans)
	return plan
}

func reconcileHookPath(id string) string {
	path := filepath.Join(session.GetHooksDir(), "sandbox", id, id+".json")
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		path = filepath.Join(session.GetHooksDir(), id+".json")
	}
	return path
}

func readReconcileHook(id string) reconcileHook {
	path := reconcileHookPath(id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return reconcileHook{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return reconcileHook{}
	}
	var h reconcileHook
	var raw struct {
		Status     string `json:"status"`
		Event      string `json:"event"`
		SessionID  string `json:"session_id"`
		Timestamp  int64  `json:"ts"`
		Generation string `json:"hook_generation"`
		Sequence   uint64 `json:"sequence"`
	}
	if json.Unmarshal(data, &raw) == nil {
		h = reconcileHook{raw.Status, raw.Event, raw.SessionID, raw.Generation, raw.Timestamp, raw.Sequence}
	}
	return h
}

func handleReconcile(args []string) {
	fs := flag.NewFlagSet("reconcile", flag.ExitOnError)
	apply := fs.Bool("apply", false, "Replay missing end hooks, clear stale state, and kill orphan tmux sessions")
	dryRun := fs.Bool("dry-run", false, "Read-only report (the default)")
	jsonOutput := fs.Bool("json", false, "Output counts as JSON")
	details := fs.Bool("details", false, "Also list candidate session IDs and orphan tmux names")
	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}
	if *apply && *dryRun {
		fmt.Fprintln(os.Stderr, "--apply and --dry-run are mutually exclusive")
		os.Exit(1)
	}
	profiles, err := session.ListProfiles()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	rows := make(map[string][]*session.Instance)
	stores := make(map[string]*session.Storage)
	sockets := map[string]bool{tmux.DefaultSocketName(): true}
	for _, profile := range profiles {
		store, instances, _, loadErr := loadSessionData(profile)
		if loadErr != nil {
			fmt.Fprintln(os.Stderr, loadErr)
			os.Exit(1)
		}
		stores[profile], rows[profile] = store, instances
		for _, inst := range instances {
			sockets[inst.TmuxSocketName] = true
		}
	}
	defer func() {
		for _, store := range stores {
			_ = store.Close()
		}
	}()
	live := make(map[string][]string)
	for socket := range sockets {
		names, listErr := tmux.ListAgentDeckSessionsOnSocket(socket)
		if listErr != nil {
			fmt.Fprintln(os.Stderr, listErr)
			os.Exit(1)
		}
		live[socket] = names
	}
	hooks := make(map[string]reconcileHook)
	for _, instances := range rows {
		for _, inst := range instances {
			if hook := readReconcileHook(inst.ID); hook.Event != "" && hook.Status != "" {
				hooks[inst.ID] = hook
			}
		}
	}
	plan := classifyReconcile(rows, live, hooks)
	if *jsonOutput {
		var report any = plan.counts
		if *details {
			candidates := make([]map[string]any, 0, len(plan.candidates))
			for _, candidate := range plan.candidates {
				candidates = append(candidates, map[string]any{"profile": candidate.profile, "id": candidate.inst.ID, "title": candidate.inst.Title, "missing_end": candidate.missingEnd, "stale_hook": candidate.staleHook, "archived_live": candidate.archivedLive})
			}
			orphans := make([]map[string]string, 0, len(plan.orchans))
			for _, orphan := range plan.orchans {
				orphans = append(orphans, map[string]string{"socket": orphan.socket, "name": orphan.name})
			}
			report = map[string]any{"counts": plan.counts, "candidates": candidates, "orphans": orphans}
		}
		data, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("total=%d archived=%d archived_but_tmux_alive=%d killed_without_session_end=%d stale_hooks=%d orphan_tmux_sessions=%d\n", plan.counts.Total, plan.counts.Archived, plan.counts.ArchivedTmuxAlive, plan.counts.KilledWithoutSessionEnd, plan.counts.StaleHooks, plan.counts.OrphanTmux)
		for status, count := range plan.counts.LiveByStatus {
			fmt.Printf("live_%s=%d\n", status, count)
		}
		if *details {
			for _, candidate := range plan.candidates {
				fmt.Printf("candidate profile=%s id=%s title=%q missing_end=%t stale_hook=%t archived_live=%t\n", candidate.profile, candidate.inst.ID, candidate.inst.Title, candidate.missingEnd, candidate.staleHook, candidate.archivedLive)
			}
			for _, orphan := range plan.orchans {
				fmt.Printf("orphan socket=%q name=%s\n", orphan.socket, orphan.name)
			}
		}
	}
	if !*apply {
		return
	}
	changed := make(map[string]bool)
	for _, candidate := range plan.candidates {
		inst := candidate.inst
		if candidate.archivedLive {
			if _, err := inst.StopWithOptions(session.StopOptions{Force: true}); err != nil {
				fmt.Fprintln(os.Stderr, err)
				continue
			}
		}
		if inst.Exists() {
			continue
		}
		if candidate.missingEnd {
			if err := replayMissedEndHooks(inst, hooks[inst.ID]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				continue
			}
		}
		inst.ClearHookStatus()
		inst.Status = session.StatusStopped
		// The terminal record makes the repaired row visibly dead to every
		// hook reader, and makes the next dry-run idempotent.
		terminal := hookStatusFile{
			Status: "dead", Event: "SessionEnd", SessionID: hooks[inst.ID].SessionID,
			Timestamp: time.Now().Unix(), HookGeneration: hooks[inst.ID].Generation,
			Sequence: hooks[inst.ID].Sequence + 1,
		}
		if data, err := json.Marshal(terminal); err == nil {
			if err := atomicHookWrite(reconcileHookPath(inst.ID), data); err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
		}
		changed[candidate.profile] = true
	}
	for profile := range changed {
		_, instances, groups, loadErr := loadSessionData(profile)
		if loadErr != nil {
			fmt.Fprintln(os.Stderr, loadErr)
			continue
		}
		byID := make(map[string]*session.Instance)
		for _, candidate := range plan.candidates {
			if candidate.profile == profile {
				byID[candidate.inst.ID] = candidate.inst
			}
		}
		for _, inst := range instances {
			if updated := byID[inst.ID]; updated != nil && !updated.Exists() {
				inst.Status = session.StatusStopped
			}
		}
		if err := saveSessionData(stores[profile], instances, groups); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}
	ownedNow, err := freshReconcileOwnership()
	if err != nil {
		fmt.Fprintf(os.Stderr, "skip orphan cleanup: %v\n", err)
		return
	}
	for _, orphan := range plan.orchans {
		if ownedNow[orphan.socket+"\x00"+orphan.name] {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := tmux.ExecContext(ctx, orphan.socket, "kill-session", "-t", orphan.name).Run()
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "orphan %s: %v\n", orphan.name, err)
		}
	}
}

// Re-read the registry immediately before the destructive orphan pass. A
// newly registered session must never be killed using the earlier dry-run
// snapshot's classification.
func freshReconcileOwnership() (map[string]bool, error) {
	profiles, err := session.ListProfiles()
	if err != nil {
		return nil, err
	}
	owned := make(map[string]bool)
	for _, profile := range profiles {
		store, err := session.NewStorageWithProfile(profile)
		if err != nil {
			return nil, err
		}
		instances, loadErr := store.Load()
		_ = store.Close()
		if loadErr != nil {
			return nil, loadErr
		}
		for _, inst := range instances {
			if pane := inst.GetTmuxSession(); pane != nil {
				owned[inst.TmuxSocketName+"\x00"+pane.Name] = true
			}
		}
	}
	return owned, nil
}

// A create-only marker is written BEFORE invoking user hooks. This gives
// at-most-once delivery even if a hook or the reaper crashes mid-run.
func claimReconcileHook(id string, started time.Time) (bool, error) {
	if !validInstanceID.MatchString(id) {
		return false, errors.New("invalid instance id")
	}
	dir := filepath.Join(session.GetHooksDir(), "reconciled")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return false, err
	}
	key := id
	if !started.IsZero() {
		key += fmt.Sprintf("-%d", started.UnixNano())
	}
	f, err := os.OpenFile(filepath.Join(dir, key+".claimed"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if os.IsExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, f.Close()
}

func replayMissedEndHooks(inst *session.Instance, prior reconcileHook) error {
	claimed, err := claimReconcileHook(inst.ID, inst.LastStartedAt)
	if err != nil || !claimed {
		return err
	}
	configDir := ""
	if session.IsClaudeCompatible(inst.Tool) {
		configDir = session.GetClaudeConfigDirForInstance(inst)
	}
	if inst.Tool == "gemini" {
		configDir = session.GetGeminiConfigDir()
	}
	if configDir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct{ Type, Command string }
		}
	}
	if len(data) > 0 && json.Unmarshal(data, &settings) != nil {
		return errors.New("invalid hook settings")
	}
	events := []string{"SessionEnd"}
	if session.IsClaudeCompatible(inst.Tool) && strings.ToLower(prior.Event) != "stop" {
		events = []string{"Stop", "SessionEnd"}
	}
	for _, event := range events {
		payload, _ := json.Marshal(map[string]string{"hook_event_name": event, "session_id": prior.SessionID, "cwd": inst.ProjectPath, "reason": "agent-deck-reconcile"})
		for _, group := range settings.Hooks[event] {
			for _, hook := range group.Hooks {
				if hook.Type != "command" || hook.Command == "" {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				cmd := exec.CommandContext(ctx, "sh", "-c", hook.Command)
				cmd.Stdin = bytes.NewReader(payload)
				cmd.Env = append(os.Environ(), "AGENTDECK_INSTANCE_ID="+inst.ID)
				if info, statErr := os.Stat(inst.ProjectPath); statErr == nil && info.IsDir() {
					cmd.Dir = inst.ProjectPath
				}
				output, runErr := cmd.CombinedOutput()
				cancel()
				if runErr != nil {
					return fmt.Errorf("replayed %s hook for %s: %w (%s)", event, inst.ID, runErr, strings.TrimSpace(string(output)))
				}
			}
		}
	}
	return nil
}
