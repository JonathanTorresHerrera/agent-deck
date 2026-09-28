package session

// Patch 26: one shared index for the transcript glob fallback.
//
// resolveClaudeTranscriptPath's fallback located a transcript by its unique
// session-id filename with Glob(projects/*/<id>.jsonl), once per session. On a
// WSL deck driving Windows Claude the exact candidates always miss (the
// directory names are Windows-encoded), so every session stat'ed every project
// directory across /mnt/c: 83 dirs x ~140 sessions = ~60 s per `list --json`.
//
// The index is one ReadDir per project directory, mapping "<id>.jsonl" to the
// lexically first path that holds it (what Glob's sorted result returned). It
// is rebuilt when older than transcriptIndexTTL, or on a miss once
// transcriptIndexMissRefresh has passed, so a transcript that appears in a
// long-running TUI is still found within seconds.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	transcriptIndexTTL         = 60 * time.Second
	transcriptIndexMissRefresh = 5 * time.Second
)

var transcriptIndexNow = time.Now

type transcriptIndex struct {
	builtAt time.Time
	paths   map[string]string // "<id>.jsonl" -> full path
}

var (
	transcriptIndexMu         sync.Mutex
	transcriptIndexes         = map[string]*transcriptIndex{}
	transcriptIndexBuildCount int
)

// lookupTranscriptByID returns the path of <sessionID>.jsonl in any direct
// subdirectory of projectsDir, or "".
func lookupTranscriptByID(projectsDir, sessionID string) string {
	name := sessionID + ".jsonl"
	transcriptIndexMu.Lock()
	defer transcriptIndexMu.Unlock()
	now := transcriptIndexNow()
	idx := transcriptIndexes[projectsDir]
	if idx == nil || now.Sub(idx.builtAt) > transcriptIndexTTL {
		idx = buildTranscriptIndex(projectsDir, now)
	}
	// A hit is confirmed with one stat, so a transcript deleted since the scan
	// is not handed out (the Glob this replaces never returned a gone file).
	if p, ok := idx.paths[name]; ok {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if now.Sub(idx.builtAt) >= transcriptIndexMissRefresh {
		idx = buildTranscriptIndex(projectsDir, now)
		if p, ok := idx.paths[name]; ok {
			return p
		}
	}
	return ""
}

// buildTranscriptIndex scans projectsDir once. Caller holds transcriptIndexMu.
func buildTranscriptIndex(projectsDir string, now time.Time) *transcriptIndex {
	transcriptIndexBuildCount++
	idx := &transcriptIndex{builtAt: now, paths: map[string]string{}}
	transcriptIndexes[projectsDir] = idx
	dirs, err := os.ReadDir(projectsDir)
	if err != nil {
		return idx
	}
	names := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			names = append(names, d.Name())
		}
	}
	sort.Strings(names) // lexically first wins, as with Glob's sorted matches
	for _, dir := range names {
		entries, err := os.ReadDir(filepath.Join(projectsDir, dir))
		if err != nil {
			continue
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".jsonl") {
				continue
			}
			if _, seen := idx.paths[n]; !seen {
				idx.paths[n] = filepath.Join(projectsDir, dir, n)
			}
		}
	}
	return idx
}

func transcriptIndexBuilds() int {
	transcriptIndexMu.Lock()
	defer transcriptIndexMu.Unlock()
	return transcriptIndexBuildCount
}

func resetTranscriptIndexForTest() {
	transcriptIndexMu.Lock()
	defer transcriptIndexMu.Unlock()
	transcriptIndexes = map[string]*transcriptIndex{}
}
