package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// `agent-deck codex-markers` (WO-DECK-CODEX-DELIVERY-01 part 7): inspect and
// archive stale Codex submission markers. A marker is the double-send fence
// for one Codex attempt; a stale one refuses every later send to its session
// with "unresolved Codex submission".
//
// Safety rules:
//   - Dry run by default: it lists every marker with a verdict and changes
//     nothing. --apply acts only on the markers the listing marked archive.
//   - It ARCHIVES (moves) markers into a dated directory under
//     locks/codex-submissions-archive/ with a MANIFEST.jsonl line each, and
//     never deletes anything.
//   - It takes a marker only when the exact rollout proves the attempt was
//     delivered (a turn started after it, or its input folded into a running
//     turn) or its Deck session is archived, superseded, removed or rebound
//     to another Codex thread.
//   - It never touches a marker updated in the last --min-age (20 minutes):
//     that attempt may still be in flight.
//   - Right before each move it takes the session's acceptance lock and
//     re-checks the attempt id and updated_at it listed.

const codexMarkersDefaultMinAge = 20 * time.Minute

// Verdicts.
const (
	markerArchiveDelivered  = "archive_delivered"
	markerArchiveArchived   = "archive_session_archived"
	markerArchiveSuperseded = "archive_session_superseded"
	markerArchiveRemoved    = "archive_session_removed"
	markerArchiveRebound    = "archive_session_rebound"
	markerLeaveRecent       = "leave_recent"
	markerLeaveNoProof      = "leave_no_proof"
	markerLeaveUnreadable   = "leave_unreadable"
	markerLeaveUnknown      = "leave_session_unknown"
	markerLeaveRolloutError = "leave_rollout_unreadable"
)

// codexMarkerRow is one marker's classification. It is content-free.
type codexMarkerRow struct {
	File           string                         `json:"file"`
	InstanceID     string                         `json:"instance_id,omitempty"`
	Title          string                         `json:"title,omitempty"`
	CodexSessionID string                         `json:"codex_session_id,omitempty"`
	AttemptID      string                         `json:"attempt_id,omitempty"`
	Phase          string                         `json:"phase,omitempty"`
	Version        int                            `json:"version,omitempty"`
	CreatedAt      time.Time                      `json:"created_at,omitempty"`
	UpdatedAt      time.Time                      `json:"updated_at,omitempty"`
	AgeMinutes     int                            `json:"age_minutes"`
	Verdict        string                         `json:"verdict"`
	Detail         string                         `json:"detail,omitempty"`
	Evidence       *session.CodexDeliveryEvidence `json:"evidence,omitempty"`
	ArchivedTo     string                         `json:"archived_to,omitempty"`
	ApplyError     string                         `json:"apply_error,omitempty"`

	marker *session.CodexSubmissionMarker
}

func (r codexMarkerRow) archivable() bool { return strings.HasPrefix(r.Verdict, "archive_") }

// codexMarkerSessions answers "which Deck session owns this instance id".
// complete is false when some profile could not be loaded: an instance that
// is not found then may still exist, so it is never called removed.
type codexMarkerSessions struct {
	byID     map[string]*session.Instance
	complete bool
}

func loadCodexMarkerSessions() codexMarkerSessions {
	out := codexMarkerSessions{byID: map[string]*session.Instance{}, complete: true}
	profiles, err := session.ListProfiles()
	if err != nil || len(profiles) == 0 {
		out.complete = false
		return out
	}
	for _, profile := range profiles {
		storage, err := session.NewStorageWithProfile(profile)
		if err != nil {
			out.complete = false
			continue
		}
		instances, _, err := storage.LoadWithGroups()
		storage.Close()
		if err != nil {
			out.complete = false
			continue
		}
		for _, inst := range instances {
			if inst != nil && inst.ID != "" {
				out.byID[inst.ID] = inst
			}
		}
	}
	return out
}

