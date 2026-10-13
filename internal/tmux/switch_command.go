package tmux

import (
	"errors"
	"strings"
	"time"
)

// TypeCommand submits one agent slash command after allowing its command
// palette to settle. The caller must first verify an empty, idle composer.
func (s *Session) TypeCommand(command string) error {
	return s.TypeCommandChecked(command, nil)
}

// SendClaudeExitKeys sends Claude's double Ctrl+D exit gesture in one tmux
// command. Claude requires both keys within a short confirmation window;
// separate tmux subprocesses can miss that window under WSL load.
func (s *Session) SendClaudeExitKeys() error {
	s.invalidateCache()
	return runSendKeysBounded(keySenderExec(s.SocketName, "send-keys", "-t", s.Name, "C-d", "C-d"))
}

// TypeCommandChecked gives the caller a final pane check after the command is
// typed but before Enter can submit it. On a refused check, clear the command
// only when a fresh capture still shows exactly the injected draft.
func (s *Session) TypeCommandChecked(command string, beforeEnter func(string) error, matchesInjected ...func(string) bool) error {
	if command == "" || strings.ContainsAny(command, "\r\n") {
		return errors.New("TypeCommand needs a single non-empty line")
	}
	s.invalidateCache()
	s.ensureInsertModeOnTarget(s.Name)
	if err := s.sendKeysToTarget(s.Name, command); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)
	if beforeEnter != nil {
		raw, err := s.CapturePaneFresh()
		if err != nil {
			s.clearTypedCommandIfExact(matchesInjected)
			return err
		}
		if err := beforeEnter(raw); err != nil {
			s.clearTypedCommandIfExact(matchesInjected)
			return err
		}
	}
	return s.sendEnterRawToTarget(s.Name)
}

func (s *Session) clearTypedCommandIfExact(matchesInjected []func(string) bool) {
	if len(matchesInjected) == 0 || matchesInjected[0] == nil {
		return
	}
	if current, err := s.CapturePaneFresh(); err == nil && matchesInjected[0](current) {
		_ = s.SendCtrlU()
	}
}

// ForgetCachedExistence makes a deliberate agent exit visible to teardown.
func (s *Session) ForgetCachedExistence() {
	sessionCacheMu.Lock()
	delete(sessionCacheData, s.Name)
	sessionCacheMu.Unlock()
	s.invalidateCache()
}
