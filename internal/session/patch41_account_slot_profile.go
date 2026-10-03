package session

import (
	"path/filepath"
	"strings"
)

// Patch 41 (account-slot CLAUDE_CONFIG_DIR): see ResolveProfileForStorage.
// Every deck command run from a session on a Jev rotation slot carries
// CLAUDE_CONFIG_DIR=/mnt/c/Users/<u>/.aida-accounts/claude/claude-3, which the
// generic last-segment inference reads as profile "3" and then warned about on
// stderr on every call. A dir that is a configured account slot is recognised
// here so the fallback to the default profile is silent.

// isConfiguredAccountSlotDir reports whether dir is the config_dir of any
// configured account slot ([profiles.<slot>.claude] or [profiles.<slot>.codex]).
// Comparison is on a normalised form so the WSL (/mnt/c/...) and Windows
// (C:\... or C:/...) spellings of the same folder match.
func isConfiguredAccountSlotDir(dir string) bool {
	if strings.TrimSpace(dir) == "" {
		return false
	}
	cfg, err := LoadUserConfig()
	if err != nil || cfg == nil {
		return false
	}
	want := normalizeSlotDir(ExpandPath(dir))
	for name := range cfg.Profiles {
		for _, d := range []string{cfg.GetProfileClaudeConfigDir(name), cfg.GetProfileCodexConfigDir(name)} {
			if d != "" && normalizeSlotDir(d) == want {
				return true
			}
		}
	}
	return false
}

// normalizeSlotDir maps a folder path to a comparable form: forward slashes,
// a Windows drive ("C:/x") as its WSL mount ("/mnt/c/x"), no trailing slash,
// lower case (Windows-side folders are case-insensitive).
func normalizeSlotDir(p string) string {
	p = strings.ReplaceAll(strings.TrimSpace(p), `\`, "/")
	if len(p) >= 2 && p[1] == ':' {
		p = "/mnt/" + strings.ToLower(p[:1]) + p[2:]
	}
	p = filepath.ToSlash(filepath.Clean(p))
	return strings.ToLower(strings.TrimRight(p, "/"))
}