// classifyCodexMarker decides one marker's verdict. evidence searches the
// owning session's exact rollout; generation reads its newest turn.
func classifyCodexMarker(
	entry session.CodexSubmissionMarkerEntry,
	sessions codexMarkerSessions,
	now time.Time,
	minAge time.Duration,
	evidence func(inst *session.Instance, marker *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error),
) codexMarkerRow {
	row := codexMarkerRow{File: entry.File}
	if entry.ReadErr != nil || entry.Marker == nil {
		row.Verdict = markerLeaveUnreadable
		if entry.ReadErr != nil {
			row.Detail = entry.ReadErr.Error()
		}
		return row
	}
	m := entry.Marker
	row.marker = m
	row.InstanceID, row.CodexSessionID, row.AttemptID = m.InstanceID, m.CodexSessionID, m.AttemptID
	row.Phase, row.Version, row.CreatedAt, row.UpdatedAt = m.Phase, m.Version, m.CreatedAt, m.UpdatedAt
	row.AgeMinutes = int(now.Sub(m.UpdatedAt) / time.Minute)
	if now.Sub(m.UpdatedAt) < minAge {
		row.Verdict = markerLeaveRecent
		row.Detail = fmt.Sprintf("updated %s ago; the attempt may still be in flight", now.Sub(m.UpdatedAt).Round(time.Second))
		return row
	}
	inst := sessions.byID[m.InstanceID]
	switch {
	case inst == nil && !sessions.complete:
		row.Verdict = markerLeaveUnknown
		row.Detail = "the session registry could not be read in full"
		return row
	case inst == nil:
		row.Verdict = markerArchiveRemoved
		return row
	}
	row.Title = inst.Title
	switch {
	case inst.IsArchived():
		row.Verdict = markerArchiveArchived
		return row
	case strings.TrimSpace(inst.SupersededBy) != "":
		row.Verdict = markerArchiveSuperseded
		return row
	case !session.IsCodexCompatible(inst.Tool) || strings.TrimSpace(inst.CodexSessionID) != m.CodexSessionID:
		row.Verdict = markerArchiveRebound
		row.Detail = "the session no longer owns this Codex thread"
		return row
	}
	proof, err := evidence(inst, m)
	if err != nil {
		row.Verdict = markerLeaveRolloutError
		row.Detail = err.Error()
		return row
	}
	if proof == nil {
		row.Verdict = markerLeaveNoProof
		row.Detail = "no turn started and no input landed after this attempt in the exact rollout"
		return row
	}
	row.Verdict, row.Evidence = markerArchiveDelivered, proof
	return row
}

// codexMarkerEvidence is the production evidence source: a changed exact
// generation over a real prior, else rollout evidence after the cut.
func codexMarkerEvidence(inst *session.Instance, m *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
	if !inst.CodexRolloutIsResolvableLocally() {
		return nil, errCodexRolloutNotLocal
	}
	if m.PriorTurnGeneration != "" {
		if current, err := inst.LatestCodexTurnGeneration(); err == nil && current != "" && current != m.PriorTurnGeneration {
			return &session.CodexDeliveryEvidence{Kind: session.CodexEvidenceNewTurn, Basis: session.CodexEvidenceBasisGeneration, TurnGeneration: current}, nil
		}
	}
	cut := int64(-1)
	if m.RolloutOffset != nil {
		cut = *m.RolloutOffset
	}
	return inst.CodexDeliveryEvidenceSince(cut, m.CreatedAt)
}

// applyCodexMarkerRows archives every archivable row under its session's
// acceptance lock, re-checking the attempt it listed right before the move.
func applyCodexMarkerRows(rows []codexMarkerRow, dirName string, now time.Time, lockWait time.Duration) []codexMarkerRow {
	for i := range rows {
		row := &rows[i]
		if !row.archivable() || row.marker == nil {
			continue
		}
		lock, err := session.AcquireCodexAcceptanceLock(row.CodexSessionID, lockWait)
		if err != nil {
			row.ApplyError = fmt.Sprintf("skipped: %v", err)
			continue
		}
		func() {
			defer lock.Release()
			entries, err := session.ListCodexSubmissionMarkers()
			if err != nil {
				row.ApplyError = fmt.Sprintf("skipped: %v", err)
				return
			}
			var current *session.CodexSubmissionMarker
			for _, e := range entries {
				if e.File == row.File && e.Marker != nil {
					current = e.Marker
				}
			}
			switch {
			case current == nil:
				row.ApplyError = "skipped: the marker is gone"
				return
			case current.AttemptID != row.AttemptID || !current.UpdatedAt.Equal(row.UpdatedAt):
				row.ApplyError = "skipped: the marker changed since it was listed"
				return
			}
			archived, err := session.ArchiveCodexSubmissionMarker(current, dirName, "codex-markers cleanup", row.Verdict, row.Evidence, now)
			if archived != "" {
				row.ArchivedTo = archived
			}
			if err != nil {
				row.ApplyError = err.Error()
			}
		}()
	}
	return rows
}

