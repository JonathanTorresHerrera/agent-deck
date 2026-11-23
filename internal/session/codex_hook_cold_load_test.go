package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCodexHookStatusOwnership(t *testing.T) {
	skipIfNoTmuxBinary(t)
	for _, mode := range []string{"cold", "cached", "rejected-cache"} {
		// user: a sibling user thread with no evidence that it is this
		// session's (the shared app-server daemon files a sibling's turn under
		// the first TUI's environment). user-pane: the notify writer saw this
		// pane run it (/new or /resume in the pane).
		for _, source := range []string{"subagent", "unbacked", "user", "user-pane"} {
			t.Run(mode+"/"+source, func(t *testing.T) {
				inst, codexHome := newCodexGateInstance(t)
				root, candidate := uniqueSID(t), uniqueSID(t)
				seedCodexRolloutWithMeta(t, codexHome, root, "user", "", false)
				parent := ""
				if source == "subagent" {
					parent = root
				}
				evidence := ""
				threadSource := source
				if source == "user-pane" {
					evidence, threadSource = CodexThreadEvidencePane, "user"
				}
				if source != "unbacked" {
					seedCodexRolloutWithMeta(t, codexHome, candidate, threadSource, parent, false)
				}
				inst.CodexSessionID = root
				inst.Status = StatusRunning
				if err := inst.tmuxSession.Start("sleep 300"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = inst.tmuxSession.Kill() })
				// The fixture keeps a real pane alive without launching an agent.
				inst.tmuxSession.Command = "codex"
				if err := inst.tmuxSession.SetEnvironment("CODEX_SESSION_ID", root); err != nil {
					t.Fatal(err)
				}
				now := time.Now()
				event := "UserPromptSubmit"
				if source == "unbacked" {
					event = "agent-turn-complete"
				}
				hook := &HookStatus{Status: "running", SessionID: candidate, Event: event, UpdatedAt: now, CodexThreadEvidence: evidence}
				if mode == "cold" {
					data, err := json.Marshal(map[string]any{
						"status": hook.Status, "session_id": candidate, "event": hook.Event, "ts": now.Unix(),
						"codex_thread_evidence": evidence,
					})
					if err != nil {
						t.Fatal(err)
					}
					if err := os.MkdirAll(GetHooksDir(), 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(GetHooksDir(), inst.ID+".json"), data, 0o600); err != nil {
						t.Fatal(err)
					}
				} else {
					inst.hookStatus, inst.hookEvent = hook.Status, hook.Event
					inst.hookLastUpdate, inst.hookSessionID = now, candidate
					inst.hookCodexThreadEvidence = evidence
					if mode == "rejected-cache" {
						inst.UpdateHookStatus(hook)
					}
				}
				if err := inst.UpdateStatus(); err != nil {
					t.Fatal(err)
				}
				want := root
				if source == "user-pane" {
					want = candidate
				}
				if inst.CodexSessionID != want {
					t.Fatalf("%s hook changed owning conversation: got %q, want %q", source, inst.CodexSessionID, want)
				}
				if source != "user-pane" && inst.hookSessionID == candidate {
					t.Fatal("rejected foreign thread remains cached for the next status pass")
				}
			})
		}
	}
}
