// Patch 12: tool_data.last_prompt_at — a durable record of the last time a
// PROMPT was submitted to this session, kept separate from #1846's
// last_activity_at.
//
// Why a second record at all: last_activity_at advances on ANY hook
// evidence, and SessionStart is hook evidence. A fleet recovery therefore
// stamps "active just now" onto every session it restarts — measured on the
// live deck 2026-09-16, where 24 of 123 sessions shared a last_activity_at
// inside the four-minute window 2026-09-13T04:44..04:47Z, which was the mass
// restart, not anybody working. The "when was this last worked on" surfaces
// inherited that lie.
//
// last_prompt_at advances ONLY on turn-start edges — the events that mean a
// prompt was submitted (UserPromptSubmit / BeforeSubmitPrompt for Claude and
// Codex-compatible tools, BeforeAgent for Gemini). A restart fires none of
// them, which is the property these tests pin.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

// readLastPromptAtFromDB returns tool_data.last_prompt_at for the given
// instance via a fresh LoadInstances query — what a restarted TUI would see.
func readLastPromptAtFromDB(t *testing.T, db *statedb.StateDB, id string) time.Time {
	t.Helper()
	rows, err := db.LoadInstances()
	if err != nil {
		t.Fatalf("LoadInstances: %v", err)
	}
	for _, row := range rows {
		if row.ID != id {
			continue
		}
		return ReadLastPromptAtFromToolData(row.ToolData)
	}
	t.Fatalf("instance %q not found in DB", id)
	return time.Time{}
}

// TestLastPromptAt_ToolDataPersistenceRoundTrip mirrors the last_activity_at
// round-trip: the extras-zone helpers round-trip, preserve unrelated keys
// (notably claude_session_id and the notes blob agent-deck-mcp reads), and a
// zero time clears the key so legacy rows stay indistinguishable from
// never-prompted ones.
func TestLastPromptAt_ToolDataPersistenceRoundTrip(t *testing.T) {
	at := time.Date(2026, 9, 16, 14, 32, 0, 0, time.UTC)

	td := WriteLastPromptAtToToolData(nil, at)
	if got := ReadLastPromptAtFromToolData(td); !got.Equal(at) {
		t.Fatalf("ReadLastPromptAtFromToolData after Write = %v, want %v", got, at)
	}

	// Unrelated keys survive. last_activity_at in particular must not be
	// disturbed: the two records coexist in one blob.
	seeded := json.RawMessage(`{"claude_session_id":"abc","last_activity_at":123,"notes":"keep me"}`)
	merged := WriteLastPromptAtToToolData(seeded, at)
	var blob map[string]json.RawMessage
	if err := json.Unmarshal(merged, &blob); err != nil {
		t.Fatalf("unmarshal merged tool_data: %v", err)
	}
	for _, k := range []string{"claude_session_id", "last_activity_at", "notes"} {
		if _, ok := blob[k]; !ok {
			t.Fatalf("WriteLastPromptAtToToolData dropped key %q; blob=%s", k, merged)
		}
	}
	if got := ReadLastPromptAtFromToolData(merged); !got.Equal(at) {
		t.Fatalf("merged blob read back = %v, want %v", got, at)
	}

	// A legacy row (no key) reads as zero — "unknown", never epoch.
	if got := ReadLastPromptAtFromToolData(json.RawMessage(`{"claude_session_id":"abc"}`)); !got.IsZero() {
		t.Fatalf("legacy row read = %v, want zero", got)
	}
	if got := ReadLastPromptAtFromToolData(nil); !got.IsZero() {
		t.Fatalf("nil tool_data read = %v, want zero", got)
	}

	// Zero clears rather than writing 0. Unmarshal into a FRESH map:
	// json.Unmarshal merges into a non-nil map rather than replacing it, so
	// reusing blob here would keep the key from the assertion above and the
	// test would fail against a correct implementation.
	cleared := WriteLastPromptAtToToolData(merged, time.Time{})
	clearedBlob := map[string]json.RawMessage{}
	if err := json.Unmarshal(cleared, &clearedBlob); err != nil {
		t.Fatalf("unmarshal cleared tool_data: %v", err)
	}
	if _, ok := clearedBlob["last_prompt_at"]; ok {
		t.Fatalf("zero time should remove last_prompt_at; blob=%s", cleared)
	}
	// Clearing must not take the neighbours with it.
	for _, k := range []string{"claude_session_id", "last_activity_at", "notes"} {
		if _, ok := clearedBlob[k]; !ok {
			t.Fatalf("clearing last_prompt_at dropped key %q; blob=%s", k, cleared)
		}
	}
}

