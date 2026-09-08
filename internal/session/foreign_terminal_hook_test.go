package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Terminal hook events (SessionEnd and friends) returned from UpdateHookStatus
// BEFORE the issue-#1729 ownership check, while the status bookkeeping above it
// had already been applied. A claude child that inherits AGENTDECK_INSTANCE_ID
// from the pane environment and exits from an unrelated cwd therefore wrote
// "dead" onto its parent instance, and the deck's hook fast path renders that as
// the error glyph until the session's next real hook overwrites it.
//
// Observed 2026-09-08 on a session running background agents out of /tmp: the
// instance alternated between error and running indefinitely, roughly once per
// agent exit, while tmux reported the pane alive and unchanged throughout.
//
// The cold-load path in UpdateStatus needs the same check: the file on disk
// still holds the foreign sample, so any fresh reader re-adopts it.

// foreignTerminalInstance builds an instance owning projDir, with its own hook
// status already established, plus a foreign directory to fire events from.
func foreignTerminalInstance(t *testing.T, id string) (inst *Instance, projDir, foreignDir string) {
	t.Helper()
	home := isolateHome1729(t)
	projDir = filepath.Join(home, "realproject")
	foreignDir = filepath.Join(home, "fake-tmpdir", "T")
	for _, d := range []string{projDir, foreignDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	inst = &Instance{
		ID:          id,
		Title:       "agents",
		ProjectPath: projDir,
		GroupPath:   DefaultGroupPath,
		Tool:        "claude",
		Status:      StatusRunning,
		CreatedAt:   time.Now(),
	}
	inst.UpdateHookStatus(&HookStatus{
		Status:    "running",
		SessionID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
		Event:     "UserPromptSubmit",
		UpdatedAt: time.Now().Add(-time.Second),
		Cwd:       projDir,
	})
	return inst, projDir, foreignDir
}

// TestUpdateHookStatus_ForeignCwdTerminalEventDoesNotKillStatus is the core
// regression: a SessionEnd from a cwd the instance does not own must leave the
// live status alone.
func TestUpdateHookStatus_ForeignCwdTerminalEventDoesNotKillStatus(t *testing.T) {
	inst, _, foreignDir := foreignTerminalInstance(t, "inst-foreign-terminal")

	inst.UpdateHookStatus(&HookStatus{
		Status:    "dead",
		SessionID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Event:     "SessionEnd",
		UpdatedAt: time.Now(),
		Cwd:       foreignDir,
	})

	if inst.hookStatus != "running" {
		t.Fatalf("a foreign ephemeral's SessionEnd stuck: hookStatus = %q, want the restored %q",
			inst.hookStatus, "running")
	}
	if inst.hookEvent != "UserPromptSubmit" {
		t.Fatalf("hookEvent not restored: got %q, want %q", inst.hookEvent, "UserPromptSubmit")
	}
}

// TestUpdateHookStatus_OwnedTerminalEventStillMarksDead is the other half: a
// SessionEnd from the instance's OWN directory is the real thing and must still
// register, or a genuinely finished session would look alive forever.
func TestUpdateHookStatus_OwnedTerminalEventStillMarksDead(t *testing.T) {
	inst, projDir, _ := foreignTerminalInstance(t, "inst-owned-terminal")

	inst.UpdateHookStatus(&HookStatus{
		Status:    "dead",
		SessionID: "cccccccc-cccc-cccc-cccc-cccccccccccc",
		Event:     "SessionEnd",
		UpdatedAt: time.Now(),
		Cwd:       projDir,
	})

	if inst.hookStatus != "dead" {
		t.Fatalf("the instance's own SessionEnd was swallowed: hookStatus = %q, want %q",
			inst.hookStatus, "dead")
	}
}

// TestUpdateHookStatus_TerminalEventWithoutCwdStillApplies pins the legacy
// path: hook files that carry no cwd (older writers, agents that send none) are
// not evidence of foreignness and must keep working as before.
func TestUpdateHookStatus_TerminalEventWithoutCwdStillApplies(t *testing.T) {
	inst, _, _ := foreignTerminalInstance(t, "inst-nocwd-terminal")

	inst.UpdateHookStatus(&HookStatus{
		Status:    "dead",
		SessionID: "dddddddd-dddd-dddd-dddd-dddddddddddd",
		Event:     "SessionEnd",
		UpdatedAt: time.Now(),
		Cwd:       "",
	})

	if inst.hookStatus != "dead" {
		t.Fatalf("a cwd-less terminal event was blocked: hookStatus = %q, want %q",
			inst.hookStatus, "dead")
	}
}

// TestUpdateHookStatus_TerminalEventFromSubdirStillApplies guards against
// over-blocking: a session that cd'd deeper inside its own tree still owns the
// event.
func TestUpdateHookStatus_TerminalEventFromSubdirStillApplies(t *testing.T) {
	inst, projDir, _ := foreignTerminalInstance(t, "inst-subdir-terminal")
	subDir := filepath.Join(projDir, "sub", "repo")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	inst.UpdateHookStatus(&HookStatus{
		Status:    "dead",
		SessionID: "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee",
		Event:     "SessionEnd",
		UpdatedAt: time.Now(),
		Cwd:       subDir,
	})

	if inst.hookStatus != "dead" {
		t.Fatalf("a terminal event from an owned subdir was blocked: hookStatus = %q, want %q",
			inst.hookStatus, "dead")
	}
}
