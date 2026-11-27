package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Root cause C, second path (WO-DECK-CODEX-DELIVERY-01 follow-up). Codex's
// shared app-server daemon is a child of the first TUI in its CODEX_HOME, so
// it runs in that host session's pane tree and holds the rollout and writer
// lock of every thread it serves, riders' included. The pane probes must not
// read those files as the host pane's own.

type sharedDaemonFixture struct {
	inst *Instance
	home string
}

func newSharedDaemonFixture(t *testing.T) sharedDaemonFixture {
	t.Helper()
	inst, home := newCodexGateInstance(t)
	require.NoError(t, os.MkdirAll(filepath.Join(home, "thread-writer-locks"), 0o700))
	inst.LastStartedAt = time.Now().Add(-time.Hour)
	return sharedDaemonFixture{inst: inst, home: home}
}

// rollout writes a user rollout in the instance's project, last written two
// days before this launch, so the disk branch's launch window never sees it.
func (f sharedDaemonFixture) rollout(t *testing.T, id string) string {
	t.Helper()
	return writeRollout(t, f.home, id, f.inst.ProjectPath, "", "", time.Now().Add(-48*time.Hour))
}

func (f sharedDaemonFixture) subagentRollout(t *testing.T, id, parent string) string {
	t.Helper()
	return writeRollout(t, f.home, id, f.inst.ProjectPath, parent, "", time.Now().Add(-48*time.Hour))
}

func (f sharedDaemonFixture) lock(id string) string {
	return filepath.Join(f.home, "thread-writer-locks", id+".lock")
}

// pane makes the host pane run a Codex TUI (pid 100) holding tuiFiles and the
// shared daemon (pid 101) holding daemonFiles.
func (f sharedDaemonFixture) pane(t *testing.T, tuiFiles, daemonFiles []string) {
	t.Helper()
	stubCodexPaneProcesses(t,
		[]codexPaneProcess{{pid: 100}, {pid: 101, shared: true}},
		map[int][]string{100: tuiFiles, 101: daemonFiles})
}

// stubCodexPaneProcesses makes procs the pane's Codex processes, each holding
// files[pid] open, and keeps the pid-only seam consistent with them.
func stubCodexPaneProcesses(t *testing.T, procs []codexPaneProcess, files map[int][]string) {
	t.Helper()
	restoreProcs, restoreFiles := codexPaneProcesses, codexProcessOpenFiles
	t.Cleanup(func() { codexPaneProcesses, codexProcessOpenFiles = restoreProcs, restoreFiles })
	codexPaneProcesses = func(*Instance) ([]codexPaneProcess, error) { return procs, nil }
	codexProcessOpenFiles = func(pid int) ([]string, error) { return files[pid], nil }
	pids := make([]int, 0, len(procs))
	for _, p := range procs {
		pids = append(pids, p.pid)
	}
	stubCodexPaneProcessPIDs(t, pids, nil)
}

// holdFilesOpen starts a process that keeps paths open, in order, from
// descriptor 3 up, until the test ends, and returns its pid.
func holdFilesOpen(t *testing.T, paths ...string) int {
	t.Helper()
	if !procFSAvailable() {
		t.Skip("needs /proc")
	}
	redirs := make([]string, 0, len(paths))
	for k, p := range paths {
		redirs = append(redirs, fmt.Sprintf("%d<'%s'", 3+k, strings.ReplaceAll(p, "'", `'\''`)))
	}
	cmd := exec.Command("sh", "-c", "exec "+strings.Join(redirs, " ")+"; exec sleep 300")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		open, _ := procFDLinkTargets(cmd.Process.Pid)
		held := 0
		for _, target := range open {
			for _, p := range paths {
				if canonicalDir(target) == canonicalDir(p) {
					held++
				}
			}
		}
		if held == len(paths) {
			return cmd.Process.Pid
		}
	}
	t.Fatalf("helper never held %v open", paths)
	return 0
}

// seedCodexClaims records live sessions' thread claims (tmux session name ->
// thread) in the ownership cache for inst's tmux socket.
func seedCodexClaims(t *testing.T, inst *Instance, claims map[string]string) {
	t.Helper()
	resetCodexOwnershipCache(t)
	codexOwnershipCache.Lock()
	defer codexOwnershipCache.Unlock()
	codexOwnershipCache.bySocket[inst.tmuxSession.SocketName] = codexOwnershipSnapshot{
		at:     time.Now(),
		claims: &codexOwnershipClaims{bySession: claims},
	}
}

func TestCodexSharedDaemon_HostKeepsItsThreadBesideARider(t *testing.T) {
	f := newSharedDaemonFixture(t)
	host, rider := uniqueSID(t), uniqueSID(t)
	f.inst.CodexSessionID = host
	f.pane(t, nil, []string{f.rollout(t, rider), f.lock(rider), f.rollout(t, host), f.lock(host)})

	id, err := resolveCurrentCodexIdentity(f.inst, nil)
	require.NoError(t, err, "the rider's thread in the daemon made the host's identity ambiguous")
	require.Equal(t, host, id)
	require.Empty(t, f.inst.LiveCodexThreadID(), "a daemon serving two conversations does not say which one is this pane's")
}