// TestIsPromptHookEvent_ClassifiesTurnStartEdgesOnly is the semantic core of
// the patch. SessionStart is the one that matters most: it is what a fleet
// recovery fires, and treating it as a prompt would reproduce exactly the
// bug this record exists to avoid.
func TestIsPromptHookEvent_ClassifiesTurnStartEdgesOnly(t *testing.T) {
	prompts := []string{
		"UserPromptSubmit",
		"user_prompt_submit",
		"userpromptsubmit",
		"USERPROMPTSUBMIT",
		"  UserPromptSubmit  ",
		"BeforeSubmitPrompt",
		"before-submit-prompt",
		"BeforeAgent", // Gemini: received user input and is processing
	}
	for _, ev := range prompts {
		if !isPromptHookEvent(ev) {
			t.Errorf("isPromptHookEvent(%q) = false, want true", ev)
		}
	}

	notPrompts := []string{
		"SessionStart", // a restart — the whole reason this record exists
		"session_start",
		"SessionEnd",
		"Stop",
		"PreToolUse",
		"PostToolUse",
		"PreCompact",
		"Notification",
		"PermissionRequest",
		"PreApiRequest",
		"AfterAgent",
		"PreLLMCall", // Hermes fires this per LLM call, not per prompt
		"",
	}
	for _, ev := range notPrompts {
		if isPromptHookEvent(ev) {
			t.Errorf("isPromptHookEvent(%q) = true, want false", ev)
		}
	}
}

