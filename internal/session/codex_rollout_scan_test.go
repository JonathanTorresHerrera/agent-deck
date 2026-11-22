package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Synthetic rollouts only: every record below is generated, no real Codex
// content (WO-DECK-CODEX-DELIVERY-01).

type rolloutBuilder struct {
	t     *testing.T
	lines []string
}

func (b *rolloutBuilder) add(record map[string]interface{}) *rolloutBuilder {
	b.t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		b.t.Fatal(err)
	}
	b.lines = append(b.lines, string(data))
	return b
}

func rolloutTS(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (b *rolloutBuilder) start(at time.Time, turn string) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "event_msg",
		"payload": map[string]interface{}{"type": "task_started", "turn_id": turn}})
}

func (b *rolloutBuilder) complete(at time.Time, turn string) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "event_msg",
		"payload": map[string]interface{}{"type": "task_complete", "turn_id": turn, "last_agent_message": "done"}})
}

func (b *rolloutBuilder) aborted(at time.Time, turn string) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "event_msg",
		"payload": map[string]interface{}{"type": "turn_aborted", "turn_id": turn}})
}

func (b *rolloutBuilder) user(at time.Time, text string) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "response_item",
		"payload": map[string]interface{}{"type": "message", "role": "user",
			"content": []map[string]string{{"type": "input_text", "text": text}}}})
}

func (b *rolloutBuilder) userEvent(at time.Time, text string) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "event_msg",
		"payload": map[string]interface{}{"type": "user_message", "message": text}})
}

func (b *rolloutBuilder) work(at time.Time, outputBytes int) *rolloutBuilder {
	return b.add(map[string]interface{}{"timestamp": rolloutTS(at), "type": "response_item",
		"payload": map[string]interface{}{"type": "function_call_output", "call_id": "call-synthetic",
			"output": strings.Repeat("x", outputBytes)}})
}

func (b *rolloutBuilder) bytes() []byte { return []byte(strings.Join(b.lines, "\n") + "\n") }

func newRollout(t *testing.T) *rolloutBuilder { return &rolloutBuilder{t: t} }

// writeCodexRollout installs content as the exact rollout of sessionID under
// an isolated CODEX_HOME and returns the bound instance and the path.
func installSyntheticRollout(t *testing.T, sessionID string, content []byte) (*Instance, string) {
	t.Helper()
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		home = t.TempDir()
		t.Setenv("CODEX_HOME", home)
	}
	path := filepath.Join(home, "sessions", "2026", "11", "22", "rollout-2026-11-22T10-00-00-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return &Instance{ID: "inst-" + sessionID, Tool: "codex", CodexSessionID: sessionID}, path
}

func appendRollout(t *testing.T, path string, b *rolloutBuilder) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(b.bytes()); err != nil {
		t.Fatal(err)
	}
}

var rolloutT0 = time.Date(2026, 11, 22, 18, 0, 0, 0, time.UTC)

func TestCodexTurnStateFindsAStartFarBeyondTheOldWindow(t *testing.T) {
	// The two wedged sessions of 2026-11-22 had their only turn start 13 and
	// 16 MB before EOF. Build 12 MiB of one turn's tool output after it.
	b := newRollout(t).start(rolloutT0, "turn-early").user(rolloutT0.Add(100*time.Millisecond), "first prompt")
	for i := 0; i < 12*1024; i++ {
		b.work(rolloutT0.Add(time.Duration(i)*time.Millisecond), 1000)
	}
	b.complete(rolloutT0.Add(30*time.Second), "turn-early")
	inst, path := installSyntheticRollout(t, "thread-long-turn", b.bytes())
	if info, _ := os.Stat(path); info.Size() < 12<<20 {
		t.Fatalf("fixture too small: %d", info.Size())
	}
	state, err := inst.CodexRolloutTurnState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Generation != "thread-long-turn:turn-early" || state.TurnOpen || !state.RolloutPresent {
		t.Fatalf("state = %+v, want the far start, closed", state)
	}
	if info, _ := os.Stat(path); state.RolloutOffset != info.Size() {
		t.Fatalf("rollout offset = %d, want %d", state.RolloutOffset, info.Size())
	}
	generation, err := inst.LatestCodexTurnGeneration()
	if err != nil || generation != "thread-long-turn:turn-early" {
		t.Fatalf("LatestCodexTurnGeneration = %q, %v", generation, err)
	}
}

