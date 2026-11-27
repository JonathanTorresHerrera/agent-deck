package session

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Codex's shared app-server daemon (WO-DECK-CODEX-DELIVERY-01, root cause C,
// second path). Codex 0.161+ can serve every TUI of one CODEX_HOME from one
// app-server daemon. The first TUI starts it as its own child, so the daemon
// sits in that session's tmux pane tree, and it holds open the rollout and
// writer lock of every thread it serves: the host pane's own and each rider's.
// Rider panes hold none of their threads' files. The pane probes read open
// files to learn which thread a pane talks to, so the daemon's files must not
// read as the host pane's own.

// IsCodexAppServerArgv reports whether argv is Codex's app-server, the
// process that serves every TUI of one CODEX_HOME: `codex app-server ...`,
// a binary from the managed app-server-daemon package, or a process started
// with --managed-daemon. Any one marker is enough; a daemon that carried none
// would read as a pane, so they are deliberately redundant. Unlike a pane
// process, Codex may also sit second, behind a launcher such as node: reading
// a process as the daemon only ever withholds trust from what it holds, so
// erring this way fails closed.
func IsCodexAppServerArgv(argv []string) bool {
	k := -1
	for i := 0; i < len(argv) && i < 2; i++ {
		if strings.Contains(strings.ToLower(filepath.Base(argv[i])), "codex") {
			k = i
			break
		}
	}
	if k < 0 {
		return false
	}
	if strings.Contains(filepath.ToSlash(argv[k]), "/app-server-daemon/") {
		return true
	}
	for _, arg := range argv[k+1:] {
		if arg == "app-server" || arg == "--managed-daemon" || strings.HasPrefix(arg, "--managed-daemon=") {
			return true
		}
	}
	return false
}

// ArgvFromPS rebuilds argv from ps output. ps joins arguments with spaces, so
// the executable is taken whole from comm (a full path on macOS, spaces
// included, as in "/Users/First Last/.codex/...") and only the remainder of
// args is split.
func ArgvFromPS(comm, args string) []string {
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

// codexPaneProcess is one live Codex process in a pane's tree. shared marks
// Codex's app-server daemon (IsCodexAppServerArgv).
type codexPaneProcess struct {
	pid    int
	shared bool
}

// codexPaneProcesses lists the pane's live Codex processes and marks the
// shared daemon among them. A non-nil error means the list may be
// incomplete. Test seam.
var codexPaneProcesses = (*Instance).collectCodexPaneProcesses

// codexProcessOpenFiles lists the paths one process holds open. Test seam.
var codexProcessOpenFiles = openFilesOfProcess

func openFilesOfProcess(pid int) ([]string, error) {
	if runtime.GOOS == "linux" {
		return procFDLinkTargets(pid)
	}
	return procfdOpenVnodePaths(pid)
}

// codexProcessKind reads a process's command line once and reports whether
// it is a Codex process and whether it is the shared app-server daemon.
//
// Patch 53: the command line comes from /proc instead of one `ps -p` fork per
// process. The probe runs with the instance lock held, and at ~20 ms a fork a
// 31-process pane tree cost ~650 ms of every status pass it landed in.
// /proc/<pid>/cmdline is NUL-separated argv, the same text `ps -o args=`
// prints. ps stays the fallback where /proc is unavailable.
func codexProcessKind(pid int) (codex, shared bool, err error) {
	if raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
		codex = strings.Contains(strings.ToLower(strings.ReplaceAll(string(raw), "\x00", " ")), "codex")
		if !codex {
			return false, false, nil
		}
		return true, IsCodexAppServerArgv(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")), nil
	} else if errors.Is(err, os.ErrNotExist) && procFSAvailable() {
		return false, false, fmt.Errorf("codex process probe: inspect pid %d: %w", pid, err)
	}
	// #nosec G204 -- "ps" is a fixed binary; only arg is strconv.Itoa(int).
	argsOut, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return false, false, fmt.Errorf("codex process probe: inspect pid %d: %w", pid, err)
	}
	args := strings.TrimSpace(string(argsOut))
	if !strings.Contains(strings.ToLower(args), "codex") {
		return false, false, nil
	}
	// comm keeps an executable path with spaces whole. Without it, the args
	// split alone still finds the daemon's own arguments.
	// #nosec G204 -- "ps" is a fixed binary; only arg is strconv.Itoa(int).
	comm, _ := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "comm=").Output()
	return true, IsCodexAppServerArgv(ArgvFromPS(string(comm), args)), nil
}

