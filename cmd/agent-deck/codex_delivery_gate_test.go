package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
)

// WO-DECK-CODEX-DELIVERY-01 part 2. Pane shapes come from live Codex panes;
// none carries operator or agent content.
const gateUpdatePromptPane = ">_ OpenAI Codex (v0.161.0)\n\n  Update available · 0.161.0 → 0.171.0\n  Release notes: https://github.com/openai/codex/releases/latest\n\n" +
	"› 1. Update now (runs `npm install -g @openai/codex`)\n  2. Skip\n  3. Skip until next version\n\n  enter continue · esc skip\n"

const gateIdlePane = ">_ OpenAI Codex (v0.161.0)\n\n• Done.\n\n› Ask Codex to do anything\n\n  gpt-5.5 high · ~/scratch\n  ← for agents · ? for shortcuts\n"

const gateBusyPane = ">_ OpenAI Codex (v0.161.0)\n\n• Running the synthetic task.\n\n• Working (12s • esc to interrupt)\n\n› Ask Codex to do anything\n\n  gpt-5.5 high · ~/scratch\n"

func TestDecideCodexDelivery(t *testing.T) {
	open := &session.CodexTurnState{TurnOpen: true}
	closed := &session.CodexTurnState{}
	dialog := &send.CodexDialog{Kind: send.CodexDialogUpdatePrompt}
	cases := []struct {
		name               string
		pane               codexPaneState
		hookBusy, hookKnow bool
		rollout            *session.CodexTurnState
		busy               bool
		reason             string
		blocked            bool
	}{
		{name: "modal blocks", pane: codexPaneState{captured: true, dialog: dialog}, blocked: true},
		{name: "status line is busy", pane: codexPaneState{captured: true, statusLine: true, composerVisible: true}, rollout: closed, busy: true, reason: "pane_status_line"},
		{name: "idle composer beats a stale hook", pane: codexPaneState{captured: true, composerVisible: true}, hookBusy: true, hookKnow: true, rollout: open},
		{name: "unreadable pane, fresh running hook", pane: codexPaneState{}, hookBusy: true, hookKnow: true, rollout: closed, busy: true, reason: "hook_running"},
		{name: "unreadable pane, open rollout turn", pane: codexPaneState{}, rollout: open, busy: true, reason: "rollout_open_turn"},
		{name: "no composer, closed turn, no hook", pane: codexPaneState{captured: true}, rollout: closed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := decideCodexDelivery(tc.pane, tc.hookBusy, tc.hookKnow, tc.rollout)
			if v.busy != tc.busy || v.reason != tc.reason || (v.dialog != nil) != tc.blocked {
				t.Fatalf("verdict = %+v, want busy=%v reason=%q blocked=%v", v, tc.busy, tc.reason, tc.blocked)
			}
		})
	}
}

// keyRecorder is a Codex pane that records every key a test sends it.
type keyRecorder struct {
	mu    sync.Mutex
	panes []string
	keys  []string
	typed []string
}

func (k *keyRecorder) CapturePaneFresh() (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if len(k.panes) == 0 {
		return "", errors.New("no pane")
	}
	pane := k.panes[0]
	if len(k.panes) > 1 {
		k.panes = k.panes[1:]
	}
	return pane, nil
}

func (k *keyRecorder) SendNamedKey(key string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, key)
	return nil
}

func TestSkipCodexUpdatePromptPressesOnlyEscape(t *testing.T) {
	target := &keyRecorder{panes: []string{gateUpdatePromptPane, gateIdlePane}}
	dialog, skipped, err := skipCodexUpdatePrompt(target, time.Second)
	if err != nil || !skipped {
		t.Fatalf("skipped=%v err=%v", skipped, err)
	}
	if dialog.From != "0.161.0" || dialog.To != "0.171.0" {
		t.Fatalf("dialog = %+v", dialog)
	}
	if fmt.Sprint(target.keys) != "[Escape]" {
		t.Fatalf("keys sent = %v, want exactly [Escape] (never Enter or a digit: Enter picks Update now)", target.keys)
	}
}

