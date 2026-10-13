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
	transcriptIndexBuilding   = map[string]chan struct{}{}
	transcriptIndexBuildCount int
)

// lookupTranscriptByID returns the path of <sessionID>.jsonl in any direct
// subdirectory of projectsDir, or "".
//
// Patch 52: transcriptIndexMu guards only the maps, never the scan. A rescan
// of a /mnt/c projects dir takes ~1.2 s; held under the shared lock it stalled
// every status worker in the TUI sweep behind it. Now one caller scans while
// the others use the previous index (or, for a dir never scanned, wait for
// the first scan).
func lookupTranscriptByID(projectsDir, sessionID string) string {
	name := sessionID + ".jsonl"
	now := transcriptIndexNow()
	transcriptIndexMu.Lock()
	idx := transcriptIndexes[projectsDir]
	transcriptIndexMu.Unlock()
	if idx == nil || now.Sub(idx.builtAt) > transcriptIndexTTL {
		idx = rebuildTranscriptIndex(projectsDir, now)
	}
	// A hit is confirmed with one stat, so a transcript deleted since the scan
	// is not handed out (the Glob this replaces never returned a gone file).
	if p, ok := idx.paths[name]; ok {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if now.Sub(idx.builtAt) >= transcriptIndexMissRefresh {
		idx = rebuildTranscriptIndex(projectsDir, now)
		if p, ok := idx.paths[name]; ok {
			return p
		}
	}
	return ""
}

// rebuildTranscriptIndex scans projectsDir unless another caller already is:
// then it returns the previous index without waiting, or waits when there is
// none yet. The returned index is never mutated after it is published.
func rebuildTranscriptIndex(projectsDir string, now time.Time) *transcriptIndex {
	transcriptIndexMu.Lock()
	if busy, ok := transcriptIndexBuilding[projectsDir]; ok {
		prev := transcriptIndexes[projectsDir]
		transcriptIndexMu.Unlock()
		if prev != nil {
			return prev
		}
		<-busy
		transcriptIndexMu.Lock()
		defer transcriptIndexMu.Unlock()
		if idx := transcriptIndexes[projectsDir]; idx != nil {
			return idx
		}
		return &transcriptIndex{builtAt: now, paths: map[string]string{}}
	}
	done := make(chan struct{})
	transcriptIndexBuilding[projectsDir] = done
	transcriptIndexBuildCount++
	transcriptIndexMu.Unlock()

	idx := scanTranscriptIndex(projectsDir, now)

	transcriptIndexMu.Lock()
	transcriptIndexes[projectsDir] = idx
	delete(transcriptIndexBuilding, projectsDir)
	transcriptIndexMu.Unlock()
	close(done)
	return idx
}

// scanTranscriptIndex reads projectsDir once. It takes no lock.
func scanTranscriptIndex(projectsDir string, now time.Time) *transcriptIndex {
	idx := &transcriptIndex{builtAt: now, paths: map[string]string{}}
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
