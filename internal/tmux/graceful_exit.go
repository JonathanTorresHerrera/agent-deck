package tmux

import (
	"errors"
	"strings"
	"time"
)

// typeCommandEnterDelay separates a typed command from its Enter. Agent TUIs
// (Ink, ratatui) coalesce a keystroke burst into a paste, and an Enter inside
// that burst lands as a newline in the composer instead of submitting it.
// SendKeysAndEnter uses 100ms for arbitrary payloads; a slash command also has
// to let the command palette open and settle on its match, so give it longer.
var typeCommandEnterDelay = 250 * time.Millisecond

// TypeCommand types a single-line command (e.g. "/exit") into the agent's
// composer and submits it. It is the primitive behind `session stop
// --graceful`: the agent must receive its own exit command, not a signal,
// because a SIGHUP/SIGTERM never reaches a Windows-native agent running
// behind WSL interop, so its SessionEnd hooks would not run.
//
// Vim-mode composers are put in insert mode first, exactly as SendKeysAndEnter
// does, so the command is typed as text and not read as a motion.
func (s *Session) TypeCommand(command string) error {
	if command == "" || strings.ContainsAny(command, "\r\n") {
		return errors.New("TypeCommand needs a single non-empty line")
	}
	s.invalidateCache()
	s.ensureInsertModeOnTarget(s.Name)
	if err := s.sendKeysToTarget(s.Name, command); err != nil {
		return err
	}
	time.Sleep(typeCommandEnterDelay)
	return s.sendEnterRawToTarget(s.Name)
}

// ForgetCachedExistence drops this session from the shared liveness cache.
// Exists() trusts a positive cache hit for sessionCacheTTL, and the CLI warms
// that cache while loading sessions. An agent that exits on its own inside
// that window would otherwise make the teardown's "already gone?" re-probe
// answer "still exists", turning a clean graceful stop into a failed one.
func (s *Session) ForgetCachedExistence() {
	sessionCacheMu.Lock()
	delete(sessionCacheData, s.Name)
	sessionCacheMu.Unlock()
	s.invalidateCache()
}

// PanePID returns the PID of the session's pane process from a live, bounded
// probe. Agent sessions exec the agent as the pane process, so this PID
// exiting is the agent exiting.
func (s *Session) PanePID() (int, error) {
	out, err := s.runBoundedOutput("list-panes", "-t", s.Name+":", "-F", "#{pane_pid}")
	return parsePanePID(out, err)
}