func TestSkipCodexUpdatePromptRefusesAnIncompletePromptAndHonorsTheSwitch(t *testing.T) {
	noHint := strings.Replace(gateUpdatePromptPane, "enter continue · esc skip", "", 1)
	target := &keyRecorder{panes: []string{noHint}}
	if _, skipped, _ := skipCodexUpdatePrompt(target, time.Second); skipped || len(target.keys) != 0 {
		t.Fatalf("skipped=%v keys=%v on a prompt without its esc hint", skipped, target.keys)
	}
	t.Setenv("AGENTDECK_CODEX_UPDATE_SKIP", "0")
	off := &keyRecorder{panes: []string{gateUpdatePromptPane}}
	if _, skipped, _ := skipCodexUpdatePrompt(off, time.Second); skipped || len(off.keys) != 0 {
		t.Fatalf("skip ran with AGENTDECK_CODEX_UPDATE_SKIP=0: keys=%v", off.keys)
	}
}

func TestSkipCodexUpdatePromptReportsAPromptThatStaysOpen(t *testing.T) {
	target := &keyRecorder{panes: []string{gateUpdatePromptPane}}
	_, skipped, err := skipCodexUpdatePrompt(target, 300*time.Millisecond)
	if skipped || err == nil || !strings.Contains(err.Error(), "still open") {
		t.Fatalf("skipped=%v err=%v", skipped, err)
	}
	if fmt.Sprint(target.keys) != "[Escape]" {
		t.Fatalf("keys = %v, want one Escape and nothing else", target.keys)
	}
}

func TestExecuteSendTypesNothingIntoACodexModalOrARunningTurn(t *testing.T) {
	for _, tc := range []struct {
		name, pane, delivery, reason string
	}{
		{"update prompt", gateUpdatePromptPane, deliveryDialogBlocked, send.CodexDialogUpdatePrompt},
		{"running turn", gateBusyPane, deliveryTargetBusy, reasonTurnRunning},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockSendRetryTarget{panes: []string{tc.pane}, statuses: []string{"waiting"}}
			tun := noWaitSendTuning()
			tun.codexLastMoment = true
			res, err := executeSend(mock, "codex", "synthetic message", true, tun)
			if err == nil || res.delivery != tc.delivery || res.reasonCode != tc.reason {
				t.Fatalf("res=%+v err=%v, want %s/%s", res, err, tc.delivery, tc.reason)
			}
			if mock.sendKeysCalls != 0 || mock.sendEnterCalls != 0 || mock.sendChunkedCalls != 0 {
				t.Fatalf("typed into the pane: keys=%d enter=%d chunked=%d", mock.sendKeysCalls, mock.sendEnterCalls, mock.sendChunkedCalls)
			}
			fields := res.jsonFields()
			if fields["reason_code"] != tc.reason || fields["delivery"] != tc.delivery || fields["submitted"] != false {
				t.Fatalf("json fields = %v", fields)
			}
		})
	}
}

func TestCodexAcceptanceReasonCodes(t *testing.T) {
	cases := map[error]string{
		fmt.Errorf("wrapped: %w", session.ErrUnresolvedCodexSubmission):  reasonUnresolvedSubmission,
		fmt.Errorf("%w after 5s", session.ErrCodexAcceptanceLockTimeout): reasonAcceptanceLockHeld,
		session.ErrCodexMarkerOwnerMismatch:                              reasonMarkerOwnerMismatch,
		errCodexIdentityUnavailable:                                      reasonIdentityUnavailable,
		errCodexGenerationUnavailable:                                    reasonGenerationUnavailable,
		errCodexIdentityContested:                                        reasonIdentityContested,
		errCodexRolloutNotLocal:                                          reasonRolloutNotLocal,
		errors.New("something else"):                                     reasonAcceptanceError,
	}
	for err, want := range cases {
		if got := codexAcceptanceReasonCode(err); got != want {
			t.Errorf("codexAcceptanceReasonCode(%v) = %q, want %q", err, got, want)
		}
	}
}

