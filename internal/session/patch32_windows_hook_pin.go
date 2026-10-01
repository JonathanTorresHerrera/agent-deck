package session

import (
	"os"
	"regexp"
	"strings"
)

// Fork patch 32: never pin a WSL path into a Windows-side Claude config.
//
// From 1.16.11 the hook install, the TUI-start reinstall, the notify daemon's
// start-up heal and the statusLine usage feed all write this binary's
// absolute install path (~/.local/bin/agent-deck) into settings.json. On a
// WSL host whose Claude Code is the Windows build, settings.json lives on a
// Windows mount (/mnt/c/Users/<u>/.claude) and its hooks run under Git Bash,
// where /home/<u>/.local/bin does not exist. The bare `agent-deck` resolves
// to a Windows-side shim that forwards into WSL; the pinned path resolves to
// nothing. Field case 2026-10-01: a stock 1.16.22 start rewrote all seven
// hooks and two account slots' statusLines, and hook status writes stopped
// fleet-wide (24 in the 45 minutes before, 0 in the 44 after).
//
// Reporting the binary as unpinnable reuses upstream's own rule for dev
// builds: entries keep the program they already name (the bare command), and
// the heal and the usage-feed wrapper never write.

// windowsMountPath matches a WSL drvfs mount of a Windows drive.
var windowsMountPath = regexp.MustCompile(`^/mnt/[a-zA-Z](/|$)`)

// isWSLHost reports whether this process runs inside WSL. A seam for tests.
var isWSLHost = func() bool {
	data, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

// claudeConfigDirForPin is the config dir the hook entries are written to. A
// seam for tests.
var claudeConfigDirForPin = GetClaudeConfigDir

// claudeConfigOnWindowsMount reports whether this is a WSL host whose Claude
// config dir sits on a Windows mount, i.e. Claude Code runs on the Windows
// side and cannot exec a WSL path.
func claudeConfigOnWindowsMount() bool {
	return isWSLHost() && windowsMountPath.MatchString(claudeConfigDirForPin())
}
