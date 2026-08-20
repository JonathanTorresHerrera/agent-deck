package session

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// remoteNameProbeTTL bounds how often RemoteSessionName re-reads the live
// pane. The preview panel calls it on every render of the selected session;
// the probe (one tmux call + one /proc read) only runs when the cache is
// this stale.
const remoteNameProbeTTL = 30 * time.Second

// RemoteSessionName returns the --name value the live claude pane was
// spawned with — i.e. the display name claude.ai's remote-control session
// list shows for it — or "" when the pane is gone or was spawned without
// --name (the remote list then shows an auto-generated hostname-petname).
// buildClaudeExtraFlags emits --name from the deck title on every
// start/restart/resume, so "" effectively means "pane predates the --name
// patch; the title takes over on the next restart".
//
// Only plausibly-live sessions are probed, and the probe is silent: a dead
// pane has no registered name, and probing it from the render path shells
// out to tmux just to fail — readPanePID's failure WARN then lands on
// stderr underneath the TUI's alt screen and ghosts rows across the session
// tree (2026-08-19 regression: five "Global" rows after navigating over
// stopped sessions).
func (i *Instance) RemoteSessionName() string {
	i.remoteNameMu.Lock()
	defer i.remoteNameMu.Unlock()
	if time.Since(i.remoteNameAt) < remoteNameProbeTTL {
		return i.remoteNameVal
	}
	i.remoteNameAt = time.Now()
	i.remoteNameVal = ""

	switch i.GetStatusThreadSafe() {
	case StatusRunning, StatusWaiting, StatusIdle:
		// pane plausibly alive — worth one probe
	default:
		return ""
	}
	if i.IsArchived() {
		return ""
	}
	sess := i.GetTmuxSession()
	if sess == nil {
		return ""
	}
	// Same probe as readPanePID, minus its WARN: this path races pane death
	// by design (status is a cached snapshot), so a failure here is routine,
	// not reportable.
	out, err := tmux.OutputBounded(i.TmuxSocketName, "list-panes", "-t", sess.Name+":", "-F", "#{pane_pid}")
	if err != nil {
		return ""
	}
	pidStr := strings.TrimSpace(string(out))
	if idx := strings.IndexByte(pidStr, '\n'); idx >= 0 {
		pidStr = pidStr[:idx]
	}
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return ""
	}
	i.remoteNameVal = claudeNameFlagFromCmdline(fmt.Sprintf("/proc/%d/cmdline", pid))
	return i.remoteNameVal
}

// claudeNameFlagFromCmdline extracts the value following --name/-n from a
// /proc/<pid>/cmdline file (NUL-separated argv). The pane process is the
// already-tokenized claude invocation, so no shell unquoting is needed.
// Returns "" on any read failure or when the flag is absent.
func claudeNameFlagFromCmdline(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	for idx, a := range args {
		if (a == "--name" || a == "-n") && idx+1 < len(args) {
			return args[idx+1]
		}
	}
	return ""
}