func TestQueueWorkerRetriesADialogRefusal(t *testing.T) {
	outcome, reason := classifyChild(map[string]interface{}{"success": false, "delivery": deliveryDialogBlocked, "reason_code": send.CodexDialogUpdatePrompt}, 1)
	if outcome != childNotSent || reason != deliveryDialogBlocked {
		t.Fatalf("classifyChild = %v %q, want childNotSent: nothing was typed", outcome, reason)
	}
}

// writeGateRollout installs a synthetic rollout with one finished turn.
func writeGateRollout(codexHome, sessionID string) error {
	dir := codexHome + "/sessions/2026/11/22"
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	content := `{"timestamp":"2026-11-22T18:00:00.000Z","type":"event_msg","payload":{"type":"task_started","turn_id":"turn-1"}}` + "\n" +
		`{"timestamp":"2026-11-22T18:00:01.000Z","type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-1"}}` + "\n"
	return os.WriteFile(dir+"/rollout-2026-11-22T10-00-00-"+sessionID+".jsonl", []byte(content), 0o600)
}

func TestCodexGuardClearsItsOwnMarkerWhenADialogBlockedTheSend(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", home+"/data")
	t.Setenv("CODEX_HOME", home+"/codex")
	inst := &session.Instance{ID: "instance-dialog", Tool: "codex", CodexSessionID: "thread-dialog"}
	if err := writeGateRollout(home+"/codex", "thread-dialog"); err != nil {
		t.Fatal(err)
	}
	guard, err := acquireCodexAcceptanceGuard(inst, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Release()
	if err := guard.Prepare(inst.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := guard.RecordTransportOutcome(deliveryDialogBlocked, time.Now()); err != nil {
		t.Fatal(err)
	}
	guard.Release()
	again, err := acquireCodexAcceptanceGuard(inst, time.Second)
	if err != nil {
		t.Fatalf("a nothing-typed dialog refusal left its marker behind: %v", err)
	}
	again.Release()
}

// Review finding (codex escalation pass, 2026-11-22): a failed capture used to
// read as "no modal, no turn" and let executeSend type. While the update
// prompt is up that Enter would choose Update now, so the gate fails closed.
func TestExecuteSendRefusesWhenTheCodexPaneCannotBeRead(t *testing.T) {
	unreadable := errors.New("capture-pane failed")
	mock := &mockSendRetryTarget{
		panes:    []string{"", "", "", gateIdlePane},
		paneErrs: []error{unreadable, unreadable, unreadable},
		statuses: []string{"waiting"},
	}
	tun := noWaitSendTuning()
	tun.codexLastMoment = true
	res, err := executeSend(mock, "codex", "synthetic message", true, tun)
	if err == nil || res.delivery != deliveryComposerBlocked || res.reasonCode != reasonPaneUnreadable {
		t.Fatalf("res=%+v err=%v, want composer_blocked/pane_unreadable", res, err)
	}
	if mock.sendKeysCalls != 0 || mock.sendEnterCalls != 0 || mock.sendChunkedCalls != 0 {
		t.Fatalf("typed into an unreadable pane: keys=%d enter=%d chunked=%d", mock.sendKeysCalls, mock.sendEnterCalls, mock.sendChunkedCalls)
	}
}

func TestExecuteSendToleratesOneTransientCaptureFailure(t *testing.T) {
	mock := &mockSendRetryTarget{
		panes:    []string{"", gateIdlePane},
		paneErrs: []error{errors.New("transient")},
		statuses: []string{"waiting"},
	}
	tun := noWaitSendTuning()
	tun.codexLastMoment = true
	tun.retry.maxRetries, tun.retry.checkDelay = 1, time.Millisecond
	res, _ := executeSend(mock, "codex", "synthetic message", true, tun)
	if res.delivery == deliveryComposerBlocked || mock.sendKeysCalls != 1 {
		t.Fatalf("a pane readable on the second try was refused: res=%+v keys=%d", res, mock.sendKeysCalls)
	}
}
