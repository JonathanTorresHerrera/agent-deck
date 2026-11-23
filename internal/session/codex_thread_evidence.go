package session

import (
	"strconv"
	"strings"
	"sync"
)

// Shared app-server daemon attribution (WO-DECK-CODEX-DELIVERY-01, root
// cause C, proven live 2026-11-23).
//
// Codex 0.161+ runs turns in an app-server daemon shared per CODEX_HOME. The
// first TUI started in a home spawns it, so the daemon inherits that TUI's
// environment, AGENTDECK_INSTANCE_ID and CODEX_SESSION_ID included, and the
// notify hook it fires for every TUI riding it names that first session. The
// payload's thread id is right; the env instance is not. Trusting the env
// filed every daemon-hosted turn under the first session, and the hook readers
// then rebound that session to whichever sibling thread finished last: a send
// to it wrote its acceptance marker under the sibling's thread, where no
// evidence can ever resolve it.
//
// The notify writer now records what it knew about the thread when it chose
// the instance (codex_thread_evidence on the hook record):
//
//	pane     the notify's parent is an interactive Codex process, so the env
//	         it carries is that pane's own and the pane runs the thread (/new,
//	         /resume, a fresh composer).
//	binding  the session store binds the thread to this instance.
//
// An instance that already has a Codex binding moves to another thread only
// on a record carrying one of them. A record without evidence (an older
// writer, a notify whose parent could not be classified) still gives an
// unbound instance its first binding, as before, and still reports status
// when it names the thread the instance is bound to.
const (
	CodexThreadEvidencePane    = "pane"
	CodexThreadEvidenceBinding = "binding"
)

// codexHookMayMoveBinding reports whether a hook record naming threadID may
// bind or rebind this instance. An unbound instance takes any thread (fresh
// session bootstrap); a bound one only its own thread, or another thread the
// writer had pane or binding evidence for.
func (i *Instance) codexHookMayMoveBinding(threadID, evidence string) bool {
	threadID = strings.TrimSpace(threadID)
	if i.CodexSessionID == "" || threadID == "" || threadID == i.CodexSessionID {
		return true
	}
	switch strings.TrimSpace(evidence) {
	case CodexThreadEvidencePane, CodexThreadEvidenceBinding:
		return true
	}
	return false
}

// codexForeignHookRejects remembers, per instance, the last foreign record
// written to the session-id lifecycle log. The TUI and web feeds re-apply the
// latest hook record on every sweep; without this a stale foreign record
// would be logged on each one.
var codexForeignHookRejects sync.Map // instance id -> "<thread>@<unix nanos>"

func (i *Instance) logCodexForeignHookReject(source, candidate string, status *HookStatus) {
	key := candidate + "@" + strconv.FormatInt(status.UpdatedAt.UnixNano(), 10)
	if prev, ok := codexForeignHookRejects.Load(i.ID); ok && prev == key {
		return
	}
	codexForeignHookRejects.Store(i.ID, key)
	_ = WriteSessionIDLifecycleEvent(SessionIDLifecycleEvent{
		InstanceID: i.ID, Tool: i.Tool, Action: "reject",
		Source: source, OldID: i.CodexSessionID, Candidate: candidate,
		HookEvent: status.Event, Reason: "candidate_thread_has_no_session_evidence",
	})
}