func TestCodexTurnStateOpenTurnAndPrompt(t *testing.T) {
	cases := []struct {
		name       string
		build      func(*rolloutBuilder)
		open, seen bool
	}{
		{"just started", func(b *rolloutBuilder) { b.start(rolloutT0, "t1") }, true, false},
		{"prompt written", func(b *rolloutBuilder) { b.start(rolloutT0, "t1").user(rolloutT0.Add(time.Second), "prompt") }, true, true},
		{"injected only", func(b *rolloutBuilder) {
			b.start(rolloutT0, "t1").user(rolloutT0, "# AGENTS.md instructions for /tmp/x\n\n<INSTRUCTIONS>").user(rolloutT0, "<environment_context>\n</environment_context>")
		}, true, false},
		{"completed", func(b *rolloutBuilder) {
			b.start(rolloutT0, "t1").user(rolloutT0, "p").complete(rolloutT0.Add(time.Second), "t1")
		}, false, false},
		{"aborted", func(b *rolloutBuilder) {
			b.start(rolloutT0, "t1").user(rolloutT0, "p").aborted(rolloutT0.Add(time.Second), "t1")
		}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newRollout(t)
			tc.build(b)
			inst, _ := installSyntheticRollout(t, "thread-"+strings.ReplaceAll(tc.name, " ", "-"), b.bytes())
			state, err := inst.CodexRolloutTurnState()
			if err != nil {
				t.Fatal(err)
			}
			if state.TurnOpen != tc.open || state.PromptSeen != tc.seen {
				t.Fatalf("state = %+v, want open=%v promptSeen=%v", state, tc.open, tc.seen)
			}
		})
	}
}

func TestCodexTurnStateSkipsARecordStillBeingWritten(t *testing.T) {
	content := append(newRollout(t).start(rolloutT0, "t-complete").bytes(), []byte(`{"timestamp":"2026-11-22T18:00:01.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"t-partial"`)...)
	inst, _ := installSyntheticRollout(t, "thread-partial", content)
	state, err := inst.CodexRolloutTurnState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Generation != "thread-partial:t-complete" {
		t.Fatalf("generation = %q, want the last complete start", state.Generation)
	}
	if want := int64(len(newRollout(t).start(rolloutT0, "t-complete").bytes())); state.RolloutOffset != want {
		t.Fatalf("offset = %d, want the end of the last complete record %d", state.RolloutOffset, want)
	}
}

func TestCodexTurnStateReadsALineLongerThanAScanChunk(t *testing.T) {
	b := newRollout(t).start(rolloutT0, "t-big").user(rolloutT0.Add(time.Second), strings.Repeat("y", int(3*codexRolloutScanChunkMin)))
	inst, _ := installSyntheticRollout(t, "thread-bigline", b.bytes())
	state, err := inst.CodexRolloutTurnState()
	if err != nil {
		t.Fatal(err)
	}
	if state.Generation != "thread-bigline:t-big" || !state.TurnOpen || !state.PromptSeen {
		t.Fatalf("state = %+v", state)
	}
}

