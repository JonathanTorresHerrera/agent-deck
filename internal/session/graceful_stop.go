package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/asheshgoplani/agent-deck/internal/send"
	"github.com/asheshgoplani/agent-deck/internal/tmux"
)

// StopOutcome describes how the agent and its tmux scope were retired.
type StopOutcome string

const (
	StopGraceful       StopOutcome = "graceful"
	StopTimeoutKilled  StopOutcome = "timeout_killed"
	StopAlreadyDead    StopOutcome = "already_dead"
	StopForced         StopOutcome = "forced"
	DefaultStopTimeout             = 20 * time.Second
)

type StopOptions struct {
	Timeout time.Duration
	Force   bool
}

type gracefulStopPane interface {
	CapturePaneFresh() (string, error)
	PanePID() (int, error)
	TypeCommandChecked(string, func(string) error, ...func(string) bool) error
	SendClaudeExitKeys() error
	SendNamedKey(string) error
	ForgetCachedExistence()
}

var gracefulStopNow = time.Now
var gracefulStopSleep = time.Sleep
var gracefulStopAlive = switchExitAlive

// StopWithOptions asks the agent to exit first. A missing, busy, unreadable,
// or unresponsive agent is always reaped by the existing synchronous teardown.
// The timeout is a total budget, including waiting for an idle composer.
func (i *Instance) StopWithOptions(opts StopOptions) (StopOutcome, error) {
	if !i.Exists() {
		// A dead pane can still have an ownership receipt or detached MCP
		// descendants. Run teardown even when tmux has already gone away.
		err := i.KillAndWait()
		if err != nil && i.Exists() {
			return StopAlreadyDead, err
		}
		return StopAlreadyDead, nil
	}
	if opts.Force {
		return StopForced, i.KillAndWait()
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultStopTimeout
	}
	deadline := gracefulStopNow().Add(opts.Timeout)
	tool := strings.ToLower(strings.TrimSpace(i.Tool))
	command := stopExitCommand(tool)
	if command == "" || i.GetTmuxSession() == nil {
		return StopTimeoutKilled, i.KillAndWait()
	}
	// A successful native exit can close the pane before KillAndWait runs.
	// Persist stop intent and freeze fast-death observers first so reboot
	// recovery cannot mistake that exit for a crash. Discover detached MCP
	// descendants while their parent process is still inspectable.
	markUserStopped(i.ID)
	i.bumpSpawnGenAndBarrier()
	i.discoverMCPChildrenFromPaneTree()
	// The pane PID is the tmux-owned process. Its exit means synchronous
	// SessionEnd hooks have returned. Teardown then reaps MCP descendants.
	if gracefulStopExit(i.GetTmuxSession(), tool, command, deadline) {
		err := i.KillAndWait()
		if err != nil && i.Exists() {
			return StopGraceful, err
		}
		return StopGraceful, nil
	}
	return StopTimeoutKilled, i.KillAndWait()
}

func stopExitCommand(tool string) string {
	switch {
	case IsClaudeCompatible(tool):
		return "ctrl-d-twice"
	case IsCodexCompatible(tool), tool == "grok":
		return "/exit"
	case tool == "gemini":
		return "/quit"
	default:
		return ""
	}
}

func gracefulStopExit(pane gracefulStopPane, tool, command string, deadline time.Time) bool {
	pid, err := pane.PanePID()
	if err != nil || pid <= 0 {
		return false
	}
	for gracefulStopNow().Before(deadline) {
		if !gracefulStopAlive(pid) {
			pane.ForgetCachedExistence()
			return true
		}
		raw, err := pane.CapturePaneFresh()
		if err != nil {
			return false
		}
		if stopComposerReady(raw, tool) {
			if command == "ctrl-d-twice" {
				err = pane.SendClaudeExitKeys()
			} else {
				err = pane.TypeCommandChecked(command, func(current string) error {
					if !stopTypedCommandVisible(current, tool, command) {
						return fmt.Errorf("exit command changed before submission")
					}
					return nil
				}, func(current string) bool { return stopTypedCommandVisible(current, tool, command) })
			}
			if err != nil {
				return false
			}
			for gracefulStopNow().Before(deadline) {
				if !gracefulStopAlive(pid) {
					pane.ForgetCachedExistence()
					return true
				}
				if IsClaudeCompatible(tool) {
					if raw, captureErr := pane.CapturePaneFresh(); captureErr == nil {
						if _, open := claudeBackgroundExitSelection(raw); open {
							if confirmClaudeBackgroundExit(pane) != nil {
								return false
							}
						}
					}
				}
				gracefulStopSleep(200 * time.Millisecond)
			}
			return false
		}
		gracefulStopSleep(200 * time.Millisecond)
	}
	return false
}

func stopComposerReady(raw, tool string) bool {
	if IsClaudeCompatible(tool) {
		return send.ClaudeComposerReady(raw).Ready
	}
	if IsCodexCompatible(tool) {
		ready, _ := switchComposerReady(raw, "codex")
		return ready
	}
	// Gemini and Grok do not expose the Claude/Codex composer contract.
	// Only send at an unmistakable empty prompt on the final screen line.
	lines := strings.Split(strings.TrimSpace(tmux.StripANSI(raw)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if tool == "gemini" {
		return geminiPromptRE.MatchString(last)
	}
	return tool == "grok" && (last == ">" || last == "❯")
}

func stopTypedCommandVisible(raw, tool, command string) bool {
	if IsCodexCompatible(tool) {
		draft, visible := send.CodexComposerDraft(raw, tmux.StripANSI)
		return visible && draft == command && !tmux.CodexTurnRunning(tmux.StripANSI(raw))
	}
	lines := strings.Split(strings.TrimSpace(tmux.StripANSI(raw)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	return last == "> "+command || last == "❯ "+command || last == "gemini> "+command
}
