package session

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Patch 52: a rescan in progress never blocks a lookup that already has an
// index; it returns at once from the previous one.
func TestPatch52_LookupDoesNotWaitOnARescan(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	id := "77777777-0000-4000-8000-000000000001"
	want := p26WriteTranscript(t, projects, "D--a", id)
	if got := lookupTranscriptByID(projects, id); got != want {
		t.Fatalf("setup: got %q", got)
	}

	// Pretend another caller is mid-scan.
	busy := make(chan struct{})
	transcriptIndexMu.Lock()
	transcriptIndexBuilding[projects] = busy
	transcriptIndexMu.Unlock()
	defer func() {
		transcriptIndexMu.Lock()
		delete(transcriptIndexBuilding, projects)
		transcriptIndexMu.Unlock()
		close(busy)
	}()

	got := make(chan string, 1)
	go func() { got <- lookupTranscriptByID(projects, "77777777-0000-4000-8000-00000000ffff") }()
	select {
	case p := <-got:
		if p != "" {
			t.Fatalf("unknown id resolved to %q", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lookup blocked behind a rescan in progress")
	}
	if got := lookupTranscriptByID(projects, id); got != want {
		t.Fatalf("known id during rescan: got %q", got)
	}
}

// Concurrent first lookups on a cold dir scan it once and all find the file.
func TestPatch52_ColdDirScannedOnceByConcurrentCallers(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	id := "88888888-0000-4000-8000-000000000001"
	want := p26WriteTranscript(t, projects, "D--b", id)
	before := transcriptIndexBuilds()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := lookupTranscriptByID(projects, id); got != want {
				t.Errorf("got %q", got)
			}
		}()
	}
	wg.Wait()
	if n := transcriptIndexBuilds() - before; n != 1 {
		t.Errorf("cold dir scanned %d times, want 1", n)
	}
}

// GetJSONLPath resolves under the session's seat dir, not the process-wide one.
func TestPatch52_JSONLPathUsesTheSeatConfigDir(t *testing.T) {
	resetTranscriptIndexForTest()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	os.Unsetenv("CLAUDE_CONFIG_DIR")
	t.Setenv("AGENTDECK_PROFILE", "")
	os.Unsetenv("AGENTDECK_PROFILE")
	seatDir := filepath.Join(home, "seat-2")
	id := "99999999-0000-4000-8000-000000000001"
	want := p26WriteTranscript(t, filepath.Join(seatDir, "projects"), "C--Users-x-proj", id)
	writeTestUserConfig(t, home, "[profiles.seat-2.claude]\nconfig_dir = \""+seatDir+"\"\n")

	inst := &Instance{ID: "p52", Tool: "claude", Account: "seat-2", ClaudeSessionID: id, ProjectPath: "/home/x/proj"}
	if got := inst.GetJSONLPath(); got != want {
		t.Fatalf("GetJSONLPath = %q, want the seat transcript %q", got, want)
	}
}

func writeTestUserConfig(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".agent-deck")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)
}