// TestUpdateHookStatus_AdvancesLastPromptAtOnUserPromptSubmit: an accepted
// prompt edge advances the record in memory AND in the DB row, so a later
// TUI restart still sees it.
func TestUpdateHookStatus_AdvancesLastPromptAtOnUserPromptSubmit(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmpHome, ".claude"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	db := withTempGlobalStateDB(t)

	projectPath := filepath.Join(tmpHome, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	inst := NewInstanceWithTool("prompt-advance", projectPath, "claude")
	sessionID := "5ea244ce-0000-0000-0000-0000000000b1"
	inst.ClaudeSessionID = sessionID
	seedInstanceRow(t, db, inst, `{"claude_session_id":"`+sessionID+`"}`)

	eventAt := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	inst.UpdateHookStatus(&HookStatus{
		Status:    "running",
		Event:     "UserPromptSubmit",
		SessionID: sessionID,
		UpdatedAt: eventAt,
		Cwd:       projectPath,
	})

	if got := inst.LastPromptAt(); !got.Equal(eventAt) {
		t.Fatalf("LastPromptAt after prompt edge = %v, want %v", got, eventAt)
	}
	if got := readLastPromptAtFromDB(t, db, inst.ID); !got.Equal(eventAt) {
		t.Fatalf("DB last_prompt_at after prompt edge = %v, want %v", got, eventAt)
	}
}

// TestUpdateHookStatus_SessionStartDoesNotAdvanceLastPromptAt is the
// regression test for the defect that motivated the patch: a fleet recovery
// restarts a session, SessionStart fires, last_activity_at advances (that is
// correct and unchanged) — but last_prompt_at must NOT move, because nobody
// prompted anything.
func TestUpdateHookStatus_SessionStartDoesNotAdvanceLastPromptAt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmpHome, ".claude"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	withTempGlobalStateDB(t)

	projectPath := filepath.Join(tmpHome, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	inst := NewInstanceWithTool("prompt-restart-immune", projectPath, "claude")
	sessionID := "5ea244ce-0000-0000-0000-0000000000b2"
	inst.ClaudeSessionID = sessionID

	promptAt := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	restartAt := time.Now().Add(-1 * time.Minute).Truncate(time.Second)

	inst.UpdateHookStatus(&HookStatus{Status: "running", Event: "UserPromptSubmit", SessionID: sessionID, UpdatedAt: promptAt, Cwd: projectPath})
	inst.UpdateHookStatus(&HookStatus{Status: "waiting", Event: "SessionStart", SessionID: sessionID, UpdatedAt: restartAt, Cwd: projectPath})

	if got := inst.LastPromptAt(); !got.Equal(promptAt) {
		t.Fatalf("SessionStart moved LastPromptAt to %v, want it held at %v", got, promptAt)
	}
	// The general activity record SHOULD have advanced — the two records are
	// deliberately different questions, and this asserts we did not break the
	// existing one.
	if got := inst.LastActivityAt(); !got.Equal(restartAt) {
		t.Fatalf("LastActivityAt after restart = %v, want %v", got, restartAt)
	}
}

// TestUpdateHookStatus_StaleReplayDoesNotRewindLastPromptAt: the watcher
// re-applies the same on-disk file on rescans; an older prompt edge must
// never pull the record backwards.
func TestUpdateHookStatus_StaleReplayDoesNotRewindLastPromptAt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmpHome, ".claude"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	withTempGlobalStateDB(t)

	projectPath := filepath.Join(tmpHome, "project")
	if err := os.MkdirAll(projectPath, 0o755); err != nil {
		t.Fatalf("mkdir project: %v", err)
	}
	inst := NewInstanceWithTool("prompt-rewind", projectPath, "claude")
	sessionID := "5ea244ce-0000-0000-0000-0000000000b3"
	inst.ClaudeSessionID = sessionID

	newer := time.Now().Add(-1 * time.Minute).Truncate(time.Second)
	older := newer.Add(-30 * time.Minute)

	inst.UpdateHookStatus(&HookStatus{Status: "running", Event: "UserPromptSubmit", SessionID: sessionID, UpdatedAt: newer, Cwd: projectPath})
	inst.UpdateHookStatus(&HookStatus{Status: "running", Event: "UserPromptSubmit", SessionID: sessionID, UpdatedAt: older, Cwd: projectPath})

	if got := inst.LastPromptAt(); !got.Equal(newer) {
		t.Fatalf("stale replay rewound LastPromptAt to %v, want %v", got, newer)
	}
}

// TestUpdateHookStatus_ForeignRejectDoesNotAdvanceLastPromptAt: a foreign
// ephemeral (a `claude -p` child that inherited AGENTDECK_INSTANCE_ID, with a
// cwd provably outside the instance's paths) is restored to a no-op by the
// #1729 guard. Its prompt must not be credited to this instance either —
// this is why the fold reads the COMMITTED hook fields in a defer rather
// than the incoming status argument.
func TestUpdateHookStatus_ForeignRejectDoesNotAdvanceLastPromptAt(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(tmpHome, ".claude"))
	ClearUserConfigCache()
	t.Cleanup(ClearUserConfigCache)

	withTempGlobalStateDB(t)

	projectPath := filepath.Join(tmpHome, "project")
	foreignPath := filepath.Join(tmpHome, "elsewhere")
	for _, p := range []string{projectPath, foreignPath} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
	}
	inst := NewInstanceWithTool("prompt-foreign", projectPath, "claude")
	sessionID := "5ea244ce-0000-0000-0000-0000000000b4"
	inst.ClaudeSessionID = sessionID

	inst.UpdateHookStatus(&HookStatus{
		Status:    "running",
		Event:     "UserPromptSubmit",
		SessionID: "5ea244ce-0000-0000-0000-00000000ffff",
		UpdatedAt: time.Now().Truncate(time.Second),
		Cwd:       foreignPath,
	})

	if got := inst.LastPromptAt(); !got.IsZero() {
		t.Fatalf("foreign prompt edge advanced LastPromptAt to %v, want zero", got)
	}
}