func TestCodexSharedDaemon_NeverMovesABinding(t *testing.T) {
	f := newSharedDaemonFixture(t)
	host, rider := uniqueSID(t), uniqueSID(t)
	f.rollout(t, host)
	f.inst.CodexSessionID = host
	// The host's own files are closed; the daemon still serves the rider.
	f.pane(t, nil, []string{f.rollout(t, rider), f.lock(rider)})

	id, err := resolveCurrentCodexIdentity(f.inst, nil)
	require.NoError(t, err)
	require.Equal(t, host, id, "the host moved onto the rider's thread from the daemon's files")
	require.Empty(t, f.inst.LiveCodexThreadID(), "the rider's thread read as the host pane's live thread")
}

func TestCodexSharedDaemon_RotationProbeSkipsTheDaemon(t *testing.T) {
	f := newSharedDaemonFixture(t)
	host, rider := uniqueSID(t), uniqueSID(t)
	f.inst.CodexSessionID = host
	// The rider's rollout sits on the lower descriptor, so a first-match scan
	// of these descriptors meets it first.
	holder := holdFilesOpen(t, f.rollout(t, rider), f.rollout(t, host))
	setProcs := func(shared bool) {
		restore := codexPaneProcesses
		t.Cleanup(func() { codexPaneProcesses = restore })
		codexPaneProcesses = func(*Instance) ([]codexPaneProcess, error) {
			return []codexPaneProcess{{pid: holder, shared: shared}}, nil
		}
	}

	setProcs(true)
	id, err := f.inst.queryCodexSessionFromHostProcFD()
	require.NoError(t, err)
	require.Empty(t, id, "the rotation probe read the shared daemon's descriptors as the host pane's")

	// A pane's own Codex process still answers the way it always did.
	setProcs(false)
	id, err = f.inst.queryCodexSessionFromHostProcFD()
	require.NoError(t, err)
	require.Equal(t, rider, id)
}

func TestCodexSharedDaemon_StillBootstrapsAFreshHost(t *testing.T) {
	f := newSharedDaemonFixture(t)
	host, rider := uniqueSID(t), uniqueSID(t)

	// Fresh composer: the daemon the host just started holds only its lock.
	f.pane(t, nil, []string{f.lock(host)})
	require.Equal(t, host, f.inst.LiveCodexThreadID(), "a daemon serving one conversation is the host pane's own")

	f.pane(t, nil, []string{f.lock(host), f.lock(rider)})
	require.Empty(t, f.inst.LiveCodexThreadID(), "with a rider's thread there too, neither is attributable")
}

func TestCodexSharedDaemon_SubagentsDoNotCountAsConversations(t *testing.T) {
	f := newSharedDaemonFixture(t)
	host, child := uniqueSID(t), uniqueSID(t)
	f.inst.CodexSessionID = host
	f.pane(t, nil, []string{f.rollout(t, host), f.lock(host), f.subagentRollout(t, child, host)})

	require.Equal(t, host, f.inst.LiveCodexThreadID())
	id, err := resolveCurrentCodexIdentity(f.inst, nil)
	require.NoError(t, err)
	require.Equal(t, host, id)
}

func TestCodexSharedDaemon_ThreadAPeerClaimsIsNotEvidence(t *testing.T) {
	f := newSharedDaemonFixture(t)
	rider := uniqueSID(t)
	// A host already misbound to its rider's thread before this fix shipped.
	f.inst.CodexSessionID = rider
	f.pane(t, nil, []string{f.rollout(t, rider)})
	seedCodexClaims(t, f.inst, map[string]string{"agentdeck_rider_peer": rider, f.inst.tmuxSession.Name: rider})

	require.Empty(t, f.inst.LiveCodexThreadID(), "the daemon confirmed a thread another live session is bound to")
}

func TestCodexSharedDaemon_PaneOwnFilesComeFirst(t *testing.T) {
	f := newSharedDaemonFixture(t)
	old, current := uniqueSID(t), uniqueSID(t)
	f.inst.CodexSessionID = old
	f.pane(t, []string{f.rollout(t, current), f.lock(current)}, []string{f.rollout(t, old), f.lock(old)})

	require.Equal(t, current, f.inst.LiveCodexThreadID(), "the pane's own Codex process is the authority on its thread")
	id, err := resolveCurrentCodexIdentity(f.inst, nil)
	require.NoError(t, err)
	require.Equal(t, current, id)
}

func TestCodexProcessKindMarksTheSharedDaemon(t *testing.T) {
	if !procFSAvailable() {
		t.Skip("needs /proc")
	}
	sh, err := exec.LookPath("sh")
	require.NoError(t, err)
	start := func(args ...string) int {
		cmd := &exec.Cmd{Path: sh, Args: args}
		require.NoError(t, cmd.Start())
		t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
		return cmd.Process.Pid
	}
	daemon := start("codex", "-c", "sleep 300; :", "app-server")
	tui := start("codex", "-c", "sleep 300; :", "resume")
	other := start("sh", "-c", "sleep 300; :")
	time.Sleep(50 * time.Millisecond)

	for _, tc := range []struct {
		name          string
		pid           int
		codex, shared bool
	}{
		{"daemon", daemon, true, true},
		{"tui", tui, true, false},
		{"other", other, false, false},
	} {
		codex, shared, err := codexProcessKind(tc.pid)
		require.NoError(t, err, tc.name)
		require.Equal(t, tc.codex, codex, tc.name)
		require.Equal(t, tc.shared, shared, tc.name)
	}
}
