package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// WO-DECK-CODEX-DELIVERY-01 part 2: a Codex send never types into a running
// turn or a Codex modal.
//
// Codex 0.161 folds text typed during a turn into that turn and starts no new
// one, which left the acceptance marker unresolved forever; and its startup
// update prompt defaults to "Update now", so an Enter typed into it would
// install an update. Every Codex send therefore looks before it types:
// --no-wait sends refuse at once with nothing typed (target_busy /
// dialog_blocked, both retry-safe, so callers queue them), and readiness-
// waited sends wait for the turn to end first.

// deliveryDialogBlocked: no input sent because a Codex modal owns the
// keyboard (update prompt, approval, trust or another selection menu). Typed
// text would answer it and Enter would pick its default. Nothing was typed,
// so a retry once the modal closes is safe.
const deliveryDialogBlocked = "dialog_blocked"

// Machine-readable reasons for the nothing-typed refusals, reported as
// `reason_code` next to `delivery` in --json output.
const (
	reasonTurnRunning           = "turn_running"
	reasonSendLockHeld          = "send_lock_held"
	reasonUnresolvedSubmission  = "unresolved_submission"
	reasonAcceptanceLockHeld    = "lock_held"
	reasonGenerationUnavailable = "generation_unavailable"
	reasonIdentityUnavailable   = "identity_unavailable"
	reasonIdentityContested     = "identity_contested"
	reasonMarkerOwnerMismatch   = "marker_owner_mismatch"
	reasonRolloutNotLocal       = "rollout_not_local"
	reasonAcceptanceError       = "acceptance_error"
)

var (
	errCodexRolloutNotLocal   = errors.New("exact rollout is unavailable for remote or sandboxed sessions")
	errCodexIdentityContested = errors.New("live Codex session identity is already owned by another session")
)

const (
	// codexBusyPollInterval is how often a waiting send re-reads the target.
	codexBusyPollInterval = time.Second
	// codexQueueWorkerBusyWait caps the wait inside a durable-queue child:
	// the queue worker owns longer waits and retries a target_busy refusal.
	codexQueueWorkerBusyWait = 30 * time.Second
	// codexUpdateSkipVerify bounds the check that Escape closed the prompt.
	codexUpdateSkipVerify = 3 * time.Second
)

// codexEvidencePastFence is the delivery evidence for the send that captured
// fence, reading only what the rollout gained after it; nil when none (or on
// a read error, which leaves the marker for the next sender to reconcile).
func codexEvidencePastFence(inst *session.Instance, fence codexAcceptanceFence, notBefore time.Time) *session.CodexDeliveryEvidence {
	if inst == nil || !fence.available || inst.CodexSessionID != fence.codexSessionID {
		return nil
	}
	evidence, err := inst.CodexDeliveryEvidenceAfter(fence.state, notBefore)
	if err != nil {
		return nil
	}
	return evidence
}

// codexAcceptanceReasonCode names why exact Codex turn acceptance could not
// be established. Every one of these refusals typed nothing.
func codexAcceptanceReasonCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, session.ErrUnresolvedCodexSubmission):
		return reasonUnresolvedSubmission
	case errors.Is(err, session.ErrCodexAcceptanceLockTimeout):
		return reasonAcceptanceLockHeld
	case errors.Is(err, session.ErrCodexMarkerOwnerMismatch):
		return reasonMarkerOwnerMismatch
	case errors.Is(err, errCodexIdentityUnavailable):
		return reasonIdentityUnavailable
	case errors.Is(err, errCodexGenerationUnavailable):
		return reasonGenerationUnavailable
	case errors.Is(err, errCodexIdentityContested):
		return reasonIdentityContested
	case errors.Is(err, errCodexRolloutNotLocal):
		return reasonRolloutNotLocal
	default:
		return reasonAcceptanceError
	}
}

// codexPaneState is what one capture says about typing into a Codex pane.
type codexPaneState struct {
	captured        bool
	dialog          *send.CodexDialog
	statusLine      bool
	composerVisible bool
}

type paneCapturer interface {
	CapturePaneFresh() (string, error)
}

