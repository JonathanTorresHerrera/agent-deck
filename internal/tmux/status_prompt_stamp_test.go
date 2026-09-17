// Patch 12 (C): once you are attached inside a session, agent-deck has no
// chrome left except the tmux status bar — so "when was this last worked on"
// became unanswerable at exactly the moment you are deciding whether to keep
// working on it.
//
// The stamp is an ABSOLUTE wall clock, set once at attach. Deliberately not a
// relative "3h ago" via tmux's #() interpolation: that would fork a shell on
// every status-interval tick for every attached client, and the relative half
// is worthless here anyway — once you are attached, the session's recent
// activity is you.
package tmux

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newStampTestSession(stamp string) *Session {
	s := &Session{
		Name:             "test-sess",
		DisplayName:      "my-session",
		WorkDir:          "/home/user/my-project",
		injectStatusLine: true,
	}
	s.SetLastPromptStamp(stamp)
	return s
}

func TestThemedStatusRight_IncludesPromptStampWhenSet(t *testing.T) {
	s := newStampTestSession("Sep 16 14:32")
	got := s.themedStatusRight(currentTmuxThemeStyle())

	assert.Contains(t, got, "Sep 16 14:32",
		"the attach-time prompt stamp must appear in status-right")
	assert.Contains(t, got, "prompt",
		"the stamp needs a label or the bare time is ambiguous")
}

// Truncation-survival. tmux clips status-right at status-right-length, cutting
// from the RIGHT — so anything appended after the session name and project is
// the first thing to disappear on a long name. The stamp therefore sits
// immediately after the key hints, ahead of the name.
func TestThemedStatusRight_StampPrecedesSessionName(t *testing.T) {
	s := newStampTestSession("Sep 16 14:32")
	got := s.themedStatusRight(currentTmuxThemeStyle())

	stampAt := strings.Index(got, "Sep 16 14:32")
	nameAt := strings.Index(got, "my-session")
	assert.NotEqual(t, -1, stampAt, "stamp missing")
	assert.NotEqual(t, -1, nameAt, "session name missing")
	assert.Less(t, stampAt, nameAt,
		"stamp must precede the session name so tmux truncation does not eat it first")
}

// An unknown stamp renders nothing at all — no label, no orphan separator.
// Every session predating this patch has no prompt record until its next
// prompt, so this is the common case on first run.
func TestThemedStatusRight_OmitsPromptStampWhenUnset(t *testing.T) {
	s := newStampTestSession("")
	got := s.themedStatusRight(currentTmuxThemeStyle())

	assert.NotContains(t, got, "prompt",
		"no prompt record must render no prompt segment")
	assert.Contains(t, got, "my-session",
		"the rest of status-right is unaffected")
	assert.NotContains(t, got, "│ │",
		"an omitted stamp must not leave a doubled separator")
}

// The stamp rides inside status-right, so a user who overrides status-right
// in [tmux].options still wins outright — agent-deck skips the whole key.
func TestBuildStatusBarArgs_UserStatusRightOverrideStillWins(t *testing.T) {
	s := newStampTestSession("Sep 16 14:32")
	s.OptionOverrides = map[string]string{"status-right": "my own bar"}

	args := s.buildStatusBarArgs()

	assert.NotContains(t, strings.Join(args, " "), "Sep 16 14:32",
		"a user-defined status-right must suppress agent-deck's stamp entirely")
}

// Setting the stamp must be safe to call repeatedly (every attach) and must
// replace rather than accumulate.
func TestSetLastPromptStamp_Replaces(t *testing.T) {
	s := newStampTestSession("Sep 15 09:00")
	s.SetLastPromptStamp("Sep 16 14:32")
	got := s.themedStatusRight(currentTmuxThemeStyle())

	assert.Contains(t, got, "Sep 16 14:32")
	assert.NotContains(t, got, "Sep 15 09:00",
		"a re-stamp must replace the previous value, not append")
}