func handleCodexMarkers(args []string) {
	fs := flag.NewFlagSet("codex-markers", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)
	apply := fs.Bool("apply", false, "Archive the markers the listing marks archive_* (default: dry run, changes nothing)")
	jsonOutput := fs.Bool("json", false, "Output as JSON")
	minAge := fs.Duration("min-age", codexMarkersDefaultMinAge, "Never touch a marker updated more recently than this")
	fs.Usage = func() {
		fmt.Println("Usage: agent-deck codex-markers [--apply] [--json] [--min-age 20m]")
		fmt.Println()
		fmt.Println("List Codex submission markers with a verdict; with --apply, archive (move, never delete) the ones")
		fmt.Println("whose delivery the exact rollout proves, or whose Deck session is archived, superseded, removed")
		fmt.Println("or rebound. Markers updated within --min-age are never touched. Archive:")
		fmt.Println("  <agent-deck>/locks/codex-submissions-archive/cleanup-<timestamp>/ (+ MANIFEST.jsonl)")
		fmt.Println()
		fs.PrintDefaults()
	}
	if err := fs.Parse(normalizeArgs(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}
	if *minAge < codexMarkersDefaultMinAge {
		fmt.Fprintf(os.Stderr, "--min-age below %s is refused: a younger marker may belong to a send still in flight\n", codexMarkersDefaultMinAge)
		os.Exit(2)
	}
	entries, err := session.ListCodexSubmissionMarkers()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot list Codex submission markers: %v\n", err)
		os.Exit(1)
	}
	now := time.Now()
	sessions := loadCodexMarkerSessions()
	rows := make([]codexMarkerRow, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, classifyCodexMarker(entry, sessions, now, *minAge, codexMarkerEvidence))
	}
	dirName := ""
	if *apply {
		dirName = "cleanup-" + now.UTC().Format("20060102-150405")
		rows = applyCodexMarkerRows(rows, dirName, now, 5*time.Second)
	}
	printCodexMarkerRows(rows, *apply, *jsonOutput, dirName, sessions.complete)
	for _, row := range rows {
		if row.ApplyError != "" && row.ArchivedTo == "" && !strings.HasPrefix(row.ApplyError, "skipped:") {
			os.Exit(1)
		}
	}
}

func printCodexMarkerRows(rows []codexMarkerRow, applied, jsonOutput bool, dirName string, registryComplete bool) {
	counts := map[string]int{}
	archived := 0
	for _, row := range rows {
		counts[row.Verdict]++
		if row.ArchivedTo != "" {
			archived++
		}
	}
	if jsonOutput {
		data, _ := json.MarshalIndent(map[string]interface{}{
			"dry_run": !applied, "archive_dir": dirName, "registry_complete": registryComplete,
			"markers": rows, "counts": counts, "archived": archived,
		}, "", "  ")
		fmt.Println(string(data))
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tSESSION\tPHASE\tAGE\tVERDICT\tEVIDENCE / DETAIL")
	for _, row := range rows {
		detail := row.Detail
		if row.Evidence != nil {
			detail = row.Evidence.Kind + " (" + row.Evidence.Basis + ")"
		}
		if row.ArchivedTo != "" {
			detail += " -> archived"
		}
		if row.ApplyError != "" {
			detail += " [" + row.ApplyError + "]"
		}
		name := row.InstanceID
		if row.Title != "" {
			name = row.InstanceID + " " + truncateForColumn(row.Title, 40)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%dm\t%s\t%s\n", truncateForColumn(row.File, 16), name, row.Phase, row.AgeMinutes, row.Verdict, detail)
	}
	_ = w.Flush()
	fmt.Println()
	if applied {
		fmt.Printf("Archived %d marker(s) to %s (moved, nothing deleted).\n", archived, dirName)
	} else {
		n := 0
		for _, row := range rows {
			if row.archivable() {
				n++
			}
		}
		fmt.Printf("Dry run: %d of %d marker(s) would be archived. Re-run with --apply to move them.\n", n, len(rows))
	}
	if !registryComplete {
		fmt.Println("Note: some profiles could not be read; markers of sessions not found are left alone.")
	}
}

func truncateForColumn(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
