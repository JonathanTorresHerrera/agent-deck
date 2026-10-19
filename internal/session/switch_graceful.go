package session

import (
	"errors"
	"fmt"
	"os"
	"strings"
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
	SendNamedKey(string) error
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
	// A WSL Windows process can remain as an unreaped /init zombie after its
	// tmux pane has closed. kill(pid, 0) still succeeds for that defunct PID,
	// so a signal-only probe falsely times out a completed graceful exit.
	if status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid)); err == nil {
		for _, line := range strings.Split(string(status), "\n") {
			if !strings.HasPrefix(line, "State:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 1 && (fields[1] == "Z" || fields[1] == "X") {
				return false
			}
			break
		}
	}
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
	notifyHarnessSwitchLifecycle(inst, "waiting_for_idle")
	if err := gracefulSwitchExit(pane, tool, func() {
		notifyHarnessSwitchLifecycle(inst, "stopping")
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
	exitDialogOpen := false
	for {
		raw, captureErr := pane.CapturePaneFresh()
		if captureErr != nil {
			return switchStopFailure(fmt.Errorf("cannot read switch source composer: %w", captureErr), false, false)
		}
		ready, reason := switchComposerReady(raw, tool)
		if ready {
			break
		}
		if tool == "claude" {
			if _, exitDialogOpen = claudeBackgroundExitSelection(raw); exitDialogOpen {
				break
			}
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
		if exitDialogOpen {
			if err := confirmClaudeBackgroundExit(pane); err != nil {
				return switchStopFailure(fmt.Errorf("confirm existing Claude exit dialog: %w", err), false, false)
			}
		} else {
			// Claude's app:exit binding is Ctrl+D twice. This also works when
			// a usage limit prevents /exit from leaving the running TUI.
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
		if tool == "claude" && !exitDialogOpen {
			raw, captureErr := pane.CapturePaneFresh()
			if captureErr == nil {
				if _, open := claudeBackgroundExitSelection(raw); open {
					if err := confirmClaudeBackgroundExit(pane); err != nil {
						return switchStopFailure(fmt.Errorf("confirm Claude exit dialog: %w", err), true, false)
					}
					exitDialogOpen = true
				}
			}
		}
		if !switchExitNow().Before(deadline) {
			return switchStopFailure(fmt.Errorf("switch source did not exit within %s after exit request", switchExitWait), true, false)
		}
		switchExitSleep(switchPoll)
	}
	pane.ForgetCachedExistence()
	return nil
}

// Claude asks what to do with scheduled tasks before exiting. Only this
// exact dialog is safe to automate; other dialogs still block the switch.
func claudeBackgroundExitSelection(raw string) (int, bool) {
	lines := strings.Split(tmux.StripANSI(raw), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	content := strings.Join(lines, "\n")
	if !strings.Contains(content, "Background work is running") ||
		!strings.Contains(content, "Exit and stop tasks") ||
		!strings.Contains(content, "Move to background and exit") ||
		!strings.Contains(content, "Stay") ||
		!strings.Contains(content, "Enter to confirm") {
		return 0, false
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "> ") && !strings.HasPrefix(line, "❯ ") {
			continue
		}
		switch {
		case strings.Contains(line, "1. Exit and stop tasks"):
			return 1, true
		case strings.Contains(line, "2. Move to background and exit"):
			return 2, true
		case strings.Contains(line, "3. Stay"):
			return 3, true
		}
	}
	return 0, false
}

func confirmClaudeBackgroundExit(pane switchExitPane) error {
	raw, err := pane.CapturePaneFresh()
	if err != nil {
		return err
	}
	selected, ok := claudeBackgroundExitSelection(raw)
	if !ok {
		return errors.New("Claude background exit dialog changed")
	}
	if selected != 2 {
		key := "Down"
		if selected == 3 {
			key = "Up"
		}
		if err := pane.SendNamedKey(key); err != nil {
			return err
		}
		switchExitSleep(100 * time.Millisecond)
		raw, err = pane.CapturePaneFresh()
		if err != nil {
			return err
		}
		if selected, ok = claudeBackgroundExitSelection(raw); !ok || selected != 2 {
			return errors.New("Claude background exit option was not selected")
		}
	}
	return pane.SendNamedKey("Enter")
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
		// A review modal can leave the old empty composer in the captured
		// background. It must be reviewed in Codex, never treated as readiness.
		rows := strings.Split(tmux.StripANSI(raw), "\n")
		if len(rows) > 40 {
			rows = rows[len(rows)-40:]
		}
		tail := strings.ToLower(strings.Join(rows, "\n"))
		if strings.Contains(tail, "hooks need review") && (strings.Contains(tail, "new or changed") || strings.Contains(tail, "review hooks")) {
			return false, "Codex hooks need review in this account home"
		}
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