func evidenceFor(t *testing.T, inst *Instance, cut int64, notBefore time.Time) *CodexDeliveryEvidence {
	t.Helper()
	ev, err := inst.CodexDeliveryEvidenceSince(cut, notBefore)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestCodexDeliveryEvidence(t *testing.T) {
	marker := rolloutT0.Add(10 * time.Second)

	t.Run("a turn that starts after the cut", func(t *testing.T) {
		before := newRollout(t).start(rolloutT0, "t-old").user(rolloutT0, "p").complete(rolloutT0.Add(time.Second), "t-old")
		inst, path := installSyntheticRollout(t, "thread-ev-new", before.bytes())
		cut := int64(len(before.bytes()))
		if ev := evidenceFor(t, inst, cut, marker); ev != nil {
			t.Fatalf("evidence before anything happened: %+v", ev)
		}
		appendRollout(t, path, newRollout(t).start(marker.Add(200*time.Millisecond), "t-new").user(marker.Add(300*time.Millisecond), "ours"))
		ev := evidenceFor(t, inst, cut, marker)
		if ev == nil || ev.Kind != CodexEvidenceNewTurn || ev.TurnGeneration != "thread-ev-new:t-new" || ev.Basis != CodexEvidenceBasisOffset {
			t.Fatalf("evidence = %+v, want new_turn t-new on the offset basis", ev)
		}
	})

	t.Run("input folded into a running turn", func(t *testing.T) {
		// The 2026-11-18 shape: the turn and its prompt predate the marker,
		// the send's text lands 4 s later with no new turn.
		before := newRollout(t).start(marker.Add(-470*time.Millisecond), "t-run").user(marker.Add(-400*time.Millisecond), "turn prompt").work(marker.Add(-100*time.Millisecond), 10)
		inst, path := installSyntheticRollout(t, "thread-ev-fold", before.bytes())
		cut := int64(len(before.bytes()))
		appendRollout(t, path, newRollout(t).work(marker.Add(3*time.Second), 10).user(marker.Add(4*time.Second), "ours, folded"))
		ev := evidenceFor(t, inst, cut, marker)
		if ev == nil || ev.Kind != CodexEvidenceInputFolded || ev.TurnGeneration != "" {
			t.Fatalf("evidence = %+v, want input_folded", ev)
		}
	})

	t.Run("the running turn's own opening prompt is not evidence", func(t *testing.T) {
		// The 2026-11-10 shape: the turn started 71 ms before the marker and
		// wrote its own prompt 0.5 s after it.
		before := newRollout(t).start(marker.Add(-71*time.Millisecond), "t-opening")
		inst, path := installSyntheticRollout(t, "thread-ev-opening", before.bytes())
		cut := int64(len(before.bytes()))
		appendRollout(t, path, newRollout(t).
			user(marker.Add(400*time.Millisecond), "# AGENTS.md instructions for /tmp/x").
			user(marker.Add(500*time.Millisecond), "the turn's own prompt").
			work(marker.Add(time.Second), 10))
		if ev := evidenceFor(t, inst, cut, marker); ev != nil {
			t.Fatalf("opening prompt counted as delivery: %+v", ev)
		}
		appendRollout(t, path, newRollout(t).user(marker.Add(5*time.Second), "ours, folded later"))
		if ev := evidenceFor(t, inst, cut, marker); ev == nil || ev.Kind != CodexEvidenceInputFolded {
			t.Fatalf("evidence = %+v, want the later fold", ev)
		}
	})

	t.Run("context Codex injects is never evidence", func(t *testing.T) {
		before := newRollout(t).start(rolloutT0, "t-run").user(rolloutT0, "prompt").work(rolloutT0, 10)
		inst, path := installSyntheticRollout(t, "thread-ev-injected", before.bytes())
		cut := int64(len(before.bytes()))
		appendRollout(t, path, newRollout(t).
			user(marker.Add(time.Second), "# AGENTS.md instructions for /tmp/x\n\n<INSTRUCTIONS>").
			user(marker.Add(time.Second), "<environment_context>\n  <cwd>/tmp/x</cwd>\n</environment_context>").
			user(marker.Add(time.Second), "<codex_internal_context>synthetic</codex_internal_context>").
			userEvent(marker.Add(time.Second), "<turn_aborted>synthetic</turn_aborted>"))
		if ev := evidenceFor(t, inst, cut, marker); ev != nil {
			t.Fatalf("injected context counted as delivery: %+v", ev)
		}
	})

	t.Run("a legacy user_message event folds too", func(t *testing.T) {
		before := newRollout(t).start(rolloutT0, "t-run").userEvent(rolloutT0, "prompt").work(rolloutT0, 10)
		inst, path := installSyntheticRollout(t, "thread-ev-event", before.bytes())
		cut := int64(len(before.bytes()))
		appendRollout(t, path, newRollout(t).userEvent(marker.Add(time.Second), "ours"))
		if ev := evidenceFor(t, inst, cut, marker); ev == nil || ev.Kind != CodexEvidenceInputFolded {
			t.Fatalf("evidence = %+v, want input_folded", ev)
		}
	})

	t.Run("version 1 markers cut by created_at", func(t *testing.T) {
		b := newRollout(t).
			start(marker.Add(-470*time.Millisecond), "t-run").
			user(marker.Add(-400*time.Millisecond), "turn prompt").
			work(marker.Add(time.Second), 10).
			user(marker.Add(4*time.Second), "ours, folded")
		inst, _ := installSyntheticRollout(t, "thread-ev-v1", b.bytes())
		ev := evidenceFor(t, inst, -1, marker)
		if ev == nil || ev.Kind != CodexEvidenceInputFolded || ev.Basis != CodexEvidenceBasisCreatedAt {
			t.Fatalf("evidence = %+v, want input_folded on the created_at basis", ev)
		}
		quiet := newRollout(t).start(marker.Add(-470*time.Millisecond), "t-run").user(marker.Add(-400*time.Millisecond), "turn prompt").work(marker.Add(time.Second), 10)
		inst2, _ := installSyntheticRollout(t, "thread-ev-v1-quiet", quiet.bytes())
		if ev := evidenceFor(t, inst2, -1, marker); ev != nil {
			t.Fatalf("evidence = %+v, want none", ev)
		}
	})

	t.Run("an offset that does not describe the file falls back to created_at", func(t *testing.T) {
		b := newRollout(t).start(rolloutT0, "t-old").user(rolloutT0, "p").complete(rolloutT0, "t-old").start(marker.Add(time.Second), "t-new")
		inst, _ := installSyntheticRollout(t, "thread-ev-badoffset", b.bytes())
		ev := evidenceFor(t, inst, 1<<40, marker)
		if ev == nil || ev.Kind != CodexEvidenceNewTurn || ev.Basis != CodexEvidenceBasisCreatedAt || ev.TurnGeneration != "thread-ev-badoffset:t-new" {
			t.Fatalf("evidence = %+v", ev)
		}
	})

	t.Run("an old record after the offset never attributes", func(t *testing.T) {
		// Offset 0 on a rollout that predates the marker (for example a
		// replaced file): the timestamp floor rejects the old start.
		b := newRollout(t).start(rolloutT0, "t-old").user(rolloutT0, "p").work(rolloutT0, 10)
		inst, _ := installSyntheticRollout(t, "thread-ev-floor", b.bytes())
		if ev := evidenceFor(t, inst, 0, marker); ev != nil {
			t.Fatalf("old start attributed: %+v", ev)
		}
	})

	t.Run("no rollout yet holds no evidence", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("CODEX_HOME", home)
		inst := &Instance{ID: "inst-x", Tool: "codex", CodexSessionID: "thread-ev-absent"}
		if ev := evidenceFor(t, inst, 0, marker); ev != nil {
			t.Fatalf("evidence without a rollout: %+v", ev)
		}
	})
}

