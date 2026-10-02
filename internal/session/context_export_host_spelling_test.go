package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A Claude session that claude.exe launched from a WSL deck is keyed by the Windows spelling of its cwd
// (D--Dev-Projects-devy), not by the distro spelling (-mnt-d-Dev-Projects-devy) the deck would compute. The
// exact transcript path must find it, or every switch-account of such a session fails (Jev rotation, 2026-10-01).
func TestCanonicalClaudeExactTranscriptPath_FindsTheHostSpelledTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	configDir := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	prev := hostSpellingsEnabled
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = prev })

	const project = "/mnt/d/Dev_Projects/devy"
	const sid = "11111111-2222-3333-4444-555555555555"
	inst := &Instance{ID: "s", Tool: "claude", ProjectPath: project, Title: "s", GroupPath: "g", ClaudeSessionID: sid}
	distro := filepath.Join(configDir, "projects", "-mnt-d-Dev-Projects-devy", sid+".jsonl")
	host := filepath.Join(configDir, "projects", "D--Dev-Projects-devy", sid+".jsonl")
	write := func(path string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"sessionId":"`+sid+`","type":"user"}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Neither transcript exists: the distro spelling is still the answer, so callers see the same error as before.
	got, err := canonicalClaudeExactTranscriptPath(inst)
	if err != nil || got != distro {
		t.Fatalf("no transcript: got %q, %v; want %q", got, err, distro)
	}

	// Only the Windows-spelled transcript exists (claude.exe wrote it): that is the exact path.
	write(host)
	got, err = canonicalClaudeExactTranscriptPath(inst)
	if err != nil || got != host {
		t.Fatalf("host transcript only: got %q, %v; want %q", got, err, host)
	}

	// Both exist: the distro spelling wins, as before this change.
	write(distro)
	got, err = canonicalClaudeExactTranscriptPath(inst)
	if err != nil || got != distro {
		t.Fatalf("both transcripts: got %q, %v; want %q", got, err, distro)
	}
}

// Outside WSL nothing changes: no host spelling is tried.
func TestCanonicalClaudeExactTranscriptPath_NoHostSpellingOutsideWSL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	configDir := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	prev := hostSpellingsEnabled
	hostSpellingsEnabled = func() bool { return false }
	t.Cleanup(func() { hostSpellingsEnabled = prev })

	const sid = "11111111-2222-3333-4444-555555555555"
	inst := &Instance{ID: "s", Tool: "claude", ProjectPath: "/mnt/d/Dev_Projects/devy", Title: "s", GroupPath: "g", ClaudeSessionID: sid}
	host := filepath.Join(configDir, "projects", "D--Dev-Projects-devy", sid+".jsonl")
	if err := os.MkdirAll(filepath.Dir(host), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(host, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalClaudeExactTranscriptPath(inst)
	want := filepath.Join(configDir, "projects", "-mnt-d-Dev-Projects-devy", sid+".jsonl")
	if err != nil || got != want {
		t.Fatalf("outside WSL: got %q, %v; want %q", got, err, want)
	}
}

// The account sync bridges the distro spelling to the host spelling with a junction (projects/-mnt-d-x -> D--x).
// A symlinked component is never an exact source, so the real host-spelled directory must win over the bridge.
func TestCanonicalClaudeExactTranscriptPath_SkipsTheBridgedSpellingForTheRealOne(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	configDir := filepath.Join(home, "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
	prev := hostSpellingsEnabled
	hostSpellingsEnabled = func() bool { return true }
	t.Cleanup(func() { hostSpellingsEnabled = prev })

	const sid = "11111111-2222-3333-4444-555555555555"
	inst := &Instance{ID: "s", Tool: "claude", ProjectPath: "/mnt/d/Dev_Projects/devy", Title: "s", GroupPath: "g", ClaudeSessionID: sid}
	hostDir := filepath.Join(configDir, "projects", "D--Dev-Projects-devy")
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostDir, sid+".jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(hostDir, filepath.Join(configDir, "projects", "-mnt-d-Dev-Projects-devy")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	got, err := canonicalClaudeExactTranscriptPath(inst)
	if err != nil || got != filepath.Join(hostDir, sid+".jsonl") {
		t.Fatalf("bridged: got %q, %v; want the real host path", got, err)
	}

	// Only the bridge resolves (the real directory is elsewhere): refused, as before.
	if err := os.RemoveAll(hostDir); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, sid+".jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(configDir, "projects", "-mnt-d-Dev-Projects-devy")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(configDir, "projects", "-mnt-d-Dev-Projects-devy")); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalClaudeExactTranscriptPath(inst); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("bridge only: want a symlink refusal, got %v", err)
	}
}
