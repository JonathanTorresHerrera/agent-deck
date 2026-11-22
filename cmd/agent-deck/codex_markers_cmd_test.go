package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// WO-DECK-CODEX-DELIVERY-01 part 7. Marker shapes match the markers archived
// on 2026-11-22 (version 1, content-free) with synthetic identities.

func isolateMarkerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("CODEX_HOME", filepath.Join(home, "codex"))
	return home
}

func prepareOldMarker(t *testing.T, instanceID, sessionID string, age time.Duration) *session.CodexSubmissionMarker {
	t.Helper()
	m, err := session.PrepareCodexSubmissionMarker(instanceID, sessionID, sessionID+":turn-prior", 0, time.Now().Add(-age))
	if err != nil {
		t.Fatal(err)
	}
	if err := m.MarkSubmitted(time.Now().Add(-age).Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return m
}

func markerEntry(t *testing.T, m *session.CodexSubmissionMarker) session.CodexSubmissionMarkerEntry {
	t.Helper()
	entries, err := session.ListCodexSubmissionMarkers()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Marker != nil && e.Marker.AttemptID == m.AttemptID {
			return e
		}
	}
	t.Fatalf("marker %s not listed", m.AttemptID)
	return session.CodexSubmissionMarkerEntry{}
}

func TestClassifyCodexMarker(t *testing.T) {
	isolateMarkerHome(t)
	now := time.Now()
	noEvidence := func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
		return nil, nil
	}
	folded := func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
		return &session.CodexDeliveryEvidence{Kind: session.CodexEvidenceInputFolded, Basis: session.CodexEvidenceBasisCreatedAt}, nil
	}
	broken := func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
		return nil, errors.New("rollout unreadable")
	}
	live := func(id, codexID string) *session.Instance {
		return &session.Instance{ID: id, Title: "synthetic", Tool: "codex", CodexSessionID: codexID}
	}
	cases := []struct {
		name     string
		age      time.Duration
		inst     *session.Instance
		complete bool
		evidence func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error)
		verdict  string
	}{
		{"recent marker is left alone even with proof", 5 * time.Minute, live("i-recent", "t-recent"), true, folded, markerLeaveRecent},
		{"delivered", time.Hour, live("i-deliv", "t-deliv"), true, folded, markerArchiveDelivered},
		{"no proof stays", time.Hour, live("i-noproof", "t-noproof"), true, noEvidence, markerLeaveNoProof},
		{"unreadable rollout stays", time.Hour, live("i-broken", "t-broken"), true, broken, markerLeaveRolloutError},
		{"removed session", time.Hour, nil, true, noEvidence, markerArchiveRemoved},
		{"unknown when the registry is partial", time.Hour, nil, false, noEvidence, markerLeaveUnknown},
		{"archived session", time.Hour, &session.Instance{ID: "i-arch", Tool: "codex", CodexSessionID: "t-arch", ArchivedAt: now.Add(-time.Hour)}, true, noEvidence, markerArchiveArchived},
		{"superseded session", time.Hour, &session.Instance{ID: "i-sup", Tool: "codex", CodexSessionID: "t-sup", SupersededBy: "i-next"}, true, noEvidence, markerArchiveSuperseded},
		{"rebound session", time.Hour, live("i-rebound", "t-other-thread"), true, noEvidence, markerArchiveRebound},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instanceID := "i-case-" + string(rune('a'+i))
			if tc.inst != nil {
				instanceID = tc.inst.ID
			}
			codexID := "t-case-" + string(rune('a'+i))
			if tc.inst != nil && tc.verdict != markerArchiveRebound {
				codexID = tc.inst.CodexSessionID
			}
			m := prepareOldMarker(t, instanceID, codexID, tc.age)
			sessions := codexMarkerSessions{byID: map[string]*session.Instance{}, complete: tc.complete}
			if tc.inst != nil {
				sessions.byID[tc.inst.ID] = tc.inst
			}
			row := classifyCodexMarker(markerEntry(t, m), sessions, now, codexMarkersDefaultMinAge, tc.evidence)
			if row.Verdict != tc.verdict {
				t.Fatalf("verdict = %s (%s), want %s", row.Verdict, row.Detail, tc.verdict)
			}
		})
	}
}

