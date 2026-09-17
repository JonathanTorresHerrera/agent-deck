// Patch 12: tool_data.last_prompt_at — the durable record of when a prompt
// was last submitted to this session.
//
// #1846's last_activity_at answers "when did this session last do anything",
// and it is correct at that job: it advances on ANY hook evidence. But
// SessionStart is hook evidence, so a fleet recovery stamps "active just
// now" onto every session it restarts. Measured on the live deck
// 2026-09-16: 24 of 123 sessions shared a last_activity_at inside the
// four-minute window 2026-09-13T04:44..04:47Z — the mass restart, not work.
// Every "last active" surface inherited that.
//
// last_prompt_at answers the different question the operator is actually
// asking of a 100+ session deck: when was this last WORKED ON. It advances
// only on turn-start edges, the events that mean a prompt was submitted. A
// restart fires none of them, so the record is restart-immune by
// construction rather than by heuristic.
//
// Deliberately NOT named "last user prompt": `agent-deck session send` and
// anything relayed through agent-deck-mcp fire the same UserPromptSubmit
// hook as a human typing. The record cannot distinguish them, so it does not
// claim to.
//
// Storage rides the same extras-zone mechanism as last_activity_at: merged
// into the tool_data JSON blob outside the positional MarshalToolData
// signature, so legacy binaries preserve it via MergeToolDataExtras (the key
// is not in toolDataBlob, so it is an extras key) and legacy rows read as
// zero ("unknown").
package session

import (
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/statedb"
)

const toolDataLastPromptAtKey = "last_prompt_at"

// lastPromptPersistInterval throttles the targeted DB write, matching
// lastActivityPersistInterval. Prompt edges are far rarer than general hook
// events (at most one per turn), so in practice almost every prompt flushes
// immediately; the throttle only bounds a pathological submit loop.
const lastPromptPersistInterval = 60 * time.Second

// promptHookEvents are the hook events that mean "a prompt was submitted and
// a turn is starting", normalized by normalizePromptEventKey.
//
//   - userpromptsubmit   — Claude and Codex-compatible tools
//   - beforesubmitprompt — the same edge under its alternate spelling
//   - beforeagent        — Gemini: received user input and is processing
//
// Hermes has no unambiguous prompt edge: prellmcall fires once per LLM call
// inside a turn, not once per prompt, and onsessionend fires at turn END.
// Crediting either would report the wrong time rather than no time, so
// Hermes sessions simply have no prompt record and the UI says "unknown".
// See mapEventToStatus in cmd/agent-deck/hook_handler.go for the full event
// vocabulary these are drawn from.
var promptHookEvents = map[string]bool{
	"userpromptsubmit":   true,
	"beforesubmitprompt": true,
	"beforeagent":        true,
}

// normalizePromptEventKey mirrors normalizeHookEventKey in
// cmd/agent-deck/hook_handler.go (package main, not importable from here):
// lowercase and strip the separators that differ between harnesses, so
// "UserPromptSubmit", "user_prompt_submit" and "user-prompt-submit" all
// collapse to one key.
func normalizePromptEventKey(event string) string {
	s := strings.ToLower(strings.TrimSpace(event))
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(s)
}

// isPromptHookEvent reports whether a hook event name is a turn-start edge.
func isPromptHookEvent(event string) bool {
	return promptHookEvents[normalizePromptEventKey(event)]
}

// WriteLastPromptAtToToolData merges last_prompt_at into the given tool_data
// JSON blob as a Unix-seconds integer. A zero time removes the key, keeping
// never-prompted sessions indistinguishable from rows saved by an older
// binary. Unrelated keys are preserved untouched — including
// last_activity_at and the notes blob agent-deck-mcp reads.
func WriteLastPromptAtToToolData(td json.RawMessage, t time.Time) json.RawMessage {
	m := map[string]json.RawMessage{}
	if len(td) > 0 {
		_ = json.Unmarshal(td, &m)
	}
	if !t.IsZero() {
		raw, _ := json.Marshal(t.Unix())
		m[toolDataLastPromptAtKey] = raw
	} else {
		delete(m, toolDataLastPromptAtKey)
	}
	out, _ := json.Marshal(m)
	return out
}

// ReadLastPromptAtFromToolData extracts last_prompt_at from the blob.
// Returns the zero time for missing/malformed/legacy rows — callers must
// treat that as "unknown", never as "prompted at epoch".
func ReadLastPromptAtFromToolData(td json.RawMessage) time.Time {
	if len(td) == 0 {
		return time.Time{}
	}
	var blob struct {
		LastPromptAt int64 `json:"last_prompt_at"`
	}
	_ = json.Unmarshal(td, &blob)
	if blob.LastPromptAt == 0 {
		return time.Time{}
	}
	return time.Unix(blob.LastPromptAt, 0).UTC()
}

// LastPromptAt returns the durable last-prompt timestamp: the most recent
// turn-start edge this or any previous process recorded. Zero means no
// prompt has ever been recorded (legacy row, a harness with no prompt edge,
// or genuinely never prompted). Thread-safe.
func (i *Instance) LastPromptAt() time.Time {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.lastPromptAt
}

// notePromptActivityLocked folds t into the in-memory last-prompt record,
// but only when event is a turn-start edge. Monotonic: an older timestamp
// never rewinds it. Caller must hold i.mu.
//
// Deliberately memory-only for the same reason as noteAgentActivityLocked:
// the SQLite flush lives in persistLastPrompt, which callers invoke AFTER
// releasing i.mu, because i.mu gates the TUI render path and the write can
// stall for seconds under SQLITE_BUSY.
func (i *Instance) notePromptActivityLocked(event string, t time.Time) {
	if !isPromptHookEvent(event) {
		return
	}
	if !t.IsZero() && t.After(i.lastPromptAt) {
		i.lastPromptAt = t
	}
}

// persistLastPrompt flushes the in-memory last-prompt record to SQLite.
// Caller must NOT hold i.mu. force bypasses the write throttle; pass true
// when the evidence backing the in-memory value is about to be destroyed
// (ClearHookStatus deleting the hook file).
//
// lastPromptPersistMu serializes persists per instance so two concurrent
// callers cannot write out of order; each re-snapshots the latest value once
// it holds the persist lock, so writes are strictly monotonic.
func (i *Instance) persistLastPrompt(force bool) {
	i.lastPromptPersistMu.Lock()
	defer i.lastPromptPersistMu.Unlock()

	i.mu.RLock()
	to := i.lastPromptAt
	skip := to.IsZero() || !to.After(i.lastPromptPersisted) ||
		(!force && to.Sub(i.lastPromptPersisted) < lastPromptPersistInterval)
	i.mu.RUnlock()
	if skip {
		return
	}

	db := statedb.GetGlobal()
	if db == nil {
		return
	}
	if err := db.WriteLastPromptAt(i.ID, to); err != nil {
		sessionLog.Debug("last_prompt_persist_failed",
			slog.String("instance", i.ID),
			slog.String("error", err.Error()),
		)
		return
	}

	i.mu.Lock()
	if to.After(i.lastPromptPersisted) {
		i.lastPromptPersisted = to
	}
	i.mu.Unlock()
}
