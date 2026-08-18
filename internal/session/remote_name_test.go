package session

import (
	"os"
	"path/filepath"
	"testing"
)

func writeCmdline(t *testing.T, args ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cmdline")
	var raw []byte
	for _, a := range args {
		raw = append(raw, []byte(a)...)
		raw = append(raw, 0)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestClaudeNameFlagFromCmdline(t *testing.T) {
	path := writeCmdline(t,
		"/init", "/mnt/c/Users/x/.local/bin/claude.exe", "/mnt/c/Users/x/.local/bin/claude.exe",
		"--resume", "026545af-3bc1", "--dangerously-skip-permissions",
		"--name", "Sergio Mata 2", "--chrome")
	if got := claudeNameFlagFromCmdline(path); got != "Sergio Mata 2" {
		t.Fatalf("expected name from --name, got %q", got)
	}
}

func TestClaudeNameFlagFromCmdline_ShortFlag(t *testing.T) {
	path := writeCmdline(t, "claude", "-n", "Bug Tickets", "--resume", "x")
	if got := claudeNameFlagFromCmdline(path); got != "Bug Tickets" {
		t.Fatalf("expected name from -n, got %q", got)
	}
}

func TestClaudeNameFlagFromCmdline_AbsentOrUnreadable(t *testing.T) {
	path := writeCmdline(t, "claude", "--resume", "x", "--chrome")
	if got := claudeNameFlagFromCmdline(path); got != "" {
		t.Fatalf("expected empty for absent flag, got %q", got)
	}
	if got := claudeNameFlagFromCmdline(filepath.Join(t.TempDir(), "missing")); got != "" {
		t.Fatalf("expected empty for unreadable path, got %q", got)
	}
	// trailing --name with no value must not panic or invent a name
	path = writeCmdline(t, "claude", "--name")
	if got := claudeNameFlagFromCmdline(path); got != "" {
		t.Fatalf("expected empty for valueless --name, got %q", got)
	}
}
