package main

import (
	"strings"
	"testing"
)

// WO-DECK-CODEX-DELIVERY-01 added two operator commands for a Codex session
// that refuses every send: `codex-markers` (a stale submission marker) and
// `session skip-codex-update` (the Codex update prompt holding the pane).
// Neither appeared in any help screen, so the operator who needed one had to
// know its name already. These tests pin the help lines. captureStdout lives
// in cursor_hooks_cmd_test.go.

// helpBlock returns the text from the heading line start up to the heading
// line end, failing the test when either is missing.
func helpBlock(t *testing.T, help, start, end string) string {
	t.Helper()
	from := strings.Index(help, "\n"+start+"\n")
	if from < 0 {
		t.Fatalf("help has no %q block", start)
	}
	rest := help[from+1:]
	to := strings.Index(rest, "\n"+end+"\n")
	if to < 0 {
		t.Fatalf("help has no %q block after %q", end, start)
	}
	return rest[:to]
}

func TestHelpNamesTheCodexDeliveryCommands(t *testing.T) {
	help := captureStdout(t, printHelp)

	commands := helpBlock(t, help, "Commands:", "Session Commands:")
	if !strings.Contains(commands, "\n  codex-markers ") {
		t.Error("`agent-deck --help` Commands list omits codex-markers: a stale Codex submission marker refuses every send and the command that clears it cannot be found")
	}

	sessionCommands := helpBlock(t, help, "Session Commands:", "Fleet Recovery Commands:")
	if !strings.Contains(sessionCommands, "\n  session skip-codex-update <id>") {
		t.Error("`agent-deck --help` Session Commands omit `session skip-codex-update`: a Codex update prompt holding the pane cannot be skipped from the CLI without knowing the name")
	}
}

func TestSessionHelpNamesSkipCodexUpdate(t *testing.T) {
	help := captureStdout(t, printSessionHelp)
	if !strings.Contains(help, "\n  skip-codex-update <id>") {
		t.Error("`agent-deck session --help` omits skip-codex-update")
	}
}
