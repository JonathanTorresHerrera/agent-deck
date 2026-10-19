package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCLISwitchFailureIncludesOneDurableAttempt(t *testing.T) {
	for _, args := range [][]string{
		{"session", "switch", "--json"},
		{"session", "switch-account", "--json"},
		{"session", "switch", "missing-session", "--to-harness", "codex", "--to-account", "codex-1", "--json"},
		{"session", "switch-account", "missing-session", "work", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			home := t.TempDir()
			out, stderr, code := runAgentDeck(t, home, args...)
			require.NotEqual(t, 0, code, "stdout=%s stderr=%s", out, stderr)
			var payload map[string]any
			require.NoError(t, json.Unmarshal([]byte(out), &payload), "stdout=%s stderr=%s", out, stderr)
			id, ok := payload["attempt_id"].(string)
			require.True(t, ok)
			require.NotEmpty(t, id)
			require.Contains(t, payload["error"], "Account switch failed (log "+id+")")
			data, err := os.ReadFile(filepath.Join(home, ".agent-deck", "switch-attempts.jsonl"))
			require.NoError(t, err)
			rows := strings.Split(strings.TrimSpace(string(data)), "\n")
			require.Len(t, rows, 1)
			var row map[string]any
			require.NoError(t, json.Unmarshal([]byte(rows[0]), &row))
			require.Equal(t, id, row["attempt_id"])
			require.Equal(t, "failed", row["outcome"])
			require.NotEmpty(t, row["step"])
		})
	}
}
