// Regression coverage for WO-DECK-SWITCH-SIDECAR-STALE-01: deck_switch_account
// failed twice for Aida-Prime (source slot claude-c0e6cb2b, target claude-3)
// with "sidecar destination already differs: .../subagents/agent-*.jsonl".
// Both times the destination was a stale earlier copy that was an exact byte
// prefix of the source — a leftover from an earlier switch — and
// installStagedDirectory refused it one file at a time instead of treating a
// provable prefix the same way installStagedArtifact already does for the
// main transcript.
package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeSidecarFile(t *testing.T, root, rel, body string) string {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// identical: a destination that already matches the staged source byte for
// byte is left alone (no error, no backup, no-op).
func TestInstallStagedDirectory_IdenticalDestinationIsNoop(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "same content\n")
	destPath := writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "same content\n")

	require.NoError(t, installStagedDirectory(stage, dest))

	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, "same content\n", string(data))

	matches, _ := filepath.Glob(destPath + ".bak-*")
	require.Empty(t, matches, "an identical destination must never be backed up")
}

// missing: a destination file that does not exist yet is copied straight in.
func TestInstallStagedDirectory_MissingDestinationIsCopied(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	stagePath := writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "fresh content\n")

	require.NoError(t, installStagedDirectory(stage, dest))

	installed, err := os.ReadFile(filepath.Join(dest, "subagents", "agent-1.jsonl"))
	require.NoError(t, err)
	source, err := os.ReadFile(stagePath)
	require.NoError(t, err)
	require.Equal(t, string(source), string(installed))
}

// stale byte-prefix: a shorter destination that is an exact byte prefix of
// the staged source is a leftover from an earlier switch. It must be backed
// up as "<name>.bak-<YYYYMMDD>" (never clobbering an existing .bak) and the
// source installed in its place, instead of refusing.
func TestInstallStagedDirectory_StaleBytePrefixDestinationIsBackedUpAndAdvanced(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "prefix line\nmore of the conversation\n")
	destPath := writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "prefix line\n")

	require.NoError(t, installStagedDirectory(stage, dest))

	installed, err := os.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, "prefix line\nmore of the conversation\n", string(installed), "the source must be installed in place of the stale prefix")

	matches, err := filepath.Glob(destPath + ".bak-*")
	require.NoError(t, err)
	require.Len(t, matches, 1, "the stale prefix must be preserved as a dated backup")
	backup, err := os.ReadFile(matches[0])
	require.NoError(t, err)
	require.Equal(t, "prefix line\n", string(backup))
}

// stale byte-prefix, backup already present: a second switch back to this
// account on the same day must not clobber the first backup.
func TestInstallStagedDirectory_StalePrefixNeverClobbersExistingBackup(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "prefix\nextended\n")
	destPath := writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "prefix\n")

	require.NoError(t, installStagedDirectory(stage, dest))
	matches, err := filepath.Glob(destPath + ".bak-*")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	firstBackup := matches[0]

	// A second stale prefix shows up again (e.g. a retried switch). The first
	// backup must survive untouched; a second, distinctly named backup holds
	// the new one.
	writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "prefix\n")
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "prefix\nextended again\n")
	require.NoError(t, installStagedDirectory(stage, dest))

	matches, err = filepath.Glob(destPath + ".bak-*")
	require.NoError(t, err)
	require.Len(t, matches, 2, "the first backup must not be clobbered by the second")

	firstStillThere, err := os.ReadFile(firstBackup)
	require.NoError(t, err)
	require.Equal(t, "prefix\n", string(firstStillThere))
}

// true divergence: a destination that is NOT a byte prefix of the source (a
// genuinely different history) must still refuse, never silently overwrite.
func TestInstallStagedDirectory_DivergentDestinationRefuses(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "source history\n")
	destPath := writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "unrelated destination history\n")

	err := installStagedDirectory(stage, dest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "subagents")
	require.Contains(t, err.Error(), "agent-1.jsonl")

	installed, readErr := os.ReadFile(destPath)
	require.NoError(t, readErr)
	require.Equal(t, "unrelated destination history\n", string(installed), "a divergent destination must never be overwritten")
}

// multi-conflict pre-scan: every divergent file must be pre-scanned and
// reported together in ONE error, before any file is copied — never fail on
// the first conflicting file while silently installing the rest.
func TestInstallStagedDirectory_MultipleConflictsReportedTogetherNoPartialInstall(t *testing.T) {
	stage := t.TempDir()
	dest := t.TempDir()
	writeSidecarFile(t, stage, "subagents/agent-1.jsonl", "source 1\n")
	writeSidecarFile(t, dest, "subagents/agent-1.jsonl", "divergent destination 1\n")
	writeSidecarFile(t, stage, "subagents/agent-2.jsonl", "source 2\n")
	writeSidecarFile(t, dest, "subagents/agent-2.jsonl", "divergent destination 2\n")
	// A third, non-conflicting file in the same batch must not be installed
	// either: the whole install is all-or-nothing.
	writeSidecarFile(t, stage, "subagents/agent-3.jsonl", "fresh\n")

	err := installStagedDirectory(stage, dest)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agent-1.jsonl")
	require.Contains(t, err.Error(), "agent-2.jsonl")
	require.Equal(t, 1, strings.Count(err.Error(), "agent-1.jsonl"), "each conflict must be reported once")

	_, statErr := os.Stat(filepath.Join(dest, "subagents", "agent-3.jsonl"))
	require.True(t, os.IsNotExist(statErr), "no file may be installed while any conflict is unresolved")

	installed1, readErr := os.ReadFile(filepath.Join(dest, "subagents", "agent-1.jsonl"))
	require.NoError(t, readErr)
	require.Equal(t, "divergent destination 1\n", string(installed1))
}
