package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/asheshgoplani/agent-deck/internal/session"
)

// Which session a Codex notify belongs to (WO-DECK-CODEX-DELIVERY-01, root
// cause C). Codex 0.161+ runs turns in an app-server daemon shared per
// CODEX_HOME. The first TUI in a home spawns it, so every notify the daemon
// fires carries that TUI's AGENTDECK_INSTANCE_ID and CODEX_SESSION_ID, for
// every TUI riding it. The parent process of the notify tells the two cases
// apart:
//
//   - pane: an interactive Codex process. Its environment is its own pane's,
//     so the env instance is the session running the thread, including after
//     /new or /resume in that pane.
//   - daemon: Codex's shared app-server. Its environment names whichever TUI
//     started it and says nothing about the thread. Only the session store
//     can: the event goes to the one session bound to the thread, or nowhere.
//   - unknown: the parent could not be read or is not Codex. The store
//     decides when it can; otherwise the event keeps its old destination
//     without evidence, so it can bootstrap an unbound session but never move
//     a bound one.
//
// The decision is written into the record (codex_thread_evidence) so that no
// reader has to probe processes on every status sweep; see
// internal/session/codex_thread_evidence.go for the reader side.
type codexNotifySource int

const (
	codexNotifyFromUnknown codexNotifySource = iota
	codexNotifyFromPane
	codexNotifyFromDaemon
)

func (s codexNotifySource) String() string {
	switch s {
	case codexNotifyFromPane:
		return "pane"
	case codexNotifyFromDaemon:
		return "daemon"
	default:
		return "unknown"
	}
}

// codexNotifyProcs is the slice of the process table the classifier reads.
// Test seam.
var codexNotifyProcs = struct {
	parent func() int
	argv   func(pid int) ([]string, error)
	ppid   func(pid int) (int, error)
}{os.Getppid, processArgv, processParentPID}

// codexNotifySourceOf classifies the running notify by its parent. Test seam.
var codexNotifySourceOf = classifyCodexNotifySource

// classifyCodexNotifySource walks up from the notify's parent through
// wrappers such as env or sh to the first Codex process. A read error, a
// non-Codex parent or a chain longer than a few wrappers is unknown.
func classifyCodexNotifySource() codexNotifySource {
	pid := codexNotifyProcs.parent()
	for hop := 0; hop < 4 && pid > 1; hop++ {
		argv, err := codexNotifyProcs.argv(pid)
		if err != nil || len(argv) == 0 {
			return codexNotifyFromUnknown
		}
		switch {
		case isCodexAppServerArgv(argv):
			return codexNotifyFromDaemon
		case isCodexArgv(argv):
			return codexNotifyFromPane
		case !isNotifyWrapperArgv(argv):
			return codexNotifyFromUnknown
		}
		next, err := codexNotifyProcs.ppid(pid)
		if err != nil {
			return codexNotifyFromUnknown
		}
		pid = next
	}
	return codexNotifyFromUnknown
}

// isCodexArgv reports whether the process is the Codex executable itself.
// Only argv[0] counts: the process that spawns notify is the native Codex
// binary, and an interpreter running a script whose name mentions codex
// (`python3 codex_bridge.py`) is a wrapper of unknown provenance, not a pane.
func isCodexArgv(argv []string) bool {
	return len(argv) > 0 && strings.Contains(strings.ToLower(filepath.Base(argv[0])), "codex")
}

// isCodexAppServerArgv reports whether argv is Codex's app-server, the
// process that serves every TUI of one CODEX_HOME: `codex app-server ...`,
// or a binary from the managed app-server-daemon package.
func isCodexAppServerArgv(argv []string) bool {
	if !isCodexArgv(argv) {
		return false
	}
	if strings.Contains(filepath.ToSlash(argv[0]), "/app-server-daemon/") {
		return true
	}
	for _, arg := range argv[1:] {
		if arg == "app-server" {
			return true
		}
	}
	return false
}

// isNotifyWrapperArgv reports whether argv is a program that may sit between
// Codex and the notify command without being its source.
func isNotifyWrapperArgv(argv []string) bool {
	switch filepath.Base(argv[0]) {
	case "env", "sh", "bash", "dash", "zsh", "fish", "nice", "nohup", "timeout", "stdbuf", "setsid":
		return true
	}
	return false
}

// processArgv reads a process's argv: /proc on Linux, ps elsewhere.
func processArgv(pid int) ([]string, error) {
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		if len(args) == 1 && args[0] == "" {
			return nil, errors.New("empty cmdline")
		}
		return args, nil
	} else if _, statErr := os.Stat("/proc/self/cmdline"); statErr == nil {
		return nil, err
	}
	// #nosec G204 -- "ps" is a fixed binary; the only argument is strconv.Itoa(int).
	comm, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	if err != nil {
		return nil, err
	}
	// #nosec G204 -- "ps" is a fixed binary; the only argument is strconv.Itoa(int).
	args, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return nil, err
	}
	return argvFromPS(string(comm), string(args)), nil
}

// argvFromPS rebuilds argv from ps output. ps joins arguments with spaces, so
// the executable is taken whole from comm (a full path on macOS, spaces
// included, as in "/Users/First Last/.codex/...") and only the remainder of
// args is split.
func argvFromPS(comm, args string) []string {
	comm, args = strings.TrimSpace(comm), strings.TrimSpace(args)
	if comm == "" {
		return strings.Fields(args)
	}
	rest := args
	if strings.HasPrefix(args, comm) {
		rest = args[len(comm):]
	} else if fields := strings.Fields(args); len(fields) > 0 {
		rest = strings.Join(fields[1:], " ")
	}
	return append([]string{comm}, strings.Fields(rest)...)
}