// codexSharedDaemonEvidence picks, from the files a shared app-server daemon
// in the pane's tree holds open, the ones that count as this pane's own
// evidence. The rules, in order:
//
//   - The pane's own Codex processes are the authority on its thread: when
//     they hold a conversation's rollout or writer lock, nothing the daemon
//     holds counts.
//   - A daemon serving several conversations says nothing about which one
//     this pane talks to, so none of them counts.
//   - The daemon's one conversation counts only if it can be this instance's:
//     never a thread another live session claims, and never a thread other
//     than the one a bound instance is already bound to. Daemon files can
//     confirm a binding or bootstrap a fresh host, but never move a binding.
//
// own is what the pane's other Codex processes hold open; shared is what the
// daemon holds open. The result is the subset of shared that counts.
func (i *Instance) codexSharedDaemonEvidence(own, shared []string) []string {
	if len(shared) == 0 {
		return nil
	}
	home := i.getCodexHomeDir()
	lockDir := codexWriterLockDir(home)
	for _, path := range own {
		if id := codexOpenFileThread(path, lockDir); id != "" && codexConversationFile(path, id, home) {
			return nil
		}
	}
	byThread := map[string][]string{}
	for _, path := range shared {
		if id := codexOpenFileThread(path, lockDir); id != "" && codexConversationFile(path, id, home) {
			byThread[id] = append(byThread[id], path)
		}
	}
	if len(byThread) != 1 {
		return nil
	}
	for id, files := range byThread {
		if bound := strings.TrimSpace(i.CodexSessionID); bound != "" && id != bound {
			return nil
		}
		if cachedCodexPeerIDs(i)[id] {
			return nil
		}
		return files
	}
	return nil
}

// codexWriterLockDir is home's thread-writer-locks directory, symlinks
// resolved, with a trailing separator.
func codexWriterLockDir(home string) string {
	lockDir := filepath.Join(ExpandPath(home), "thread-writer-locks")
	if resolved, err := filepath.EvalSymlinks(lockDir); err == nil {
		lockDir = resolved
	}
	return lockDir + string(filepath.Separator)
}

// codexOpenFileThread names the thread an open file belongs to: a rollout's
// thread, or the thread of a writer lock under lockDir. Other files name none.
func codexOpenFileThread(path, lockDir string) string {
	if id := extractCodexSessionIDFromPath(path); id != "" {
		return id
	}
	if strings.HasPrefix(path, lockDir) {
		if m := codexThreadWriterLockPathRE.FindStringSubmatch(path); m != nil {
			return m[1]
		}
	}
	return ""
}

// codexConversationFile reports whether an open rollout or writer lock of
// thread id belongs to a conversation: not a subagent thread and not a
// `codex exec` run. A rollout is judged by its own head, a lock by the
// thread's flushed rollout. A thread whose metadata cannot be read yet (a
// fresh composer holds only its lock; a rollout mid-write) counts, so it can
// never hide a second conversation.
func codexConversationFile(path, id, home string) bool {
	var meta codexThreadMeta
	if extractCodexSessionIDFromPath(path) != "" {
		meta = readCodexRolloutThreadMeta(path)
	} else {
		meta, _ = codexThreadMetaForSession(id, home)
	}
	return !meta.valid || (!meta.Exec && meta.ThreadSource != "subagent")
}

// codexPaneExists reports whether the instance's tmux pane exists. Test seam.
var codexPaneExists = func(i *Instance) bool { return i.tmuxSession != nil && i.tmuxSession.Exists() }

// codexPaneMayRunCodex reports whether Codex may be running in the
// instance's pane: a Codex process was found there, or the pane exists but
// its process tree could not be read in full.
func codexPaneMayRunCodex(inst *Instance) bool {
	pids, err := codexPaneProcessPIDs(inst)
	if len(pids) > 0 {
		return true
	}
	return err != nil && codexPaneExists(inst)
}

// codexDiskMovesRefused remembers, per instance, the last disk-scan
// candidate a live pane kept it from moving to, so the lifecycle log records
// each refusal once instead of on every status pass.
var codexDiskMovesRefused sync.Map // instance ID -> candidate thread

func noteCodexDiskMoveRefused(inst *Instance, candidate string) {
	if prev, ok := codexDiskMovesRefused.Load(inst.ID); ok && prev == candidate {
		return
	}
	codexDiskMovesRefused.Store(inst.ID, candidate)
	_ = WriteSessionIDLifecycleEvent(SessionIDLifecycleEvent{
		InstanceID: inst.ID, Tool: inst.Tool, Action: "reject", Source: "disk_scan",
		OldID: inst.CodexSessionID, Candidate: candidate, Reason: "live_pane_disk_move",
	})
}

// collectCodexPaneProcesses returns the pane tree's Codex processes, each
// classified from one read of its command line.
func (i *Instance) collectCodexPaneProcesses() ([]codexPaneProcess, error) {
	pids, probeErr := i.collectTmuxPaneProcessTreePIDs()
	procs := make([]codexPaneProcess, 0, len(pids))
	for _, pid := range pids {
		codex, shared, err := codexProcessKind(pid)
		if err != nil {
			probeErr = errors.Join(probeErr, err)
			continue
		}
		if codex {
			procs = append(procs, codexPaneProcess{pid: pid, shared: shared})
		}
	}
	return procs, probeErr
}
