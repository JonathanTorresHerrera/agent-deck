package session

import (
	"bufio"
	"encoding/json"
	"os"
	"testing"
	"time"
)

// Shared app-server daemon attribution (codex_thread_evidence.go). All ids and
// paths here are synthetic.

func countForeignRejects(t *testing.T, candidate string) int {
	t.Helper()
	f, err := os.Open(GetSessionIDLifecycleLogPath())
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev SessionIDLifecycleEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Candidate == candidate &&
			ev.Reason == "candidate_thread_has_no_session_evidence" {
			n++
		}
	}
	return n
}

// siblingTurnEnd is the record the shared daemon's notify wrote for a sibling
// session's finished turn before the writer learned to route it: filed under
// this session, naming the sibling's thread.
func siblingTurnEnd(sibling, turn, evidence string, at time.Time) *HookStatus {
	gen := sibling + ":" + turn
	return &HookStatus{
		Status: "waiting", SessionID: sibling, Event: "agent-turn-complete", UpdatedAt: at,
		CodexStartedGeneration: gen, CodexCompletedGeneration: gen,
		CodexStartedSessionID: sibling, CodexCompletedSessionID: sibling,
		CodexStartedSequence: 2, CodexCompletedSequence: 2,
		CodexThreadEvidence: evidence,
	}
}

// The live 2026-11-23 shape: the busy session's own turn is running when the
// idle sibling's turn-end lands in its hook file. The binding, the running
// status and the turn evidence must all stay the busy session's.
func TestCodexHookSiblingThreadWithoutEvidenceKeepsBindingAndStatus(t *testing.T) {
	inst, codexHome := newCodexGateInstance(t)
	own, sibling := uniqueSID(t), uniqueSID(t)
	ownTurn, siblingTurn := uniqueSID(t), uniqueSID(t)
	seedCodex155Rollout(t, codexHome, own, ownTurn)
	seedCodex155Rollout(t, codexHome, sibling, siblingTurn)
	inst.CodexSessionID = own

	ownGen := own + ":" + ownTurn
	start := time.Now()
	inst.UpdateHookStatus(&HookStatus{
		Status: "running", SessionID: own, Event: "turn/started", UpdatedAt: start,
		CodexStartedGeneration: ownGen, CodexStartedSessionID: own, CodexStartedSequence: 1,
	})
	foreign := siblingTurnEnd(sibling, siblingTurn, "", start.Add(time.Second))
	inst.UpdateHookStatus(foreign)
	inst.UpdateHookStatus(foreign) // the next status sweep re-feeds the same record

	if inst.CodexSessionID != own {
		t.Fatalf("binding moved to the sibling's thread: got %q, want %q", inst.CodexSessionID, own)
	}
	if hs, fresh := inst.GetHookStatus(); hs != "running" || !fresh {
		t.Fatalf("hook status = %q (fresh %v), want the session's own running turn", hs, fresh)
	}
	if inst.codexStartedGeneration != ownGen || inst.codexCompletedGeneration != "" {
		t.Fatalf("turn evidence = started %q completed %q; want the own turn's start only",
			inst.codexStartedGeneration, inst.codexCompletedGeneration)
	}
	if got := countForeignRejects(t, sibling); got != 1 {
		t.Fatalf("lifecycle rejects for the re-fed record = %d, want 1", got)
	}
}

// /new or /resume inside the pane: the notify came from the pane's own Codex
// process, so the writer stamped pane evidence and the binding follows.
func TestCodexHookPaneEvidenceMovesBoundSession(t *testing.T) {
	for _, evidence := range []string{CodexThreadEvidencePane, CodexThreadEvidenceBinding} {
		t.Run(evidence, func(t *testing.T) {
			inst, codexHome := newCodexGateInstance(t)
			old, next := uniqueSID(t), uniqueSID(t)
			nextTurn := uniqueSID(t)
			seedCodex155Rollout(t, codexHome, old, uniqueSID(t))
			seedCodex155Rollout(t, codexHome, next, nextTurn)
			inst.CodexSessionID = old

			inst.UpdateHookStatus(siblingTurnEnd(next, nextTurn, evidence, time.Now()))

			if inst.CodexSessionID != next {
				t.Fatalf("binding = %q, want the pane's new thread %q", inst.CodexSessionID, next)
			}
			if hs, _ := inst.GetHookStatus(); hs != "waiting" {
				t.Fatalf("hook status = %q, want waiting from the accepted record", hs)
			}
		})
	}
}

// A fresh session has no binding yet; its first event binds it, with or
// without evidence, exactly as before.
func TestCodexHookFreshSessionBindsWithoutEvidence(t *testing.T) {
	inst, codexHome := newCodexGateInstance(t)
	first, turn := uniqueSID(t), uniqueSID(t)
	seedCodex155Rollout(t, codexHome, first, turn)

	inst.UpdateHookStatus(siblingTurnEnd(first, turn, "", time.Now()))

	if inst.CodexSessionID != first {
		t.Fatalf("fresh session did not bind its first thread: got %q, want %q", inst.CodexSessionID, first)
	}
}

func TestCodexHookFromForeignThreadEvidenceRule(t *testing.T) {
	inst, codexHome := newCodexGateInstance(t)
	own, sibling := uniqueSID(t), uniqueSID(t)
	seedCodex155Rollout(t, codexHome, own, uniqueSID(t))
	seedCodex155Rollout(t, codexHome, sibling, uniqueSID(t))
	cases := []struct {
		name     string
		bound    string
		sid      string
		evidence string
		foreign  bool
	}{
		{"own thread", own, own, "", false},
		{"sibling without evidence", own, sibling, "", true},
		{"sibling with pane evidence", own, sibling, CodexThreadEvidencePane, false},
		{"sibling with binding evidence", own, sibling, CodexThreadEvidenceBinding, false},
		{"sibling with unknown evidence", own, sibling, "rumour", true},
		{"unbound session", "", sibling, "", false},
		{"record without a thread", own, "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inst.CodexSessionID = tc.bound
			hs := &HookStatus{Status: "waiting", SessionID: tc.sid, Event: "agent-turn-complete", CodexThreadEvidence: tc.evidence}
			if got := inst.codexHookFromForeignThread(hs); got != tc.foreign {
				t.Fatalf("codexHookFromForeignThread = %v, want %v", got, tc.foreign)
			}
		})
	}
}
