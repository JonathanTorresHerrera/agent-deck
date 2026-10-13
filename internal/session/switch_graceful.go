package session

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// Switches must let the agent finish its own shutdown. In particular, killing
// a Claude pane skips SessionEnd hooks when Claude runs behind WSL interop.
// A source that is busy, has a draft, or has an unreadable composer is left
// running so the caller can retry without losing an in-flight turn.
const (
	switchIdleWait = 30 * time.Second
	switchExitWait = 30 * time.Second
	switchPoll     = 250 * time.Millisecond
)

type switchExitPane interface {
	CapturePaneFresh() (string, error)
	PanePID() (int, error)
	TypeCommandChecked(string, func(string) error, ...func(string) bool) error
	SendClaudeExitKeys() error
	ForgetCachedExistence()
}

type switchStopError struct {
	Cause             error
	ExitSubmitted     bool
	Terminated        bool
	TeardownConfirmed bool
}

func (e *switchStopError) Error() string { return e.Cause.Error() }
func (e *switchStopError) Unwrap() error { return e.Cause }

func switchStopFailure(err error, submitted, terminated bool) error {
	return &switchStopError{Cause: err, ExitSubmitted: submitted, Terminated: terminated}
}

var switchExitNow = time.Now
var switchExitSleep = time.Sleep
var switchExitAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func gracefulSwitchStop(inst *Instance) error {
	if inst == nil {
		return switchStopFailure(errors.New("switch source is nil"), false, false)
	}
	if !IsClaudeCompatible(inst.Tool) && !IsCodexCompatible(inst.Tool) {
		return switchStopFailure(fmt.Errorf("graceful switch is unavailable for %q", inst.Tool), false, false)
	}
	if inst.exitToShellEnabled() && !inst.IsSandboxed() {
		return switchStopFailure(errors.New("graceful switch requires exit_to_shell=false for this session; source left running"), false, false)
	}
	pane := inst.GetTmuxSession()
	if pane == nil {
		return switchStopFailure(errors.New("switch source has no tmux pane"), false, false)
	}
	tool := "codex"
	if IsClaudeCompatible(inst.Tool) {
		tool = "claude"
	}
	if err := gracefulSwitchExit(pane, tool, func() {
		// Freeze fast-death observers and discover MCP descendants while the
		// agent process still exists. KillAndWait reaps them after shutdown.
		inst.bumpSpawnGenAndBarrier()
		inst.discoverMCPChildrenFromPaneTree()
	}); err != nil {
		return err
	}
	if err := inst.KillAndWait(); err != nil {
		return switchStopFailure(fmt.Errorf("teardown after source exit: %w", err), true, true)
	}
	return nil
}

func gracefulSwitchExit(pane switchExitPane, tool string, beforeExit func()) error {
	pid, err := pane.PanePID()
	if err != nil {
		return switchStopFailure(fmt.Errorf("cannot identify switch source process: %w", err), false, false)
	}
	if pid <= 0 {
		return switchStopFailure(errors.New("cannot identify switch source process: invalid pane PID"), false, false)
	}
	deadline := switchExitNow().Add(switchIdleWait)
	for {
		raw, captureErr := pane.CapturePaneFresh()
		if captureErr != nil {
			return switchStopFailure(fmt.Errorf("cannot read switch source composer: %w", captureErr), false, false)
		}
		ready, reason := switchComposerReady(raw, tool)
		if ready {
			break
		}
		if !switchExitNow().Before(deadline) {
			return switchStopFailure(fmt.Errorf("switch source did not become idle within %s (%s)", switchIdleWait, reason), false, false)
		}
		switchExitSleep(switchPoll)
	}
	if beforeExit != nil {
		beforeExit()
	}
	if tool == "claude" {
		// Claude's app:exit binding is Ctrl+D twice. This also works when a
		// usage limit prevents /exit from leaving the running TUI. Confirm
		// the composer is still empty immediately before sending the keys.
		raw, captureErr := pane.CapturePaneFresh()
		if captureErr != nil {
			return switchStopFailure(fmt.Errorf("cannot recheck switch source composer: %w", captureErr), false, false)
		}
		if ready, reason := switchComposerReady(raw, tool); !ready {
			return switchStopFailure(fmt.Errorf("source composer changed before exit keys (%s)", reason), false, false)
		}
		if err := pane.SendClaudeExitKeys(); err != nil {
			return switchStopFailure(fmt.Errorf("send agent exit keys: %w", err), false, false)
		}
	} else if err := pane.TypeCommandChecked("/exit", func(raw string) error {
		draft, visible := send.CodexComposerDraft(raw, tmux.StripANSI)
		if !visible || draft != "/exit" || tmux.CodexTurnRunning(tmux.StripANSI(raw)) {
			return errors.New("source composer changed before /exit could be submitted")
		}
		return nil
	}, func(raw string) bool { return switchComposerDraft(raw, tool) == "/exit" }); err != nil {
		return switchStopFailure(fmt.Errorf("send agent exit command: %w", err), false, false)
	}
	deadline = switchExitNow().Add(switchExitWait)
	for switchExitAlive(pid) {
		if !switchExitNow().Before(deadline) {
			return switchStopFailure(fmt.Errorf("switch source did not exit within %s after exit request", switchExitWait), true, false)
		}
		switchExitSleep(switchPoll)
	}
	pane.ForgetCachedExistence()
	return nil
}

func switchComposerDraft(raw, tool string) string {
	if tool == "claude" {
		draft, visible := send.ComposerDraft(raw, tmux.StripANSI)
		if visible {
			return draft
		}
	} else if tool == "codex" {
		draft, visible := send.CodexComposerDraft(raw, tmux.StripANSI)
		if visible {
			return draft
		}
	}
	return ""
}

func switchComposerReady(raw, tool string) (bool, string) {
	if tool == "claude" {
		ready := send.ClaudeComposerReady(raw)
		return ready.Ready, ready.Code
	}
	if tool == "codex" {
		// Codex has its own prompt glyph and placeholder rules. A visible,
		// empty composer is necessary before submitting a slash command.
		draft, visible := send.CodexComposerDraft(raw, tmux.StripANSI)
		if !visible {
			return false, "no Codex composer"
		}
		if draft != "" {
			return false, "Codex composer holds a draft"
		}
		if tmux.CodexTurnRunning(tmux.StripANSI(raw)) {
			return false, "Codex turn is running"
		}
		return true, ""
	}
	return false, "unsupported tool"
}
