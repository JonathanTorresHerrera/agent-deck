package main

import (
	"errors"
	"fmt"
	"syscall"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/session"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// Graceful stop (`session stop --graceful`, `session archive --graceful`).
//
// A plain stop is `tmux kill-session` plus a SIGTERM->SIGKILL escalation. For
// an agent that is a Windows-native binary behind WSL interop (claude.exe on
// this rig) none of those signals reach the real process as a clean shutdown,
// so the agent's SessionEnd hooks never run. The graceful path instead types
// the agent's own exit command into its composer, waits for the pane process
// to exit, and only then runs the normal teardown — which still happens in
// every case, so the stop is never weaker than before.

// gracefulExitCommands is the per-agent exit command. Verified against the
// installed CLIs (2026-09-23): Claude Code `/exit`; Codex 0.156 `/exit` (alias
// of `/quit`); Gemini CLI `/quit` (`exit` is only an altName). Agents missing
// here fall back to the plain kill and say so.
var gracefulExitCommands = map[string]string{
	"claude": "/exit",
	"codex":  "/exit",
	"gemini": "/quit",
}

const (
	defaultGracefulTimeout = 30 * time.Second
	maxGracefulTimeout     = 5 * time.Minute
	gracefulPollInterval   = 250 * time.Millisecond
)

// gracefulStopResult is reported under "graceful" in --json output.
type gracefulStopResult struct {
	Attempted bool   `json:"attempted"`
	Command   string `json:"command,omitempty"`
	Exited    bool   `json:"exited"`
	WaitedMs  int64  `json:"waited_ms"`
	FellBack  bool   `json:"fell_back"`
	Reason    string `json:"reason,omitempty"`
}

// gracefulExitTarget is the pane surface the graceful exit needs.
// *tmux.Session satisfies it.
type gracefulExitTarget interface {
	CapturePaneFresh() (string, error)
	TypeCommand(string) error
	PanePID() (int, error)
}

type gracefulClock struct {
	now   func() time.Time
	sleep func(time.Duration)
	alive func(pid int) bool
}

var realGracefulClock = gracefulClock{now: time.Now, sleep: time.Sleep, alive: pidAlive}

// pidAlive reports whether pid still exists. EPERM means it exists but is not
// ours to signal, which still counts as alive.
func pidAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// runGracefulExit asks the agent to exit and waits up to timeout for its pane
// process to go away. It never kills anything: the caller always runs the
// normal teardown afterwards, and FellBack says whether that teardown is the
// thing that actually stopped the agent.
//
// It deliberately waits for the PROCESS, not for the hook file to flip to
// "dead": SessionEnd hooks (DoorBell among them) may still be running when
// that status is written, and killing on it would cut them off — the very bug
// this exists to fix.
func runGracefulExit(t gracefulExitTarget, tool string, timeout time.Duration, clk gracefulClock) gracefulStopResult {
	command, ok := gracefulExitCommands[tool]
	if !ok {
		return gracefulStopResult{FellBack: true, Reason: fmt.Sprintf("no graceful exit command for agent %q", tool)}
	}
	res := gracefulStopResult{Command: command}
	pid, err := t.PanePID()
	if err != nil {
		res.FellBack, res.Reason = true, fmt.Sprintf("could not read the pane process: %v", err)
		return res
	}
	// Typing into a composer that holds an unsent draft would append the exit
	// command to it and submit the result as a prompt. Never do that: fall back
	// to the plain stop, which discards the draft without sending it. A pane we
	// cannot read is treated the same way, since we cannot rule a draft out.
	raw, err := t.CapturePaneFresh()
	if err != nil {
		res.FellBack, res.Reason = true, fmt.Sprintf("could not read the composer: %v", err)
		return res
	}
	if send.ComposerHasDraft(raw, tmux.StripANSI) {
		res.FellBack, res.Reason = true, "composer holds an unsent draft; not typing into it"
		return res
	}
	if err := t.TypeCommand(command); err != nil {
		res.FellBack, res.Reason = true, fmt.Sprintf("could not type %s: %v", command, err)
		return res
	}
	res.Attempted = true
	start := clk.now()
	deadline := start.Add(timeout)
	for {
		if !clk.alive(pid) {
			res.Exited = true
			break
		}
		if !clk.now().Before(deadline) {
			break
		}
		clk.sleep(gracefulPollInterval)
	}
	res.WaitedMs = clk.now().Sub(start).Milliseconds()
	if !res.Exited {
		res.FellBack = true
		res.Reason = fmt.Sprintf("agent did not exit within %s", timeout)
	}
	return res
}

// validateGracefulTimeout bounds --graceful-timeout.
func validateGracefulTimeout(d time.Duration) error {
	if d <= 0 || d > maxGracefulTimeout {
		return fmt.Errorf("--graceful-timeout must be > 0 and <= %s", maxGracefulTimeout)
	}
	return nil
}

// gracefulStop runs the graceful exit for inst. The user-stop marker is written
// BEFORE the agent is asked to exit: once the pane vanishes on its own, reboot
// recovery would otherwise read it as a crash and resurrect the session.
func gracefulStop(inst *session.Instance, timeout time.Duration) gracefulStopResult {
	inst.MarkUserStopIntent()
	ts := inst.GetTmuxSession()
	if ts == nil {
		return gracefulStopResult{FellBack: true, Reason: "session has no tmux pane"}
	}
	return runGracefulExit(ts, inst.Tool, timeout, realGracefulClock)
}

// gracefulSummary is the human-readable suffix for a graceful stop.
func gracefulSummary(res *gracefulStopResult) string {
	switch {
	case res == nil:
		return ""
	case res.Exited:
		return fmt.Sprintf(" (agent exited on %s after %dms)", res.Command, res.WaitedMs)
	default:
		return fmt.Sprintf(" (graceful exit fell back to kill: %s)", res.Reason)
	}
}