func TestCodexMarkersApplyArchivesNeverDeletesAndRechecksTheAttempt(t *testing.T) {
	isolateMarkerHome(t)
	now := time.Now()
	gone := prepareOldMarker(t, "i-gone", "t-gone", time.Hour)       // session removed: archive
	keep := prepareOldMarker(t, "i-keep", "t-keep", time.Hour)       // no proof: leave
	young := prepareOldMarker(t, "i-young", "t-young", time.Minute)  // recent: leave
	moving := prepareOldMarker(t, "i-moving", "t-moving", time.Hour) // changes after listing
	sessions := codexMarkerSessions{complete: true, byID: map[string]*session.Instance{
		"i-keep":  {ID: "i-keep", Tool: "codex", CodexSessionID: "t-keep"},
		"i-young": {ID: "i-young", Tool: "codex", CodexSessionID: "t-young"},
	}}
	noEvidence := func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
		return nil, nil
	}
	var rows []codexMarkerRow
	for _, m := range []*session.CodexSubmissionMarker{gone, keep, young, moving} {
		rows = append(rows, classifyCodexMarker(markerEntry(t, m), sessions, now, codexMarkersDefaultMinAge, noEvidence))
	}
	// Between the listing and --apply, the "moving" attempt is updated by its
	// own sender: the cleanup must not take it.
	if err := moving.MarkTransportAmbiguous(time.Now()); err != nil {
		t.Fatal(err)
	}

	before, _ := session.ListCodexSubmissionMarkers()
	rows = applyCodexMarkerRows(rows, "cleanup-test", now, time.Second)
	after, _ := session.ListCodexSubmissionMarkers()

	byAttempt := map[string]codexMarkerRow{}
	for _, r := range rows {
		byAttempt[r.AttemptID] = r
	}
	if r := byAttempt[gone.AttemptID]; r.Verdict != markerArchiveRemoved || r.ArchivedTo == "" || r.ApplyError != "" {
		t.Fatalf("removed-session marker not archived: %+v", r)
	}
	if r := byAttempt[moving.AttemptID]; r.ArchivedTo != "" || !strings.Contains(r.ApplyError, "changed since it was listed") {
		t.Fatalf("a marker that changed after listing was taken: %+v", r)
	}
	if byAttempt[keep.AttemptID].ArchivedTo != "" || byAttempt[young.AttemptID].ArchivedTo != "" {
		t.Fatal("a leave_* marker was archived")
	}
	if len(before) != 4 || len(after) != 3 {
		t.Fatalf("live markers before=%d after=%d, want 4 -> 3", len(before), len(after))
	}
	archived, err := os.ReadFile(byAttempt[gone.AttemptID].ArchivedTo)
	if err != nil || !strings.Contains(string(archived), gone.AttemptID) {
		t.Fatalf("archived file missing or not the marker: %v", err)
	}
	f, err := os.Open(filepath.Join(filepath.Dir(byAttempt[gone.AttemptID].ArchivedTo), "MANIFEST.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	lines := 0
	for sc.Scan() {
		var line session.CodexMarkerManifestLine
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatal(err)
		}
		if line.AttemptID != gone.AttemptID || line.Reason != "codex-markers cleanup" || line.Detail != markerArchiveRemoved {
			t.Fatalf("manifest line = %+v", line)
		}
		lines++
	}
	if lines != 1 {
		t.Fatalf("manifest lines = %d, want 1", lines)
	}
}

func TestCodexMarkersReadsARealShapeVersion1Marker(t *testing.T) {
	isolateMarkerHome(t)
	// The exact field set the 2026-11-22 archive held, re-keyed.
	raw := `{"version":1,"instance_id":"0000aaaa-1790000000","codex_session_id":"01a10000-0000-7000-8000-000000000001","attempt_id":"d939aae2c64823d3d13d993c24330715","prior_turn_generation":"","phase":"submitted","created_at":"2026-10-30T05:38:13.001306162Z","updated_at":"2026-10-30T05:38:13.437390345Z"}` + "\n"
	root, err := session.CodexSubmissionArchiveRoot()
	if err != nil {
		t.Fatal(err)
	}
	liveDir := filepath.Join(filepath.Dir(root), "codex-submissions")
	m, err := session.PrepareCodexSubmissionMarker("probe", "probe-thread", "", 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_ = m
	entries, _ := session.ListCodexSubmissionMarkers()
	var probePath string
	for _, e := range entries {
		if e.Marker != nil && e.Marker.CodexSessionID == "probe-thread" {
			probePath = e.Path
		}
	}
	if probePath == "" || filepath.Dir(probePath) != liveDir {
		t.Fatalf("marker dir = %q, want %q", filepath.Dir(probePath), liveDir)
	}
	name := sessionDigestName(t, "01a10000-0000-7000-8000-000000000001")
	if err := os.WriteFile(filepath.Join(liveDir, name), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err = session.ListCodexSubmissionMarkers()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.File == name {
			found = true
			if e.ReadErr != nil || e.Marker == nil || e.Marker.Version != 1 || e.Marker.RolloutOffset != nil {
				t.Fatalf("v1 marker not read as v1: %+v", e)
			}
			row := classifyCodexMarker(e, codexMarkerSessions{complete: true, byID: map[string]*session.Instance{}}, time.Now(), codexMarkersDefaultMinAge,
				func(*session.Instance, *session.CodexSubmissionMarker) (*session.CodexDeliveryEvidence, error) {
					return nil, nil
				})
			if row.Verdict != markerArchiveRemoved {
				t.Fatalf("verdict = %s", row.Verdict)
			}
		}
	}
	if !found {
		t.Fatal("real-shape v1 marker not listed")
	}
}

// sessionDigestName is the marker file name for a Codex session id.
func sessionDigestName(t *testing.T, codexSessionID string) string {
	t.Helper()
	m, err := session.PrepareCodexSubmissionMarker("digest-probe", codexSessionID, "", 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := session.ListCodexSubmissionMarkers()
	for _, e := range entries {
		if e.Marker != nil && e.Marker.AttemptID == m.AttemptID {
			if err := os.Remove(e.Path); err != nil {
				t.Fatal(err)
			}
			return e.File
		}
	}
	t.Fatal("digest probe not listed")
	return ""
}
