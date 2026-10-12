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

// TypeCommandChecked gives the caller a final pane check after the command is
// typed but before Enter can submit it.
func (s *Session) TypeCommandChecked(command string, beforeEnter func(string) error) error {
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
			return err
		}
		if err := beforeEnter(raw); err != nil {
			return err
		}
	}
	return s.sendEnterRawToTarget(s.Name)
}

// ForgetCachedExistence makes a deliberate agent exit visible to teardown.
func (s *Session) ForgetCachedExistence() {
	sessionCacheMu.Lock()
	delete(sessionCacheData, s.Name)
	sessionCacheMu.Unlock()
	s.invalidateCache()
}
