package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Codex submission markers are archived, never deleted, once they stop being
// their own sender's live attempt (WO-DECK-CODEX-DELIVERY-01): an earlier
// attempt resolved by reconciliation, or a stale marker the codex-markers
// cleanup proves delivered or orphaned. A sender clearing its OWN attempt
// (nothing was typed, or it saw its own turn accepted) still removes it.
//
// Layout: <locks>/codex-submissions-archive/<dir>/<marker file> plus one
// MANIFEST.jsonl line per moved marker. Everything written is content-free.

const codexSubmissionArchiveDirName = "codex-submissions-archive"

// CodexMarkerManifestLine is one archived marker's record.
type CodexMarkerManifestLine struct {
	ArchivedAt     time.Time              `json:"archived_at"`
	Reason         string                 `json:"reason"`
	Detail         string                 `json:"detail,omitempty"`
	File           string                 `json:"file"`
	ArchivedAs     string                 `json:"archived_as"`
	AttemptID      string                 `json:"attempt_id"`
	InstanceID     string                 `json:"instance_id"`
	CodexSessionID string                 `json:"codex_session_id"`
	Phase          string                 `json:"phase"`
	Version        int                    `json:"version"`
	CreatedAt      time.Time              `json:"created_at"`
	UpdatedAt      time.Time              `json:"updated_at"`
	Evidence       *CodexDeliveryEvidence `json:"evidence,omitempty"`
}

// CodexSubmissionArchiveRoot is <locks>/codex-submissions-archive.
func CodexSubmissionArchiveRoot() (string, error) {
	dir, err := codexSubmissionMarkerDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), codexSubmissionArchiveDirName), nil
}

func ensurePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if err := validatePrivateCodexMarkerPath(info, true); err != nil {
		return fmt.Errorf("unsafe Codex submission archive directory %s: %w", dir, err)
	}
	return nil
}

// ArchiveCodexSubmissionMarker moves marker into the archive directory
// dirName (a single path component) with a manifest line. The caller holds
// the marker's acceptance lock; the attempt id is re-read immediately before
// the move, so a marker that changed hands is never moved.
func ArchiveCodexSubmissionMarker(marker *CodexSubmissionMarker, dirName, reason, detail string, evidence *CodexDeliveryEvidence, now time.Time) (string, error) {
	return archiveCodexSubmissionMarkerDetail(marker, dirName, reason, detail, evidence, now)
}

func archiveCodexSubmissionMarker(marker *CodexSubmissionMarker, dirName, reason string, evidence *CodexDeliveryEvidence, now time.Time) (string, error) {
	return archiveCodexSubmissionMarkerDetail(marker, dirName, reason, "", evidence, now)
}

