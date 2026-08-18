package session

import (
	"fmt"
	"os"
	"strings"
	"time"
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
func (i *Instance) RemoteSessionName() string {
	i.remoteNameMu.Lock()
	defer i.remoteNameMu.Unlock()
	if time.Since(i.remoteNameAt) < remoteNameProbeTTL {
		return i.remoteNameVal
	}
	i.remoteNameAt = time.Now()
	i.remoteNameVal = ""
	if pid := i.readPanePID(); pid > 0 {
		i.remoteNameVal = claudeNameFlagFromCmdline(fmt.Sprintf("/proc/%d/cmdline", pid))
	}
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
