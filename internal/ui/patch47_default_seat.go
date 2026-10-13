package ui

// Patch 47 (always show the seat). A row showed " @claude-3" only when the
// session had a seat stored AND the title left room for it. Sessions launched
// with no seat (Account == "") run on the tool's default login, and their
// tag was hidden, so half the list said which seat it ran on and half did not.
//
// Rows with no stored seat now show the seat they actually run on: the
// profile whose config dir the session's group resolves to, else the tool's
// default login, named the way the quota tap and Jev name it ("claude" for
// [claude] config_dir, "codex" for the default CODEX_HOME). The tag is also
// reserved before the title, which truncates with "…" instead of the tag
// disappearing; only a column too narrow for the title's floor drops it.

import (
	"path/filepath"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// defaultSeatLabel is the seat a session with no stored account runs on.
// "" when the tool has no login seat (a shell).
//
// Only the group level of the Claude config-dir chain is consulted besides the
// global default: the account level is empty by definition here, and the env
// level is the deck process's own CLAUDE_CONFIG_DIR, which says nothing about
// the login a session started earlier was given. Conductor sessions are rare
// enough that they show the default.
func defaultSeatLabel(tool, groupPath string) string {
	tool = strings.TrimSpace(tool)
	switch {
	case tool == "" || tool == "shell":
		return ""
	case session.IsClaudeCompatible(tool):
		cfg, _ := session.LoadUserConfig()
		if cfg != nil {
			if dir := cfg.GetGroupClaudeConfigDir(groupPath); dir != "" {
				return seatForClaudeDir(cfg, dir)
			}
		}
		return "claude"
	case session.IsCodexCompatible(tool):
		return "codex"
	}
	return tool
}

// seatForClaudeDir names a Claude config dir: the profile that owns it, else
// the directory's own name.
func seatForClaudeDir(cfg *session.UserConfig, dir string) string {
	clean := filepath.Clean(session.ExpandPath(dir))
	for name := range cfg.Profiles {
		if pd := cfg.GetProfileClaudeConfigDir(name); pd != "" && filepath.Clean(session.ExpandPath(pd)) == clean {
			return name
		}
	}
	return filepath.Base(clean)
}

// seatPresentation is the row tag for a session: its stored seat, or for an
// unset one the seat it runs on (when [display] show_inherited_account is on).
func seatPresentation(account, defaultSeat string, slotsConfigured bool) accountPresentation {
	if account != "" || defaultSeat == "" {
		return newAccountPresentation(account, slotsConfigured)
	}
	p := newAccountPresentation(defaultSeat, true)
	return p
}