func readCodexPane(target paneCapturer) codexPaneState {
	if target == nil {
		return codexPaneState{}
	}
	raw, err := target.CapturePaneFresh()
	if err != nil {
		return codexPaneState{}
	}
	content := tmux.StripANSI(raw)
	state := codexPaneState{captured: true}
	if dialog, ok := send.DetectCodexDialog(content); ok {
		state.dialog = &dialog
		return state
	}
	state.statusLine = tmux.CodexTurnRunning(content)
	_, state.composerVisible = send.CodexComposerDraft(raw, tmux.StripANSI)
	return state
}

// codexBusyVerdict is whether a Codex target can take typed input now.
type codexBusyVerdict struct {
	busy   bool
	reason string // pane_status_line | hook_running | rollout_open_turn
	dialog *send.CodexDialog
}

func (v codexBusyVerdict) deliverable() bool { return !v.busy && v.dialog == nil }

// decideCodexDelivery weighs the evidence. The pane is the most direct: a
// modal blocks, a live status line is busy, and an idle composer with no
// status line is idle even when a hook or the rollout disagrees (a stale
// hook, a turn that crashed without a completion record). Without a readable
// idle composer, a fresh "running" hook or an open rollout turn is busy.
func decideCodexDelivery(pane codexPaneState, hookBusy, hookKnown bool, rollout *session.CodexTurnState) codexBusyVerdict {
	switch {
	case pane.dialog != nil:
		return codexBusyVerdict{dialog: pane.dialog}
	case pane.statusLine:
		return codexBusyVerdict{busy: true, reason: "pane_status_line"}
	case pane.captured && pane.composerVisible:
		return codexBusyVerdict{}
	case hookKnown && hookBusy:
		return codexBusyVerdict{busy: true, reason: "hook_running"}
	case rollout != nil && rollout.TurnOpen:
		return codexBusyVerdict{busy: true, reason: "rollout_open_turn"}
	}
	return codexBusyVerdict{}
}

// probeCodexDelivery gathers the evidence for decideCodexDelivery. rollout
// may be the acceptance fence's state; nil scans the rollout now.
func probeCodexDelivery(inst *session.Instance, target paneCapturer, rollout *session.CodexTurnState) codexBusyVerdict {
	pane := readCodexPane(target)
	hookBusy, hookKnown := hookDrivenBusy(inst)
	if rollout == nil && inst != nil {
		if state, err := inst.CodexRolloutTurnState(); err == nil {
			rollout = &state
		}
	}
	return decideCodexDelivery(pane, hookBusy, hookKnown, rollout)
}

type codexKeyTarget interface {
	paneCapturer
	SendNamedKey(key string) error
}

