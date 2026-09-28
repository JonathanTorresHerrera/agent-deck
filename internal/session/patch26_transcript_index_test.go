// Patch 26: the Claude transcript glob fallback uses one shared index.
//
// resolveClaudeTranscriptPath fell back to Glob(projects/*/<id>.jsonl) per
// session. Claude runs as a Windows program here, so its project directories
// carry the Windows-encoded path and the exact candidates always miss: every
// session stat'ed every project directory over /mnt/c. 83 directories x ~140
// sessions made `agent-deck list --json` take 50-76 s (measured 2026-09-28;
// goroutine dump: list -> Substate -> usageLimited -> locateHandoffTranscript
// -> resolveClaudeTranscriptPath -> filepath.Glob).
package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func p26WriteTranscript(t *testing.T, projectsDir, dir, id string) string {
	t.Helper()
	p := filepath.Join(projectsDir, dir, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPatch26_FallbackFindsTranscriptUnderForeignDirName(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	for i := 0; i < 5; i++ {
		p26WriteTranscript(t, projects, "D--other-"+string(rune('a'+i)), "11111111-0000-4000-8000-00000000000"+string(rune('0'+i)))
	}
	want := p26WriteTranscript(t, projects, "--wsl-localhost-Ubuntu-home-x", "7e60e581-5c76-4523-ae62-5de8741cd18c")
	if got := resolveClaudeTranscriptPath(cfg, "/home/x", "7e60e581-5c76-4523-ae62-5de8741cd18c"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPatch26_ManyLookupsScanTheProjectsDirOnce(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	ids := []string{}
	for i := 0; i < 20; i++ {
		id := "22222222-0000-4000-8000-0000000000" + string(rune('a'+i/10)) + string(rune('0'+i%10))
		p26WriteTranscript(t, projects, "D--proj-"+string(rune('a'+i)), id)
		ids = append(ids, id)
	}
	before := transcriptIndexBuilds()
	for _, id := range ids {
		if resolveClaudeTranscriptPath(cfg, "/nowhere", id) == "" {
			t.Fatalf("did not find %s", id)
		}
	}
	if n := transcriptIndexBuilds() - before; n != 1 {
		t.Errorf("index built %d times for 20 lookups, want 1", n)
	}
}

func TestPatch26_NewTranscriptIsFoundAfterTheRefreshInterval(t *testing.T) {
	resetTranscriptIndexForTest()
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	transcriptIndexNow = func() time.Time { return now }
	defer func() { transcriptIndexNow = time.Now }()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	p26WriteTranscript(t, projects, "D--a", "33333333-0000-4000-8000-000000000001")
	resolveClaudeTranscriptPath(cfg, "/nowhere", "33333333-0000-4000-8000-000000000001")

	late := p26WriteTranscript(t, projects, "D--b", "33333333-0000-4000-8000-000000000002")
	now = now.Add(transcriptIndexMissRefresh + time.Second)
	if got := resolveClaudeTranscriptPath(cfg, "/nowhere", "33333333-0000-4000-8000-000000000002"); got != late {
		t.Errorf("new transcript not found after refresh interval: got %q", got)
	}
}

func TestPatch26_ExactCandidateStillWinsAndSharedIDPicksFirstLikeGlob(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	id := "44444444-0000-4000-8000-000000000001"
	// "AAA-..." sorts AFTER the "-home-..." exact dir ('-' < 'A'), so a
	// fallback-first resolver would return it; the exact candidate must win.
	exact := p26WriteTranscript(t, projects, ConvertToClaudeDirName("/home/x/proj"), id)
	p26WriteTranscript(t, projects, "AAA-other", id)
	if got := resolveClaudeTranscriptPath(cfg, "/home/x/proj", id); got != exact {
		t.Errorf("exact candidate must win: got %q", got)
	}

	// Fallback with two dirs sharing an id: Glob's sorted result gave the
	// lexically first, and the index keeps that choice (#1720).
	shared := "44444444-0000-4000-8000-000000000002"
	p26WriteTranscript(t, projects, "b-second", shared)
	first := p26WriteTranscript(t, projects, "a-first", shared)
	resetTranscriptIndexForTest()
	if got := resolveClaudeTranscriptPath(cfg, "/elsewhere", shared); got != first {
		t.Errorf("shared id fallback = %q, want lexically first %q", got, first)
	}
}

func TestPatch26_MissingProjectsDirIsEmpty(t *testing.T) {
	resetTranscriptIndexForTest()
	if got := resolveClaudeTranscriptPath(t.TempDir(), "/x", "55555555-0000-4000-8000-000000000001"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestPatch26_DeletedTranscriptIsNotReturned(t *testing.T) {
	resetTranscriptIndexForTest()
	cfg := t.TempDir()
	projects := filepath.Join(cfg, "projects")
	id := "66666666-0000-4000-8000-000000000001"
	p := p26WriteTranscript(t, projects, "D--gone", id)
	if resolveClaudeTranscriptPath(cfg, "/nowhere", id) != p {
		t.Fatal("setup: not found")
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if got := resolveClaudeTranscriptPath(cfg, "/nowhere", id); got != "" {
		t.Errorf("deleted transcript returned %q", got)
	}
}
