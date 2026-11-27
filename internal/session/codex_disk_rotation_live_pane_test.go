package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Root cause C, rider side (WO-DECK-CODEX-DELIVERY-01 follow-up). A rider of
// Codex's shared app-server daemon holds none of its threads' files: the
// daemon in the host's pane does. Its identity step therefore always falls
// through to the disk branch, which can only say which user rollout started in
// this project since the launch, not whose pane it runs in.

// riderWithNewerSibling returns a Codex instance bound to own, launched an
// hour ago, and a sibling's user rollout in the same project that started a
// minute ago.
func riderWithNewerSibling(t *testing.T) (inst *Instance, own, sibling string) {
	t.Helper()
	inst, home := newCodexGateInstance(t)
	now := time.Now()
	inst.LastStartedAt = now.Add(-time.Hour)
	own, sibling = uniqueSID(t), uniqueSID(t)
	switchRotationRollout(t, home, own, inst.ProjectPath, now.Add(-48*time.Hour))
	switchRotationRollout(t, home, sibling, inst.ProjectPath, now.Add(-time.Minute))
	inst.CodexSessionID = own
	return inst, own, sibling
}

func TestCodexDiskScan_DoesNotMoveABoundSessionWhileCodexRuns(t *testing.T) {
	inst, own, sibling := riderWithNewerSibling(t)
	// Codex runs in the pane, but it holds no thread files of its own.
	stubCodexPaneProcesses(t, []codexPaneProcess{{pid: 200}}, nil)

	id, err := resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, own, id, "a bound rider moved onto %s, a sibling's newer thread in the same project", sibling)

	inst.UpdateCodexSession(nil)
	require.Equal(t, own, inst.CodexSessionID)
}

func stubCodexPaneExists(t *testing.T, exists bool) {
	t.Helper()
	restore := codexPaneExists
	t.Cleanup(func() { codexPaneExists = restore })
	codexPaneExists = func(*Instance) bool { return exists }
}

func countDiskMoveRefusals(t *testing.T, candidate string) int {
	t.Helper()
	f, err := os.Open(GetSessionIDLifecycleLogPath())
	if os.IsNotExist(err) {
		return 0
	}
	require.NoError(t, err)
	defer f.Close()
	n := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev SessionIDLifecycleEvent
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Candidate == candidate && ev.Reason == "live_pane_disk_move" {
			n++
		}
	}
	return n
}

func TestCodexDiskScan_RefusalIsLoggedOnce(t *testing.T) {
	inst, own, sibling := riderWithNewerSibling(t)
	stubCodexPaneProcesses(t, []codexPaneProcess{{pid: 200}}, nil)

	for range 3 {
		id, err := resolveCurrentCodexIdentity(inst, nil)
		require.NoError(t, err)
		require.Equal(t, own, id)
	}
	require.Equal(t, 1, countDiskMoveRefusals(t, sibling), "every status pass would log the same refusal")
}

func TestCodexDiskScan_IncompleteProbeOfALivePaneDoesNotMove(t *testing.T) {
	inst, own, sibling := riderWithNewerSibling(t)
	stubCodexPaneProcesses(t, nil, nil)
	stubCodexPaneProcessPIDs(t, nil, errors.New("codex process probe: list tmux panes: exit status 1"))

	stubCodexPaneExists(t, true)
	id, err := resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, own, id, "a pane whose process tree could not be read may still run Codex")

	// No pane at all: nothing runs there, and the disk branch decides as before.
	stubCodexPaneExists(t, false)
	id, err = resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, sibling, id)
}

func TestCodexDiskScan_StillBootstrapsAnUnboundSession(t *testing.T) {
	inst, _, sibling := riderWithNewerSibling(t)
	inst.CodexSessionID = ""
	stubCodexPaneProcesses(t, []codexPaneProcess{{pid: 200}}, nil)

	// The disk branch stays the bootstrap source riders depend on.
	id, err := resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, sibling, id)
}

func TestCodexDiskScan_StillFollowsRotationWithoutCodexInThePane(t *testing.T) {
	inst, _, newer := riderWithNewerSibling(t)
	stubCodexPaneProcesses(t, nil, nil)

	id, err := resolveCurrentCodexIdentity(inst, nil)
	require.NoError(t, err)
	require.Equal(t, newer, id, "with no Codex process in the pane the disk branch keeps following rotation")
}