// skipCodexUpdatePrompt answers Codex's startup update prompt with Escape,
// which the prompt's own footer ("enter continue · esc skip") defines as
// Skip. It acts only on the complete prompt shape, verifies the prompt
// closed, and never sends Enter or a digit, so it can never choose "Update
// now" (JT: never auto-accept updates). AGENTDECK_CODEX_UPDATE_SKIP=0
// disables it; the send then refuses with dialog_blocked instead.
func skipCodexUpdatePrompt(target codexKeyTarget, verify time.Duration) (send.CodexDialog, bool, error) {
	if os.Getenv("AGENTDECK_CODEX_UPDATE_SKIP") == "0" {
		return send.CodexDialog{}, false, nil
	}
	raw, err := target.CapturePaneFresh()
	if err != nil {
		return send.CodexDialog{}, false, err
	}
	dialog, ok := send.IsCodexUpdatePrompt(tmux.StripANSI(raw))
	if !ok {
		return send.CodexDialog{}, false, nil
	}
	if err := target.SendNamedKey("Escape"); err != nil {
		return dialog, false, fmt.Errorf("send Escape to the Codex update prompt: %w", err)
	}
	deadline := time.Now().Add(verify)
	for {
		time.Sleep(200 * time.Millisecond)
		raw, err := target.CapturePaneFresh()
		if err == nil {
			content := tmux.StripANSI(raw)
			if _, still := send.IsCodexUpdatePrompt(content); !still {
				if d, open := send.DetectCodexDialog(content); !open || d.Kind != send.CodexDialogUpdatePrompt {
					fmt.Fprintf(os.Stderr, "Note: skipped the Codex update prompt (%s → %s) with Escape; no update was installed\n", dialog.From, dialog.To)
					return dialog, true, nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			return dialog, false, fmt.Errorf("the Codex update prompt (%s → %s) is still open after Escape", dialog.From, dialog.To)
		}
	}
}

// codexVerdictRefusal is the nothing-typed refusal for an undeliverable
// verdict: its delivery, reason code and human line.
func codexVerdictRefusal(v codexBusyVerdict, title string) (delivery, reasonCode, message string) {
	if v.dialog != nil {
		return deliveryDialogBlocked, v.dialog.Kind, fmt.Sprintf(
			"message not delivered to '%s': a Codex dialog holds the pane (%s); nothing was typed. Answer it in the session, then resend", title, v.dialog.Line)
	}
	return deliveryTargetBusy, reasonTurnRunning, fmt.Sprintf(
		"message not delivered to '%s': its Codex turn is still running (%s); nothing was typed. Queue it for the next turn (deck_send_message queues it; `session send --defer-if-busy` waits) instead of typing into the running turn", title, v.reason)
}

// waitForCodexDeliverable waits, before the acceptance lock is taken, until
// the target is neither mid-turn nor behind a modal, skipping the update
// prompt once. It returns the last verdict; the caller refuses on a verdict
// that is still not deliverable when the budget runs out.
func waitForCodexDeliverable(inst *session.Instance, target codexKeyTarget, budget, poll time.Duration) codexBusyVerdict {
	deadline := time.Now().Add(budget)
	skipped := false
	for {
		verdict := probeCodexDelivery(inst, target, nil)
		if verdict.dialog != nil && verdict.dialog.Kind == send.CodexDialogUpdatePrompt && !skipped {
			skipped = true
			if _, ok, _ := skipCodexUpdatePrompt(target, 3*time.Second); ok {
				continue
			}
		}
		if verdict.deliverable() || !time.Now().Before(deadline) {
			return verdict
		}
		time.Sleep(min(poll, time.Until(deadline)))
	}
}

// reasonPaneUnreadable: the Codex pane could not be captured right before
// typing, so composer safety was not established (delivery composer_blocked).
const reasonPaneUnreadable = "pane_unreadable"

// codexLastMomentCaptureAttempts bounds the re-reads of a pane whose capture
// failed before the gate gives up and refuses.
const codexLastMomentCaptureAttempts = 3

// readCodexPaneSettled reads the pane, retrying a failed capture briefly. A
// pane that still cannot be read is reported captured=false.
func readCodexPaneSettled(target paneCapturer) codexPaneState {
	var pane codexPaneState
	for attempt := 0; attempt < codexLastMomentCaptureAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		if pane = readCodexPane(target); pane.captured {
			return pane
		}
	}
	return pane
}

// codexLastMomentGate is the check executeSend makes right before typing,
// under the per-target send lock: a modal or a live status line on the pane
// refuses with nothing typed. It fails CLOSED: a pane that cannot be read
// may be showing the update prompt, where the submitting Enter would choose
// "Update now", so an unreadable pane refuses too.
func codexLastMomentGate(target paneCapturer) (delivery, reasonCode string, err error) {
	pane := readCodexPaneSettled(target)
	if !pane.captured {
		return deliveryComposerBlocked, reasonPaneUnreadable, fmt.Errorf("the Codex pane could not be read right before typing; nothing was typed")
	}
	if pane.dialog != nil {
		return deliveryDialogBlocked, pane.dialog.Kind, fmt.Errorf("a Codex dialog appeared before typing (%s); nothing was typed", pane.dialog.Line)
	}
	if pane.statusLine {
		return deliveryTargetBusy, reasonTurnRunning, fmt.Errorf("a Codex turn started before typing; nothing was typed")
	}
	return "", "", nil
}