// processParentPID reads a process's parent pid: /proc/<pid>/stat on Linux
// (split after the last ')' since the command name may hold spaces or
// parens), ps elsewhere.
func processParentPID(pid int) (int, error) {
	if data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil {
		idx := strings.LastIndexByte(string(data), ')')
		if idx < 0 {
			return 0, errors.New("malformed /proc stat")
		}
		fields := strings.Fields(string(data[idx+1:]))
		if len(fields) < 2 {
			return 0, errors.New("short /proc stat")
		}
		return strconv.Atoi(fields[1])
	}
	// #nosec G204 -- "ps" is a fixed binary; the only argument is strconv.Itoa(int).
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "ppid=").Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

// codexThreadLookup reads which sessions the stores bind a thread to. Test
// seam.
var codexThreadLookup = session.LookupCodexThreadBindings

// codexNotifyRoute is where one notify event is filed (instanceID, with the
// evidence the writer had for the thread), or, with an empty instanceID, why
// it is dropped.
type codexNotifyRoute struct {
	instanceID string
	evidence   string
	reason     string
}

// routeCodexNotify picks the instance a turn event belongs to. envID is the
// notify's AGENTDECK_INSTANCE_ID; threadFromPayload says whether threadID
// came from the payload rather than the inherited CODEX_SESSION_ID; cwd is
// the payload's cwd ("" when absent); home is the notify's Codex home.
func routeCodexNotify(envID, threadID string, threadFromPayload bool, cwd string, source codexNotifySource, home string) codexNotifyRoute {
	switch source {
	case codexNotifyFromPane:
		if !threadFromPayload || threadID == "" {
			return codexNotifyRoute{instanceID: envID}
		}
		// A `codex exec` started inside the pane inherits its environment.
		// Its thread may still report status for an unbound session (a
		// task-worker pane runs exactly that), but never moves a binding.
		// Without a Codex home the rollout cannot be read, so the thread
		// cannot be shown not to be one.
		if home == "" || session.CodexExecThread(threadID, home) {
			return codexNotifyRoute{instanceID: envID}
		}
		return codexNotifyRoute{instanceID: envID, evidence: session.CodexThreadEvidencePane}
	case codexNotifyFromDaemon:
		if !threadFromPayload || threadID == "" {
			// The inherited CODEX_SESSION_ID names the daemon's first TUI.
			return codexNotifyRoute{reason: "daemon_event_without_thread"}
		}
		return routeCodexNotifyByBinding(envID, threadID, cwd, false)
	default:
		if !threadFromPayload || threadID == "" {
			return codexNotifyRoute{instanceID: envID}
		}
		return routeCodexNotifyByBinding(envID, threadID, cwd, true)
	}
}

// routeCodexNotifyByBinding files the event under the one live session the
// stores bind threadID to. envFallback keeps the old destination, without
// evidence, when no store can be read at all (a sandbox or remote host has
// none), or when every store was read, none binds the thread, and the env
// session is not bound elsewhere.
func routeCodexNotifyByBinding(envID, threadID, cwd string, envFallback bool) codexNotifyRoute {
	lookup, err := codexThreadLookup(threadID, envID)
	if err != nil {
		if envFallback {
			return codexNotifyRoute{instanceID: envID}
		}
		return codexNotifyRoute{reason: "session_store_unreadable"}
	}
	switch len(lookup.Owners) {
	case 1:
		owner := lookup.Owners[0]
		// A thread's cwd is the directory its session started it in. One
		// outside every path the bound session owns means the binding itself
		// is wrong (a sibling's thread), so the event goes nowhere.
		if owner.CwdIsForeign(cwd) {
			return codexNotifyRoute{reason: "thread_cwd_outside_bound_session"}
		}
		return codexNotifyRoute{instanceID: owner.InstanceID, evidence: session.CodexThreadEvidenceBinding}
	case 0:
		if !envFallback {
			return codexNotifyRoute{reason: "thread_bound_to_no_session"}
		}
		// A store that could not be read may hold the owner; filing the
		// thread under the env session could then bind it to a sibling's.
		if lookup.Incomplete {
			return codexNotifyRoute{reason: "session_store_incomplete"}
		}
		if env := lookup.Env; env != nil {
			if env.CodexSessionID != "" && env.CodexSessionID != threadID {
				return codexNotifyRoute{reason: "thread_not_bound_to_env_session"}
			}
			if env.CwdIsForeign(cwd) {
				return codexNotifyRoute{reason: "thread_cwd_outside_env_session"}
			}
		}
		return codexNotifyRoute{instanceID: envID}
	default:
		return codexNotifyRoute{reason: "thread_bound_to_several_sessions"}
	}
}

// logCodexNotifyRoute records, in the session-id lifecycle log, a notify the
// writer dropped or filed under another instance than its environment named.
func logCodexNotifyRoute(envID, threadID, event string, source codexNotifySource, route codexNotifyRoute) {
	ev := session.SessionIDLifecycleEvent{
		Tool: "codex", Source: "codex_notify_" + source.String(),
		Candidate: threadID, HookEvent: event, Reason: route.reason,
	}
	switch {
	case route.instanceID == "":
		ev.InstanceID, ev.Action = envID, "drop"
	case route.instanceID != envID:
		ev.InstanceID, ev.Action, ev.RoutedFrom = route.instanceID, "route", envID
		if ev.Reason == "" {
			ev.Reason = "thread_bound_to_session"
		}
	default:
		return
	}
	_ = session.WriteSessionIDLifecycleEvent(ev)
}
