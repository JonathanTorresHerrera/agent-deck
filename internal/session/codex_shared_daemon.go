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
