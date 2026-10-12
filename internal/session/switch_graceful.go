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
	TypeCommandChecked(string, func(string) error) error
	ForgetCachedExistence()
}

var switchExitNow = time.Now
var switchExitSleep = time.Sleep
var switchExitAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func gracefulSwitchStop(inst *Instance) error {
	if inst == nil {
		return errors.New("switch source is nil")
	}
	if !IsClaudeCompatible(inst.Tool) && !IsCodexCompatible(inst.Tool) {
		return fmt.Errorf("graceful switch is unavailable for %q", inst.Tool)
	}
	if inst.exitToShellEnabled() && !inst.IsSandboxed() {
		return errors.New("graceful switch requires exit_to_shell=false for this session; source left running")
	}
	pane := inst.GetTmuxSession()
	if pane == nil {
		return errors.New("switch source has no tmux pane")
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
	return inst.KillAndWait()
}

func gracefulSwitchExit(pane switchExitPane, tool string, beforeExit func()) error {
	pid, err := pane.PanePID()
	if err != nil {
		return fmt.Errorf("cannot identify switch source process: %w", err)
	}
	if pid <= 0 {
		return errors.New("cannot identify switch source process: invalid pane PID")
	}
	deadline := switchExitNow().Add(switchIdleWait)
	for {
		raw, captureErr := pane.CapturePaneFresh()
		if captureErr != nil {
			return fmt.Errorf("cannot read switch source composer: %w", captureErr)
		}
		ready, reason := switchComposerReady(raw, tool)
		if ready {
			break
		}
		if !switchExitNow().Before(deadline) {
			return fmt.Errorf("switch source did not become idle within %s (%s)", switchIdleWait, reason)
		}
		switchExitSleep(switchPoll)
	}
	if beforeExit != nil {
		beforeExit()
	}
	if err := pane.TypeCommandChecked("/exit", func(raw string) error {
		if tool == "claude" {
			if send.ClaudeTurnRunning(raw) || !send.HasUnsentComposerPrompt(tmux.StripANSI(raw), "/exit") {
				return errors.New("source composer changed before /exit could be submitted")
			}
		} else {
			draft, visible := send.CodexComposerDraft(raw, tmux.StripANSI)
			if !visible || draft != "/exit" || tmux.CodexTurnRunning(tmux.StripANSI(raw)) {
				return errors.New("source composer changed before /exit could be submitted")
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("send agent exit command: %w", err)
	}
	deadline = switchExitNow().Add(switchExitWait)
	for switchExitAlive(pid) {
		if !switchExitNow().Before(deadline) {
			return fmt.Errorf("switch source did not exit within %s after /exit", switchExitWait)
		}
		switchExitSleep(switchPoll)
	}
	pane.ForgetCachedExistence()
	return nil
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
