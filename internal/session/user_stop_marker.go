package session

import (
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// User-stop markers record EXPLICIT teardown intent — Kill/KillAndWait, i.e.
// the TUI kill dialog, web kill actions, `session remove`, worktree finish.
// The reboot-recovery script (recover-running.sh) consults them to decide
// which snapshot sessions it must NOT resurrect. The DB status column cannot
// carry this signal: the vanished-pane sweep also persists StatusStopped
// (predates-boot classification), and treating that as user intent would make
// recovery skip the very fleet it exists to restore (2026-08-16 incident).
// One file per instance, named by instance ID; any (re)start clears it.

// userStopMarkerDir returns <data>/runtime/user-stopped, falling back to a
// temp path when the data dir cannot be resolved (mirrors spawnFailureDir).
func userStopMarkerDir() string {
	path, err := runtimeDataPath("user-stopped")
	if err != nil {
		return tempAgentDeckPath("runtime", "user-stopped")
	}
	return path
}

// markUserStopped persists the marker. Best-effort: a failure to write must
// never block or fail the teardown path.
func markUserStopped(instanceID string) {
	if instanceID == "" {
		return
	}
	dir := userStopMarkerDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	payload := strconv.FormatInt(time.Now().Unix(), 10) + "\n"
	_ = os.WriteFile(filepath.Join(dir, instanceID), []byte(payload), 0o644)
}

// clearUserStopMarker removes the marker. Called from every start path so a
// session the user revives is recoverable again.
func clearUserStopMarker(instanceID string) {
	if instanceID == "" {
		return
	}
	_ = os.Remove(filepath.Join(userStopMarkerDir(), instanceID))
}