func TestCodexMarkerReconcilesOnFoldedInputAndArchives(t *testing.T) {
	isolateCodexSubmissionMarkers(t)
	marker := time.Now().UTC()
	before := newRollout(t).start(marker.Add(-2*time.Second), "t-run").user(marker.Add(-2*time.Second), "turn prompt").work(marker.Add(-time.Second), 10)
	inst, path := installSyntheticRollout(t, "thread-reconcile-fold", before.bytes())
	state, err := inst.CodexRolloutTurnState()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareCodexSubmissionMarker(inst.ID, inst.CodexSessionID, state.Generation, state.RolloutOffset, marker)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.MarkSubmitted(marker.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	markerPath, _ := codexSubmissionMarkerPath(inst.CodexSessionID)

	// Today's wedge: same generation, no evidence yet: refuse, keep the marker.
	_, err = ReconcileCodexSubmissionMarkerWithEvidence(inst.ID, inst.CodexSessionID, state.Generation, inst.CodexDeliveryEvidenceSince, time.Now())
	if !errors.Is(err, ErrUnresolvedCodexSubmission) {
		t.Fatalf("err = %v, want unresolved", err)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("marker removed without evidence: %v", statErr)
	}

	// The send's text folds into the running turn: the marker resolves.
	appendRollout(t, path, newRollout(t).user(marker.Add(4*time.Second), "ours, folded"))
	resolution, err := ReconcileCodexSubmissionMarkerWithEvidence(inst.ID, inst.CodexSessionID, state.Generation, inst.CodexDeliveryEvidenceSince, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resolution == nil || resolution.Evidence == nil || resolution.Evidence.Kind != CodexEvidenceInputFolded || resolution.AttemptID != prepared.AttemptID {
		t.Fatalf("resolution = %+v", resolution)
	}
	if _, statErr := os.Stat(markerPath); !os.IsNotExist(statErr) {
		t.Fatalf("resolved marker still live: %v", statErr)
	}
	archived, err := os.ReadFile(resolution.ArchivedTo)
	if err != nil {
		t.Fatalf("archived marker missing: %v", err)
	}
	if !strings.Contains(string(archived), prepared.AttemptID) {
		t.Fatalf("archived file is not the marker: %s", archived)
	}
	manifest, err := os.ReadFile(filepath.Join(filepath.Dir(resolution.ArchivedTo), "MANIFEST.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var line CodexMarkerManifestLine
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(manifest))), &line); err != nil {
		t.Fatal(err)
	}
	if line.Reason != "reconcile" || line.AttemptID != prepared.AttemptID || line.Evidence == nil || line.Evidence.Kind != CodexEvidenceInputFolded {
		t.Fatalf("manifest line = %+v", line)
	}
	if strings.Contains(string(manifest), "ours, folded") || strings.Contains(string(manifest), "turn prompt") {
		t.Fatalf("manifest carries content: %s", manifest)
	}
}

func TestCodexMarkerEmptyPriorNeedsEvidenceNotJustAGeneration(t *testing.T) {
	isolateCodexSubmissionMarkers(t)
	marker := time.Now().UTC()
	// A "" prior from the old 8 MiB window on a session whose real newest
	// turn predates the marker: a different generation alone is no proof.
	inst, path := installSyntheticRollout(t, "thread-empty-prior", newRollout(t).start(marker.Add(-time.Hour), "t-ancient").user(marker.Add(-time.Hour), "p").work(marker.Add(-time.Hour), 10).bytes())
	raw := `{"version":1,"instance_id":"` + inst.ID + `","codex_session_id":"thread-empty-prior","attempt_id":"0123456789abcdef0123456789abcdef","prior_turn_generation":"","phase":"submitted","created_at":"` + marker.Format(time.RFC3339Nano) + `","updated_at":"` + marker.Add(400*time.Millisecond).Format(time.RFC3339Nano) + `"}` + "\n"
	writeRawCodexMarker(t, "thread-empty-prior", raw)
	_, err := ReconcileCodexSubmissionMarkerWithEvidence(inst.ID, inst.CodexSessionID, "thread-empty-prior:t-ancient", inst.CodexDeliveryEvidenceSince, time.Now())
	if !errors.Is(err, ErrUnresolvedCodexSubmission) {
		t.Fatalf("err = %v, want unresolved: the ancient turn is not this attempt", err)
	}
	// The real case: the first turn started 258 ms after the marker.
	appendRollout(t, path, newRollout(t).start(marker.Add(258*time.Millisecond), "t-first"))
	resolution, err := ReconcileCodexSubmissionMarkerWithEvidence(inst.ID, inst.CodexSessionID, "thread-empty-prior:t-first", inst.CodexDeliveryEvidenceSince, time.Now())
	if err != nil || resolution == nil || resolution.Evidence.Kind != CodexEvidenceNewTurn || resolution.Evidence.Basis != CodexEvidenceBasisCreatedAt {
		t.Fatalf("resolution = %+v, err = %v", resolution, err)
	}
}

// writeRawCodexMarker installs a marker exactly as an earlier build wrote it
// (real shape, synthetic identities).
func writeRawCodexMarker(t *testing.T, sessionID, raw string) {
	t.Helper()
	path, err := codexSubmissionMarkerPath(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexMarkerVersionRules(t *testing.T) {
	isolateCodexSubmissionMarkers(t)
	base := `"instance_id":"inst-v","codex_session_id":"thread-version","attempt_id":"0123456789abcdef0123456789abcdef","prior_turn_generation":"","phase":"submitted","created_at":"2026-11-22T18:00:00Z","updated_at":"2026-11-22T18:00:01Z"`
	cases := []struct {
		name string
		raw  string
		ok   bool
	}{
		{"v1 without offset", `{"version":1,` + base + `}`, true},
		{"v2 with offset", `{"version":2,` + base + `,"rollout_offset":123}`, true},
		{"v2 with zero offset", `{"version":2,` + base + `,"rollout_offset":0}`, true},
		{"v1 with offset", `{"version":1,` + base + `,"rollout_offset":123}`, false},
		{"v2 without offset", `{"version":2,` + base + `}`, false},
		{"v2 negative offset", `{"version":2,` + base + `,"rollout_offset":-1}`, false},
		{"v3", `{"version":3,` + base + `,"rollout_offset":1}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeRawCodexMarker(t, "thread-version", tc.raw+"\n")
			_, found, err := readCodexSubmissionMarker("thread-version")
			if tc.ok && (err != nil || !found) {
				t.Fatalf("read failed: %v", err)
			}
			if !tc.ok && err == nil {
				t.Fatal("invalid marker accepted")
			}
		})
	}
}