func archiveCodexSubmissionMarkerDetail(marker *CodexSubmissionMarker, dirName, reason, detail string, evidence *CodexDeliveryEvidence, now time.Time) (string, error) {
	if err := validateCodexSubmissionMarker(marker); err != nil {
		return "", err
	}
	if dirName == "" || filepath.Base(dirName) != dirName || strings.ContainsAny(dirName, `/\`) || strings.HasPrefix(dirName, ".") {
		return "", fmt.Errorf("invalid Codex submission archive directory name %q", dirName)
	}
	current, found, err := readCodexSubmissionMarker(marker.CodexSessionID)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("Codex submission marker %s is missing", marker.AttemptID)
	}
	if current.InstanceID != marker.InstanceID || current.CodexSessionID != marker.CodexSessionID || current.AttemptID != marker.AttemptID {
		return "", fmt.Errorf("Codex submission marker ownership changed; refusing to archive")
	}
	src, err := codexSubmissionMarkerPath(marker.CodexSessionID)
	if err != nil {
		return "", err
	}
	root, err := CodexSubmissionArchiveRoot()
	if err != nil {
		return "", err
	}
	if err := ensurePrivateDir(root); err != nil {
		return "", err
	}
	dir := filepath.Join(root, dirName)
	if err := ensurePrivateDir(dir); err != nil {
		return "", err
	}
	base := filepath.Base(src)
	dst := filepath.Join(dir, base)
	if _, statErr := os.Lstat(dst); statErr == nil {
		dst = filepath.Join(dir, strings.TrimSuffix(base, ".json")+"."+current.AttemptID+".json")
	} else if !os.IsNotExist(statErr) {
		return "", statErr
	}
	if _, statErr := os.Lstat(dst); statErr == nil {
		return "", fmt.Errorf("archive destination %s already exists", dst)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("archive Codex submission marker: %w", err)
	}
	if err := fsyncDirStrict(filepath.Dir(src)); err != nil {
		return dst, fmt.Errorf("sync Codex submission marker directory: %w", err)
	}
	if err := fsyncDirStrict(dir); err != nil {
		return dst, fmt.Errorf("sync Codex submission archive directory: %w", err)
	}
	if now.IsZero() {
		now = time.Now()
	}
	line := CodexMarkerManifestLine{
		ArchivedAt: now.UTC(), Reason: reason, Detail: detail, File: base, ArchivedAs: filepath.Base(dst),
		AttemptID: current.AttemptID, InstanceID: current.InstanceID, CodexSessionID: current.CodexSessionID,
		Phase: current.Phase, Version: current.Version, CreatedAt: current.CreatedAt, UpdatedAt: current.UpdatedAt,
		Evidence: evidence,
	}
	if err := appendCodexMarkerManifest(filepath.Join(dir, "MANIFEST.jsonl"), line); err != nil {
		// The marker itself is preserved in the archive; only its manifest
		// line is missing. Report it, never undo the move.
		return dst, fmt.Errorf("write Codex submission archive manifest: %w", err)
	}
	return dst, nil
}

func appendCodexMarkerManifest(path string, line CodexMarkerManifestLine) error {
	data, err := json.Marshal(line)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600) // #nosec G304 -- private archive dir
	if err != nil {
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// CodexSubmissionMarkerEntry is one live marker file as the cleanup sees it.
type CodexSubmissionMarkerEntry struct {
	File    string
	Path    string
	Marker  *CodexSubmissionMarker
	ReadErr error
}

// ListCodexSubmissionMarkers reads every live marker file. A file that does
// not parse is listed with its error so the operator sees it; it is never
// moved by the cleanup.
func ListCodexSubmissionMarkers() ([]CodexSubmissionMarkerEntry, error) {
	dir, err := codexSubmissionMarkerDir()
	if err != nil {
		return nil, err
	}
	names, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var entries []CodexSubmissionMarkerEntry
	for _, entry := range names {
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(dir, name)
		marker, readErr := readCodexSubmissionMarkerFile(path)
		entries = append(entries, CodexSubmissionMarkerEntry{File: name, Path: path, Marker: marker, ReadErr: readErr})
	}
	sort.Slice(entries, func(a, b int) bool { return entries[a].File < entries[b].File })
	return entries, nil
}

// readCodexSubmissionMarkerFile parses one marker file by path and checks
// that it is stored under its own session's digest name.
func readCodexSubmissionMarkerFile(path string) (*CodexSubmissionMarker, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- private marker dir
	if err != nil {
		return nil, err
	}
	if len(data) > codexSubmissionMarkerMaxSize {
		return nil, fmt.Errorf("Codex submission marker exceeds %d bytes", codexSubmissionMarkerMaxSize)
	}
	var probe struct {
		CodexSessionID string `json:"codex_session_id"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("parse Codex submission marker: %w", err)
	}
	if probe.CodexSessionID == "" {
		return nil, errors.New("Codex submission marker has no session identity")
	}
	want, err := codexSubmissionMarkerPath(probe.CodexSessionID)
	if err != nil {
		return nil, err
	}
	if filepath.Base(want) != filepath.Base(path) {
		return nil, errors.New("Codex submission marker is not stored under its session's name")
	}
	marker, found, err := readCodexSubmissionMarker(probe.CodexSessionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, os.ErrNotExist
	}
	return marker, nil
}
