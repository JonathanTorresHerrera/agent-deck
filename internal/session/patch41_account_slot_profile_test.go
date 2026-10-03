package session

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatch41_NormalizeSlotDir(t *testing.T) {
	a := normalizeSlotDir("/mnt/c/Users/jonat/.aida-accounts/claude/claude-3/")
	for _, other := range []string{
		`C:\Users\jonat\.aida-accounts\claude\claude-3`,
		"C:/Users/jonat/.aida-accounts/claude/claude-3",
		"/mnt/c/users/JONAT/.aida-accounts/claude/claude-3",
	} {
		if got := normalizeSlotDir(other); got != a {
			t.Fatalf("normalizeSlotDir(%q) = %q, want %q", other, got, a)
		}
	}
	if normalizeSlotDir("/mnt/c/Users/jonat/.aida-accounts/claude/claude-2") == a {
		t.Fatal("different slots must not compare equal")
	}
}

// captureStderr runs fn and returns what it wrote to os.Stderr.
func p41CaptureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	var b bytes.Buffer
	_, _ = io.Copy(&b, r)
	return b.String()
}

func TestPatch41_AccountSlotDirFallsBackSilently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("AGENTDECK_PROFILE", "")
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	slot := filepath.Join(home, "accounts", "claude-3")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".agent-deck"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[profiles.claude-3.claude]\n  config_dir = \"" + slot + "\"\n"
	if err := os.WriteFile(filepath.Join(home, ".agent-deck", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	ClearUserConfigCache()

	// A configured slot: no warning.
	t.Setenv("CLAUDE_CONFIG_DIR", slot)
	var got string
	out := p41CaptureStderr(t, func() {
		p, err := ResolveProfileForStorage("")
		if err != nil {
			t.Fatal(err)
		}
		got = p
	})
	if strings.Contains(out, "would select profile") {
		t.Fatalf("configured account slot must not warn, got stderr %q", out)
	}
	if got == "3" {
		t.Fatalf("must not resolve to the inferred profile %q", got)
	}

	// An unrelated dir with an inferable name still warns (upstream #1790 kept).
	other := filepath.Join(home, ".claude-work")
	t.Setenv("CLAUDE_CONFIG_DIR", other)
	out = p41CaptureStderr(t, func() { _, _ = ResolveProfileForStorage("") })
	if !strings.Contains(out, "would select profile") {
		t.Fatalf("an unconfigured inferred dir must still warn, got %q", out)
	}
}
